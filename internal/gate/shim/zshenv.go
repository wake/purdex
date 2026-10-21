package shim

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const (
	blockBegin = "# >>> pdx gate >>>"
	blockEnd   = "# <<< pdx gate <<<"
)

// Block is the text `pdx setup` keeps in ~/.zshenv for shimDir: it moves the directory to the front of PATH wherever it was
// (path=(dir ${path:#dir}) puts it first and removes any other copy, so a nested `zsh -c` does not grow PATH), and prints
// nothing. An interactive shell also registers a precmd that does the same before every prompt, because ~/.zshrc (brew, nvm,
// bun, Herd) and a later `nvm use` put their directories in front of it after ~/.zshenv ran.
func Block(shimDir string) string {
	return blockBegin + "\n" + strings.Join(blockBody(shimDir), "\n") + "\n" + blockEnd + "\n"
}

// blockBody are the lines between the markers.
func blockBody(shimDir string) []string {
	q := shQuoteInner(shimDir)
	front := `path=("` + q + `" ${path:#"` + q + `"})`
	return []string{
		front,
		`if [[ -o interactive ]]; then`,
		`  _pdx_gate_front() { ` + front + ` }`,
		`  precmd_functions=(${precmd_functions:#_pdx_gate_front} _pdx_gate_front)`,
		`fi`,
	}
}

// bodyLine is what the first version's single body line looked like (an unclosed begin marker followed by it).
var bodyLine = regexp.MustCompile(`^case ":\$PATH:" in \*":.*:"\*\) ;; \*\) PATH=".*:\$PATH" ;; esac$`)

// bodyLines match, in order, the lines of the current block's body (any shim directory); an unclosed block is repaired by
// dropping the longest prefix of them right after its marker.
var bodyLines = []*regexp.Regexp{
	regexp.MustCompile(`^path=\(".*" \$\{path:#".*"\}\)$`),
	regexp.MustCompile(`^if \[\[ -o interactive \]\]; then$`),
	regexp.MustCompile(`^  _pdx_gate_front\(\) \{ path=\(".*" \$\{path:#".*"\}\) \}$`),
	regexp.MustCompile(`^  precmd_functions=\(\$\{precmd_functions:#_pdx_gate_front\} _pdx_gate_front\)$`),
	regexp.MustCompile(`^fi$`),
}

// Strip removes every pdx gate block from s (several, from an old double install, too; the first version's one-line block and the current multi-line one) and returns the rest untouched, byte for
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
		// unclosed: drop the marker and the body lines right after it that are ours (the first version's one line, or a prefix
		// of the current body), and nothing else
		if i+1 < len(lines) && bodyLine.MatchString(strings.TrimRight(lines[i+1], "\r\n")) {
			i++
			continue
		}
		for k := 0; k < len(bodyLines) && i+1 < len(lines) && bodyLines[k].MatchString(strings.TrimRight(lines[i+1], "\r\n")); k++ {
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

// resolveLink is where a write to path must land: path with every symbolic link in its last component followed, also one that
// points at a file that is not there yet (a dotfiles repo not yet cloned; the write then creates the target and the link
// stays). Relative targets are taken from the link's own directory. A chain longer than 40 links is an error.
func resolveLink(path string) (string, error) {
	for i := 0; i < 40; i++ {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		t, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(path), t)
		}
		path = t
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", path)
}

// writeThrough replaces the file at path with data, in the file's own mode, through a rename in its directory. A symlink at
// path (a dotfiles repo) is followed, a dangling one too: the link stays a link and its target is replaced or created.
func writeThrough(path string, data []byte, defaultMode os.FileMode) error {
	target, err := resolveLink(path)
	if err != nil {
		return err
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
	target, err := resolveLink(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(b), err
}

// ConflictError is Install's refusal: files in the shim directory that are not shims pdx wrote (a wrapper of the user's own,
// a symlink), which Install does not overwrite. Nothing has been written when it is returned.
type ConflictError struct{ Paths []string }

func (e *ConflictError) Error() string {
	return "not pdx shims, left alone (move them away to let the gate in): " + strings.Join(e.Paths, ", ")
}

// generations are the generators of every form of shim pdx has ever written, newest first. A file is a pdx shim only if it is
// byte for byte what one of them writes; when Script changes, the old form is kept here (so an upgrade still recognizes it).
var generations = []func(tool, pdxPath string) string{Script}

// pdxPathOf is the pdx path written into a shim (the default of its PDX_BIN line); ok is false when there is no such line.
func pdxPathOf(content string) (path string, ok bool) {
	const head = `PDX_BIN="${PDX_BIN:-`
	i := strings.Index(content, head)
	if i < 0 {
		return "", false
	}
	rest := content[i+len(head):]
	j := strings.Index(rest, "}\"\n")
	if j < 0 {
		return "", false
	}
	return shUnquoteInner(rest[:j]), true
}

// owned reports whether the regular file at p is a shim pdx wrote for tool: its content is exactly what a generator writes for
// the pdx path it names. A file that merely carries a shim's comment, or a shim someone edited, is not.
func owned(p, tool string) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	pdx, ok := pdxPathOf(string(b))
	if !ok {
		return false
	}
	for _, gen := range generations {
		if gen(tool, pdx) == string(b) {
			return true
		}
	}
	return false
}

// lock serializes Install and Uninstall of one home across processes (two `pdx setup` at once); the returned func releases it.
func lock(home string) (func(), error) {
	dir := filepath.Join(home, ".config", "pdx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".gate-setup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// Install writes the shims (for pdxPath) into DirIn(home) and keeps one block in home/.zshenv. It is idempotent: a second run
// changes no byte, and files it would not change are not rewritten. It takes a lock for the home; it looks at every target
// before writing any (a file there that is not a pdx shim is a ConflictError and nothing is written); it stages all the shims
// and only then puts them in place; and it writes the .zshenv block last, so the shims are in place before PATH leads to them.
func Install(home, pdxPath string) error {
	if pdxPath == "" {
		return errors.New("no pdx path for the shims")
	}
	unlock, err := lock(home)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer unlock()
	dir := DirIn(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("shim dir: %w", err)
	}
	var conflicts []string
	type staged struct{ tool, dst, tmp string }
	var todo []staged
	for _, tool := range Tools {
		p := filepath.Join(dir, tool)
		if _, err := os.Lstat(p); err == nil && !owned(p, tool) {
			conflicts = append(conflicts, p)
			continue
		}
		want := []byte(Script(tool, pdxPath))
		if have, err := os.ReadFile(p); err == nil && bytes.Equal(have, want) {
			if fi, err := os.Stat(p); err == nil && fi.Mode().Perm() == 0o755 {
				continue
			}
		}
		todo = append(todo, staged{tool: tool, dst: p})
	}
	if len(conflicts) > 0 {
		return &ConflictError{Paths: conflicts}
	}
	cleanup := func() {
		for _, st := range todo {
			if st.tmp != "" {
				_ = os.Remove(st.tmp)
			}
		}
	}
	for i := range todo {
		tmp, err := os.CreateTemp(dir, "."+todo[i].tool+".pdx-*")
		if err != nil {
			cleanup()
			return fmt.Errorf("shim %s: %w", todo[i].tool, err)
		}
		todo[i].tmp = tmp.Name()
		_, werr := tmp.WriteString(Script(todo[i].tool, pdxPath))
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Chmod(todo[i].tmp, 0o755)
		}
		if werr != nil {
			cleanup()
			return fmt.Errorf("shim %s: %w", todo[i].tool, werr)
		}
	}
	for i := range todo {
		if err := os.Rename(todo[i].tmp, todo[i].dst); err != nil {
			cleanup()
			return fmt.Errorf("shim %s: %w", todo[i].tool, err)
		}
		todo[i].tmp = ""
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
// if it was a plain file that held nothing but the block, and emptied, never removed, if it is a symlink) and the shims it wrote.
func Uninstall(home string) error {
	unlock, err := lock(home)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer unlock()
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
		if owned(p, tool) {
			_ = os.Remove(p)
		}
	}
	_ = os.Remove(dir) // only if empty
	return nil
}

// RemoveOutcome is what RemoveBlock found and did.
type RemoveOutcome int

const (
	// NoBlock: there was no pdx gate block (no file, no marker, or an end marker alone); nothing was written.
	NoBlock RemoveOutcome = iota
	// BlockRemoved: the block (every complete one) was taken out of the file.
	BlockRemoved
	// BlockUnclosed: a begin marker has no end marker. The file is left exactly as it is (such a file was edited by hand, and nothing here
	// guesses what is the block's and what is not), also when a complete block is there too.
	BlockUnclosed
	// BlockNotOurs: between a begin and an end marker there is text that is not what pdx ever wrote (a here-document of the user's that holds
	// the two marker lines, a block someone edited). The markers alone do not show a block is ours, so the file is left exactly as it is.
	BlockNotOurs
)

// blockBodyOurs says whether the lines between the markers are what pdx has written: the first version's one line, or the five lines of the
// current block (for any shim directory). Anything else, an added line included, is not provably ours.
func blockBodyOurs(inner []string) bool {
	if len(inner) == 1 && bodyLine.MatchString(inner[0]) {
		return true
	}
	if len(inner) != len(bodyLines) {
		return false
	}
	for i, re := range bodyLines {
		if !re.MatchString(inner[i]) {
			return false
		}
	}
	return true
}

// allBlocksOurs says whether every complete block of s (a begin marker, then an end marker before the next begin marker) holds only what pdx
// wrote. Unclosed ones are hasUnclosed's.
func allBlocksOurs(s string) bool {
	lines := strings.SplitAfter(s, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") != blockBegin {
			continue
		}
		var inner []string
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimRight(lines[j], "\r\n")
			if t == blockEnd {
				if !blockBodyOurs(inner) {
					return false
				}
				i = j
				break
			}
			if t == blockBegin {
				break
			}
			inner = append(inner, t)
		}
	}
	return true
}

// hasUnclosed says whether s has a begin marker that no end marker follows before the next begin marker (or the end of the file).
func hasUnclosed(s string) bool {
	lines := strings.SplitAfter(s, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") != blockBegin {
			continue
		}
		closed := false
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimRight(lines[j], "\r\n")
			if t == blockEnd {
				closed, i = true, j
				break
			}
			if t == blockBegin {
				break
			}
		}
		if !closed {
			return true
		}
	}
	return false
}

// RemoveBlock takes the pdx gate block out of home/.zshenv and touches nothing else (WWD-9 S0-b: `pdx setup` no longer puts the gate's shims
// first on PATH). It does not write a shim and does not touch the shims that are there. The rest of the file stays byte for byte; a file left
// with nothing in it is removed if it is a plain file and emptied, never removed, if it is a symbolic link (a dotfiles repo's). With no file
// or no block it writes and makes nothing, not even the lock's directory. It takes the lock of the home that Install and Uninstall take.
func RemoveBlock(home string) (RemoveOutcome, error) {
	zshenv := filepath.Join(home, ".zshenv")
	cur, err := readIfExists(zshenv)
	if err != nil {
		return NoBlock, fmt.Errorf(".zshenv: %w", err)
	}
	if hasUnclosed(cur) {
		return BlockUnclosed, nil
	}
	if Strip(cur) == cur {
		return NoBlock, nil
	}
	if !allBlocksOurs(cur) {
		return BlockNotOurs, nil
	}
	unlock, err := lock(home)
	if err != nil {
		return NoBlock, fmt.Errorf("lock: %w", err)
	}
	defer unlock()
	cur, err = readIfExists(zshenv) // again, under the lock
	if err != nil {
		return NoBlock, fmt.Errorf(".zshenv: %w", err)
	}
	if hasUnclosed(cur) {
		return BlockUnclosed, nil
	}
	next := Strip(cur)
	if next == cur {
		return NoBlock, nil
	}
	if !allBlocksOurs(cur) {
		return BlockNotOurs, nil
	}
	// The file goes only when the block was all of it (next is empty): blanks the user left around it are theirs, and stay byte for byte.
	if next == "" && !isSymlink(zshenv) {
		if err := os.Remove(zshenv); err != nil && !os.IsNotExist(err) {
			return NoBlock, fmt.Errorf(".zshenv: %w", err)
		}
		return BlockRemoved, nil
	}
	if err := writeThrough(zshenv, []byte(next), 0o644); err != nil {
		return NoBlock, fmt.Errorf(".zshenv: %w", err)
	}
	return BlockRemoved, nil
}
