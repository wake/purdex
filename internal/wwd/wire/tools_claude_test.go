package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tools.Claude is omitted when it is null, so a record of a host without claude reads as every record did before the field (#3908), and a record
// from a peer that predates the field reads with no claude.
func TestToolsClaudeIsOmittedWhenNull(t *testing.T) {
	b, err := json.Marshal(Tools{PHP: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "claude") {
		t.Errorf("a null claude is written: %s", b)
	}
	var old Tools
	if err := json.Unmarshal([]byte(`{"go":"1.26.0","node":null,"pnpm":null,"php":[],"xcode":null}`), &old); err != nil || old.Claude != nil {
		t.Errorf("old record: %v %v", old.Claude, err)
	}
	v := "2.1.306"
	b, _ = json.Marshal(Tools{Claude: &v})
	var back Tools
	if err := json.Unmarshal(b, &back); err != nil || back.Claude == nil || *back.Claude != "2.1.306" {
		t.Errorf("round trip: %s %v", b, err)
	}
}
