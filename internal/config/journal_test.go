package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func writeCfg(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The journal pushes nothing unless config.toml lists the project (spec §3).
func TestJournal_AbsentSectionPushesNothing(t *testing.T) {
	if cfg := loadToml(t, "port = 9000\n"); len(cfg.JournalPushProjects()) != 0 {
		t.Fatalf("push_projects = %v, want none", cfg.JournalPushProjects())
	}
	if cfg := loadToml(t, "[journal]\n"); len(cfg.JournalPushProjects()) != 0 {
		t.Fatalf("empty section: push_projects = %v, want none", cfg.JournalPushProjects())
	}
}

func TestJournal_PushProjectsAreReadAndCloned(t *testing.T) {
	cfg := loadToml(t, "[journal]\npush_projects = [\"github.com/wake/purdex\", \"git.example.com/team/app\"]\n")
	want := []string{"github.com/wake/purdex", "git.example.com/team/app"}
	if got := cfg.JournalPushProjects(); !reflect.DeepEqual(got, want) {
		t.Fatalf("push_projects = %v", got)
	}
	c := cfg.Clone()
	c.Journal.PushProjects[0] = "changed"
	if cfg.JournalPushProjects()[0] != want[0] {
		t.Fatal("Clone shares the push_projects array")
	}
}

// The push round reads the file again, so an edit applies without a restart; a broken or missing file pushes nothing and
// the rest of the file (other sections) is not its business.
func TestJournal_ReadPushProjectsReReadsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if got, err := config.ReadJournalPushProjects(path); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	writeCfg(t, path, "[nex]\nenabled = \"not a bool\"\n[journal]\npush_projects = [\"github.com/wake/purdex\"]\n")
	if got, err := config.ReadJournalPushProjects(path); err != nil || !reflect.DeepEqual(got, []string{"github.com/wake/purdex"}) {
		t.Fatalf("first read: %v, %v", got, err)
	}
	writeCfg(t, path, "[journal]\npush_projects = []\n")
	if got, err := config.ReadJournalPushProjects(path); err != nil || len(got) != 0 {
		t.Fatalf("after the edit: %v, %v", got, err)
	}
	writeCfg(t, path, "[journal\n")
	if got, err := config.ReadJournalPushProjects(path); err == nil || len(got) != 0 {
		t.Fatalf("broken file: %v, %v, want an error and nothing", got, err)
	}
}
