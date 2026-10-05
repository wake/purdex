// cmd/pdx/lastshutdown.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/core"
)

const lastShutdownFile = "last-shutdown.json"

type lastShutdownJSON struct {
	At     time.Time `json:"at"`
	Errors []string  `json:"errors"`
}

// writeLastShutdown records a restart's cleanup errors for the next image
// (spec D13): tmp file 0600, then rename.
func writeLastShutdown(dataDir string, errs []string, now time.Time) error {
	data, err := json.Marshal(lastShutdownJSON{At: now.UTC(), Errors: errs})
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, lastShutdownFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// takeLastShutdown reads and deletes the record. Missing → nil, nil.
// Unparsable → deleted, error.
func takeLastShutdown(dataDir string) (*core.ShutdownReport, error) {
	path := filepath.Join(dataDir, lastShutdownFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// Consume it anyway so it is not reported on every boot; a directory
		// squatting on the name would also break writeLastShutdown's rename.
		if fi, serr := os.Lstat(path); serr == nil && fi.IsDir() {
			os.RemoveAll(path)
		} else {
			os.Remove(path)
		}
		return nil, err
	}
	// Consume it whatever it holds: a record must be reported at most once.
	rmErr := os.Remove(path)
	var rec lastShutdownJSON
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if len(rec.Errors) == 0 {
		// A record exists only when there were errors.
		return nil, errors.New("record has no errors")
	}
	rep := &core.ShutdownReport{At: rec.At, Errors: rec.Errors}
	if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		// Report is still valid; the caller logs the error and keeps it.
		return rep, fmt.Errorf("remove record: %w", rmErr)
	}
	return rep, nil
}
