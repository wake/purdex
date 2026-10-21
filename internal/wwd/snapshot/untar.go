// Package snapshot gets a job's snapshot commit from the origin to the host that claimed the job (WWD spec §4.3, §5):
// the origin serves a bundle or a tar of it for a live claim, and the worker makes a workspace from it.
package snapshot

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxTarBytes and maxTarEntries bound what a tar may unpack to: the origin is a peer with the pool token, not a stranger, but a
// worker's disk is not for a runaway archive either. A purdex snapshot is 70 MB in 5,500 files.
const (
	maxTarBytes   = 4 << 30
	maxTarEntries = 500_000
)

// Untar unpacks the tar r (the output of `git archive`) into dst, which is made when it is not there. It writes nothing outside dst:
// every file system operation goes through an os.Root on dst, which resolves names (symlinks included) inside the root or fails, so what
// a chain of symlinks built cannot lead out, and no check of ours has to be complete. On top of that a name that is absolute or climbs
// out, a symlink whose target does, a hard link, a device or a fifo is an error.
// The pax global header that git archive puts first is not a file: its comment is the commit id, which is returned ("" when the
// archive has none) so that the caller can check it is the commit it asked for.
func Untar(r io.Reader, dst string) (commit string, err error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	var total int64
	links := map[string]string{}
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// Root never follows a symlink out, but it does not stop one from being made, and a consumer of the workspace that does not go
			// through a root would follow it. Now that the tree is complete every link is resolved on paper, whether or not its target is
			// there (a missing component can be made later and then the link leads out), and through the archive's own links (a chain
			// can leave only through the whole tree); a link that would leave, or loops, is an error. One that points at nothing yet,
			// inside, is fine.
			for l := range links {
				if linkLeaves(links, l) {
					return "", &OutsideLinkError{Path: l, Target: links[l]}
				}
				if _, err := root.Stat(l); err != nil && !errors.Is(err, fs.ErrNotExist) { // and what the file system itself says
					return "", fmt.Errorf("snapshot tar: symlink %q: %w", l, err)
				}
			}
			return commit, nil
		}
		if err != nil {
			return "", fmt.Errorf("snapshot tar: %w", err)
		}
		if n >= maxTarEntries {
			return "", errors.New("snapshot tar: too many entries")
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			if c, ok := h.PAXRecords["comment"]; ok {
				commit = c
			}
			continue
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return "", fmt.Errorf("snapshot tar: %q is not a path inside the workspace", h.Name)
		}
		if dir := filepath.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxTarBytes {
				return "", errors.New("snapshot tar: too large")
			}
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode&0o777)|0o200)
			if err != nil {
				return "", err
			}
			if _, err := io.CopyN(f, tr, h.Size); err != nil {
				f.Close()
				return "", fmt.Errorf("snapshot tar: %s: %w", h.Name, err)
			}
			if err := f.Close(); err != nil {
				return "", err
			}
			if err := root.Chmod(name, os.FileMode(h.Mode&0o777)); err != nil { // the umask does not decide it, and no setuid bit comes along
				return "", err
			}
		case tar.TypeSymlink:
			// Root does not stop a symlink from being made with an outside target, only from being followed; none is made.
			if path.IsAbs(h.Linkname) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), h.Linkname)) {
				return "", &OutsideLinkError{Path: h.Name, Target: h.Linkname}
			}
			if err := root.Symlink(h.Linkname, name); err != nil {
				return "", err
			}
			links[name] = h.Linkname
		default:
			return "", fmt.Errorf("snapshot tar: %q is of a kind a snapshot does not have (%c)", h.Name, h.Typeflag)
		}
	}
}

// linkLeaves says whether the symlink name, resolved on paper, leaves the workspace. The path is the directory of the link followed by
// its target, taken component by component: a component that is another symlink of the archive is replaced by that link's target, ".."
// goes up one (above the root is leaving), and any other name is taken as a directory that is or will be there. More than 255
// expansions is a loop, and a loop is leaving too (a path that never ends cannot be shown to stay).
func linkLeaves(links map[string]string, name string) bool {
	pending := append(strings.Split(path.Dir(name), "/"), strings.Split(links[name], "/")...)
	var stack []string
	for budget := 255; len(pending) > 0; {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return true
			}
			stack = stack[:len(stack)-1]
			continue
		}
		next := append(append([]string(nil), stack...), c)
		if target, ok := links[strings.Join(next, "/")]; ok {
			if budget--; budget < 0 || path.IsAbs(target) {
				return true
			}
			pending = append(strings.Split(target, "/"), pending...) // relative to the directory the link is in: the stack
			continue
		}
		stack = next
	}
	return false
}
