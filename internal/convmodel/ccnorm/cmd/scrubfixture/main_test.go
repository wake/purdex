package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_FileToFile(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "raw.jsonl"), filepath.Join(dir, "input.jsonl")
	raw := `{"type":"user","uuid":"u1","cwd":"/Users/ann/p","message":{"content":"hi ann at /Users/ann/p/x"}}` + "\n" +
		`{"type":"last-prompt"}` + "\n"
	if err := os.WriteFile(in, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := run([]string{"-in", in, "-out", out, "-home", "/Users/ann", "-user", "ann"}, strings.NewReader(""), &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "hi user at /work/fixture/x") || strings.Contains(string(got), "ann") {
		t.Errorf("output = %s", got)
	}
	if strings.Count(string(got), "\n") != 1 {
		t.Errorf("want one kept row: %s", got)
	}
	if !strings.Contains(stderr.String(), "kept 1 of 2") {
		t.Errorf("summary = %q", stderr.String())
	}
}

func TestRun_StdinToStdoutAndBadFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-user", "bob", "-home", "/Users/bob"}, strings.NewReader(`{"type":"ai-title","aiTitle":"bob"}`+"\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"aiTitle":"user"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
	if code := run([]string{"-nope"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Errorf("bad flag exit = %d, want 2", code)
	}
	if code := run([]string{"-in", "/nonexistent/x"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Errorf("missing file exit = %d, want 1", code)
	}
}
