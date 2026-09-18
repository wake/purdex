package codex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

const codexFrozenVersion = "0.153.4"

type codexFrozenManifest struct {
	Tag               string `json:"tag"`
	CommitSha         string `json:"commitSha"`
	Version           string `json:"version"`
	SchemaStage       string `json:"schemaStage"`
	PayloadFixtureDir string `json:"payloadFixtureDir"`
	CatalogSummary    struct {
		Installable int `json:"installable"`
		Ignored     int `json:"ignored"`
		Unsupported int `json:"unsupported"`
	} `json:"catalogSummary"`
}

type codexFrozenEvents struct {
	Version string `json:"version"`
	Events  []struct {
		UpstreamKey string `json:"upstreamKey"`
		Purdex      struct {
			Kind            string `json:"kind"` // installable | ignored | retired
			PurdexEventName string `json:"purdexEventName"`
			Status          string `json:"status,omitempty"` // expected DeriveStatus status for the payload fixture ("" = detail-only)
		} `json:"purdex"`
	} `json:"events"`
}

func loadCodexFrozenManifest(t *testing.T) codexFrozenManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	var m codexFrozenManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func loadCodexFrozenEvents(t *testing.T) codexFrozenEvents {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-events.json"))
	if err != nil {
		t.Fatalf("load events.json: %v", err)
	}
	var e codexFrozenEvents
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("parse events.json: %v", err)
	}
	return e
}

// TestCodexEvents_ClassifyAgainstFrozenManifest: catalog ⇔ events.json,
// bidirectional. kind=installable ⇒ Handling status/detail; kind=ignored
// or retired ⇒ Handling ignored. Every catalog entry must appear exactly
// once in events.json and vice versa.
func TestCodexEvents_ClassifyAgainstFrozenManifest(t *testing.T) {
	specs := codex.NewProvider().Events()
	byUpstream := map[string]agent.HookEventSpec{}
	for _, s := range specs {
		for _, k := range s.UpstreamKeys {
			byUpstream[k] = s
		}
	}
	frozen := loadCodexFrozenEvents(t)
	if frozen.Version != codexFrozenVersion {
		t.Fatalf("events.json version = %q, want %q", frozen.Version, codexFrozenVersion)
	}
	seen := map[string]int{}
	counts := map[string]int{}
	for _, e := range frozen.Events {
		spec, ok := byUpstream[e.UpstreamKey]
		if !ok {
			t.Errorf("events.json[%s] has no catalog entry", e.UpstreamKey)
			continue
		}
		seen[spec.PurdexName]++
		counts[e.Purdex.Kind]++
		if spec.PurdexName != e.Purdex.PurdexEventName {
			t.Errorf("events.json[%s] purdexEventName=%q, catalog=%q", e.UpstreamKey, e.Purdex.PurdexEventName, spec.PurdexName)
		}
		h := agent.EffectiveHookHandling(spec)
		switch e.Purdex.Kind {
		case "installable":
			if h != agent.HookHandlingStatus && h != agent.HookHandlingDetail {
				t.Errorf("events.json[%s] kind=installable but catalog handling=%q", e.UpstreamKey, h)
			}
		case "ignored", "retired":
			if h != agent.HookHandlingIgnored {
				t.Errorf("events.json[%s] kind=%s but catalog handling=%q", e.UpstreamKey, e.Purdex.Kind, h)
			}
		default:
			t.Errorf("events.json[%s] unknown kind %q", e.UpstreamKey, e.Purdex.Kind)
		}
	}
	for _, s := range specs {
		if seen[s.PurdexName] != 1 {
			t.Errorf("catalog %q appears %d times in events.json, want 1", s.PurdexName, seen[s.PurdexName])
		}
	}
	m := loadCodexFrozenManifest(t)
	if m.Version != codexFrozenVersion || m.Tag != "rust-v"+codexFrozenVersion {
		t.Errorf("manifest version/tag = %q/%q", m.Version, m.Tag)
	}
	if m.CatalogSummary.Installable != counts["installable"] {
		t.Errorf("manifest installable=%d, events.json=%d", m.CatalogSummary.Installable, counts["installable"])
	}
	if m.CatalogSummary.Ignored != counts["ignored"]+counts["retired"] {
		t.Errorf("manifest ignored=%d, events.json ignored+retired=%d", m.CatalogSummary.Ignored, counts["ignored"]+counts["retired"])
	}
	if m.CatalogSummary.Unsupported != 0 {
		t.Errorf("manifest unsupported=%d, want 0", m.CatalogSummary.Unsupported)
	}
	if m.PayloadFixtureDir != "internal/agent/codex/testdata/codex-"+codexFrozenVersion+"-payloads/" {
		t.Errorf("manifest payloadFixtureDir = %q", m.PayloadFixtureDir)
	}
	ver, err := os.ReadFile(filepath.Join("testdata", "codex-"+codexFrozenVersion+"-version.txt"))
	if err != nil || string(ver) != codexFrozenVersion+"\n" {
		t.Errorf("version.txt = %q / %v", ver, err)
	}
}
