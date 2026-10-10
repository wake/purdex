package teammod

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2254: a team root is canonicalised once, when it is granted, and stored as a string. What is judged at a spawn is the
// root AS IT IS NOW (re-resolved, still a directory, still itself) against the cwd AS IT IS NOW (resolved), by path
// components. These are the regression tests of that rule for the forwarded spawn (cross-host team spec §5.5, X4a).

func mustMkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Containment is by path components, never by string prefix: /a/bc is not under /a/b; a child named like "..x" is.
// Mutation gate: containment by strings.HasPrefix → the sibling case is admitted (red); by HasPrefix(rel, "..") → the
// "..x" child is refused (red).
func TestUnderRoots_ByPathComponents(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := mustMkdir(t, filepath.Join(base, "b"))
	sibling := mustMkdir(t, filepath.Join(base, "bc"))
	dotted := mustMkdir(t, filepath.Join(root, "..x"))
	deep := mustMkdir(t, filepath.Join(root, "p", "q"))
	for name, c := range map[string]struct {
		dir  string
		want bool
	}{
		"the root itself":              {root, true},
		"a child":                      {deep, true},
		"a child named like ..x":       {dotted, true},
		"a sibling sharing the name":   {sibling, false},
		"the parent":                   {base, false},
		"a sibling with a dotted tail": {mustMkdir(t, filepath.Join(base, "b.old")), false},
	} {
		if got := underRoots(c.dir, []string{root}); got != c.want {
			t.Errorf("underRoots(%s): %v, want %v", name, got, c.want)
		}
		if _, got := resolveUnderRoots([]string{root}, c.dir); got != c.want {
			t.Errorf("resolveUnderRoots(%s): %v, want %v", name, got, c.want)
		}
	}
}

// The root changed since it was granted: renamed away, its parent swapped for a symlink, or turned into a file. None of
// them grants anything, including the directory the root used to be.
func TestResolveUnderRoots_ARootThatChangedGrantsNothing(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := mustMkdir(t, filepath.Join(base, "outside"))
	mustMkdir(t, filepath.Join(outside, "grant", "work"))

	// renamed away: the stored path no longer exists
	parent := mustMkdir(t, filepath.Join(base, "p1"))
	root := mustMkdir(t, filepath.Join(parent, "grant"))
	work := mustMkdir(t, filepath.Join(root, "work"))
	if _, ok := resolveUnderRoots([]string{root}, work); !ok {
		t.Fatal("setup: the work dir is not under the root")
	}
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveUnderRoots([]string{root}, work); ok {
		t.Fatal("a renamed-away root still granted its old work dir")
	}
	if _, ok := resolveUnderRoots([]string{root}, filepath.Join(root+"-away", "work")); ok {
		t.Fatal("the renamed directory is granted under its new name")
	}

	// an ancestor of the root replaced by a symlink to elsewhere
	parent2 := mustMkdir(t, filepath.Join(base, "p2"))
	root2 := mustMkdir(t, filepath.Join(parent2, "grant"))
	mustMkdir(t, filepath.Join(root2, "work"))
	if err := os.Rename(parent2, parent2+"-away"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent2); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveUnderRoots([]string{root2}, filepath.Join(root2, "work")); ok {
		t.Fatal("a root behind a swapped ancestor still granted")
	}

	// a root replaced by a file
	root3 := filepath.Join(base, "p3")
	mustMkdir(t, root3)
	if err := os.Remove(root3); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root3, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveUnderRoots([]string{root3}, root3); ok {
		t.Fatal("a root that became a file granted")
	}
}

// The forwarded runner judges again before it creates the session, and again on the pane's real directory before any key:
// the GRANTED ROOT replaced by a symlink to elsewhere (the dir inside it exists at the target too) is refused either way.
// Mutation gates: skip the check in spawnCreate → the first case creates a session (red); skip it in checkLaunchPane → the
// second case launches (red).
func TestRemoteSpawn_ARootSwappedAfterTheAcceptIsNeverLaunched(t *testing.T) {
	for _, c := range []struct {
		name, reason   string
		creates, kills int
		duringCreate   bool
	}{
		{"swapped before the create", team.SpawnReasonCreateFailed, 0, 0, false},
		{"swapped during the create", team.SpawnReasonLaunchFailed, 1, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, base := newSpawnFixture(t, 2)
			grantRoot := mustMkdir(t, filepath.Join(base, "grant"))
			work := mustMkdir(t, filepath.Join(grantRoot, "work"))
			outside := mustMkdir(t, filepath.Join(base, "elsewhere"))
			mustMkdir(t, filepath.Join(outside, "work"))
			grantLeadHost(f, true, grantRoot)
			f.register("%0", "sid-m1")
			swap := func() {
				if err := os.Rename(grantRoot, grantRoot+"-away"); err != nil {
					t.Error(err)
					return
				}
				if err := os.Symlink(outside, grantRoot); err != nil {
					t.Error(err)
				}
			}
			if c.duringCreate {
				f.sessions.beforeCreate = swap
			} else {
				f.m.beforeSpawnStep = func(op spawnRow) {
					if op.Step == team.StepAccepted {
						swap()
					}
				}
			}
			if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, work)); code != http.StatusOK {
				t.Fatalf("accept = %d %s", code, body)
			}
			f.m.spawnWG.Wait()
			op, _, _ := f.m.store.GetSpawnOp(cmdUUID1)
			if op.State != team.SpawnFailed || op.Reason != c.reason {
				t.Fatalf("op = %+v, want failed %s", op, c.reason)
			}
			if f.sessions.count() != c.creates || len(f.tmux.KillIfInstanceCalls()) != c.kills || len(f.tmux.RawKeysSent()) != 0 {
				t.Fatalf("creates %d kills %+v keys %+v", f.sessions.count(), f.tmux.KillIfInstanceCalls(), f.tmux.RawKeysSent())
			}
			facts := f.factsToLead()
			if len(facts) != 1 || facts[0].Kind != team.FactSpawnFailed {
				t.Fatalf("facts = %+v", facts)
			}
		})
	}
}
