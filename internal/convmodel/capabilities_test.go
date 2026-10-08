package convmodel

import (
	"encoding/json"
	"slices"
	"testing"
)

// valueSets is [D §13] exactly: every capability and the values it may take.
var valueSets = map[string][]string{
	"source":            {"mod", "transcript", "nexen", "app_server"},
	"text_streaming":    {"none", "message", "token"},
	"thinking":          {"none", "duration", "summary"},
	"answer_question":   {"none", "terminal_only", "remote"},
	"answer_permission": {"none", "terminal_only", "remote"},
	"answer_plan":       {"none", "terminal_only", "remote"},
	"usage":             {"none", "context", "full"},
	"todo":              {"none", "partial", "full"},
	"subagent":          {"none", "partial", "full"},
	"background_tasks":  {"none", "partial", "full"},
	"peer_inbound":      {"none", "partial", "full"},
	"send":              {"keys", "prompt", "turn"},
	"interrupt":         {"keys", "rpc"},
	"steer":             {"none", "partial", "full"},
}

var reasonValues = []string{"no_mod", "terminal_only", "pending_p8b", "not_wired", "daemon_too_old", "provider_unsupported"}

func TestTranscriptCapabilities_FailClosed(t *testing.T) {
	b, err := json.Marshal(TranscriptCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{
		"source": "transcript", "text_streaming": "message", "thinking": "duration", "subagent": "partial",
	}
	var reasons map[string]string
	if err := json.Unmarshal(got["reasons"], &reasons); err != nil {
		t.Fatalf("reasons: %v", err)
	}

	for name, values := range valueSets {
		raw, present := got[name]
		if want, ok := declared[name]; ok {
			var v string
			if !present || json.Unmarshal(raw, &v) != nil || v != want {
				t.Errorf("%s = %s, want %q", name, raw, want)
			}
			if !slices.Contains(values, want) {
				t.Errorf("%s: %q is outside [D §13]", name, want)
			}
			if _, has := reasons[name]; has {
				t.Errorf("declared %s must not carry a reason", name)
			}
			continue
		}
		if present {
			t.Errorf("%s declared (%s), want omitted: undeclared = unsupported", name, raw)
		}
		if reasons[name] != "not_wired" {
			t.Errorf("reasons[%s] = %q, want not_wired", name, reasons[name])
		}
	}

	for key, val := range reasons {
		if _, ok := valueSets[key]; !ok {
			t.Errorf("reasons names unknown capability %q", key)
		}
		if !slices.Contains(reasonValues, val) {
			t.Errorf("reasons[%s] = %q outside [D §13]", key, val)
		}
	}
	for key := range got {
		if _, ok := valueSets[key]; !ok && key != "reasons" {
			t.Errorf("unexpected field %q", key)
		}
	}
	if len(reasons) != len(valueSets)-len(declared) {
		t.Errorf("%d reasons, want %d", len(reasons), len(valueSets)-len(declared))
	}
}

func TestTranscriptCapabilities_FreshMapEachCall(t *testing.T) {
	a := TranscriptCapabilities()
	a.Reasons["send"] = "mutated"
	if TranscriptCapabilities().Reasons["send"] != "not_wired" {
		t.Error("callers share one Reasons map")
	}
}
