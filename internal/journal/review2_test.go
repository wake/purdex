package journal

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
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

const twoTables = "## A\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S-1 | d | 未開始 |\n| S-2 | d | 未開始 |\n"

func TestSetStatusConcurrentKeepsBoth(t *testing.T) {
	file := writeRoadmap(t, twoTables)
	var wg sync.WaitGroup
	for _, s := range []string{"S-1", "S-2"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			if _, err := SetStatus(file, "A", s, "完成"); err != nil {
				t.Error(err)
			}
		}(s)
	}
	wg.Wait()
	fs, _ := ParseRoadmap(file)
	for _, st := range fs[0].Stages {
		if st.Status != "完成" {
			t.Fatalf("lost update: %+v", fs[0].Stages)
		}
	}
}

func TestSetStatusDuplicateStageIsAmbiguous(t *testing.T) {
	file := writeRoadmap(t, twoTables+"\n## A\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S-1 | d | 未開始 |\n")
	before, _ := os.ReadFile(file)
	changed, err := SetStatus(file, "A", "S-1", "完成")
	if changed || !errors.Is(err, ErrUnparsable) {
		t.Fatalf("%v %v", changed, err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatal("file changed")
	}
}

func TestSetStatusSameFeatureNameFindsLaterSection(t *testing.T) {
	file := writeRoadmap(t, "## A\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S-0 | d | 完成 |\n\n## A\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S-1 | d | 未開始 |\n")
	if changed, err := SetStatus(file, "A", "S-1", "完成"); !changed || err != nil {
		t.Fatalf("%v %v", changed, err)
	}
}

func TestSetStatusWriteFailureNotChanged(t *testing.T) {
	file := writeRoadmap(t, twoTables)
	if err := os.Chmod(filepath.Dir(file), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(file), 0o755)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory mode")
	}
	changed, err := SetStatus(file, "A", "S-1", "完成")
	if changed || err == nil {
		t.Fatalf("%v %v", changed, err)
	}
}

func TestWriteTasksRejectsUnknownState(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.md")
	if err := WriteTasks(file, []Task{{Key: "#1", Title: "a", State: TaskDoing}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	for _, st := range []TaskState{"", "blocked"} {
		err := WriteTasks(file, []Task{{Key: "#2", State: TaskDoing}, {Key: "#3", State: st}})
		if !errors.Is(err, ErrUnknownStatus) {
			t.Fatalf("state %q: %v", st, err)
		}
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatal("file changed")
	}
}
