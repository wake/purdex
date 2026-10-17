package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func load(t *testing.T, body string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(p, home)
}

func TestConfigWorkerSlotsAndOwner(t *testing.T) {
	c, err := load(t, "[worker]\nenabled = true\nheavy = 0\nlight = 1\n[owner]\nenabled = true\nidle_minutes = 3\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Worker.Slots(); got != (wire.Slots{Heavy: 0, Light: 1}) {
		t.Errorf("explicit zero lost: %+v", got)
	}
	if !c.Owner.Enabled || c.Owner.IdleMinutes != 3 {
		t.Errorf("%+v", c.Owner)
	}
	c, _ = load(t, "")
	if got := c.Worker.Slots(); got != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Errorf("defaults: %+v", got)
	}
	if c.Owner.Enabled {
		t.Error("owner rule must be off by default")
	}
}

func TestConfigPeers(t *testing.T) {
	c, err := load(t, "[[peers]]\nalias = \"air26\"\naddr = \"100.64.0.4\"\n[[peers]]\nalias = \"mserv\"\naddr = \"100.64.0.6:7999\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Peers) != 2 || c.Peers[0].HostPort() != "100.64.0.4:7871" || c.Peers[1].HostPort() != "100.64.0.6:7999" {
		t.Fatalf("%+v", c.Peers)
	}
	// the pool token goes to these addresses: nothing outside loopback/tailnet, no names
	for _, bad := range []string{
		"[[peers]]\nalias = \"x\"\naddr = \"192.168.1.5\"\n",
		"[[peers]]\nalias = \"x\"\naddr = \"example.com\"\n",
		"[[peers]]\nalias = \"x\"\naddr = \"0.0.0.0:7871\"\n",
		"[[peers]]\nalias = \"x\"\naddr = \"100.64.0.4:abc\"\n",
		"[[peers]]\nalias = \"x\"\naddr = \"100.64.0.4:99999\"\n",
		"[[peers]]\nalias = \"x\"\naddr = \"100.64.0.4:0\"\n",
		"[[peers]]\nalias = \"\"\naddr = \"100.64.0.4\"\n",
		"[[peers]]\nalias = \"a\"\naddr = \"100.64.0.4\"\n[[peers]]\nalias = \"a\"\naddr = \"100.64.0.5\"\n",
	} {
		if _, err := load(t, bad); err == nil || !strings.Contains(err.Error(), "peers") {
			t.Errorf("accepted %q (err %v)", bad, err)
		}
	}
}
