// cmd/pdx/lastshutdown.go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/core"
)

const lastShutdownFile = "last-shutdown.json"

type lastShutdownJSON struct {
	At     time.Time `json:"at"`
	Errors []string  `json:"errors"`
}

// writeLastShutdown records a restart's cleanup errors for the next image
// (spec D13): an exclusively-created 0600 tmp file (never a predictable name,
// so a planted symlink or leftover file cannot be followed or inherited),
// then rename, which replaces rather than follows a symlink at the destination.
func writeLastShutdown(dataDir string, errs []string, now time.Time) (err error) {
	data, err := json.Marshal(lastShutdownJSON{At: now.UTC(), Errors: errs})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir, "last-shutdown-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	closed := false
	defer func() {
		if err != nil {
			if !closed {
				f.Close()
			}
			os.Remove(name)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	closed = true
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dataDir, lastShutdownFile))
}

// takeLastShutdown consumes the record and returns it. Missing → nil, nil.
// The record is consumed by renaming it aside before it is read, so a report
// is only ever published for a record that can no longer be re-reported.
// A removal failure of the consumed copy is returned alongside the report.
func takeLastShutdown(dataDir string) (*core.ShutdownReport, error) {
	path := filepath.Join(dataDir, lastShutdownFile)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat record: %w", err)
	}
	if !fi.Mode().IsRegular() {
		// Never delete recursively: os.Remove drops a symlink (not its target)
		// or an empty directory, and fails on a non-empty one.
		kind := "non-regular file"
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		case fi.IsDir():
			kind = "directory"
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, fmt.Errorf("record slot is a %s; left in place: %w", kind, rerr)
		}
		return nil, fmt.Errorf("record slot was a %s; removed", kind)
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, fmt.Errorf("consume record: %w", err)
	}
	// Unpredictable per-take name: nothing can squat on it in advance.
	// A leftover from a crash between rename and remove is never read again.
	consumed := path + ".consumed-" + hex.EncodeToString(rnd[:])
	if err := os.Rename(path, consumed); err != nil {
		return nil, fmt.Errorf("consume record: %w", err)
	}
	data, rerr := readConsumed(consumed)
	var rmErr error
	if rerr != nil && data != nil {
		// read succeeded; only the removal failed
		rmErr, rerr = rerr, nil
	}
	if rerr != nil {
		return nil, fmt.Errorf("read record: %w", rerr)
	}
	var rec lastShutdownJSON
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, errors.Join(err, rmErr)
	}
	if len(rec.Errors) == 0 {
		// A record exists only when there were errors.
		return nil, errors.Join(errors.New("record has no errors"), rmErr)
	}
	return &core.ShutdownReport{At: rec.At, Errors: rec.Errors}, rmErr
}

// readConsumed reads the already-renamed record without following a symlink
// and removes the entry (os.Remove drops a link, never its target). On a read
// failure it returns nil data. If only the removal fails it returns the data
// together with that error.
func readConsumed(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	data, rerr := io.ReadAll(f)
	f.Close()
	if rerr != nil {
		os.Remove(path)
		return nil, rerr
	}
	if data == nil {
		data = []byte{}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return data, fmt.Errorf("remove consumed record: %w", err)
	}
	return data, nil
}
