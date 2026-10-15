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

// Mutation gate: default "" / ignore the configured value / skip the trim -> red.
func TestJournalPersonName(t *testing.T) {
	for _, c := range []struct{ name, toml, want string }{
		{"absent section", "port = 9000\n", "使用者"},
		{"empty section", "[journal]\n", "使用者"},
		{"blank value", "[journal]\nperson_name = \"  \"\n", "使用者"},
		{"configured", "[journal]\nperson_name = \"Wake\"\n", "Wake"},
		{"trimmed", "[journal]\nperson_name = \" Wake \"\n", "Wake"},
	} {
		if got := loadToml(t, c.toml).JournalPersonName(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A copy of the config does not share the section with the original.
func TestJournalConfig_CloneIsDeep(t *testing.T) {
	cfg := loadToml(t, "[journal]\nperson_name = \"Wake\"\n")
	cp := cfg.Clone()
	cp.Journal.PersonName = "Other"
	if cfg.JournalPersonName() != "Wake" {
		t.Fatalf("original changed to %q", cfg.JournalPersonName())
	}
}

// The journal is on unless config.toml says `enabled = false`: a missing section, a missing key and an empty section all
// mean on. Mutation gate: default off / ignore the value -> red.
func TestJournalEnabled(t *testing.T) {
	for _, c := range []struct {
		name, toml string
		want       bool
	}{
		{"absent section", "port = 9000\n", true},
		{"empty section", "[journal]\n", true},
		{"other key only", "[journal]\nperson_name = \"Wake\"\n", true},
		{"explicitly on", "[journal]\nenabled = true\n", true},
		{"explicitly off", "[journal]\nenabled = false\n", false},
	} {
		if got := loadToml(t, c.toml).JournalEnabled(); got != c.want {
			t.Errorf("%s: JournalEnabled() = %v, want %v", c.name, got, c.want)
		}
	}
	var zero config.Config
	if !zero.JournalEnabled() {
		t.Error("a zero Config must have the journal on")
	}
}

// Saving any other setting rewrites the whole file (UpdateChanged -> WriteFile): `enabled = false` must survive it, or the
// journal would switch itself back on. Mutation gate: a tag that omits a false value -> red.
func TestJournalEnabled_FalseSurvivesASaveOfAnotherSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeCfg(t, path, "[journal]\nenabled = false\nperson_name = \"Wake\"\n")
	before, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	after := before.Clone()
	after.Port = before.Port + 1
	if _, err := config.UpdateChanged(path, before, after); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.JournalEnabled() {
		t.Fatal("saving another setting switched the journal back on")
	}
	if got.JournalPersonName() != "Wake" {
		t.Fatalf("person_name = %q after the save", got.JournalPersonName())
	}
}

func TestJournalEnabled_CloneIsDeep(t *testing.T) {
	cfg := loadToml(t, "[journal]\nenabled = false\n")
	cp := cfg.Clone()
	*cp.Journal.Enabled = true
	if cfg.JournalEnabled() {
		t.Fatal("Clone shares the enabled flag")
	}
}
