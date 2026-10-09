package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Size-based rotation of the daemon's log (#2163). `pdx start` opens <data_dir>/logs/pdx.log (O_APPEND) and hands it
// to `pdx serve` as stdout and stderr, so nothing trimmed it: 621 MB on mlab. The daemon now watches its own file.
//
// A rotation, in this order, loses no line and leaves no fd on a renamed file:
//  1. rename pdx.log -> pdx.log.rotating (a writer still holding the old fd keeps writing into it: not lost);
//  2. create a fresh pdx.log (O_APPEND) and dup2 it OVER fd 1 and fd 2 — atomic, there is no instant at which the
//     descriptors point nowhere, and the descriptors survive the in-place exec restart like any stdio;
//  3. write the rotation line (into the new file) and compress the old one in the background:
//     pdx.log.rotating -> pdx.log.1.gz, after shifting .1.gz -> .2.gz ... and dropping past `keep`.
//
// It only acts when fd 2 really is the file pdx.log (same device and inode): a daemon run from a terminal, or by
// another launcher, is left alone. A crash during step 3 leaves pdx.log.rotating, which the next start compresses.
const (
	logRotateMaxBytes = 50 << 20 // rotate when pdx.log reaches this
	logRotateKeep     = 5        // compressed generations kept: pdx.log.1.gz ... pdx.log.5.gz
	logRotateEvery    = 30 * time.Second
	logRotateStopWait = 5 * time.Second // how long stop() waits for a compression in flight
)

// rotWriter is the daemon's log output: it writes to the stderr descriptor under a mutex that a rotation also holds
// while it repoints the descriptors. Darwin's dup2 is not atomic towards another thread writing to the descriptor it
// replaces (the kernel closes the target, then reuses it: a write in that window gets EBADF), so every line the
// daemon logs goes through here and none can fall into it.
type rotWriter struct {
	mu sync.Mutex
	fd int
}

func (w *rotWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for n < len(p) {
		m, err := syscall.Write(w.fd, p[n:])
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return n, err
		}
		n += m
	}
	return n, nil
}

type logRotator struct {
	w        *rotWriter // the log package's output, once start() installed it
	path     string     // .../logs/pdx.log
	maxBytes int64
	keep     int
	fds      []int // the descriptors to repoint (stdout, stderr)
	logf     func(format string, args ...any)

	mu          sync.Mutex  // one rotation at a time
	stopped     bool        // under mu: no rotation after stop()
	compressing atomic.Bool // one compression at a time
	compressWG  sync.WaitGroup
}

func newLogRotator(path string, logf func(string, ...any)) *logRotator {
	return &logRotator{w: &rotWriter{fd: 2}, path: path, maxBytes: logRotateMaxBytes, keep: logRotateKeep, fds: []int{1, 2}, logf: logf}
}

// owns says whether the first descriptor is this very file.
func (r *logRotator) owns() bool {
	if len(r.fds) == 0 {
		return false
	}
	var fst, pst syscall.Stat_t
	if syscall.Fstat(r.fds[0], &fst) != nil || syscall.Stat(r.path, &pst) != nil {
		return false
	}
	return fst.Dev == pst.Dev && fst.Ino == pst.Ino
}

// check rotates when the file has reached maxBytes. It returns whether it rotated.
func (r *logRotator) check() bool {
	fi, err := os.Stat(r.path)
	if err != nil || fi.Size() < r.maxBytes || !r.owns() {
		return false
	}
	return r.rotate() == nil
}

func (r *logRotator) rotate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return fmt.Errorf("stopped")
	}
	tmp := r.path + ".rotating"
	if _, err := os.Stat(tmp); err == nil {
		// the previous compression has not finished (or was cut short): compress it first, rotate on the next check
		r.compressAsync()
		return fmt.Errorf("%s still exists", tmp)
	}
	fi, err := os.Stat(r.path)
	if err != nil {
		return err
	}
	size := fi.Size()
	if err := os.Rename(r.path, tmp); err != nil {
		r.logf("log rotation: rename: %v", err)
		return err
	}
	nf, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = os.Rename(tmp, r.path) // put it back: the descriptors still point at it
		r.logf("log rotation: create: %v", err)
		return err
	}
	nfd := int(nf.Fd())
	// The descriptors are switched as ONE commit: the originals are saved first, and if any dup2 fails every one is put
	// back, the old file gets its name again and nothing is compressed — no descriptor is left on a file about to go.
	saved := make([]int, len(r.fds))
	for i, fd := range r.fds {
		d, err := syscall.Dup(fd)
		if err != nil {
			for _, s := range saved[:i] {
				_ = syscall.Close(s)
			}
			_ = nf.Close()
			_ = os.Rename(tmp, r.path)
			r.logf("log rotation: save fd %d: %v", fd, err)
			return err
		}
		saved[i] = d
	}
	var failed error
	r.w.mu.Lock() // no logged line is in flight while the descriptors are replaced
	for i, fd := range r.fds {
		if err := syscall.Dup2(nfd, fd); err != nil {
			failed = fmt.Errorf("dup2 onto fd %d: %w", fd, err)
			for k := 0; k < i; k++ { // put back the ones already switched
				_ = syscall.Dup2(saved[k], r.fds[k])
			}
			break
		}
	}
	r.w.mu.Unlock()
	for _, d := range saved {
		_ = syscall.Close(d)
	}
	_ = nf.Close()
	if failed != nil {
		_ = os.Rename(tmp, r.path) // replaces the empty new file: the descriptors point at this one again
		r.logf("log rotation: %v; not rotated", failed)
		return failed
	}
	r.logf("log rotation: %s reached %d bytes; rotated to %s.1.gz, keeping %d", filepath.Base(r.path), size, filepath.Base(r.path), r.keep)
	r.compressAsync()
	return nil
}

// compressAsync turns pdx.log.rotating into pdx.log.1.gz in the background.
func (r *logRotator) compressAsync() {
	if !r.compressing.CompareAndSwap(false, true) {
		return // one is running
	}
	r.compressWG.Add(1)
	go func() {
		defer r.compressWG.Done()
		defer r.compressing.Store(false)
		if err := r.compress(); err != nil {
			r.logf("log rotation: compress: %v", err)
		}
	}()
}

func (r *logRotator) gz(n int) string { return fmt.Sprintf("%s.%d.gz", r.path, n) }

func (r *logRotator) compress() error {
	tmp := r.path + ".rotating"
	src, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer src.Close()
	// Compress to a part file FIRST: the retained generations are not touched until a complete .1.gz exists, so a
	// failure (a full disk, a crash, an exec) loses nothing and a retry starts from the same state.
	part := r.gz(1) + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	_, cerr := io.Copy(zw, src)
	if err := zw.Close(); cerr == nil {
		cerr = err
	}
	if err := out.Sync(); cerr == nil {
		cerr = err
	}
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(part)
		return cerr
	}
	// then shift the older generations up (the last falls off) and commit the new one
	_ = os.Remove(r.gz(r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		if err := os.Rename(r.gz(i), r.gz(i+1)); err != nil && !os.IsNotExist(err) {
			_ = os.Remove(part)
			return err
		}
	}
	if err := os.Rename(part, r.gz(1)); err != nil {
		return err
	}
	return os.Remove(tmp)
}

// start checks once now (and finishes a compression a crash interrupted), then every logRotateEvery until stop.
func (r *logRotator) start() (stop func()) {
	if r.owns() { // the log package writes through the rotation-aware writer, only when the file is ours to rotate
		log.SetOutput(r.w)
	}
	if _, err := os.Stat(r.path + ".rotating"); err == nil {
		r.mu.Lock()
		r.compressAsync()
		r.mu.Unlock()
	}
	r.check()
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(logRotateEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				r.check()
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			r.mu.Lock() // no rotation starts after this
			r.stopped = true
			r.mu.Unlock()
			// a compression in flight finishes before the process execs itself (bounded: it is idempotent to retry)
			wait := make(chan struct{})
			go func() { r.compressWG.Wait(); close(wait) }()
			select {
			case <-wait:
			case <-time.After(logRotateStopWait):
			}
		})
	}
}
