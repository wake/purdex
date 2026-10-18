package submit

import (
	"context"
	"path/filepath"
	"strings"
)

// RepoRoot is the top level of the repository that holds dir (an undeclared command needs the repo but no manifest). A directory
// outside a repository is a UsageError.
func RepoRoot(ctx context.Context, dir, gitBin string) (string, error) {
	g := &gitRunner{bin: gitBin, dir: dir}
	if g.bin == "" {
		g.bin = "git"
	}
	root, err := g.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", usagef("not inside a git repository: %v", err)
	}
	return root, nil
}

// RelDir is dir as a slash path relative to the repository top level root ("." for the top level itself), after symlinks are
// followed. A directory outside the tree is a UsageError. An undeclared command runs where it was asked to, not at the top.
func RelDir(root, dir string) (string, error) {
	rr, err1 := filepath.EvalSymlinks(root)
	rd, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		return "", usagef("cannot resolve %s inside %s", dir, root)
	}
	rel, err := filepath.Rel(rr, rd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", usagef("%s is not inside %s", dir, root)
	}
	return filepath.ToSlash(rel), nil
}
