package plugin

import (
	"encoding/json"
	"io/fs"
	"testing"
)

func TestFiles_HasTheLayoutClaudeLoads(t *testing.T) {
	f := Files()
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "skills/pdx-team/SKILL.md"} {
		if _, err := fs.Stat(f, rel); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	b, err := fs.ReadFile(f, ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
	var h struct{ Modules []string }
	hb, _ := fs.ReadFile(f, "hooks/hooks.json")
	if err := json.Unmarshal(hb, &h); err != nil || len(h.Modules) != 1 || h.Modules[0] != "./register.js" {
		t.Fatalf("hooks.json = %s", hb)
	}
}
