package services

import (
	"os"
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
}

type StoreStat struct {
	touch time.Time
	hash  string
}

func NewCleaner(p string, keep string, free string) *Cleaner {
	return &Cleaner{
		p:    p,
		keep: keep,
		free: free,
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
		log.Infof("drop hash=%v touch=%v", v.hash, v.touch.String())
		err := s.drop(v.hash)
		if err != nil {
			return err
		}
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

func (s *Cleaner) drop(h string) error {
	err := os.RemoveAll(s.p + "/" + h)
	if err != nil {
		return err
	}
	err = os.RemoveAll(s.p + "/" + h + ".touch")
	if err != nil {
		return err
	}
	return nil
}

func (s *Cleaner) getStats() ([]StoreStat, error) {
	var res []StoreStat
	ss := map[string]StoreStat{}
	fs, err := os.ReadDir(s.p)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".touch") {
			h := strings.TrimSuffix(f.Name(), ".touch")
			info, err := f.Info()
			if err != nil {
				return nil, err
			}
			ss[h] = StoreStat{
				hash:  h,
				touch: info.ModTime(),
			}
		} else if f.IsDir() {
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
