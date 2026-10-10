// Package statuspending keeps the statusline payload a `pdx statusline-proxy` could not deliver (the daemon was down), so the daemon
// can catch up at its next boot (#2545). One small file per CC session, only the newest payload; the proxy writes it, the daemon
// reads it back and deletes it. The file name is the CC session id, so it is accepted only when it cannot leave the directory.
package statuspending

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
)

const (
	// Cap is the most files the directory holds; a failed delivery past it is dropped, as it was before this package.
	Cap = 512
	// MaxAge (ms) is how old a file may be at boot before it is deleted instead of applied.
	MaxAge = 7 * 24 * 60 * 60 * 1000

	dirName = "statusline-pending"
	ext     = ".json"
	tmpTag  = ".tmp-"
)

var (
	ErrUnsafeID = errors.New("statuspending: the session id is not a safe file name")
	ErrFull     = errors.New("statuspending: the pending directory is full")
	ErrBusy     = errors.New("statuspending: the pending directory is locked by another process")
	ErrTooBig   = errors.New("statuspending: the payload is over the size bound")
)

// MaxFileBytes bounds one file (a statusline payload is a few KB).
const MaxFileBytes = 256 << 10

const lockName = ".lock"

// testHook is a test seam: called at named points inside the locked steps.
var testHook func(point string)

// RemoveIfNotNewer deletes sid's file unless it is newer than atMs (the version the caller loaded). Scaffold.
func RemoveIfNotNewer(dir, sid string, atMs int64) {}

// safeIDRE is the alphabet a session id may have as a file name: no separator, no dot, nothing that hides or climbs.
var safeIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Entry is one pending payload.
type Entry struct {
	SessionID string
	AtMs      int64
	Raw       []byte
}

// file is the on-disk shape: the payload exactly as the proxy received it, and when (ms, the proxy's clock — the same host's
// clock as the daemon's) that render's JSON was read.
type file struct {
	AtMs      int64           `json:"at_ms"`
	RawStatus json.RawMessage `json:"raw_status"`
}

// DirFor is THE directory, from the one config both sides read: <data_dir>/statusline-pending. "" when there is no config.
func DirFor(cfg *config.Config) string {
	if cfg == nil || cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(cfg.DataDir, dirName)
}

// SafeID says whether sid may be a file name.
func SafeID(sid string) bool { return safeIDRE.MatchString(sid) }

// SessionIDOf is the CC session id inside a statusline payload ("" when there is none).
func SessionIDOf(raw []byte) string {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return p.SessionID
}

func read(path string) (file, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return file{}, false
	}
	var f file
	if json.Unmarshal(b, &f) != nil || len(f.RawStatus) == 0 {
		return file{}, false
	}
	return f, true
}

// Write keeps raw (taken at atMs) as its session's pending payload, unless a newer one is already there (two render processes
// of one session can finish in either order). Atomic: a temp file in the same directory, renamed over. Private: 0700 / 0600.
func Write(dir string, raw []byte, atMs int64) error {
	sid := SessionIDOf(raw)
	if dir == "" || !SafeID(sid) {
		return ErrUnsafeID
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700) // a directory made earlier under a looser umask
	path := filepath.Join(dir, sid+ext)
	if cur, ok := read(path); ok {
		if cur.AtMs >= atMs {
			return nil
		}
	} else if entries, err := os.ReadDir(dir); err == nil && countFiles(entries) >= Cap {
		return ErrFull
	}
	body, err := json.Marshal(file{AtMs: atMs, RawStatus: bytes.TrimSpace(raw)})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpTag+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func countFiles(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ext) {
			n++
		}
	}
	return n
}

// CleanupOnSuccess removes the pending file of raw's session when it is not newer than atMs (a delivery at atMs carries at least
// what the file held; a file from a LATER failed render must stay). It costs one stat when there is no directory — the case of
// every render but the ones after an outage — and parses the payload only when there is one.
func CleanupOnSuccess(dir string, raw []byte, atMs int64) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	sid := SessionIDOf(raw)
	if !SafeID(sid) {
		return
	}
	path := filepath.Join(dir, sid+ext)
	if cur, ok := read(path); ok && cur.AtMs > atMs {
		return
	}
	_ = os.Remove(path)
}

// Load reads every valid entry; a file that is not valid (not an entry, or named for another session) is deleted, and so is a
// temp file a crashed writer left (older than an hour). A missing directory is empty.
func Load(dir string) ([]Entry, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		if strings.HasPrefix(name, tmpTag) {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(path)
			}
			continue
		}
		if !strings.HasSuffix(name, ext) {
			continue
		}
		f, ok := read(path)
		sid := strings.TrimSuffix(name, ext)
		if !ok || !SafeID(sid) || SessionIDOf(f.RawStatus) != sid {
			_ = os.Remove(path)
			continue
		}
		out = append(out, Entry{SessionID: sid, AtMs: f.AtMs, Raw: f.RawStatus})
	}
	return out, nil
}

// Remove deletes sid's file.
func Remove(dir, sid string) {
	if dir == "" || !SafeID(sid) {
		return
	}
	_ = os.Remove(filepath.Join(dir, sid+ext))
}
