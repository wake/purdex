package fault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

type logs struct {
	mu sync.Mutex
	l  []string
}

func (g *logs) printf(f string, a ...any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.l = append(g.l, strings.TrimSpace(strings.ReplaceAll(f, "%s", "%s")))
	_ = a
}
func (g *logs) n() int { g.mu.Lock(); defer g.mu.Unlock(); return len(g.l) }

func TestNilAndOffAreInert(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "owner-active")
	os.WriteFile(f, nil, 0o600)
	all := Config{LogUploadFail: true, OwnerActiveFile: f, Zone: "danger", SnapshotDelayS: 5} // Enabled is false
	for name, h := range map[string]*Hooks{"nil": nil, "disabled": New(all, nil), "enabled but nothing set": New(Config{Enabled: true}, nil)} {
		if h.LogUploadFail() {
			t.Errorf("%s: log upload fails", name)
		}
		if h.OwnerActive(false) {
			t.Errorf("%s: owner active", name)
		}
		if got := h.ZoneOverride(); got != "" {
			t.Errorf("%s: zone override %q", name, got)
		}
		start := time.Now()
		if err := h.SnapshotDelay(context.Background()); err != nil || time.Since(start) > 50*time.Millisecond {
			t.Errorf("%s: snapshot delayed (%v)", name, err)
		}
		if d := h.Declared(); d != nil {
			t.Errorf("%s: declares %+v", name, d)
		}
	}
}

func TestEachHookOnlyWhenItsOwnKeyIsSet(t *testing.T) {
	g := &logs{}
	h := New(Config{Enabled: true, LogUploadFail: true}, g.printf)
	if g.n() != 1 {
		t.Fatalf("startup warnings = %d, want 1", g.n())
	}
	if !h.LogUploadFail() {
		t.Fatal("H1 not on")
	}
	if g.n() != 2 {
		t.Fatalf("a trigger must warn: %d", g.n())
	}
	if h.OwnerActive(false) || h.ZoneOverride() != "" {
		t.Fatal("other hooks leaked")
	}
	d := h.Declared()
	if d == nil || !d.LogUploadFail || d.OwnerActiveFile || d.Zone != "" || d.SnapshotDelayS != 0 {
		t.Fatalf("declared = %+v", d)
	}
}

func TestOwnerActiveFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "owner-active")
	g := &logs{}
	h := New(Config{Enabled: true, OwnerActiveFile: f}, g.printf)
	if h.OwnerActive(false) {
		t.Fatal("active with no file")
	}
	if !h.OwnerActive(true) {
		t.Fatal("a real active owner must stay active")
	}
	before := g.n()
	os.WriteFile(f, nil, 0o600)
	if !h.OwnerActive(false) {
		t.Fatal("file exists but owner not active")
	}
	if g.n() <= before {
		t.Fatal("no warning on trigger")
	}
	if !h.Declared().OwnerActiveFile {
		t.Fatal("not declared")
	}
}

func TestZoneOverride(t *testing.T) {
	h := New(Config{Enabled: true, Zone: "danger"}, nil)
	before := 0
	if got := h.ZoneOverride(); got != wire.ZoneDanger {
		t.Fatalf("zone = %q", got)
	}
	_ = before
	if h.Declared().Zone != "danger" {
		t.Fatal("not declared")
	}
}

func TestSnapshotDelayHonoursContext(t *testing.T) {
	h := New(Config{Enabled: true, SnapshotDelayS: 60}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := h.SnapshotDelay(ctx); err == nil {
		t.Fatal("a cancelled wait returned nil")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not stop at the context")
	}
}

func TestSnapshotDelayWaits(t *testing.T) {
	h := New(Config{Enabled: true, SnapshotDelayS: 1}, nil)
	var slept time.Duration
	h.sleep = func(ctx context.Context, d time.Duration) error { slept = d; return nil }
	if err := h.SnapshotDelay(context.Background()); err != nil || slept != time.Second {
		t.Fatalf("slept %v err %v", slept, err)
	}
}

func TestValidate(t *testing.T) {
	bad := []Config{
		{Enabled: true, Zone: "purple"},
		{Enabled: true, SnapshotDelayS: -1},
		{Enabled: true, SnapshotDelayS: 3601},
		{Enabled: true, OwnerActiveFile: "relative/path"},
		{Enabled: false, LogUploadFail: true}, // a key without enabled is a mistake worth saying
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if err := (Config{}).Validate(); err != nil {
		t.Errorf("zero config: %v", err)
	}
	if err := (Config{Enabled: true, Zone: "danger", SnapshotDelayS: 20, OwnerActiveFile: "/x/y"}).Validate(); err != nil {
		t.Errorf("good config: %v", err)
	}
}
