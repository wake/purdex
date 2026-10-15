package hostmem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The pressure cache: <dir>/pressure.json holds the last few readings of the host's memory (level and paging counters), so that
// the gates of many commands, and the daemon while it runs, read the host once between them. Whoever writes it holds
// <dir>/pressure.lock, and replaces the file by a rename, so a reader never sees half of one. The reader's reuse window is the
// reader's own business (the gate reuses a reading younger than 5 s whoever wrote it).

const (
	// KeepFor is how far back the cache keeps readings: three rate windows.
	KeepFor = 3 * RateWindow
	// KeepMax is how many readings the cache keeps.
	KeepMax = 16
)

// Sample is one reading in the cache file.
type Sample struct {
	At       int64  `json:"at"` // unix ms
	Level    int    `json:"level"`
	Pageouts uint64 `json:"pageouts"`
	Swapouts uint64 `json:"swapouts"`
}

// File is the cache file.
type File struct {
	Samples []Sample `json:"samples"`
}

// Cache is the pressure cache of a gate directory.
type Cache struct{ Dir string }

// Path is the cache file.
func (c Cache) Path() string { return filepath.Join(c.Dir, "pressure.json") }

// Load reads the cache. A file that is missing or torn is no readings; so is one whose readings cannot be told in time
// (an at of 0 or less), which is dropped sample by sample.
func (c Cache) Load() File {
	var f File
	b, err := os.ReadFile(c.Path())
	if err != nil || json.Unmarshal(b, &f) != nil {
		return File{}
	}
	keep := f.Samples[:0]
	for _, s := range f.Samples {
		if s.At > 0 {
			keep = append(keep, s)
		}
	}
	f.Samples = keep
	return f
}

// TryLock takes the one-writer lock without waiting; got is false when another process is sampling. A lock that cannot be had
// at all (a directory that cannot be written, a file system that cannot lock) lets the caller sample alone.
func (c Cache) TryLock() (unlock func(), got bool) {
	f, err := os.OpenFile(filepath.Join(c.Dir, "pressure.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}, true
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { _ = f.Close() }, true
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			_ = f.Close()
			return func() {}, false
		}
		_ = f.Close()
		return func() {}, true
	}
}

// Append adds cur to the cache, dropping the readings that are too old or newer than cur, and returns what the file now holds
// and whether it was written. The caller holds the lock (TryLock). A file that cannot be written leaves the cache as it was:
// the readings are still returned, for the caller's own use, and written is false.
func (c Cache) Append(now time.Time, cur Sample) (readings []Sample, written bool) {
	keep := []Sample{}
	for _, s := range c.Load().Samples {
		if now.Sub(time.UnixMilli(s.At)) <= KeepFor && s.At <= cur.At {
			keep = append(keep, s)
		}
	}
	keep = append(keep, cur)
	if len(keep) > KeepMax {
		keep = keep[len(keep)-KeepMax:]
	}
	if b, err := json.Marshal(File{Samples: keep}); err == nil {
		if tf, err := os.CreateTemp(c.Dir, "pressure.json.*"); err == nil {
			_, werr := tf.Write(b)
			cerr := tf.Close()
			if werr != nil || cerr != nil || os.Rename(tf.Name(), c.Path()) != nil {
				_ = os.Remove(tf.Name())
			} else {
				written = true
			}
		}
	}
	return keep, written
}

// Record is what the daemon does with each reading it takes: it adds it to the cache when no one else is writing, and gives it
// up when someone is (a gate that is sampling at that moment has a reading as new). It reports whether the reading was
// written. The gate directory is made if it is not there.
func (c Cache) Record(now time.Time, cur Sample) bool {
	if os.MkdirAll(c.Dir, 0o755) != nil {
		return false
	}
	unlock, got := c.TryLock()
	defer unlock()
	if !got {
		return false
	}
	if have := c.Load().Samples; len(have) > 0 && have[len(have)-1].At >= cur.At {
		return false // a reading as new is there already (a gate sampled meanwhile): the cache never goes back in time
	}
	_, written := c.Append(now, cur)
	return written
}
