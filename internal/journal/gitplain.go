package journal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// A folder the files-only mode wrote (git was too old or unreadable then) is moved aside to `<wt>.plain` when the worktree
// is made, and its journal folder is moved into the worktree afterwards. The move can stop half way, so ensure looks for
// `<wt>.plain` every time and finishes the job (finishPlain is idempotent: it moves what is left).

func plainDir(wt string) string { return wt + ".plain" }

// finishPlain moves the files of `<wt>.plain/.purdex` into `<wt>/.purdex` and removes the moved-aside folder when nothing
// is left. A file the worktree already has (the remote branch's) wins; the local copy is kept beside it under a name that
// is not taken (`<name>.plain`, `<name>.plain.1` ...). Nothing is overwritten or lost.
func (g *Git) finishPlain(wt string) error {
	plain := plainDir(wt)
	if _, err := os.Stat(plain); err != nil {
		return nil
	}
	rename := g.rename
	if rename == nil {
		rename = os.Rename
	}
	src, dst := filepath.Join(plain, ".purdex"), filepath.Join(wt, ".purdex")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == src {
				return nil // a folder with no journal in it
			}
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		for n := 0; ; n++ {
			if _, serr := os.Lstat(target); serr != nil {
				break
			}
			target = filepath.Join(dst, rel) + ".plain"
			if n > 0 {
				target += "." + strconv.Itoa(n)
			}
		}
		return rename(path, target)
	})
	if err != nil {
		return fmt.Errorf("journal: moving %s into the worktree: %w", src, err)
	}
	return os.RemoveAll(plain)
}
