package cc

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

func TestDetectStatuslineMode_None(t *testing.T) {
	path := writeSettings(t, `{}`)
	m, err := detectStatuslineMode(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != "none" {
		t.Errorf("mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_NullField(t *testing.T) {
	path := writeSettings(t, `{"statusLine": null}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "none" {
		t.Errorf("null statusLine: mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_NonObject(t *testing.T) {
	path := writeSettings(t, `{"statusLine": "raw string"}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "none" {
		t.Errorf("non-object statusLine: mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_Pdx(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/opt/homebrew/bin/pdx statusline-proxy"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "pdx" {
		t.Errorf("mode = %q, want pdx", m.Mode)
	}
}

func TestDetectStatuslineMode_Wrapped(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/a/b/pdx statusline-proxy --inner 'ccstatusline --format compact'"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "wrapped" {
		t.Errorf("mode = %q, want wrapped", m.Mode)
	}
	if m.Inner != "ccstatusline --format compact" {
		t.Errorf("inner = %q", m.Inner)
	}
}

func TestDetectStatuslineMode_WrappedWithSingleQuoteEscape(t *testing.T) {
	// Shell: --inner 'it'\''s'   after escape
	// JSON encodes the literal backslash as \\, so the on-disk value is 'it'\''s'.
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/x/pdx statusline-proxy --inner 'it'\\''s'"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "wrapped" {
		t.Errorf("mode = %q, want wrapped", m.Mode)
	}
	if m.Inner != "it's" {
		t.Errorf("inner = %q, want \"it's\"", m.Inner)
	}
}

func TestDetectStatuslineMode_Unmanaged(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "ccstatusline --format compact"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "unmanaged" {
		t.Errorf("mode = %q, want unmanaged", m.Mode)
	}
	if m.Inner != "ccstatusline --format compact" {
		t.Errorf("inner (raw command) = %q", m.Inner)
	}
}

func TestDetectStatuslineMode_MissingFile(t *testing.T) {
	m, err := detectStatuslineMode(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be ok: %v", err)
	}
	if m.Mode != "none" {
		t.Errorf("missing file: mode = %q, want none", m.Mode)
	}
}
