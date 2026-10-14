package journal

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// withRunRemote replaces the git call for a test and clears the cache around it.
func withRunRemote(t *testing.T, fn func(ctx context.Context, dir string) (string, error)) {
	t.Helper()
	old := runRemote
	runRemote = fn
	clearRemoteCache()
	t.Cleanup(func() { runRemote = old; clearRemoteCache() })
}

// The URL a ref links to is the https form of `origin`, without credentials, user, port or `.git`; a remote that is a local
// path, or no usable remote, is none.
// Mutation gate: pass the raw URL through -> credentials and the scp form leak into the link (red); keep a local path ->
// a file: URL is offered as a link (red).
func TestRemoteURL_NormalisedToHTTPS(t *testing.T) {
	for raw, want := range map[string]string{
		"git@github.com:wake/purdex.git":                  "https://github.com/wake/purdex",
		"https://user:secret@github.com/Wake/Purdex.git/": "https://github.com/wake/purdex",
		"ssh://git@git.example.org:2222/team/repo.git":    "https://git.example.org/team/repo",
		"https://github.com/wake/purdex":                  "https://github.com/wake/purdex",
		"/srv/git/purdex.git":                             "",
		"file:///srv/git/purdex.git":                      "",
		"relative/path":                                   "",
		"ext::sh -c evil":                                 "",
		"":                                                "",
	} {
		withRunRemote(t, func(context.Context, string) (string, error) { return raw + "\n", nil })
		if got := RemoteURL(context.Background(), "/some/dir"); got != want {
			t.Errorf("RemoteURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The answer is kept for ten minutes per folder, a repository with no origin included; a failure that is not "no origin" (a
// timeout) is not kept.
// Mutation gate: no cache -> git runs on every call (red); cache a timeout -> the link stays missing for ten minutes (red);
// never expire -> a changed remote is never seen (red).
func TestRemoteURL_CachedPerFolder(t *testing.T) {
	calls := 0
	var answer string
	var failure error
	withRunRemote(t, func(context.Context, string) (string, error) {
		calls++
		return answer, failure
	})
	clock := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	oldNow := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = oldNow })

	answer = "git@github.com:wake/purdex.git"
	if got := RemoteURL(context.Background(), "/p"); got != "https://github.com/wake/purdex" {
		t.Fatalf("first = %q", got)
	}
	answer = "git@github.com:other/repo.git" // changed behind the cache
	if got := RemoteURL(context.Background(), "/p"); got != "https://github.com/wake/purdex" || calls != 1 {
		t.Fatalf("within the TTL: %q after %d calls, want the cached answer and 1 call", got, calls)
	}
	if got := RemoteURL(context.Background(), "/q"); got != "https://github.com/other/repo" || calls != 2 {
		t.Fatalf("another folder: %q after %d calls", got, calls)
	}
	clock = clock.Add(11 * time.Minute)
	if got := RemoteURL(context.Background(), "/p"); got != "https://github.com/other/repo" || calls != 3 {
		t.Fatalf("after the TTL: %q after %d calls, want a fresh read", got, calls)
	}

	// no origin: an answer, kept
	failure = errNoRemote
	if got := RemoteURL(context.Background(), "/none"); got != "" {
		t.Fatalf("no origin = %q", got)
	}
	before := calls
	RemoteURL(context.Background(), "/none")
	if calls != before {
		t.Fatalf("a missing origin was asked again (%d calls)", calls-before)
	}

	// a timeout: not an answer, asked again
	failure = context.DeadlineExceeded
	RemoteURL(context.Background(), "/slow")
	before = calls
	RemoteURL(context.Background(), "/slow")
	if calls != before+1 {
		t.Fatalf("a timed-out read was cached (%d calls)", calls-before)
	}
}

// Against a real repository: its origin comes back as the https URL; a repository without one, and a folder that is not a
// repository, give none.
func TestRemoteURL_RealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	clearRemoteCache()
	t.Cleanup(clearRemoteCache)
	with := t.TempDir()
	without := t.TempDir()
	for _, d := range []string{with, without} {
		if out, err := exec.Command("git", "-C", d, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	if out, err := exec.Command("git", "-C", with, "remote", "add", "origin", "git@github.com:wake/purdex.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v %s", err, out)
	}
	if got := RemoteURL(context.Background(), with); got != "https://github.com/wake/purdex" {
		t.Errorf("repository with origin = %q", got)
	}
	if got := RemoteURL(context.Background(), without); got != "" {
		t.Errorf("repository without origin = %q", got)
	}
	if got := RemoteURL(context.Background(), t.TempDir()); got != "" {
		t.Errorf("not a repository = %q", got)
	}
}
