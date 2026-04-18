package cc

import (
	"encoding/json"
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

func readSettingsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func getStatusLineCommand(t *testing.T, path string) string {
	t.Helper()
	s := readSettingsMap(t, path)
	sl, _ := s["statusLine"].(map[string]any)
	if sl == nil {
		return ""
	}
	c, _ := sl["command"].(string)
	return c
}

func TestInstallStatuslinePdx_Empty(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslinePdx(path, "/opt/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	if got != "/opt/bin/pdx statusline-proxy" {
		t.Errorf("command = %q", got)
	}
}

func TestInstallStatuslinePdx_PreservesHooks(t *testing.T) {
	path := writeSettings(t, `{"hooks": {"Stop": "something"}}`)
	if err := installStatuslinePdx(path, "/opt/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	s := readSettingsMap(t, path)
	if _, ok := s["hooks"]; !ok {
		t.Error("hooks dropped")
	}
	sl, _ := s["statusLine"].(map[string]any)
	if sl["type"] != "command" {
		t.Errorf("type = %v", sl["type"])
	}
}

func TestInstallStatuslineWrap_SimpleInner(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", "ccstatusline"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	want := "/opt/bin/pdx statusline-proxy --inner 'ccstatusline'"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestInstallStatuslineWrap_InnerWithSingleQuote(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", "it's"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	// POSIX single-quote escape: ' -> '\''
	want := "/opt/bin/pdx statusline-proxy --inner 'it'\\''s'"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	// Round-trip: detect should return the original inner.
	m, _ := detectStatuslineMode(path)
	if m.Inner != "it's" {
		t.Errorf("round-trip inner = %q, want \"it's\"", m.Inner)
	}
}

func TestInstallStatuslineWrap_InnerWithSpaceAndAmpersand(t *testing.T) {
	inner := `foo "bar" & baz 'qux'`
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", inner); err != nil {
		t.Fatal(err)
	}
	m, _ := detectStatuslineMode(path)
	if m.Inner != inner {
		t.Errorf("round-trip: got %q, want %q", m.Inner, inner)
	}
}

func TestRemoveStatusline_Pdx(t *testing.T) {
	path := writeSettings(t, `{"hooks":{"Stop":"x"},"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`)
	if err := removeStatusline(path); err != nil {
		t.Fatal(err)
	}
	s := readSettingsMap(t, path)
	if _, ok := s["statusLine"]; ok {
		t.Error("statusLine should be removed")
	}
	if _, ok := s["hooks"]; !ok {
		t.Error("hooks should be preserved")
	}
}

func TestRemoveStatusline_WrappedRestoresInner(t *testing.T) {
	path := writeSettings(t, `{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy --inner 'ccstatusline --format compact'","padding":0}}`)
	if err := removeStatusline(path); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	if got != "ccstatusline --format compact" {
		t.Errorf("restored command = %q", got)
	}
	s := readSettingsMap(t, path)
	sl, _ := s["statusLine"].(map[string]any)
	if sl["type"] != "command" {
		t.Errorf("type not preserved: %v", sl["type"])
	}
	if sl["padding"] == nil {
		t.Errorf("padding not preserved")
	}
}

func TestRemoveStatusline_UnmanagedRefuses(t *testing.T) {
	path := writeSettings(t, `{"statusLine":{"type":"command","command":"ccstatusline"}}`)
	err := removeStatusline(path)
	if err == nil {
		t.Error("expected refusal on unmanaged remove")
	}
}

func TestRemoveStatusline_None_NoOp(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := removeStatusline(path); err != nil {
		t.Fatalf("remove of none should no-op: %v", err)
	}
}

func TestWriteSettings_PreservesFileMode(t *testing.T) {
	path := writeSettings(t, `{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Any install/remove operation goes through writeSettingsAtomic.
	if err := installStatuslinePdx(path, "/opt/bin/pdx"); err != nil {
		t.Fatalf("install: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0600 {
		t.Errorf("mode = %o, want 0600", got)
	}
}

func TestWriteSettings_NoTmpLeftAfterRenameFailure(t *testing.T) {
	// Force a rename failure by making the target an un-renameable-onto directory.
	// We simulate by pointing path into a dir that can't be created; MkdirAll fails first.
	// Simpler: use an invalid path under a regular file.
	base := writeSettings(t, `{}`) // a regular file
	invalid := filepath.Join(base, "nested", "settings.json")
	// MkdirAll will fail because base is a file, not a dir.
	err := installStatuslinePdx(invalid, "/opt/bin/pdx")
	if err == nil {
		t.Fatal("expected error writing under a regular file")
	}
	// No .tmp sibling should be left next to any part of the invalid path.
	// (installs must not leave garbage on failure.)
	entries, _ := filepath.Glob(base + "*.tmp")
	if len(entries) != 0 {
		t.Errorf("tmp leak: %v", entries)
	}
}
