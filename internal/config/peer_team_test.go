// internal/config/peer_team_test.go
package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func TestPeerHostTeamFieldsRoundTripAndLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{Peers: config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "lead", URL: "https://lead.example", AllowTeam: true, TeamRoots: []string{"/srv/a", "/srv/b"}},
		{Alias: "plain", URL: "https://plain.example"},
	}}}
	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := got.Peers.Hosts
	if !h[0].AllowTeam || !reflect.DeepEqual(h[0].TeamRoots, []string{"/srv/a", "/srv/b"}) {
		t.Fatalf("host0 = %+v", h[0])
	}
	if h[1].AllowTeam || len(h[1].TeamRoots) != 0 {
		t.Fatalf("host1 = %+v", h[1])
	}

	// A config written before these fields existed loads as off / none.
	legacy := filepath.Join(dir, "legacy.toml")
	body := "[[peers.hosts]]\nalias = \"old\"\nurl = \"https://old.example\"\n"
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := config.Load(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if old.Peers.Hosts[0].AllowTeam || len(old.Peers.Hosts[0].TeamRoots) != 0 {
		t.Fatalf("legacy = %+v", old.Peers.Hosts[0])
	}
}

func TestCanonicalTeamRoots(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	other := filepath.Join(base, "other")
	file := filepath.Join(base, "afile")
	link := filepath.Join(base, "link")
	for _, d := range []string{real, other} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	t.Run("canonicalises, dedups, keeps order", func(t *testing.T) {
		got, err := config.CanonicalTeamRoots([]string{link, real + "/", other, filepath.Join(real, "..", "real")})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{real, other}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("empty is a non-nil empty list", func(t *testing.T) {
		got, err := config.CanonicalTeamRoots(nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %#v err %v", got, err)
		}
	})
	for name, in := range map[string][]string{
		"relative":     {"rel/dir"},
		"tilde":        {"~/work"},
		"missing":      {filepath.Join(base, "nope")},
		"not a dir":    {file},
		"empty string": {""},
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if _, err := config.CanonicalTeamRoots(in); err == nil {
				t.Fatal("want error")
			}
		})
	}
	t.Run("rejects more than the cap", func(t *testing.T) {
		in := make([]string, config.MaxTeamRoots+1)
		for i := range in {
			p := filepath.Join(base, "d"+strings.Repeat("x", i+1))
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			in[i] = p
		}
		if _, err := config.CanonicalTeamRoots(in); err == nil {
			t.Fatal("want error")
		}
	})
}
