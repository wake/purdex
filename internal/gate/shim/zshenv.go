package shim

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	blockBegin = "# >>> pdx gate >>>"
	blockEnd   = "# <<< pdx gate <<<"
)

// Block is the text `pdx setup` keeps in ~/.zshenv for shimDir: it puts the directory in front of PATH unless PATH has it
// already (so a nested `zsh -c` does not grow PATH), and prints nothing.
func Block(shimDir string) string {
	q := shQuoteInner(shimDir)
	return blockBegin + "\n" +
		`case ":$PATH:" in *":` + q + `:"*) ;; *) PATH="` + q + `:$PATH" ;; esac` + "\n" +
		blockEnd + "\n"
}

// bodyLine is what an old, half-written block's body line looks like (an unclosed begin marker followed by it).
var bodyLine = regexp.MustCompile(`^case ":\$PATH:" in \*":.*:"\*\) ;; \*\) PATH=".*:\$PATH" ;; esac$`)

// Strip removes every pdx gate block from s (several, from an old double install, too) and returns the rest untouched, byte for
// byte. A begin marker with no end marker is repaired: the marker line and the block's own body line right after it go, and
// nothing else (the user's lines below are not part of the block). An end marker without a begin is left alone.
func Strip(s string) string {
	lines := strings.SplitAfter(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimRight(line, "\r\n") != blockBegin {
			out = append(out, line)
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimRight(lines[j], "\r\n")
			if t == blockEnd {
				end = j
				break
			}
			if t == blockBegin {
				break // a second begin before any end: the first is unclosed
			}
		}
		if end >= 0 {
			i = end
			continue
		}
		// unclosed: drop the marker and the body line that follows it, if it is ours
		if i+1 < len(lines) && bodyLine.MatchString(strings.TrimRight(lines[i+1], "\r\n")) {
			i++
		}
	}
	return strings.Join(out, "")
}

// WithBlock is s with exactly one block for shimDir at its end (any earlier ones removed first). The text before the block is
// kept as it was; a missing final newline is added before the block.
func WithBlock(s, shimDir string) string {
	s = Strip(s)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s + Block(shimDir)
}

// writeThrough replaces the file at path with data, in the file's own mode, through a rename in its directory. A symlink at
// path (a dotfiles repo) is followed: the link stays a link and its target is replaced.
func writeThrough(path string, data []byte, defaultMode os.FileMode) error {
	target := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		target = r
	}
	mode := defaultMode
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".pdx-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// isSymlink is whether path is itself a symbolic link (a dotfiles repo's): such a file is emptied, never deleted, so the link
// and the file it points to stay where the user put them.
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// readIfExists is the file's content, or "" when there is no such file.
func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(b), err
}

// Install writes the shims (for pdxPath) into DirIn(home) and keeps one block in home/.zshenv. It is idempotent: a second run
// changes no byte. Files it would not change are not rewritten.
func Install(home, pdxPath string) error {
	dir := DirIn(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("shim dir: %w", err)
	}
	for _, tool := range Tools {
		want := []byte(Script(tool, pdxPath))
		p := filepath.Join(dir, tool)
		if have, err := os.ReadFile(p); err == nil && bytes.Equal(have, want) {
			if fi, err := os.Stat(p); err == nil && fi.Mode().Perm() == 0o755 {
				continue
			}
		}
		if err := writeThrough(p, want, 0o755); err != nil {
			return fmt.Errorf("shim %s: %w", tool, err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			return fmt.Errorf("shim %s: %w", tool, err)
		}
	}
	zshenv := filepath.Join(home, ".zshenv")
	cur, err := readIfExists(zshenv)
	if err != nil {
		return fmt.Errorf(".zshenv: %w", err)
	}
	next := WithBlock(cur, dir)
	if next == cur {
		return nil
	}
	if err := writeThrough(zshenv, []byte(next), 0o644); err != nil {
		return fmt.Errorf(".zshenv: %w", err)
	}
	return nil
}

// Uninstall removes the block from home/.zshenv exactly (the rest of the file is kept; a file left empty by it is removed only
// if it was ours alone, i.e. it held nothing but the block) and the shims it wrote.
func Uninstall(home string) error {
	zshenv := filepath.Join(home, ".zshenv")
	cur, err := readIfExists(zshenv)
	if err != nil {
		return fmt.Errorf(".zshenv: %w", err)
	}
	if cur != "" {
		if next := Strip(cur); next != cur {
			if strings.TrimSpace(next) == "" && !isSymlink(zshenv) {
				if err := os.Remove(zshenv); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf(".zshenv: %w", err)
				}
			} else if err := writeThrough(zshenv, []byte(next), 0o644); err != nil {
				return fmt.Errorf(".zshenv: %w", err)
			}
		}
	}
	dir := DirIn(home)
	for _, tool := range Tools {
		p := filepath.Join(dir, tool)
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "pdx gate shim for "+tool) {
			_ = os.Remove(p)
		}
	}
	_ = os.Remove(dir) // only if empty
	return nil
}
