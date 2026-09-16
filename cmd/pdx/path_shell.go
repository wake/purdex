package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// --- pdx path add-to-shell (spec §3.3) -----------------------------------

// The block is written with markers so a later run recognises its own work.
const (
	pathBlockStart   = "# >>> pdx path >>>"
	pathBlockEnd     = "# <<< pdx path <<<"
	pathExportLine   = `export PATH="$HOME/.local/bin:$PATH"`
	pathBackupSuffix = ".pdx-backup"
)

// pathBlock is the exact text written to the rc file, newline-terminated.
func pathBlock() string {
	return pathBlockStart + "\n" + pathExportLine + "\n" + pathBlockEnd + "\n"
}

// rcFileFor picks the rc file from $SHELL's basename. Anything else — fish,
// an unset $SHELL in a packaged app — is refused rather than guessed at:
// guessing a config format for a shell we did not recognise is how a config
// file gets corrupted, and fish does not even use `export` (spec §3.3).
func rcFileFor(env pathEnv) (string, bool) {
	switch filepath.Base(env.shell) {
	case "zsh":
		return filepath.Join(env.home, ".zshrc"), true
	case "bash":
		// The usual login-vs-interactive split.
		if env.goos == "darwin" {
			return filepath.Join(env.home, ".bash_profile"), true
		}
		return filepath.Join(env.home, ".bashrc"), true
	}
	return "", false
}

func runPathAddToShell(env pathEnv, dryRun bool, stdout, stderr io.Writer) int {
	rcPath, ok := rcFileFor(env)
	if !ok {
		shown := env.shell
		if shown == "" {
			shown = "(unset)"
		}
		fmt.Fprintf(stderr, "pdx path add-to-shell: unrecognised shell %s — refusing to guess its config format.\n", shown)
		printManualBlock(stderr)
		return 1
	}

	// Resolve the rc file *before* anything else: when it is a symlink — the
	// normal shape under any dotfiles manager — the temp-file-plus-rename
	// below would replace the symlink with a regular file, quietly detaching
	// the user's shell config from the repository that manages it (spec §3.3).
	writePath, err := resolveRcTarget(env, rcPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: %v\n", err)
		printManualBlock(stderr)
		return 1
	}

	if dryRun {
		// No lock, and no ~/.local/bin created: a dry run writes nothing at
		// all, directories included.
		fmt.Fprintf(stdout, "would edit %s\n", writePath)
		if writePath != rcPath {
			fmt.Fprintf(stdout, "(%s is a symlink; its target is what would be edited)\n", rcPath)
		}
		if body, err := os.ReadFile(writePath); err == nil && strings.Contains(string(body), pathBlockStart) {
			fmt.Fprintf(stdout, "(the block is already present; a real run would write nothing)\n")
		}
		fmt.Fprintf(stdout, "\n%s\n", pathBlock())
		fmt.Fprintln(stdout, "Nothing was written (--dry-run).")
		return 0
	}

	if env.beforeLock != nil {
		env.beforeLock()
	}

	// One lock, shared with `link` (spec §3.2). Every decision below is made
	// inside it against a fresh read, so two concurrent runs produce one
	// block and a run whose earlier read went stale cannot write a version
	// that drops someone else's edit — it never uses an earlier read.
	lock, err := acquirePathLock(env.localBinDir(), env.lock)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: %v\n", err)
		return 1
	}
	defer lock.release()
	if env.afterLock != nil {
		env.afterLock()
	}

	content, mode, existed, err := readRcFile(writePath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: cannot read %s: %v\n", writePath, err)
		return 1
	}

	if strings.Contains(content, pathBlockStart) {
		fmt.Fprintf(stdout, "%s already contains the pdx path block; nothing to do.\n", writePath)
		fmt.Fprintln(stdout, "If pdx is still not found, open a new terminal, then run `pdx path`.")
		return 0
	}
	// The goal is a working PATH, not a block in a file.
	if pathContainsDir(env.path, env.localBinDir()) {
		fmt.Fprintf(stdout, "%s is already on PATH; nothing to do.\n", env.localBinDir())
		return 0
	}

	if existed {
		if err := backupOnce(writePath, content, mode); err != nil {
			fmt.Fprintf(stderr, "pdx path add-to-shell: cannot back up %s: %v\n", writePath, err)
			return 1
		}
	}

	next := content
	// Appending straight onto a file whose last line has no newline would
	// splice the marker comment onto the end of a live shell command.
	if len(next) > 0 && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += pathBlock()

	if err := writeFileAtomically(writePath, []byte(next), mode); err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: cannot write %s: %v\n", writePath, err)
		return 1
	}

	fmt.Fprintf(stdout, "added the pdx path block to %s\n", writePath)
	if existed {
		fmt.Fprintf(stdout, "a copy of the previous file is at %s\n", writePath+pathBackupSuffix)
	}
	// A child process cannot change its parent's environment, so saying
	// anything else here would be a lie.
	fmt.Fprintln(stdout, "Open a new terminal, then run `pdx path` to confirm.")
	return 0
}

func printManualBlock(w io.Writer) {
	fmt.Fprintln(w, "Add this to your shell's startup file yourself:")
	fmt.Fprintf(w, "\n%s\n", pathBlock())
}

// resolveRcTarget returns the file that should actually be written: the rc
// path itself, or — when that is a symlink — its target, so the link survives
// (spec §3.3). A symlink that does not resolve, or resolves outside $HOME, is
// refused: editing a file somewhere else in the filesystem on the strength of
// a link is not something this command should decide to do on its own.
func resolveRcTarget(env pathEnv, rcPath string) (string, error) {
	fi, err := os.Lstat(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return rcPath, nil
		}
		return "", fmt.Errorf("cannot inspect %s: %w", rcPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		if fi.IsDir() {
			return "", fmt.Errorf("%s is a directory", rcPath)
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", rcPath)
		}
		return rcPath, nil
	}
	target, evalErr := filepath.EvalSymlinks(rcPath)
	if evalErr != nil {
		raw, _ := os.Readlink(rcPath)
		return "", fmt.Errorf("%s is a symlink to %s, which does not resolve", rcPath, raw)
	}
	if !underDir(env.home, target) {
		return "", fmt.Errorf("%s is a symlink to %s, which is outside %s", rcPath, target, env.home)
	}
	return target, nil
}

// underDir reports whether target sits inside dir, comparing both the given
// and the symlink-resolved form of dir: on macOS a $HOME under /var is really
// under /private/var, and comparing a resolved path against an unresolved one
// would refuse a perfectly ordinary dotfiles layout.
func underDir(dir, target string) bool {
	for _, base := range []string{dir, evalOrKeep(dir)} {
		if base == "" {
			continue
		}
		if target == base || strings.HasPrefix(target, base+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// readRcFile reads the file to be edited. A missing file is not an error: it
// will be created 0644.
func readRcFile(p string) (content string, mode os.FileMode, existed bool, err error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0o644, false, nil
		}
		return "", 0, false, err
	}
	mode = os.FileMode(0o644)
	if fi, statErr := os.Stat(p); statErr == nil {
		mode = fi.Mode().Perm()
	}
	return string(b), mode, true, nil
}

// backupOnce copies the file to <file>.pdx-backup. An existing backup is
// never overwritten, so the copy always represents the state before pdx ever
// touched the file.
func backupOnce(p, content string, mode os.FileMode) error {
	backup := p + pathBackupSuffix
	if _, err := os.Lstat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(backup, []byte(content), mode)
}

// writeFileAtomically writes via a temp file in the same directory plus a
// rename, so an interrupted run cannot truncate a shell config.
func writeFileAtomically(p string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(p)
	f, err := os.CreateTemp(dir, ".pdx-path-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename has succeeded

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
