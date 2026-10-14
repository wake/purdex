package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpsertRefusesCorruptMetadataWithDashID(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	if err := os.WriteFile(file, []byte("## 2026-10-14\n\n### 14:32 · x\n<!-- j {\"id\":\"a\\u002d\\u002db\",\"kind\":\"comm\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(file, Entry{ID: "a--b", Kind: KindComm, At: t0, Text: "x"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err %v", err)
	}
}

func TestLockKeyIsCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "j.md")
	b := dir + "/./j.md"
	unlock := lockFile(a)
	done := make(chan struct{})
	go func() { lockFile(b)(); close(done) }()
	select {
	case <-done:
		t.Fatal("equivalent path took a different lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	<-done
}
