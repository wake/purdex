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
// Missing files are not errors (the common case after the first run, so it is
// idempotent and quiet). Only regular files are removed: the entry is checked
// with Lstat, so a symlink (which could point outside dataDir) or a directory
// with one of these names is left alone and logged. One log line per removed
// file; failures are logged and never stop the daemon.
func removeLegacyDataFiles(dataDir string, logf func(format string, args ...any)) {
	for _, name := range legacyDataFiles {
		path := filepath.Join(dataDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			logf("legacy data: stat %s: %v", path, err)
			continue
		}
		if !info.Mode().IsRegular() {
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
