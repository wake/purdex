package facts

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A reparented helper (parent 1) with the session id in its environment is attributed to that session through the reader, and
// only the ref survives: the id and the rest of the environment are not kept.
func TestProcfsReaderEnvironmentSession(t *testing.T) {
	const id = "7c1f2a9e-5b3d-4e68-9a0c-1d2e3f4a5b6c"
	f := newFakeProc(t, 1_760_000_000)
	f.proc(40, 1, "node", 0, 700, 1, []string{"node", "/x/codex/scripts/app-server-broker.mjs", "serve"}, []string{"PATH=/usr/bin", SessionEnvKey + "=" + id, "SECRET_TOKEN=do-not-keep"})
	f.proc(41, 40, "codex", 0, 710, 1, []string{"codex", "app-server"}, []string{SessionEnvKey + "=" + id})
	r := NewProcfsReader(f.root, "")
	snap, ctx, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := ctx.EnvSessionOf(40); !ok || ref != sessionRef(id) {
		t.Fatalf("env session of the broker: %q %v", ref, ok)
	}
	ctx.ensureTable(snap)
	var broker ProcRec
	for _, p := range snap.Procs {
		if p.PID == 40 {
			broker = p
		}
	}
	if o := Attribute(broker, ctx); o == nil || o.Kind != wire.OwnerResident || o.Session != sessionRef(id) {
		t.Fatalf("broker: %+v", o)
	}
	for _, a := range r.args {
		if strings.Contains(a.session, id) || strings.Contains(strings.Join(a.argv, " "), "do-not-keep") {
			t.Fatalf("the cache holds more than the ref: %+v", a)
		}
	}
}
