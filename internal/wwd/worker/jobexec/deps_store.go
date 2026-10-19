package jobexec

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// defaultStoreMaxMB bounds a package manager's download cache (pnpm-store and the others in the cache directory), which is kept across
// installs so that an install that has to happen starts from packages this host has fetched, and which the number of installed sets does
// not limit. Over the bound it is emptied before the next install: it is only a cache, and a wrong guess costs a download.
const defaultStoreMaxMB = 4096

func (e *Exec) storeMax() int64 {
	mb := e.cfg.DepsStoreMaxMB
	if mb <= 0 {
		mb = defaultStoreMaxMB
	}
	return int64(mb) << 20
}

// withStore runs an install that uses the download cache at dir. Before it, a cache over max bytes is emptied, but only when no other install
// is using it: every install holds the cache's lock shared while it runs, and the emptying takes it exclusive without waiting (an install
// that finds it busy leaves the cache as it is and is the one that runs; the next one tries again). The cache's lock is a file beside it.
func withStore(dir string, max int64, install func() error) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		if n := dirSize(dir); n > max {
			log.Printf("jobexec: the download cache %s holds %d MB, over %d MB: emptied", dir, n>>20, max>>20)
			if err := os.RemoveAll(dir); err != nil {
				log.Printf("jobexec: emptying %s: %v", dir, err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	return install()
}

// dirSize is the size of the regular files under dir (a link is not followed, and a file that goes away while it is read counts as nothing).
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
