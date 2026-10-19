package main

import (
	"os"
	"path/filepath"
	"testing"
)

// holds.log belongs to `pdx wwd hold` alone: the shared entry of `pdx jobs` and `pdx wwd` hands out a writer that opens the file only when a
// hold writes its line (R2), so `pdx jobs ls` or `pdx wwd status` never creates or opens it.
func TestLazyLogOpensOnlyOnTheFirstWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "holds.log")
	l := &lazyLog{path: path}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the file exists before any write")
	}
	if err := l.Close(); err != nil { // never written, never opened: closing is nothing
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("closing a log that was never written created it")
	}

	l = &lazyLog{path: path}
	if _, err := l.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("two\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "one\ntwo\n" {
		t.Fatalf("content %q", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	// appended to, not replaced
	l = &lazyLog{path: path}
	_, _ = l.Write([]byte("three\n"))
	l.Close()
	if b, _ := os.ReadFile(path); string(b) != "one\ntwo\nthree\n" {
		t.Fatalf("after a second run %q", b)
	}
}

// A log that cannot be opened must not stop the command: the write is dropped, quietly.
func TestLazyLogThatCannotBeOpenedIsQuiet(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l := &lazyLog{path: filepath.Join(blocker, "holds.log")} // a directory that is a file
	if n, err := l.Write([]byte("x\n")); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v: the command must not fail for its log", n, err)
	}
	l.Close()
}
