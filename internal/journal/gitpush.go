package journal

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	pushTimeout   = 60 * time.Second
	branchRefspec = "refs/heads/" + Branch + ":refs/heads/" + Branch
)

// Run commits and pushes on a timer until ctx ends: first a round at once (the commits a stopped daemon left behind), then
// one every Interval. It never blocks the writers: a round holds a project's lock only for its local git work, never
// while it waits on the network.
func (g *Git) Run(ctx context.Context) {
	g.adopt()
	every := g.Interval
	if every <= 0 {
		every = defaultInterval
	}
	g.Round(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Round(ctx)
		}
	}
}

// adopt registers the worktrees found under Dir (a restarted daemon has not created them this run).
func (g *Git) adopt() {
	entries, _ := os.ReadDir(g.Dir)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(g.Dir, e.Name(), "wt", ".git")); err == nil {
			g.remember(e.Name())
		}
	}
}

// Round commits every known project's changes and pushes the ones the allow-list names. The list is read once, here.
// Every failure is logged (once an hour per kind and project) and retried by the next round.
func (g *Git) Round(ctx context.Context) {
	var list []string
	var listErr error
	if g.PushProjects != nil {
		list, listErr = g.PushProjects()
	}
	g.mu.Lock()
	ids := make([]string, 0, len(g.known))
	for id := range g.known {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		p := Project{ID: id}
		if err := g.commitProject(ctx, p); err != nil {
			g.logOnce("commit/"+id, "journal: commit of project %s failed: %v", id, err)
		}
		if listErr != nil {
			g.logOnce("config", "journal: push_projects could not be read, nothing is pushed: %v", listErr)
			continue
		}
		g.pushProject(ctx, p, list)
	}
}

// pushProject pushes p's journal branch when its origin is on the allow-list and the branch has moved since the last push.
// A rejection because the remote moved is rebased onto the remote branch and pushed once more; a conflict aborts the rebase
// and leaves the local commits for the next round. Never forced.
func (g *Git) pushProject(ctx context.Context, p Project, list []string) {
	wt := WorktreeDir(g.Dir, p)
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		return
	}
	target, ok := g.pushTarget(ctx, p, wt, list)
	if !ok {
		return
	}
	if err := g.onBranch(ctx, wt); err != nil {
		g.logOnce("branch/"+p.ID, "%v", err)
		return
	}
	head, err := g.git(ctx, wt, 10*time.Second, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return // nothing committed yet
	}
	if g.lastPushed(p.ID) == head {
		return
	}
	out, err := g.git(ctx, wt, pushTimeout, "push", target, branchRefspec)
	if err != nil && strings.Contains(out, "rejected") {
		if !g.rebaseOntoRemote(ctx, p, wt) {
			return
		}
		out, err = g.git(ctx, wt, pushTimeout, "push", target, branchRefspec)
	}
	if err != nil {
		g.logOnce("push/"+p.ID, "journal: push of project %s failed, retried next round: %v", p.ID, err)
		return
	}
	if head, err = g.git(ctx, wt, 10*time.Second, "rev-parse", "--verify", "HEAD"); err == nil {
		g.setPushed(p.ID, head)
	}
}

// pushTarget is the URL the journal branch is pushed to, when pushing it is allowed: the URL git itself would use for
// `origin` — with pushurl, url.<base>.insteadOf and pushInsteadOf applied (`git remote get-url`), not the configured text —
// must be on the allow-list, and so must the fetch URL (the rebase takes the remote's commits from it). Exactly one push URL:
// several would send the branch somewhere the check did not look. The push then goes to that URL, not to the name `origin`,
// so a config change between the check and the push cannot redirect it.
func (g *Git) pushTarget(ctx context.Context, p Project, wt string, list []string) (string, bool) {
	fetchURL, err := g.git(ctx, wt, 10*time.Second, "remote", "get-url", "origin")
	if err != nil || !PushAllowed(fetchURL, list) {
		return "", false
	}
	out, err := g.git(ctx, wt, 10*time.Second, "remote", "get-url", "--push", "--all", "origin")
	urls := strings.Fields(out)
	if err != nil || len(urls) != 1 || !PushAllowed(urls[0], list) {
		g.logOnce("target/"+p.ID, "journal: project %s is listed but its push destination is not (%d push URL(s), first %q); nothing is pushed",
			p.ID, len(urls), NormalizeRemote(firstOr(urls, "")))
		return "", false
	}
	return urls[0], true
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

// rebaseOntoRemote fetches the remote journal branch (the network part, outside the lock) and rebases the local commits
// onto it (under the lock, after committing what the writers left, so the tree is clean). It reports whether the branch
// now sits on the remote's.
func (g *Git) rebaseOntoRemote(ctx context.Context, p Project, wt string) bool {
	if _, err := g.git(ctx, wt, pushTimeout, "fetch", "--no-tags", "origin", "+refs/heads/"+Branch+":refs/remotes/origin/"+Branch); err != nil {
		g.logOnce("push/"+p.ID, "journal: fetch for project %s failed, retried next round: %v", p.ID, err)
		return false
	}
	defer g.lock(p.ID)()
	if err := g.commit(ctx, wt); err != nil {
		g.logOnce("commit/"+p.ID, "journal: commit of project %s failed: %v", p.ID, err)
		return false
	}
	if _, err := g.git(ctx, wt, 60*time.Second, "rebase", "origin/"+Branch); err != nil {
		_, _ = g.git(ctx, wt, 20*time.Second, "rebase", "--abort")
		g.logOnce("conflict/"+p.ID, "journal: project %s: the journal branch conflicts with the remote's (%v); local commits are kept and it is retried next round", p.ID, err)
		return false
	}
	return true
}

func (g *Git) lastPushed(id string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pushed[id]
}

func (g *Git) setPushed(id, head string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pushed == nil {
		g.pushed = map[string]string{}
	}
	g.pushed[id] = head
}
