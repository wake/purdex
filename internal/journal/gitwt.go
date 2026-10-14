package journal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Branch is the journal's branch in every project that has git (spec §3 Q-A).
const Branch = "purdex/journal"

// Git owns one registered git worktree of Branch per project, under Dir/<project id>/wt, where the daemon writes the
// journal files (spec §8). The worktree is a second checkout of the repository: the main checkout and the agents'
// worktrees are never touched, and git itself refuses a second checkout of the branch anywhere else. A project without
// git, or a git older than 2.42 (no `worktree add --orphan`), keeps its files in a plain folder at the same place:
// written, never committed.
type Git struct {
	Dir string // <data dir>/journal
	// PushProjects is the allow-list of `host/owner/repo` that may be pushed; it is called at every push round, so a
	// change applies without a restart. An error or an empty list pushes nothing.
	PushProjects func() ([]string, error)
	Logf         func(format string, args ...any)
	Interval     time.Duration // commit and push period; 10 minutes when zero

	version func(ctx context.Context) (major, minor int, err error) // replaced in tests

	mu     sync.Mutex
	locks  map[string]*sync.Mutex // per project id
	known  map[string]bool        // project ids this Git has a worktree for
	logged map[string]time.Time   // log-once-an-hour keys
	pushed map[string]string      // project id -> the HEAD last pushed
}

const defaultInterval = 10 * time.Minute

var (
	errTooOld   = errors.New("journal: git is older than 2.42")
	versionRe   = regexp.MustCompile(`git version (\d+)\.(\d+)`)
	noRemoteRef = "couldn't find remote ref"
)

// WorktreeDir is where project p's journal worktree lives (or would).
func WorktreeDir(dir string, p Project) string { return filepath.Join(dir, p.ID, "wt") }

func (g *Git) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}

// logOnce logs key's message at most once an hour (a failure that repeats every round must not fill the log).
func (g *Git) logOnce(key, format string, args ...any) {
	g.mu.Lock()
	if g.logged == nil {
		g.logged = map[string]time.Time{}
	}
	last, seen := g.logged[key]
	if seen && time.Since(last) < time.Hour {
		g.mu.Unlock()
		return
	}
	g.logged[key] = time.Now()
	g.mu.Unlock()
	g.logf(format, args...)
}

func (g *Git) lock(id string) func() {
	g.mu.Lock()
	if g.locks == nil {
		g.locks = map[string]*sync.Mutex{}
	}
	l, ok := g.locks[id]
	if !ok {
		l = &sync.Mutex{}
		g.locks[id] = l
	}
	g.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (g *Git) remember(id string) {
	g.mu.Lock()
	if g.known == nil {
		g.known = map[string]bool{}
	}
	g.known[id] = true
	g.mu.Unlock()
}

// With runs fn with project p's journal folder (`<worktree>/.purdex`), creating the worktree on first use, under the
// project's lock: nothing is committed while fn runs. It is the one door the writers (the drainer) go through.
func (g *Git) With(ctx context.Context, p Project, fn func(purdexDir string) error) error {
	defer g.lock(p.ID)()
	wt, err := g.ensure(ctx, p)
	if err != nil {
		return err
	}
	dir := filepath.Join(wt, ".purdex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fn(dir)
}

// ensure makes p's worktree exist and returns its path. Called with the project lock held.
func (g *Git) ensure(ctx context.Context, p Project) (string, error) {
	wt := WorktreeDir(g.Dir, p)
	if _, err := os.Stat(filepath.Join(wt, ".git")); err == nil {
		g.remember(p.ID)
		return wt, nil
	}
	if p.CommonDir == "" || !g.gitSupportsOrphan(ctx) {
		return wt, os.MkdirAll(wt, 0o755)
	}
	if entries, err := os.ReadDir(wt); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("journal: %s exists and is not a worktree", wt)
	}
	repo := []string{"--git-dir=" + p.CommonDir}
	_, _ = g.git(ctx, "", 20*time.Second, append(repo, "worktree", "prune")...)
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return "", err
	}
	add := func(args ...string) error {
		_, err := g.git(ctx, "", 30*time.Second, append(append(repo, "worktree", "add"), args...)...)
		return err
	}
	var err error
	switch {
	case g.refExists(ctx, repo, "refs/heads/"+Branch):
		err = add(wt, Branch) // the branch survives its worktree's folder
	default:
		remote, ferr := g.fetchBranch(ctx, repo)
		switch {
		case ferr != nil:
			return "", ferr
		case remote:
			err = add("--track", "-b", Branch, wt, "origin/"+Branch)
		default:
			err = add("--orphan", "-b", Branch, wt)
		}
	}
	if err != nil {
		return "", err
	}
	g.remember(p.ID)
	return wt, nil
}

// fetchBranch fetches origin's Branch into refs/remotes/origin/ and reports whether it exists. No origin is a
// local-only journal (false); an origin that cannot be asked is an error, so a branch that exists there is never
// shadowed by a new orphan one.
func (g *Git) fetchBranch(ctx context.Context, repo []string) (bool, error) {
	if _, err := g.git(ctx, "", 10*time.Second, append(repo, "remote", "get-url", "origin")...); err != nil {
		return false, nil
	}
	out, err := g.git(ctx, "", 30*time.Second, append(repo, "fetch", "--no-tags", "origin",
		"+refs/heads/"+Branch+":refs/remotes/origin/"+Branch)...)
	switch {
	case err == nil:
		return true, nil
	case strings.Contains(out+err.Error(), noRemoteRef):
		return false, nil
	}
	return false, err
}

func (g *Git) refExists(ctx context.Context, repo []string, ref string) bool {
	_, err := g.git(ctx, "", 10*time.Second, append(repo, "show-ref", "--verify", "--quiet", ref)...)
	return err == nil
}

// gitSupportsOrphan reports git >= 2.42, logging once when it is not (the journal then keeps files only).
func (g *Git) gitSupportsOrphan(ctx context.Context) bool {
	v := g.version
	if v == nil {
		v = gitVersion
	}
	major, minor, err := v(ctx)
	if err != nil || major < 2 || (major == 2 && minor < 42) {
		g.logOnce("version", "journal: git %d.%d is older than 2.42 or unreadable (%v); journal files are written without commits", major, minor, err)
		return false
	}
	return true
}

func gitVersion(ctx context.Context) (int, int, error) {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		return 0, 0, err
	}
	m := versionRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("unreadable git version %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, nil
}

// git runs `git args` (in dir when not empty) with a timeout and the journal's settings: no prompts, no hooks, no
// signing, the journal's identity, English messages. The combined output comes back with the error.
func (g *Git) git(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true",
		"GIT_AUTHOR_NAME=Purdex journal", "GIT_AUTHOR_EMAIL=journal@purdex.local",
		"GIT_COMMITTER_NAME=Purdex journal", "GIT_COMMITTER_EMAIL=journal@purdex.local")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=20")
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if len(text) > 300 {
			text = text[:300]
		}
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}
