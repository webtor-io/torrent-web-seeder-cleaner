package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recPublisher struct{ msgs []string }

func (r *recPublisher) Publish(subject string, data []byte) error {
	r.msgs = append(r.msgs, subject+" "+string(data))
	return nil
}

const (
	hOld = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hNew = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// The whole path: a real directory, a real clean() that is forced to free
// space, and the message that comes out of it.
func TestCleanAnnouncesWhatItDropped(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, age time.Duration) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		touch := filepath.Join(dir, name+".touch")
		if err := os.WriteFile(touch, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(touch, when, when); err != nil {
			t.Fatal(err)
		}
	}
	mk(hOld, 48*time.Hour)
	mk("lost+found", 72*time.Hour) // oldest of all, but not a torrent: left alone
	mk(hNew, time.Minute)

	rec := &recPublisher{}
	// keep=100%: there is never enough free space, so clean() starts dropping;
	// free=100% is never reached either, so it drops everything, oldest first.
	c := NewCleaner(dir, "100%", "100%", &CacheEvents{p: rec})
	if err := c.clean(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`resource.uncached {"resource_id":"` + hOld + `"}`,
		`resource.uncached {"resource_id":"` + hNew + `"}`,
	}
	if len(rec.msgs) != len(want) || rec.msgs[0] != want[0] || rec.msgs[1] != want[1] {
		t.Fatalf("got %v, want %v", rec.msgs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, hOld)); !os.IsNotExist(err) {
		t.Errorf("the directory is still there: %v", err)
	}
}

func TestCleanWithoutEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, hOld), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewCleaner(dir, "100%", "100%", nil).clean(); err != nil {
		t.Fatal(err)
	}
}
