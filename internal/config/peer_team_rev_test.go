// internal/config/peer_team_rev_test.go
package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// #2340: team_roots_rev is a new field of the config file, not a table migration: an entry written before it loads with
// revision 0, and a written revision survives the roundtrip (loading the same file again changes nothing).
func TestPeerHostTeamRootsRevLegacyDefaultAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.toml")
	body := "[[peers.hosts]]\nalias = \"old\"\nurl = \"https://old.example\"\nallow_team = true\nteam_roots = [\"/srv/a\"]\n"
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := config.Load(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if h := old.Peers.Hosts[0]; h.TeamRootsRev != 0 || !reflect.DeepEqual(h.TeamRoots, []string{"/srv/a"}) {
		t.Fatalf("legacy = %+v", h)
	}
	old.Peers.Hosts[0].TeamRootsRev = 7
	out := filepath.Join(dir, "out.toml")
	if err := config.WriteFile(out, old); err != nil {
		t.Fatal(err)
	}
	again, err := config.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if h := again.Peers.Hosts[0]; h.TeamRootsRev != 7 || !reflect.DeepEqual(h.TeamRoots, []string{"/srv/a"}) {
		t.Fatalf("roundtrip = %+v", h)
	}
	if raw, _ := os.ReadFile(out); !strings.Contains(string(raw), "team_roots_rev = 7") {
		t.Fatalf("the file does not carry the revision:\n%s", raw)
	}
}
