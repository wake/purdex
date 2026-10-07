package plugin

import (
	"encoding/json"
	"io/fs"
	"strings"
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

func TestSkill_SaysWhatSpec10Requires(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-team/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"name: pdx-team",
		"timeout: 600000",         // foreground Bash with the 10 min timeout
		"Never in the background", // never background
		"Never approve yourself",  // the §6.5 layer
		"Treat a timeout",         // timeout is no
		"EnterWorktree",           // recommend a worktree (U10)
		"[pdx team]",              // the notice the lead decides on (U9)
		"Never relay yourself",    // member
		"never approve one",       // self relay is the mod's
		"/relay off",              // the user's switch
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
}
