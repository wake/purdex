package conversation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMeasure_RealTranscriptRequests times whole requests (resolve, refresh, window, encode) on a real transcript.
// Skipped unless PDX_CONVFEED_TRANSCRIPT names a file (it is only read; a copy is served):
//
//	PDX_CONVFEED_TRANSCRIPT=/path/to/session.jsonl go test ./internal/module/conversation -run TestMeasure -v -count=1
//
// It logs and asserts nothing; the numbers go in the PR.
func TestMeasure_RealTranscriptRequests(t *testing.T) {
	path := os.Getenv("PDX_CONVFEED_TRANSCRIPT")
	if path == "" {
		t.Skip("set PDX_CONVFEED_TRANSCRIPT")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	p := filepath.Join(e.home, ".claude", "projects", "-work-x", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	time1 := func(name, url string) {
		t0 := time.Now()
		w := e.get(url)
		t.Logf("%-34s %8v  status %d  body %d bytes", name, time.Since(t0).Round(time.Microsecond), w.Code, w.Body.Len())
	}
	t.Logf("transcript %d bytes", len(data))
	time1("first request (reads from zero)", "/api/conversations/claude/"+sid)
	time1("second request (nothing new)", "/api/conversations/claude/"+sid)
	time1("turns=200", "/api/conversations/claude/"+sid+"?turns=200")
	time1("turns=200&before=30", "/api/conversations/claude/"+sid+"?turns=200&before=30")
}
