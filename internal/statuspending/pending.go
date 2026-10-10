// Package statuspending keeps the statusline payload a `pdx statusline-proxy` could not deliver (the daemon was down), so the daemon
// can catch up at its next boot (#2545). One small file per CC session, only the newest payload; the proxy writes it, the daemon
// reads it back and deletes it. Scaffold: behaviour lands with the tests.
package statuspending

import (
	"errors"

	"github.com/wake/purdex/internal/config"
)

const (
	// Cap is the most files the directory holds; a failed delivery past it is dropped, as it was before this package.
	Cap = 512
	// MaxAge is how old a file may be at boot before it is deleted instead of applied.
	MaxAge = 7 * 24 * 60 * 60 * 1000
)

var (
	ErrUnsafeID = errors.New("statuspending: the session id is not a safe file name")
	ErrFull     = errors.New("statuspending: the pending directory is full")
)

// Entry is one pending payload.
type Entry struct {
	SessionID string
	AtMs      int64
	Raw       []byte
}

// DirFor is THE directory, from the one config both sides read: <data_dir>/statusline-pending.
func DirFor(cfg *config.Config) string { return "" }

// SafeID says whether sid may be a file name.
func SafeID(sid string) bool { return true }

// SessionIDOf is the CC session id inside a statusline payload ("" when there is none).
func SessionIDOf(raw []byte) string { return "" }

// Write keeps raw (taken at atMs) as sid's pending payload, unless a newer one is already there.
func Write(dir string, raw []byte, atMs int64) error { return nil }

// CleanupOnSuccess removes the pending file of raw's session when it is not newer than atMs. It costs one stat when there is no
// directory — the case of every render but the ones after an outage.
func CleanupOnSuccess(dir string, raw []byte, atMs int64) {}

// Load reads every valid entry; a file that is not valid is deleted.
func Load(dir string) ([]Entry, error) { return nil, nil }

// Remove deletes sid's file.
func Remove(dir, sid string) {}
