package team

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNormaliseTeamLabel(t *testing.T) {
	ok := map[string]string{
		"":                     "",
		"   ":                  "",
		"租約":                   "租約",
		"  A 線 ":               "A 線",
		"A線派工":                 "A線派工",  // 7
		"資源租約派":                "資源租約派", // 10: five Chinese characters
		"lease-p1":             "lease-p1",
		"0123456789":           "0123456789",           // 10 ASCII
		"資源租約a":                "資源租約a",                // 9
		"éééééééééé": "éééééééééé", // combining marks weigh 0
	}
	for in, want := range ok {
		if got, err := NormaliseTeamLabel(in); err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, err, want)
		}
	}
	for name, in := range map[string]string{
		"11 columns, ASCII":     "01234567890",
		"12 columns, Chinese":   "資源租約派工",
		"Chinese 5 + 1":         "資源租約派a",
		"emoji":                 "😀😀😀😀😀😀",
		"control character":     "a\x07b",
		"newline inside":        "a\nb",
		"invalid UTF-8":         "a\xffb",
		"over 64 bytes, narrow": strings.Repeat("á", 40),
	} {
		_, err := NormaliseTeamLabel(in)
		if !errors.Is(err, ErrTeamLabelInvalid) {
			t.Errorf("%s: %q accepted (err %v)", name, in, err)
		}
	}
	// Counted in columns, not in runes: five Chinese characters are 5 runes and
	// 10 columns, eleven ASCII are 11 runes and 11 columns.
	if _, err := NormaliseTeamLabel("資源租約派"); err != nil {
		t.Error("5 Chinese characters refused")
	}
}

// Every D-L3 example, and each separator.
func TestDeriveTeamLabel(t *testing.T) {
	for in, want := range map[string]string{
		"A 線：資源租約＋派工回報":  "A 線",
		"lease-p1: foo":  "lease-p1",
		"resource-lease": "", // 14: one compound word, too wide, never cut at the hyphen
		"lease-p1":       "lease-p1",
		"A - lease":      "A",
		"A -lease":       "A -lease", // the hyphen has no space after it: not a separator, the whole name fits (8)
		"資源／派工":          "資源",
		"I/O lease":      "I/O lease",
		"I / O lease":    "I",
		"介面線":            "介面線",
		"資源租約與派工回報":      "", // no separator, 20 wide
		"：開頭":            "",
		"   ":            "",
		"":               "",
		":":              "",
		"介面線:右邊 panel":   "介面線",
		"介面線：右邊":         "介面線",
		"介面線／右邊":         "介面線",
		"介面線|右邊":         "介面線",
		"介面線｜右邊":         "介面線",
		"介面線－右邊":         "介面線",
		"介面線—右邊":         "介面線",
		"介面線 － 右邊":       "介面線",
		"資源租約與派工回報：說明":   "", // the first part is too wide: no label, not a cut
		"  A 線  ：  x  ":  "A 線",
		"資源租約派/工":        "", // 13 wide, and a / with no spaces is no separator: no label, no cut
	} {
		if got := DeriveTeamLabel(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// The derived label is always one NormaliseTeamLabel accepts, or empty.
func TestDeriveTeamLabelIsAlwaysValid(t *testing.T) {
	for _, name := range []string{"A 線：x", "x", "資源租約與派工回報", "a:b:c", strings.Repeat("長", 40), "é" + strings.Repeat("́", 80)} {
		got := DeriveTeamLabel(name)
		if again, err := NormaliseTeamLabel(got); err != nil || again != got {
			t.Errorf("%q -> %q does not pass the label rule: %v", name, got, err)
		}
	}
}

// Printable but invisible is not a label: it would stand for "none" without
// being it.
func TestNormaliseTeamLabelNeedsAVisibleCharacter(t *testing.T) {
	for _, in := range []string{"\ufe0f", "\u0301\u0301", "\U0001F3FD", "\u0301\ufe0f"} {
		if _, err := NormaliseTeamLabel(in); !errors.Is(err, ErrTeamLabelInvalid) {
			t.Errorf("%q accepted (err %v)", in, err)
		}
	}
	if got, err := NormaliseTeamLabel("a\u0301"); err != nil || got != "a\u0301" {
		t.Errorf("a with a combining mark: %q %v", got, err)
	}
}

// Invalid UTF-8 never panics and never makes a label.
func TestDeriveTeamLabelInvalidUTF8(t *testing.T) {
	for _, in := range []string{"\xff:", "a\xff:b", "\xff", "ab\xc3", "A \xe2\x82 - x", ":\xff"} {
		if got := DeriveTeamLabel(in); got != "" {
			t.Errorf("%q -> %q, want no label", in, got)
		}
	}
}

// The wire names of the label (spec D-L4/D-L5): the payload, the team and the
// roster always carry team_label; a grant and a create request only when set.
func TestTeamLabelWireShapes(t *testing.T) {
	lp, _ := json.Marshal(LeadPayload{Reason: "r", MaxMembers: 3, Roots: []string{"/w"}})
	if !strings.Contains(string(lp), `"team_name":"","team_label":""`) {
		t.Errorf("LeadPayload = %s, want team_label always present", lp)
	}
	g, _ := json.Marshal(Grant{MaxMembers: 3})
	if strings.Contains(string(g), "team_label") {
		t.Errorf("Grant without a label = %s, want the key absent", g)
	}
	empty, label := "", "A 線"
	g, _ = json.Marshal(Grant{MaxMembers: 3, TeamLabel: &empty})
	if !strings.Contains(string(g), `"team_label":""`) {
		t.Errorf("Grant with an empty label = %s, want the key present", g)
	}
	g, _ = json.Marshal(Grant{MaxMembers: 3, TeamLabel: &label})
	if !strings.Contains(string(g), `"team_label":"A 線"`) {
		t.Errorf("Grant = %s", g)
	}
	cr, _ := json.Marshal(CreateApprovalRequest{ID: "i", Kind: KindLead, Reason: "r"})
	if strings.Contains(string(cr), "team_label") {
		t.Errorf("CreateApprovalRequest without a label = %s", cr)
	}
	cr, _ = json.Marshal(CreateApprovalRequest{ID: "i", Kind: KindLead, Reason: "r", TeamLabel: "A 線"})
	if !strings.Contains(string(cr), `"team_label":"A 線"`) {
		t.Errorf("CreateApprovalRequest = %s", cr)
	}
	tm, _ := json.Marshal(Team{TeamLabel: "A 線"})
	tr, _ := json.Marshal(TeamRoster{TeamLabel: "A 線"})
	if !strings.Contains(string(tm), `"team_label":"A 線"`) || !strings.Contains(string(tr), `"team_label":"A 線"`) {
		t.Errorf("Team %s, TeamRoster %s", tm, tr)
	}
}

// The shared derive fixture: the App (spa/src/lib/team/label.ts) mirrors
// DeriveTeamLabel to show the label the daemon will take, and reads the same
// file, so the two cannot drift.
func TestDeriveTeamLabelFixture(t *testing.T) {
	b, err := os.ReadFile("../../testdata/teamlabel/derive.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Input, Label string }
	if err := json.Unmarshal(b, &cases); err != nil || len(cases) < 20 {
		t.Fatalf("fixture: %v (%d cases)", err, len(cases))
	}
	for _, c := range cases {
		if got := DeriveTeamLabel(c.Input); got != c.Label {
			t.Errorf("DeriveTeamLabel(%q) = %q, want %q", c.Input, got, c.Label)
		}
	}
}
