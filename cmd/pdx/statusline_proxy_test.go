package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderMinimal_FullFields(t *testing.T) {
	raw := json.RawMessage(`{
		"model": {"id": "claude-sonnet-4-6", "display_name": "Sonnet"},
		"context_window": {"used_percentage": 23},
		"cost": {"total_cost_usd": 0.12}
	}`)
	got := renderMinimal(raw)
	want := "[pdx] Sonnet · ctx 23% · $0.12"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_MissingDisplayName(t *testing.T) {
	raw := json.RawMessage(`{"model":{"id":"claude-opus-4-7"}}`)
	got := renderMinimal(raw)
	if !strings.Contains(got, "claude-opus-4-7") {
		t.Errorf("renderMinimal = %q, expected id fallback", got)
	}
}

func TestRenderMinimal_NoCost(t *testing.T) {
	raw := json.RawMessage(`{"model":{"display_name":"Opus"},"context_window":{"used_percentage":8}}`)
	got := renderMinimal(raw)
	want := "[pdx] Opus · ctx 8%"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_Empty(t *testing.T) {
	got := renderMinimal(json.RawMessage(`{}`))
	want := "[pdx]"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestReadStdinWithTimeout_Valid(t *testing.T) {
	src := bytes.NewBufferString(`{"a":1}`)
	got := readStdinWithTimeout(src, 1)
	if string(got) != `{"a":1}` {
		t.Errorf("got %q, want JSON", got)
	}
}

func TestReadStdinWithTimeout_Empty(t *testing.T) {
	got := readStdinWithTimeout(bytes.NewBuffer(nil), 1)
	if string(got) != "{}" {
		t.Errorf("empty stdin got %q, want {}", got)
	}
}
