package workbook

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	if m.Name() != "workbook" {
		t.Fatalf("name = %q", m.Name())
	}
	got := map[string]bool{}
	for _, d := range m.Dependencies() {
		got[d] = true
	}
	for _, want := range []string{"agent", "team", "conversation", "hostconfig"} {
		if !got[want] {
			t.Errorf("missing dependency %q in %v", want, m.Dependencies())
		}
	}
}

func TestModule_InitOpensTheStoreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	if st := m.Status(); st["ready"] != true || st["init_error"] != "" {
		t.Fatalf("status = %v", st)
	}
	fi, err := os.Stat(filepath.Join(dir, "workbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("workbook.db is %o", fi.Mode().Perm())
	}
}

// A store that cannot be opened must not stop the daemon: the module records why and stays off.
// Mutation gate: return the error from Init → red.
func TestModule_InitSoftFailsOnABrokenDataDir(t *testing.T) {
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: filepath.Join(t.TempDir(), "missing", "dir")}})
	if err := m.Init(c); err != nil {
		t.Fatalf("Init returned %v, want nil (soft fail)", err)
	}
	st := m.Status()
	if st["ready"] != false || st["init_error"] == "" {
		t.Fatalf("status = %v", st)
	}
	if m.live() != nil {
		t.Fatal("a soft-failed module handed out a store")
	}
}

// A restart settles what a crash left pending. Mutation gate: drop the FailPending call in Start → red.
func TestModule_StartFailsLeftoverPending(t *testing.T) {
	dir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	id := mustInsert(t, m.live(), pending("c", "s1", "t1", 1))
	m.Stop(context.Background())

	m2 := New()
	if err := m2.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m2.Stop(context.Background()) })
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e, _ := m2.live().Entry(id); e.State != StateFailed || e.Reason != ReasonStopped {
		t.Fatalf("entry = %+v", e)
	}
}
