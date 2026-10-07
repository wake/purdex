package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transcriptFixture struct {
	m    *Module
	home string
	path string
}

// newTranscriptFixture wires a module whose owner resolver returns `owner`
// (with TranscriptPath filled in) for any code, under a temp HOME.
func newTranscriptFixture(t *testing.T, content string, agentType string) *transcriptFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".claude", "projects", "-p", "sid1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(t)
	m.ownerResolver = func(context.Context, string) (PaneOwner, bool, error) {
		return PaneOwner{AgentType: agentType, SessionID: "sid1", Cwd: "/p", TranscriptPath: p}, true, nil
	}
	return &transcriptFixture{m: m, home: home, path: p}
}

type transcriptResp struct {
	TranscriptID string   `json:"transcript_id"`
	Size         int64    `json:"size"`
	Mtime        int64    `json:"mtime"`
	StartOffset  int64    `json:"start_offset"`
	EndOffset    int64    `json:"end_offset"`
	More         bool     `json:"more"`
	Reset        bool     `json:"reset"`
	Lines        []string `json:"lines"`
}

func (f *transcriptFixture) get(t *testing.T, query string) (int, transcriptResp, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	f.m.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/sessions/abc/transcript"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var ok transcriptResp
	var generic map[string]any
	body := rec.Body.Bytes()
	_ = json.Unmarshal(body, &ok)
	_ = json.Unmarshal(body, &generic)
	return rec.Code, ok, generic
}

func TestTranscriptHandlerTail(t *testing.T) {
	f := newTranscriptFixture(t, "l1\nl2\nl3\npart", "cc")
	code, r, _ := f.get(t, "?tail=2")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if strings.Join(r.Lines, ",") != "l2,l3" || r.StartOffset != 3 || r.EndOffset != 9 || r.Size != 13 || r.More || r.Reset {
		t.Fatalf("%+v", r)
	}
	if r.TranscriptID != "sid1.jsonl" || r.Mtime == 0 {
		t.Fatalf("%+v", r)
	}
	// default tail returns everything complete
	_, r, _ = f.get(t, "")
	if len(r.Lines) != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestTranscriptHandlerAfterIncremental(t *testing.T) {
	f := newTranscriptFixture(t, "l1\nl2\n", "cc")
	_, r, _ := f.get(t, "?after=3")
	if strings.Join(r.Lines, ",") != "l2" || r.EndOffset != 6 {
		t.Fatalf("%+v", r)
	}
	fh, _ := os.OpenFile(f.path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = fh.WriteString("l3\n")
	fh.Close()
	_, r, _ = f.get(t, "?after=6&transcript_id=sid1.jsonl")
	if strings.Join(r.Lines, ",") != "l3" || r.StartOffset != 6 || r.EndOffset != 9 || r.Reset {
		t.Fatalf("%+v", r)
	}
}

func TestTranscriptHandlerReset(t *testing.T) {
	f := newTranscriptFixture(t, "l1\nl2\n", "cc")
	_, r, _ := f.get(t, "?after=100")
	if !r.Reset || len(r.Lines) != 0 || r.EndOffset != 6 || r.StartOffset != 6 {
		t.Fatalf("after>size: %+v", r)
	}
	_, r, _ = f.get(t, "?after=0&transcript_id=old.jsonl")
	if !r.Reset || len(r.Lines) != 0 || r.EndOffset != 6 {
		t.Fatalf("id change: %+v", r)
	}
	// lines must be a JSON array, not null, on reset
	_, _, g := f.get(t, "?after=100")
	if _, isArr := g["lines"].([]any); !isArr {
		t.Fatalf("lines = %#v", g["lines"])
	}
}

func TestTranscriptHandlerParamErrors(t *testing.T) {
	f := newTranscriptFixture(t, "l1\n", "cc")
	for _, q := range []string{"?tail=1&after=0", "?tail=abc", "?tail=0", "?tail=-1", "?after=-1", "?after=x"} {
		if code, _, _ := f.get(t, q); code != 400 {
			t.Errorf("%s -> %d", q, code)
		}
	}
	// tail above the cap is clamped, not rejected
	if code, _, _ := f.get(t, "?tail=999999"); code != 200 {
		t.Errorf("clamp -> %d", code)
	}
}

func TestTranscriptHandlerUnsupportedAgent(t *testing.T) {
	f := newTranscriptFixture(t, "l1\n", "codex")
	code, _, g := f.get(t, "")
	if code != 404 || g["error"] != "unsupported" || g["agent_type"] != "codex" {
		t.Fatalf("%d %v", code, g)
	}
}

func TestTranscriptHandlerNoAgentAndLookupFailure(t *testing.T) {
	f := newTranscriptFixture(t, "l1\n", "cc")
	f.m.ownerResolver = func(context.Context, string) (PaneOwner, bool, error) { return PaneOwner{}, false, nil }
	if code, _, g := f.get(t, ""); code != 404 || g["error"] != "no_agent" {
		t.Fatalf("%d %v", code, g)
	}
	f.m.ownerResolver = func(context.Context, string) (PaneOwner, bool, error) {
		return PaneOwner{}, false, errors.New("tmux down")
	}
	if code, _, g := f.get(t, ""); code != 503 || g["error"] != "lookup_failed" {
		t.Fatalf("%d %v", code, g)
	}
}

func TestTranscriptHandlerPathErrors(t *testing.T) {
	f := newTranscriptFixture(t, "l1\n", "cc")
	// file gone
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if code, _, g := f.get(t, ""); code != 404 || g["error"] != "file_missing" {
		t.Fatalf("%d %v", code, g)
	}
	// provenance path outside projects (untrusted) -> no_transcript
	outside := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.m.ownerResolver = func(context.Context, string) (PaneOwner, bool, error) {
		return PaneOwner{AgentType: "cc", TranscriptPath: outside}, true, nil
	}
	code, _, g := f.get(t, "")
	if code != 404 || g["error"] != "no_transcript" {
		t.Fatalf("%d %v", code, g)
	}
	// nothing known at all
	f.m.ownerResolver = func(context.Context, string) (PaneOwner, bool, error) {
		return PaneOwner{AgentType: "cc"}, true, nil
	}
	if code, _, g := f.get(t, ""); code != 404 || g["error"] != "no_transcript" {
		t.Fatalf("%d %v", code, g)
	}
}

func TestTranscriptHandlerLineTooLarge(t *testing.T) {
	f := newTranscriptFixture(t, "a\n"+strings.Repeat("x", 8<<20+10)+"\n", "cc")
	code, _, g := f.get(t, "")
	if code != 413 || g["error"] != "line_too_large" {
		t.Fatalf("%d %v", code, g)
	}
}

func TestTranscriptHandlerOversizeLineIsOwnResponse(t *testing.T) {
	big := strings.Repeat("x", 3<<20)
	f := newTranscriptFixture(t, "a\n"+big+"\n", "cc")
	_, r, _ := f.get(t, "?after=2")
	if len(r.Lines) != 1 || len(r.Lines[0]) != len(big) || !r.More {
		t.Fatalf("lines=%d more=%v", len(r.Lines), r.More)
	}
}
