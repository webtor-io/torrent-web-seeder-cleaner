package services

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// mkTorrent makes <dir>/<name> with a file in it and <name>.touch age old.
func mkTorrent(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "d", "f"), []byte("x"), 0o644); err != nil {
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

// hold opens path as the seeder opens its lock files (dir_lock.go
// openLockFile) and flocks it how.
func hold(t *testing.T, path string, how int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := unix.Flock(int(f.Fd()), how); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// cleanAll runs a pass that drops everything it may, oldest first, and
// returns the hashes it announced.
func cleanAll(t *testing.T, dir string) []string {
	t.Helper()
	rec := &recPublisher{}
	if err := NewCleaner(dir, "100%", "100%", &CacheEvents{p: rec}).clean(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range rec.msgs {
		got = append(got, m[len(`resource.uncached {"resource_id":"`):][:40])
	}
	return got
}

// A pod that has the torrent open holds <dir>/.lock shared (seeder lockDir).
// The oldest torrent is held, so it stays, and the pass goes on to the next.
func TestCleanSkipsADirASeederHolds(t *testing.T) {
	dir := t.TempDir()
	mkTorrent(t, dir, hOld, 48*time.Hour)
	mkTorrent(t, dir, hNew, time.Minute)
	hold(t, filepath.Join(dir, hOld, ".lock"), unix.LOCK_SH)

	if got := cleanAll(t, dir); !slices.Equal(got, []string{hNew}) {
		t.Errorf("announced %v, want only %s", got, hNew[:4])
	}
	if !exists(t, filepath.Join(dir, hOld, "d", "f")) || !exists(t, filepath.Join(dir, hOld+".touch")) {
		t.Error("the held torrent was dropped")
	}
	// The gate's presence tells the seeder's cache path that the torrent
	// evicts (FileCacheMap.Open); the cleaner must not make one up.
	if exists(t, filepath.Join(dir, hOld, ".evict.lock")) {
		t.Error("the cleaner created the eviction gate")
	}
	if exists(t, filepath.Join(dir, hNew)) {
		t.Error("the torrent nobody holds is still there")
	}
}

// whileAlone's gap: on Linux a pod whose upgrade of .lock failed holds
// nothing on it until it takes it shared again, with the eviction gate held
// exclusive throughout. A torrent with a gate and a .lock that nobody holds
// is dropped.
func TestCleanSkipsADirInAnEviction(t *testing.T) {
	dir := t.TempDir()
	mkTorrent(t, dir, hOld, 48*time.Hour)
	mkTorrent(t, dir, hNew, time.Minute)
	hold(t, filepath.Join(dir, hOld, ".evict.lock"), unix.LOCK_EX)
	for _, h := range []string{hOld, hNew} {
		for _, name := range []string{".lock", ".evict.lock"} {
			if err := os.WriteFile(filepath.Join(dir, h, name), nil, 0o644); err != nil && !os.IsExist(err) {
				t.Fatal(err)
			}
		}
	}

	if got := cleanAll(t, dir); !slices.Equal(got, []string{hNew}) {
		t.Errorf("announced %v, want only %s", got, hNew[:4])
	}
	if !exists(t, filepath.Join(dir, hOld, "d", "f")) {
		t.Error("the torrent in the gap was dropped")
	}
	if exists(t, filepath.Join(dir, hNew)) {
		t.Error("the evicting torrent nobody holds is still there")
	}
}

// Both locks stay exclusive until the directory is gone. A pod that took
// .lock shared in between would open the .torrent.db of files about to be
// unlinked, get them back empty, and serve zeros for the pieces its
// completion says it has.
func TestCleanHoldsTheDirWhileItRemovesIt(t *testing.T) {
	dir := t.TempDir()
	mkTorrent(t, dir, hOld, 48*time.Hour)
	for _, name := range []string{".lock", ".evict.lock"} {
		if err := os.WriteFile(filepath.Join(dir, hOld, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var removed []string
	t.Cleanup(func() { removeAll = os.RemoveAll })
	removeAll = func(path string) error {
		removed = append(removed, path)
		for _, name := range []string{".lock", ".evict.lock"} {
			f, err := os.Open(filepath.Join(path, name))
			if err != nil {
				t.Fatal(err)
			}
			err = unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
			_ = f.Close()
			if err != unix.EWOULDBLOCK {
				t.Errorf("%s is not held exclusive while the directory is removed: flock SH = %v", name, err)
			}
		}
		return os.RemoveAll(path)
	}

	if got := cleanAll(t, dir); !slices.Equal(got, []string{hOld}) {
		t.Errorf("announced %v", got)
	}
	if !slices.Equal(removed, []string{filepath.Join(dir, hOld)}) {
		t.Errorf("removed %v, want only the torrent's directory", removed)
	}
}

// A .touch whose directory is gone is removed, and the pass goes on.
func TestCleanDropsATouchWithoutADir(t *testing.T) {
	dir := t.TempDir()
	mkTorrent(t, dir, hOld, 48*time.Hour)
	mkTorrent(t, dir, hNew, time.Minute)
	if err := os.RemoveAll(filepath.Join(dir, hOld)); err != nil {
		t.Fatal(err)
	}

	if got := cleanAll(t, dir); !slices.Equal(got, []string{hOld, hNew}) {
		t.Errorf("announced %v", got)
	}
	if exists(t, filepath.Join(dir, hOld)) || exists(t, filepath.Join(dir, hOld+".touch")) {
		t.Error("the lone .touch is still there, or its dir came back")
	}
}

// The shard is shared: s3-cache keeps its chunks in <shard>/s3-cache and
// evicts them itself, and ext4 keeps lost+found. Without a .touch both used
// to sort first and go before any torrent; with one, a name that is not a
// hash is no more the cleaner's.
func TestCleanLeavesWhatIsNotATorrent(t *testing.T) {
	dir := t.TempDir()
	mkTorrent(t, dir, hOld, 48*time.Hour)
	mkTorrent(t, dir, "lost+found", 72*time.Hour)
	if err := os.MkdirAll(filepath.Join(dir, "s3-cache", "ab"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := cleanAll(t, dir); !slices.Equal(got, []string{hOld}) {
		t.Errorf("announced %v", got)
	}
	for _, d := range []string{"s3-cache/ab", "lost+found/d/f"} {
		if !exists(t, filepath.Join(dir, d)) {
			t.Errorf("%s was dropped", d)
		}
	}
	if exists(t, filepath.Join(dir, hOld)) {
		t.Error("the torrent is still there")
	}
}
