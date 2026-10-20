package push

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/notify-title-cases.json is the one rule for an agent push's title (notify-title spec §1, §3.4). The Mac's
// notification content is to run the same file against its own port (N-3), so the two cannot drift: a rule changes here
// and in the file, never in one implementation alone.
func TestNotifyTitleFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/notify-title-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Case      string `json:"case"`
		Locale    string `json:"locale"`
		Name      string `json:"name"`
		Group     string `json:"group"`
		Tmux      string `json:"tmux"`
		HostLabel string `json:"host_label"`
		Thing     string `json:"thing"`
		Want      string `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 23 {
		t.Fatalf("fixture has %d cases; it is shared with the SPA and must not shrink silently", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Case, func(t *testing.T) {
			got := NotifyTitle(NotifyLabel{Name: c.Name, Group: c.Group, Tmux: c.Tmux}, c.HostLabel, c.Thing, c.Locale)
			if got != c.Want {
				t.Errorf("NotifyTitle(%q, %q, %q, %q, %q, %q) = %q, want %q", c.Name, c.Group, c.Tmux, c.HostLabel, c.Thing, c.Locale, got, c.Want)
			}
		})
	}
}
