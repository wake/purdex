// Package journal is the pure half of the workbook journal (docs/specs/2026-10-14-workbook-journal-spec.md): project
// resolution, the entry model, and the markdown files the daemon writes (journal, tasks, roadmap). It touches only the
// files it is given and runs git read-only; it imports nothing under internal/module.
package journal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Project is the repository a cwd belongs to; every worktree of one repository is the same project.
type Project struct {
	ID        string // first 16 hex of sha256 of CommonDir (of the cwd when not in a repository)
	CommonDir string // the repository's common git dir; "" outside a repository
	Root      string // the main worktree's root; the cwd itself outside a repository
}

const (
	gitTimeout = 3 * time.Second
	cacheTTL   = 10 * time.Minute
)

type cached struct {
	p  Project
	at time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
	// runGit prints the absolute common git dir of cwd; replaced in tests.
	runGit = func(ctx context.Context, cwd string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
		return strings.TrimSpace(string(out)), err
	}
	now = time.Now
)

func hashID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// ProjectOf resolves cwd to its project. Not being in a repository is a normal answer (cached, nil error); a git that
// timed out or cannot run returns the cwd-keyed fallback together with the error and is not cached.
func ProjectOf(ctx context.Context, cwd string) (Project, error) {
	if cwd == "" {
		return Project{}, errors.New("journal: empty cwd")
	}
	cacheMu.Lock()
	if c, ok := cache[cwd]; ok && now().Sub(c.at) < cacheTTL {
		cacheMu.Unlock()
		return c.p, nil
	}
	cacheMu.Unlock()
	fallback := Project{ID: hashID(cwd), Root: cwd}
	cctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := runGit(cctx, cwd)
	p := fallback
	switch {
	case cctx.Err() != nil:
		return fallback, cctx.Err()
	case err != nil:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return fallback, err
		}
	case out != "":
		dir := filepath.Clean(out)
		p = Project{ID: hashID(dir), CommonDir: dir, Root: filepath.Dir(dir)}
	}
	cacheMu.Lock()
	cache[cwd] = cached{p: p, at: now()}
	cacheMu.Unlock()
	return p, nil
}
