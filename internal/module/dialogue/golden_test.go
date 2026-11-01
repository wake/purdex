package dialogue

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
)

var update = flag.Bool("update", false, "rewrite testdata/dialogue/*.md")

const repoRoot = "../../.."

// renderFixture normalizes a cc-transcript fixture and renders every turn; turns are separated by a blank line and a
// compaction boundary is written as "<compacted | trigger>".
func renderFixture(t *testing.T, name string) string {
	t.Helper()
	input, err := os.ReadFile(filepath.Join(repoRoot, "testdata/conversation/v1/cc-transcript", name, "input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := ccnorm.New(ccnorm.Options{SessionID: scrub.FixtureSessionID})
	lines := bytes.Split(input, []byte("\n"))
	var off int64
	for _, l := range lines {
		if len(l) > 0 {
			if _, err := n.Feed(off, l); err != nil {
				t.Fatalf("feed at %d: %v", off, err)
			}
		}
		off += int64(len(l)) + 1
	}
	n.SetLive(false)
	var parts []string
	for _, turn := range n.Conversation().Turns {
		for _, b := range renderItems(turn.Items) {
			if b.compact {
				parts = append(parts, "<compacted | "+b.trigger+">\n")
			} else {
				parts = append(parts, b.text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func TestGolden(t *testing.T) {
	for _, name := range []string{"source-kinds", "peer-message", "ask-question", "subagent", "queued-mid-turn", "compact"} {
		t.Run(name, func(t *testing.T) {
			got := renderFixture(t, name)
			path := filepath.Join(repoRoot, "testdata/dialogue", name+".md")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs from golden (run with -update to rewrite)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}
