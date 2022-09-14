package services

import (
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type Cleaner struct {
	p        string
	t        *time.Ticker
	cleaning bool
	keep     string
}

type StoreStat struct {
	touch time.Time
	hash  string
}

func NewCleaner(p string, keep string) *Cleaner {
	return &Cleaner{
		p:    p,
		keep: keep,
	}
}

func (s *Cleaner) clean() error {
	t := strings.TrimRight(s.keep, "%")
	tt, err := strconv.Atoi(t)
	if err != nil {
		return errors.Wrapf(err, "failed to parse percent value %v", t)
	}
	p := float64(tt) / 100
	free := s.getFreeSpace()
	total := s.getTotalSpace()
	keep := uint64(float64(total) * p)

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
		free := s.getFreeSpace()
		if free > keep {
			return nil
		}
	}
	log.Info("finish cleaning")
	return nil
}

func (s *Cleaner) getFreeSpace() uint64 {
	var stat unix.Statfs_t
	unix.Statfs(s.p, &stat)
	return stat.Bavail * uint64(stat.Bsize)
}

func (s *Cleaner) getTotalSpace() uint64 {
	var stat unix.Statfs_t
	unix.Statfs(s.p, &stat)
	return stat.Blocks * uint64(stat.Bsize)
}

func (s *Cleaner) drop(h string) error {
	os.RemoveAll(s.p + "/" + h)
	os.RemoveAll(s.p + "/" + h + ".touch")
	return nil
}

func (s *Cleaner) getStats() ([]StoreStat, error) {
	res := []StoreStat{}
	ss := map[string]StoreStat{}
	fs, err := ioutil.ReadDir(s.p)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".touch") {
			h := strings.TrimSuffix(f.Name(), ".touch")
			ss[h] = StoreStat{
				hash:  h,
				touch: f.ModTime(),
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
	s.t = time.NewTicker(30 * time.Second)
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
