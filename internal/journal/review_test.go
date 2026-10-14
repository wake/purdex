package journal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpsertKeepsUserHeadingBetweenEntries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	upsertAll(t, file, sample()[:2])
	b, _ := os.ReadFile(file)
	s := strings.Replace(string(b), "### 14:33", "### 我的備註\n不可刪內容\n\n### 14:33", 1)
	if s == string(b) {
		t.Fatal("setup: heading not found")
	}
	if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	e := sample()[1]
	e.Text = "changed"
	if err := Upsert(file, e); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(file)
	if !strings.Contains(string(b), "### 我的備註\n不可刪內容") || !strings.Contains(string(b), "changed") {
		t.Fatalf("user content lost:\n%s", b)
	}
}

func TestConcurrentWritesKeepAll(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Upsert(file, Entry{ID: fmt.Sprintf("c:%d", i), Kind: KindComm, At: t0, Text: "x"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, _ := Parse(file)
	if len(got) != 20 {
		t.Fatalf("kept %d of 20", len(got))
	}
}

func TestUpsertRefusesCorruptMetadata(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	if err := os.WriteFile(file, []byte("## 2026-10-14\n\n### 14:32 · x\n<!-- j {\"id\":\"wb:1:1\",\"kind\":\"comm\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	err := Upsert(file, Entry{ID: "wb:1:1", Kind: KindComm, At: t0, Text: "x"})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatal("file changed")
	}
}

func TestWriteKeepsFileMode(t *testing.T) {
	file := filepath.Join(t.TempDir(), "j.md")
	upsertAll(t, file, sample()[:1])
	if err := os.Chmod(file, 0o664); err != nil {
		t.Fatal(err)
	}
	upsertAll(t, file, sample()[1:2])
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o664 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

func TestProjectOfNonRepoNotCachedAndBareIsOwnRoot(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if p, _ := ProjectOf(context.Background(), dir); p.CommonDir != "" {
		t.Fatalf("%v", p)
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	p, err := ProjectOf(context.Background(), dir)
	real, _ := filepath.EvalSymlinks(dir)
	if err != nil || p.CommonDir == "" || p.Root != real {
		t.Fatalf("after init: %v %v (want root %s)", p, err, real)
	}
}
