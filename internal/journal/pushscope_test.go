package journal

import "testing"

func TestNormalizeRemote_EveryFormOfOneRepositoryIsOneString(t *testing.T) {
	const want = "github.com/wake/purdex"
	for _, in := range []string{
		"git@github.com:wake/purdex.git",
		"git@github.com:wake/purdex",
		"ssh://git@github.com/wake/purdex.git",
		"ssh://git@github.com:22/wake/purdex",
		"https://github.com/wake/purdex.git",
		"https://github.com/wake/purdex/",
		"https://wake:ghp_secrettoken@github.com/wake/purdex.git",
		"http://GitHub.com/Wake/Purdex",
		"git://github.com/wake/purdex.git",
		"github.com/wake/purdex",
		"  github.com/wake/purdex.git  ",
	} {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRemote_NothingUsableIsEmptyAndNeverCarriesCredentials(t *testing.T) {
	for _, in := range []string{"", "   ", "github.com", "https://", "git@github.com:", "../x", "file://otherhost/srv/git/repo.git", "/"} {
		if got := NormalizeRemote(in); got != "" {
			t.Errorf("NormalizeRemote(%q) = %q, want empty", in, got)
		}
	}
	if got := NormalizeRemote("https://u:pw@gitlab.example.com:8443/group/sub/app.git"); got != "gitlab.example.com/group/sub/app" {
		t.Errorf("subgroup url = %q", got)
	}
}

func TestNormalizeRemote_ALocalRepositoryIsItsCleanedAbsolutePath(t *testing.T) {
	for _, in := range []string{"/srv/git/repo.git", "/srv/git//repo.git/", "file:///srv/git/repo.git", "file://localhost/srv/git/repo.git"} {
		const want = "file:/srv/git/repo.git"
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
	if !PushAllowed("file:///srv/git/repo.git", []string{"/srv/git/repo.git"}) || PushAllowed("/srv/git/other.git", []string{"/srv/git/repo.git"}) {
		t.Error("a local repository is matched by its path, exactly")
	}
}

func TestPushAllowed(t *testing.T) {
	list := []string{"github.com/wake/purdex", "Git.Example.com/team/app.git"}
	for url, want := range map[string]bool{
		"git@github.com:wake/purdex.git":             true,
		"https://github.com/wake/purdex":             true,
		"https://token@git.example.com/team/app.git": true,
		"git@github.com:wake/purdex2.git":            false, // exact, not a prefix
		"git@github.com:wake/purdex-tools.git":       false,
		"git@github.com:other/purdex.git":            false,
		"git@gitlab.com:wake/purdex.git":             false,
		"":                                           false,
	} {
		if got := PushAllowed(url, list); got != want {
			t.Errorf("PushAllowed(%q) = %v, want %v", url, got, want)
		}
	}
	// The default is nothing: an empty list, and a list of entries that normalise to nothing, allow no remote.
	if PushAllowed("git@github.com:wake/purdex.git", nil) || PushAllowed("git@github.com:wake/purdex.git", []string{"", "  "}) {
		t.Error("an empty allow-list pushed")
	}
	if PushAllowed("", []string{""}) {
		t.Error("an empty remote matched an empty entry")
	}
}
