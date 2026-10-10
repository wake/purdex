package push

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/normalise.json is the one rule for turning Markdown into a lock-screen line (push spec §5.3), shared with the
// Mac's own notification content: spa/src/lib/notification-normalise.test.ts runs the same file against its port, so the
// two cannot drift (#2144). A case changes here and in the file, never in one implementation alone.
func TestNormaliseFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/normalise.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
		Max   int    `json:"max"`
		Want  string `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatalf("fixture has %d cases; it is shared with the SPA and must not shrink silently", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := Normalise(c.Input, c.Max); got != c.Want {
				t.Errorf("Normalise(%q, %d) = %q, want %q", c.Input, c.Max, got, c.Want)
			}
		})
	}
}
