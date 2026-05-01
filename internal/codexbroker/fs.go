package codexbroker

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FS is the minimal read-only filesystem interface used by the inventory
// scanner. All path operations go through this interface so tests can fake
// EvalSymlinks mappings and the read-only audit can record every access.
//
// Note: P1 is read-only by spec — this interface intentionally exposes ZERO
// mutation methods. Extending this interface to add a mutation method would
// invalidate the AC7 read-only audit (see read_only_audit_test.go) and
// requires a spec amendment.
type FS interface {
	Open(path string) (io.ReadCloser, error)
	Stat(path string) (os.FileInfo, error)
	Lstat(path string) (os.FileInfo, error)
	EvalSymlinks(path string) (string, error)
	Glob(pattern string) ([]string, error)
	ReadDir(path string) ([]os.DirEntry, error)
}

// osFS is the production FS implementation backed by os/filepath.
type osFS struct{}

// NewOsFS returns the production FS implementation.
func NewOsFS() FS { return &osFS{} }

func (osFS) Open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (osFS) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (osFS) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (osFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (osFS) Glob(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}

func (osFS) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// FSCall records a single FS operation. Used by RecordingFS.
type FSCall struct {
	Op   string // "Open" / "Stat" / "Lstat" / "EvalSymlinks" / "Glob" / "ReadDir"
	Path string
}

// RecordingFS wraps another FS and records every method call. Used by Task K
// (read-only audit) to verify the scanner makes no mutation syscalls. The
// underlying FS provides the actual data; the recording layer is purely
// observational.
type RecordingFS struct {
	mu    sync.Mutex
	inner FS
	calls []FSCall
}

// NewRecordingFS returns a recording wrapper around inner.
func NewRecordingFS(inner FS) *RecordingFS {
	return &RecordingFS{inner: inner}
}

// Calls returns a copy of all recorded calls in order.
func (r *RecordingFS) Calls() []FSCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FSCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *RecordingFS) record(op, path string) {
	r.mu.Lock()
	r.calls = append(r.calls, FSCall{Op: op, Path: path})
	r.mu.Unlock()
}

func (r *RecordingFS) Open(path string) (io.ReadCloser, error) {
	r.record("Open", path)
	return r.inner.Open(path)
}

func (r *RecordingFS) Stat(path string) (os.FileInfo, error) {
	r.record("Stat", path)
	return r.inner.Stat(path)
}

func (r *RecordingFS) Lstat(path string) (os.FileInfo, error) {
	r.record("Lstat", path)
	return r.inner.Lstat(path)
}

func (r *RecordingFS) EvalSymlinks(path string) (string, error) {
	r.record("EvalSymlinks", path)
	return r.inner.EvalSymlinks(path)
}

func (r *RecordingFS) Glob(pattern string) ([]string, error) {
	r.record("Glob", pattern)
	return r.inner.Glob(pattern)
}

func (r *RecordingFS) ReadDir(path string) ([]os.DirEntry, error) {
	r.record("ReadDir", path)
	return r.inner.ReadDir(path)
}
