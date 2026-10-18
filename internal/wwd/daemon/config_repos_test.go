package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCfg(t *testing.T, body string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(p, home)
}

func TestConfigReposTableAndHomeExpansion(t *testing.T) {
	c, err := loadCfg(t, "[repos]\npurdex = \"~/Assist/purdex\"\nmlab = \"/srv/mlab\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repos) != 2 || c.Repos["mlab"] != "/srv/mlab" {
		t.Fatalf("%v", c.Repos)
	}
	if got := c.Repos["purdex"]; !strings.HasSuffix(got, "/Assist/purdex") || strings.HasPrefix(got, "~") {
		t.Fatalf("~ is expanded against home: %q", got)
	}
	if c, err := loadCfg(t, ""); err != nil || len(c.Repos) != 0 {
		t.Fatalf("no [repos] is fine: %v %v", c.Repos, err)
	}
}

// A peer alias is held to the one rule the pool routes also apply (wire.ValidAlias): what the config accepts, the pool can name.
func TestConfigPeerAliasUsesTheSharedRule(t *testing.T) {
	peer := func(a string) string { return "[[peers]]\nalias = \"" + a + "\"\naddr = \"127.0.0.1\"\n" }
	if _, err := loadCfg(t, peer(strings.Repeat("a", 128))); err != nil {
		t.Errorf("128 bytes is allowed: %v", err)
	}
	for name, a := range map[string]string{"too long": strings.Repeat("a", 129), "control": `a\nb`} {
		if _, err := loadCfg(t, peer(a)); err == nil || !strings.Contains(err.Error(), "peers") {
			t.Errorf("%s: want a peers error, got %v", name, err)
		}
	}
}

func TestConfigReposRejectsBadNamesAndPaths(t *testing.T) {
	for _, body := range []string{
		"[repos]\n\"a b\" = \"/x\"\n",        // a name that cannot be a label
		"[repos]\n\"a:b\" = \"/x\"\n",        // a colon would forge another label namespace
		"[repos]\n\"\" = \"/x\"\n",           // empty
		"[repos]\npurdex = \"relative/p\"\n", // not absolute
		"[repos]\npurdex = \"\"\n",           // empty path
	} {
		if _, err := loadCfg(t, body); err == nil || !strings.Contains(err.Error(), "repos") {
			t.Errorf("%q should be refused naming repos, got %v", body, err)
		}
	}
}
