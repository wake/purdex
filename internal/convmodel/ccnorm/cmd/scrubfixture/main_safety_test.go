package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_RefusesSameInOut(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "raw.jsonl")
	raw := `{"type":"ai-title","aiTitle":"keep me"}` + "\n"
	if err := os.WriteFile(in, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.jsonl")
	if err := os.Symlink(in, link); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hard.jsonl")
	if err := os.Link(in, hard); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"same path": in,
		"unclean":   filepath.Join(dir, ".", "sub", "..", "raw.jsonl"),
		"symlink":   link,
		"hard link": hard,
	} {
		var stderr bytes.Buffer
		if code := run([]string{"-in", in, "-out", out}, strings.NewReader(""), &bytes.Buffer{}, &stderr); code == 0 {
			t.Errorf("%s: exit 0, want a refusal", name)
		}
		if !strings.Contains(stderr.String(), "same file") {
			t.Errorf("%s: stderr = %q", name, stderr.String())
		}
		if got, _ := os.ReadFile(in); string(got) != raw {
			t.Fatalf("%s: the input was changed: %q", name, got)
		}
	}
}

func TestRun_NoPartialOutputOnError(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "raw.jsonl")
	raw := `{"type":"ai-title","aiTitle":"ok"}` + "\n" + `SECRETMARK not json` + "\n" + `{"type":"ai-title","aiTitle":"ok2"}` + "\n"
	if err := os.WriteFile(in, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	// no output file yet: none may appear, and no temp file stays
	out := filepath.Join(dir, "out", "input.jsonl")
	if err := os.Mkdir(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-in", in, "-out", out}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("exit 0 on a bad row")
	}
	if ents, _ := os.ReadDir(filepath.Dir(out)); len(ents) != 0 {
		t.Errorf("files left in the output directory: %v", ents)
	}
	if strings.Contains(stderr.String(), "SECRETMARK") || !strings.Contains(stderr.String(), "line 2") {
		t.Errorf("stderr = %q (want the line number, not the content)", stderr.String())
	}
	// an existing output stays as it was
	if err := os.WriteFile(out, []byte("previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-in", in, "-out", out}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("exit 0 on a bad row")
	}
	if got, _ := os.ReadFile(out); string(got) != "previous\n" {
		t.Errorf("existing output replaced: %q", got)
	}
	if ents, _ := os.ReadDir(filepath.Dir(out)); len(ents) != 1 {
		t.Errorf("files left in the output directory: %v", ents)
	}
	// stdout gets nothing either
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
	var so bytes.Buffer
	if code := run(nil, strings.NewReader(raw), &so, &stderr); code == 0 || so.Len() != 0 {
		t.Errorf("stdin form: exit %d, stdout %q", code, so.String())
	}
}

func TestRun_AllowBadRowsFlag(t *testing.T) {
	raw := `{"type":"ai-title","aiTitle":"ok"}` + "\n" + `not json` + "\n"
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-allow-bad-rows"}, strings.NewReader(raw), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 || !strings.Contains(stderr.String(), "not_json=1") {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestRun_WritesCompleteFileWithReadableMode(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "raw.jsonl"), filepath.Join(dir, "input.jsonl")
	if err := os.WriteFile(in, []byte(`{"type":"ai-title","aiTitle":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-in", in, "-out", out}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("stat = %v, %v; want mode 0644", st, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Errorf("directory holds %v", ents)
	}
}
