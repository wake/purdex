package journal

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSetStatusKeepsAnotherWritersEdit(t *testing.T) {
	file := writeRoadmap(t, twoTables)
	calls := 0
	beforeReplace = func() {
		calls++
		if calls == 1 { // the lead agent edits S-2 between our read and our replace
			b, _ := os.ReadFile(file)
			_ = os.WriteFile(file, []byte(strings.Replace(string(b), "| S-2 | d | 未開始 |", "| S-2 | d | 進行中 |", 1)), 0o644)
		}
	}
	defer func() { beforeReplace = nil }()
	if changed, err := SetStatus(file, "A", "S-1", "完成"); !changed || err != nil {
		t.Fatalf("%v %v", changed, err)
	}
	fs, _ := ParseRoadmap(file)
	if got := fs[0].Stages; got[0].Status != "完成" || got[1].Status != "進行中" {
		t.Fatalf("lost the other writer's edit: %+v", got)
	}
}

func TestSetStatusGivesUpAfterThreeAttempts(t *testing.T) {
	file := writeRoadmap(t, twoTables)
	n := 0
	beforeReplace = func() {
		n++
		b, _ := os.ReadFile(file)
		_ = os.WriteFile(file, append(b, '\n'), 0o644)
	}
	defer func() { beforeReplace = nil }()
	changed, err := SetStatus(file, "A", "S-1", "完成")
	if changed || !errors.Is(err, ErrConcurrentEdit) || n != 3 {
		t.Fatalf("%v %v attempts %d", changed, err, n)
	}
}
