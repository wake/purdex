package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// legacyDataFiles are the SQLite files of modules that no longer exist (#1303):
// the old Sync module's sync.db (removed in P4a) and the device-state module's
// device_state.db (removed in P4b), each with its WAL / shared-memory siblings.
// Nothing opens them any more. Exact names only — never a pattern.
var legacyDataFiles = []string{
	"sync.db", "sync.db-wal", "sync.db-shm",
	"device_state.db", "device_state.db-wal", "device_state.db-shm",
}

// removeLegacyDataFiles deletes legacyDataFiles from dataDir at startup.
//
// The directory is listed and only an entry whose Name() is byte-equal to a
// listed name is touched: on a case-insensitive volume (the macOS default) a
// path lookup of <dataDir>/sync.db would also resolve to a user's SYNC.DB.
// Missing files are not errors (the common case after the first run, so it is
// idempotent and quiet). Only regular files are removed: both the DirEntry type
// and an Lstat of the same name must say so, so a symlink (which could point
// outside dataDir) or a directory with one of these names is left alone and
// logged. One log line per removed file; failures never stop the daemon.
func removeLegacyDataFiles(dataDir string, logf func(format string, args ...any)) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logf("legacy data: list %s: %v", dataDir, err)
		}
		return
	}
	present := make(map[string]fs.DirEntry, len(entries))
	for _, e := range entries {
		present[e.Name()] = e
	}
	for _, name := range legacyDataFiles {
		entry, ok := present[name] // byte-equal names only
		if !ok {
			continue
		}
		path := filepath.Join(dataDir, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			logf("legacy data: stat %s: %v", path, err)
			continue
		}
		if !entry.Type().IsRegular() || !info.Mode().IsRegular() {
			logf("legacy data: left %s alone (not a regular file: %s)", path, info.Mode().Type())
			continue
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				logf("legacy data: remove %s: %v", path, err)
			}
			continue
		}
		logf("removed legacy data file %s", path)
	}
}
