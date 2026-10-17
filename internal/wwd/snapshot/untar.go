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
	var links []string
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// Root never follows a symlink out, but it does not stop one from being made, and a consumer of the workspace that does not go
			// through a root would follow it: every link is resolved through the root once more, now that the tree is complete (a link
			// can lead out through a chain of the archive's own links, which only the whole tree shows). A link that points at nothing
			// yet, inside, is fine; one that cannot be resolved inside is not.
			for _, l := range links {
				if _, err := root.Stat(l); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return "", fmt.Errorf("snapshot tar: symlink %q leads out of the workspace: %w", l, err)
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
				return "", fmt.Errorf("snapshot tar: symlink %q -> %q leads out of the workspace", h.Name, h.Linkname)
			}
			if err := root.Symlink(h.Linkname, name); err != nil {
				return "", err
			}
			links = append(links, name)
		default:
			return "", fmt.Errorf("snapshot tar: %q is of a kind a snapshot does not have (%c)", h.Name, h.Typeflag)
		}
	}
}
