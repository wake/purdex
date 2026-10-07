package conversations

import (
	"encoding/json"
	"os"
	"testing"
)

func TestIsTestCwd_SharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/test-cwd-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Cwd  string `json:"cwd"`
		Want bool   `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 12 {
		t.Fatalf("case table too small: %d", len(cases))
	}
	for _, c := range cases {
		if got := IsTestCwd(c.Cwd); got != c.Want {
			t.Errorf("IsTestCwd(%q) = %v, want %v", c.Cwd, got, c.Want)
		}
	}
}
