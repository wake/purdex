package submit

import "context"

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
