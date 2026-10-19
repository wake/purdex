package jobexec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// defaultStoreMaxMB bounds a package manager's download cache (pnpm-store and the others in the cache directory), which is kept across
// installs so that an install that has to happen starts from packages this host has fetched, and which the number of installed sets does
// not limit. Over the bound it is emptied before the next install: it is only a cache, and a wrong guess costs a download.
const defaultStoreMaxMB = 4096

// storeScanLimit is how many entries (files and directories) are looked at before a download cache is taken to be over its bound: an
// install script can fill the cache it may write with many small files, which a bound in bytes would not see and a walk would not end.
var storeScanLimit = 500_000

func (e *Exec) storeMax() int64 {
	mb := e.cfg.DepsStoreMaxMB
	if mb <= 0 {
		mb = defaultStoreMaxMB
	}
	return int64(mb) << 20
}

// withStore runs an install that uses the download cache at dir. Before it, a cache over max bytes (or over storeScanLimit entries) is
// emptied, but only when no other install is using it: every install holds the cache's lock shared while it runs, and the emptying takes it
// exclusive without waiting (an install that finds it busy leaves the cache as it is and is the one that runs; the next one tries again).
// The cache's lock is a file beside it. The look at the cache ends as soon as it is over a bound, and when ctx ends.
func withStore(ctx context.Context, dir string, max int64, install func() error) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		big, err := storeTooBig(ctx, dir, max)
		if err != nil {
			return err
		}
		if big {
			// Moved aside in one step and made again, so that what holds the lock and the install behind it do not wait for the removal of
			// however many entries there are: that is done after the lock is let go, in the background (and by the next install, if this
			// process ends first).
			aside := fmt.Sprintf("%s.old-%d-%d", dir, os.Getpid(), time.Now().UnixNano())
			log.Printf("jobexec: the download cache %s is over its bound (%d MB or %d entries): emptied", dir, max>>20, storeScanLimit)
			if err := os.Rename(dir, aside); err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			defer func() { go removeAsideFn(aside) }()
		}
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	sweepAside(dir)
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	return install()
}

// removeAsideFn is removeAside; a test replaces it to show that nothing waits for the removal.
var removeAsideFn = removeAside

// removeAside removes a download cache that was moved aside; a failure is only logged (the next install tries again).
func removeAside(path string) {
	if err := os.RemoveAll(path); err != nil {
		log.Printf("jobexec: removing %s: %v", path, err)
	}
}

// sweepAside removes, in the background, what an earlier emptying of the cache at dir left: the directories moved aside that a process which
// ended before it finished removing them did not remove. One that is being removed by this or another process just now is the same
// removal twice, which is harmless.
func sweepAside(dir string) {
	left, _ := filepath.Glob(dir + ".old-*")
	for _, p := range left {
		go removeAsideFn(p)
	}
}

var errOverBound = errors.New("over the bound")

// storeTooBig says whether the regular files under dir add up to more than max bytes, or there are more than storeScanLimit entries. The
// walk stops at the first bound it passes; a link is not followed, and a file that goes away while it is read counts as nothing. An error
// is only that ctx ended.
func storeTooBig(ctx context.Context, dir string, max int64) (bool, error) {
	var n int64
	count := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil
		}
		if count++; count > storeScanLimit {
			return errOverBound
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				if n += info.Size(); n > max {
					return errOverBound
				}
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errOverBound):
		return true, nil
	case err != nil:
		return false, err
	}
	return false, nil
}
