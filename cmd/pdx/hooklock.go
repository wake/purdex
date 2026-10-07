package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeHookLock creates the empty flag file at path (spec §6.6), making
// its directory first. Best effort: a failure is one stderr line and the
// request goes on with the soft lock only — the hard lock is an extra
// guard, never a reason to refuse a lead request.
func writeHookLock(path string, stderr io.Writer) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
		return
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
	}
}

// removeHookLock deletes the flag. A file already gone (the daemon removed
// it with a {} answer) and any other failure are silent: the command is
// exiting and the daemon's sweeper prunes what is left.
func removeHookLock(path string) { _ = os.Remove(path) }

// hookLockExists is the gate `pdx hook` checks before it calls the daemon
// (spec §6.6): one stat, no daemon round trip when the flag is absent.
func hookLockExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
