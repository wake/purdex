package gate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshot of the working tree for an assist host (assist-dispatch plan X2a.2, spec §4.1): a commit with the tree as it is now,
// uncommitted and untracked files included, ignored ones (node_modules) left out. It is built in a temporary index, so the caller's
// own index, tree and branch are never touched.

// SnapshotInfo is where the snapshot was taken and what it is.
type SnapshotInfo struct {
	SHA  string // the commit
	Root string // the repo's top level
	Rel  string // the directory the command was run in, relative to Root ("." for the root)
}

// SnapshotTree commits the working tree of the repo that contains dir. git is the git program ("" = git).
func SnapshotTree(ctx context.Context, git, dir string) (SnapshotInfo, error) {
	if git == "" {
		git = "git"
	}
	run := func(env []string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	root, err := run(nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return SnapshotInfo{}, err
	}
	tmp, err := os.MkdirTemp("", "pdx-assist-index-")
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer os.RemoveAll(tmp)
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")} // not created: git starts an empty index there
	if _, err := run(idx, "add", "-A", "--", root); err != nil {
		return SnapshotInfo{}, err
	}
	tree, err := run(idx, "write-tree")
	if err != nil {
		return SnapshotInfo{}, err
	}
	args := []string{"commit-tree", tree, "-m", "assist snapshot"}
	if head, err := run(nil, "rev-parse", "-q", "--verify", "HEAD"); err == nil && head != "" {
		args = append(args, "-p", head)
	}
	sha, err := run([]string{"GIT_AUTHOR_NAME=pdx assist", "GIT_AUTHOR_EMAIL=assist@localhost", "GIT_COMMITTER_NAME=pdx assist", "GIT_COMMITTER_EMAIL=assist@localhost"}, args...)
	if err != nil {
		return SnapshotInfo{}, err
	}
	rel := "."
	if abs, aerr := filepath.EvalSymlinks(dir); aerr == nil {
		if r, rerr := filepath.EvalSymlinks(root); rerr == nil {
			if x, xerr := filepath.Rel(r, abs); xerr == nil && !strings.HasPrefix(x, "..") {
				rel = x
			}
		}
	}
	return SnapshotInfo{SHA: sha, Root: root, Rel: rel}, nil
}

// markFilter passes the remote command's stderr through and takes out the gate's own mark lines, saying which it saw. A mark is a line
// that begins with the record separator and names the sender's nonce; any other line that begins with it is the command's and passes.
type markFilter struct {
	w          interface{ Write([]byte) (int, error) }
	admitted   string
	notStarted string
	sawAdmit   bool
	sawNoStart bool
	line       []byte // a line that began with the separator, kept until it ends
	collecting bool
	atStart    bool
}

func newMarkFilter(w interface{ Write([]byte) (int, error) }, nonce string) *markFilter {
	return &markFilter{w: w, admitted: AdmittedMark(nonce), notStarted: NotStartedMark(nonce), atStart: true}
}

func (m *markFilter) Write(p []byte) (int, error) {
	n := len(p)
	var out []byte
	for _, b := range p {
		switch {
		case m.collecting:
			m.line = append(m.line, b)
			if b == '\n' {
				l := string(m.line)
				switch l {
				case m.admitted:
					m.sawAdmit = true
				case m.notStarted:
					m.sawNoStart = true
				default:
					out = append(out, m.line...)
				}
				m.line, m.collecting, m.atStart = m.line[:0], false, true
			} else if len(m.line) > 256 { // not a mark: too long to be one
				out = append(out, m.line...)
				m.line, m.collecting, m.atStart = m.line[:0], false, false
			}
		case m.atStart && b == 0x1e:
			m.collecting = true
			m.line = append(m.line[:0], b)
		default:
			out = append(out, b)
			m.atStart = b == '\n'
		}
	}
	if len(out) > 0 {
		if _, err := m.w.Write(out); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush writes out a partial line that began like a mark but never ended.
func (m *markFilter) Flush() {
	if len(m.line) > 0 {
		_, _ = m.w.Write(m.line)
		m.line, m.collecting = m.line[:0], false
	}
}
