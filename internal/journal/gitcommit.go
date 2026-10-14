package journal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commitProject commits project p's journal folder if it changed, under the project's lock (the writers go through
// With, so no file is half-written while it is staged).
func (g *Git) commitProject(ctx context.Context, p Project) error {
	defer g.lock(p.ID)()
	return g.commit(ctx, WorktreeDir(g.Dir, p))
}

// commit stages `.purdex` of the worktree wt and commits it with the journal identity when something changed. Only that
// folder is staged, whatever else is in the worktree; the project's own hooks never run (see git). A plain folder
// (no git) has nothing to commit. Called with the project lock held.
func (g *Git) commit(ctx context.Context, wt string) error {
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		return nil
	}
	if _, err := g.git(ctx, wt, 30*time.Second, "add", "-A", ".purdex"); err != nil {
		return err
	}
	if _, err := g.git(ctx, wt, 30*time.Second, "diff", "--cached", "--quiet"); err == nil {
		return nil // nothing staged
	} else if !isExit(err, 1) {
		return err
	}
	diff, err := g.git(ctx, wt, 30*time.Second, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff")
	if err != nil {
		return err
	}
	msg := "journal: update"
	if n := strings.Count("\n"+diff, "\n+<!-- j "); n > 0 {
		msg = fmt.Sprintf("journal: %d entries", n)
	}
	_, err = g.git(ctx, wt, 30*time.Second, "commit", "-q", "-m", msg)
	return err
}

// isExit reports whether err is a process exit with the given code.
func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}
