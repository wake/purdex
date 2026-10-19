package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigSilenceSettings(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	c, err := LoadConfig(filepath.Join(home, "none.toml"), home)
	if err != nil || c.Advisor.PartnerCheck {
		t.Fatalf("the partner check is off unless a host with a Purdex daemon asks: %+v err %v", c.Advisor, err)
	}
	body := "[advisor]\npartner_check = true\n" +
		"[[peers]]\nalias = \"mserv\"\naddr = \"100.64.0.6\"\n" +
		"[[peers]]\nalias = \"air19\"\naddr = \"100.64.0.1\"\nalert_unreachable = false\n"
	os.WriteFile(p, []byte(body), 0o600)
	c, err = LoadConfig(p, home)
	if err != nil || !c.Advisor.PartnerCheck || len(c.Peers) != 2 {
		t.Fatalf("%+v err %v", c, err)
	}
	if !c.Peers[0].AlertsUnreachable() {
		t.Error("a peer is told when unreachable unless it says otherwise")
	}
	if c.Peers[1].AlertsUnreachable() {
		t.Error("air19 turns the alert off")
	}
}
