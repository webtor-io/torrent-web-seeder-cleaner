package services

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"code.cloudfoundry.org/bytefmt"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type Cleaner struct {
	p        string
	t        *time.Ticker
	cleaning bool
	keep     string
	free     string
	events   *CacheEvents // nil: nothing is published
}

type StoreStat struct {
	touch time.Time
	hash  string
}

func NewCleaner(p string, keep string, free string, events *CacheEvents) *Cleaner {
	return &Cleaner{
		p:      p,
		keep:   keep,
		free:   free,
		events: events,
	}
}

func (s *Cleaner) getKeep(v string, total uint64) (keep uint64, err error) {
	if strings.HasSuffix(v, "%") {
		t := strings.TrimRight(v, "%")
		tt, err := strconv.Atoi(t)
		if err != nil {
			return 0, errors.Wrapf(err, "failed to parse percent value %v", t)
		}
		p := float64(tt) / 100
		keep = uint64(float64(total) * p)
	} else {
		keep, err = bytefmt.ToBytes(v)
		if err != nil {
			return 0, errors.Errorf("failed to parse byte value %v", v)
		}
	}
	return keep, nil
}

func (s *Cleaner) clean() error {
	free, err := s.getFreeSpace()
	if err != nil {
		return err
	}
	total, err := s.getTotalSpace()
	if err != nil {
		return err
	}
	keep, err := s.getKeep(s.keep, total)
	if err != nil {
		return errors.Wrapf(err, "failed to parse keep")
	}
	needToFreeUp, err := s.getKeep(s.free, total)
	if err != nil {
		return errors.Wrapf(err, "failed to parse free")
	}
	log.Infof("start cleaning total=%.2fG free=%.2fG keep=%.2fG", float64(total)/1024/1024/1024, float64(free)/1024/1024/1024, float64(keep)/1024/1024/1024)

	if free > keep {
		log.Info("no need to clean")
		return nil
	}
	stats, err := s.getStats()
	if err != nil {
		return err
	}
	for _, v := range stats {
		dropped, err := s.drop(v.hash)
		if err != nil {
			return err
		}
		if !dropped {
			log.Infof("skip hash=%v touch=%v: a seeder holds it", v.hash, v.touch.String())
			continue
		}
		log.Infof("drop hash=%v touch=%v", v.hash, v.touch.String())
		// After the removal, not before: a drop that failed left the
		// content where it was.
		s.events.Dropped(v.hash)
		free, err := s.getFreeSpace()
		if err != nil {
			return err
		}
		if free > needToFreeUp {
			return nil
		}
	}
	log.Info("finish cleaning")
	return nil
}

func (s *Cleaner) getFreeSpace() (uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(s.p, &stat)
	if err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func (s *Cleaner) getTotalSpace() (uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(s.p, &stat)
	if err != nil {
		return 0, err
	}
	return stat.Blocks * uint64(stat.Bsize), nil
}

// drop removes the directory of h and its .touch, and reports whether it did:
// not while a seeder pod holds the directory.
//
// Every pod on the node that has the torrent open holds <dir>/.lock shared
// (torrent-web-seeder dir_lock.go). Removing the directory under it unlinks
// files the pod still trusts: a file it opens again comes back sparse while
// its completion says the pieces are there, and the next pod locks a new
// .lock, so the two no longer keep each other from punching holes. Nor does
// it free the space while the pod has the files open. 36 of 13,680 drops on
// 2026-10-04 hit a torrent a pod of the node had loaded.
//
// So the lock is taken exclusive, without waiting, and held until the
// directory is gone. The eviction gate comes first, as in the seeder's
// whileAlone: on Linux a pod whose upgrade failed holds nothing on .lock until
// it takes it shared again, and only the gate covers that gap. The gate is
// not created: that it exists tells the seeder's cache path the torrent
// evicts.
func (s *Cleaner) drop(h string) (bool, error) {
	dir := filepath.Join(s.p, h)
	gate, err := os.Open(filepath.Join(dir, ".evict.lock"))
	if err == nil {
		defer gate.Close()
		if ok, err := tryLock(gate); !ok {
			return false, err
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDONLY|os.O_CREATE, 0o644)
	if err == nil {
		defer lock.Close()
		if ok, err := tryLock(lock); !ok {
			return false, err
		}
		if err := removeAll(dir); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(err) { // not there: only the .touch is left
		return false, err
	}
	return true, os.RemoveAll(dir + ".touch")
}

// removeAll removes the directory in drop; a test looks at the locks from
// inside it.
var removeAll = os.RemoveAll

// tryLock takes f exclusive without waiting; false with no error: someone
// holds it.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return err == nil, err
}

func (s *Cleaner) getStats() ([]StoreStat, error) {
	var res []StoreStat
	ss := map[string]StoreStat{}
	fs, err := os.ReadDir(s.p)
	if err != nil {
		return nil, err
	}
	// The shard is shared, and only what is named by a hash is the cleaner's
	// to drop: torrents (the seeder names them by infohash) and transcoder
	// output (by the sha1 of the source). s3-cache keeps its chunks in
	// <shard>/s3-cache and evicts them itself; without a .touch they used to
	// go first, ~120 times a day, ~1 TB of cache. ext4 keeps lost+found.
	for _, f := range fs {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".touch") {
			h := strings.TrimSuffix(f.Name(), ".touch")
			if !infohashRe.MatchString(h) {
				continue
			}
			info, err := f.Info()
			if err != nil {
				return nil, err
			}
			ss[h] = StoreStat{
				hash:  h,
				touch: info.ModTime(),
			}
		} else if f.IsDir() && infohashRe.MatchString(f.Name()) {
			h := f.Name()
			if _, ok := ss[h]; !ok {
				ss[h] = StoreStat{
					hash:  h,
					touch: time.Time{},
				}
			}
		}
	}
	for _, v := range ss {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].touch.Before(res[j].touch)
	})
	return res, nil
}

func (s *Cleaner) Serve() error {
	log.Infof("serving Cleaner for %v", s.p)
	s.t = time.NewTicker(5 * time.Minute)
	for ; true; <-s.t.C {
		if !s.cleaning {
			s.cleaning = true
			err := s.clean()
			if err != nil {
				log.WithError(err).Errorf("got cleaner error")
			}
			s.cleaning = false
		}
	}
	return nil
}

func (s *Cleaner) Close() {
	if s.t != nil {
		s.t.Stop()
	}
}
