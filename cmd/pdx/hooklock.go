package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The flag file (spec §6.6) is addressed by agent and session alone, so two
// `pdx lead request` processes of one session — request A closing while
// request B has just been created — share one path. The flag therefore
// carries its request id, and removal is a compare-and-remove under an
// exclusive flock on the flag's inode: A never deletes B's flag. The daemon
// and the hook only stat the path; the content is for the CLI alone.

// openHookLockLocked opens the flag at path and returns it holding
// flock(LOCK_EX), re-opening until the locked inode is the one the path
// names (another process may have removed or recreated the file while this
// one waited for the lock). With create false, a missing file is ErrNotExist.
func openHookLockLocked(path string, create bool) (*os.File, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	for {
		f, err := os.OpenFile(path, flags, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		pi, err := os.Stat(path)
		if err == nil && os.SameFile(fi, pi) {
			return f, nil // locked, and still the file at path
		}
		f.Close() // releases the lock on the stale inode
		if err != nil && !create {
			return nil, err // gone while we waited: nothing to remove
		}
	}
}

// writeHookLock raises the flag at path for request id, making its
// directory first. Best effort: a failure is one stderr line and the
// request goes on with the soft lock only — the hard lock is an extra
// guard, never a reason to refuse a lead request.
func writeHookLock(path, id string, stderr io.Writer) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
		return
	}
	f, err := openHookLockLocked(path, true)
	if err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
		return
	}
	defer f.Close()
	if err := f.Truncate(0); err == nil {
		_, err = f.WriteAt([]byte(id), 0)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx lead: 無法建立硬鎖旗標（%v），這次只有軟鎖\n", err)
	}
}

// removeHookLock lowers the flag at path if it still belongs to request
// id. A file already gone (the daemon removed it with a {} answer), a flag
// another request of the same session has since raised, and any other
// failure are silent: the command is exiting and the daemon's sweeper
// prunes what is left.
func removeHookLock(path, id string) {
	f, err := openHookLockLocked(path, false)
	if err != nil {
		return
	}
	defer f.Close()
	owner, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(bytes.TrimSpace(owner), []byte(id)) {
		return
	}
	_ = os.Remove(path)
}

// hookLockExists is the gate `pdx hook` checks before it calls the daemon
// (spec §6.6): one stat, no daemon round trip when the flag is absent.
func hookLockExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
