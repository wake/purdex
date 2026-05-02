//go:build linux

package codexbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// platformInitVerifier wires the Linux preferred path: walk /proc/<pid>/fd
// symlinks looking for one whose target equals sockPath. The walk is
// bounded by both the requested timeout and an internal context.
func platformInitVerifier(v *sockVerifierImpl) {
	v.preferred = func(sockPath string, timeout time.Duration) (bool, error) {
		if sockPath == "" {
			return false, nil
		}
		if timeout <= 0 {
			timeout = 1 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		entries, err := os.ReadDir("/proc")
		if err != nil {
			return false, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			// Skip non-numeric names quickly.
			if name == "" || name[0] < '0' || name[0] > '9' {
				continue
			}
			if ctx.Err() != nil {
				return true, ctx.Err() // conservative defer on timeout
			}
			fdDir := filepath.Join("/proc", name, "fd")
			fdEntries, ferr := os.ReadDir(fdDir)
			if ferr != nil {
				// Permission denied / process exited — skip.
				continue
			}
			for _, fdE := range fdEntries {
				link, lerr := os.Readlink(filepath.Join(fdDir, fdE.Name()))
				if lerr != nil {
					continue
				}
				// Linux stringifies socket fds as "socket:[NNN]"; ours is
				// a unix-domain bind that may show up as the path or as
				// the inode form. Match either.
				if link == sockPath || strings.HasSuffix(link, sockPath) {
					return true, nil
				}
			}
		}
		return false, nil
	}
}
