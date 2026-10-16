package fleetmod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/module/hostconfig"
)

// The files in testdata/ are the standard samples the App is built against: the daemon's GET /api/fleet and fleet.changed
// exactly as they go out. Every time is an RFC3339 string (Go's default); nothing is an epoch. Regenerate with
// UPDATE_GOLDEN=1 go test ./internal/module/fleet/ -run Golden, and read the diff before committing it.

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	require.NotContains(t, string(got), "DO-NOT-LEAK", "a slot token went out in %s", name)
	var buf bytes.Buffer
	require.NoError(t, json.Indent(&buf, got, "", "  "))
	buf.WriteByte('\n')
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), buf.String(), "%s differs from what the daemon sends", name)
}

func inject(t *testing.T, r *mrig, key string, f leaf.Fast, s *leaf.Slow) {
	t.Helper()
	r.m.mu.Lock()
	h := r.m.hosts[key]
	r.m.mu.Unlock()
	require.NotNil(t, h, key)
	(&rowSink{m: r.m, h: h}).Reading(f, s)
}

func goldenRig(t *testing.T) *mrig {
	localGOOS = "darwin" // the golden files are the same on every machine
	t.Cleanup(func() { localGOOS = runtime.GOOS })
	r := newMRig(t, silent)
	r.setProbes(
		hostconfig.ProbeHost{ID: "ab12cd34", Name: "air-2019", SSHAlias: "a19", Icon: "Laptop", IconWeight: "fill",
			Colors: json.RawMessage(`{"console":{"main":{"color":"#c2410c","alpha":100}}}`)},
		hostconfig.ProbeHost{ID: "cd34ef56", Name: "mserv", SSHAlias: "mserv", Icon: "HardDrives", IconWeight: "regular"},
	)
	r.start()
	now := r.fc.Now()

	// this daemon (mlab): from the local source
	r.local.SetFast(leaf.LocalFast{At: now, CPU: 12.5, Mem: 61, Level: 1, PageRate: 0, Load1: 2.4, NCPU: 10})
	r.local.SetSlow(leaf.LocalSlow{At: now,
		TopCPU: []leaf.Proc{{PID: 4242, Name: "node", CPU: 41.5, RSSMB: 812.5}},
		TopMem: []leaf.Proc{{PID: 4242, Name: "node", CPU: 41.5, RSSMB: 812.5}}})
	r.tick()

	// the other daemon host: over ssh, with its heavy-command queue
	inject(t, r, "peer", leaf.Fast{LocalFast: leaf.LocalFast{At: now, CPU: 78, Mem: 83.5, Level: 2, PageRate: 61, Load1: 9.1, NCPU: 8},
		OS: "Darwin", DiskPct: 71, DiskFreeGB: 66.5, RateKnown: true, LevelKnown: true},
		&leaf.Slow{AdmissionKnown: true, LocalSlow: leaf.LocalSlow{At: now,
			TopCPU: []leaf.Proc{{PID: 91, Name: "cc1plus", CPU: 120, RSSMB: 400}},
			TopMem: []leaf.Proc{{PID: 77, Name: "Electron", CPU: 3, RSSMB: 1500}},
			Gate: &gate.Status{
				// the tokens are the gate's secrets: the golden file must not carry them
				Heavy: []gate.Holder{
					{Class: "heavy", Slot: 1, PID: 10, Argv: []string{"go", "test", "-race", "./internal/module/fleet/"}, Cwd: "/Users/wake/Workspace/wake/purdex",
						Started: now.Add(-92 * time.Second), Session: "9c2f7a10-1b3d-4e55-9a60-7f0e2c8d5b11", Token: "tok-DO-NOT-LEAK-1"},
					{Class: "heavy", Slot: 2, PID: 11, Argv: []string{"pnpm", "build"}, Cwd: "/Users/wake/Workspace/wake/aigora/spa",
						Started: now.Add(-14 * time.Second), Token: "tok-DO-NOT-LEAK-2"}},
				Light: []gate.Holder{{Class: "light", Slot: 1, PID: 13, Argv: []string{"git", "status"}, Cwd: "/Users/wake/Workspace/wake/purdex",
					Started: now.Add(-2 * time.Second), Token: "tok-DO-NOT-LEAK-3"}},
				QueueHeavy: []gate.Waiter{{Class: "heavy", Place: 1, PID: 12, Since: now.Add(-150 * time.Second),
					Argv: []string{"go", "test", "./..."}, Cwd: "/Users/wake/Workspace/wake/purdex", Session: "11111111-aaaa-4bbb-8ccc-222222222222"}},
				Settings:  []gate.Setting{{Key: "slots.heavy", Value: "2"}, {Key: "slots.light", Value: "4"}},
				Admission: gate.AdmissionState{Held: true, Reason: "memory pressure level 2"},
			}}})

	// a probe host that answers
	inject(t, r, "probe:ab12cd34", leaf.Fast{LocalFast: leaf.LocalFast{At: now, CPU: 3, Mem: 22, Level: 0, Load1: 0.2, NCPU: 4},
		OS: "Linux", DiskPct: 35, DiskFreeGB: 210, LevelKnown: false},
		&leaf.Slow{LocalSlow: leaf.LocalSlow{At: now, TopCPU: []leaf.Proc{{PID: 1, Name: "systemd", CPU: 0.1, RSSMB: 12}}, TopMem: []leaf.Proc{}}, GateNone: true})

	// a probe host that does not: never read, the session cannot reach it
	r.m.mu.Lock()
	h := r.m.hosts["probe:cd34ef56"]
	r.m.mu.Unlock()
	(&rowSink{m: r.m, h: h}).State(leaf.StatusOffline, leaf.CodeUnreachable, now)
	return r
}

func TestGolden_APIFleetPrimary(t *testing.T) {
	r := goldenRig(t)
	mux := http.NewServeMux()
	r.m.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/fleet", nil))
	require.Equal(t, 200, rec.Code)
	golden(t, "api-fleet-primary.json", rec.Body.Bytes())
}

func TestGolden_FleetChanged(t *testing.T) {
	r := goldenRig(t)
	sub := r.subscribe(core.FeatureFleetV1)
	r.fc.Advance(time.Second)
	r.tick()
	_, _ = sub.TakeNewest() // the first push after the start carries every slow part

	// the next second: every host's fast part again, and a slow part for the other daemon host only
	now := r.fc.Now()
	r.local.SetFast(leaf.LocalFast{At: now, CPU: 14, Mem: 61, Level: 1, Load1: 2.4, NCPU: 10})
	inject(t, r, "peer", leaf.Fast{LocalFast: leaf.LocalFast{At: now, CPU: 80, Mem: 84, Level: 2, PageRate: 64, Load1: 9.3, NCPU: 8},
		OS: "Darwin", DiskPct: 71, DiskFreeGB: 66.5, RateKnown: true, LevelKnown: true},
		&leaf.Slow{LocalSlow: leaf.LocalSlow{At: now,
			TopCPU: []leaf.Proc{{PID: 91, Name: "cc1plus", CPU: 118, RSSMB: 401}},
			TopMem: []leaf.Proc{{PID: 77, Name: "Electron", CPU: 3, RSSMB: 1500}},
			// an older pdx (no Admission object, so the host's own level decides, level 2 here) with the light class paused
			Gate: &gate.Status{Heavy: []gate.Holder{{Class: "heavy", Slot: 1, PID: 10, Argv: []string{"go", "test"}, Cwd: "/Users/wake/Workspace/wake/purdex",
				Started: now.Add(-93 * time.Second), Token: "tok-DO-NOT-LEAK-1"}},
				Settings: []gate.Setting{{Key: "slots.heavy", Value: "2"}, {Key: "slots.light", Value: "0"}, {Key: "admission.enabled", Value: "true"},
					{Key: "admission.max_pressure_level", Value: "1"}, {Key: "admission.max_page_rate", Value: "auto"}}}}})
	inject(t, r, "probe:ab12cd34", leaf.Fast{LocalFast: leaf.LocalFast{At: now, CPU: 4, Mem: 22, Load1: 0.2, NCPU: 4},
		OS: "Linux", DiskPct: 35, DiskFreeGB: 210}, nil)
	r.tick()
	b, ok := sub.TakeNewest()
	require.True(t, ok)
	var ev core.HostEvent
	require.NoError(t, json.Unmarshal(b, &ev))
	require.Equal(t, "fleet.changed", ev.Type)
	golden(t, "fleet-changed.json", []byte(ev.Value))
}

func TestGolden_StandbyAndOff(t *testing.T) {
	r := goldenRig(t)
	r.role.Set(leaf.RoleState{Role: leaf.RoleStandby, Term: 4, Primary: "air26", Self: "mlab"})
	b, err := json.Marshal(r.m.Answer())
	require.NoError(t, err)
	golden(t, "api-fleet-standby.json", b)
	r.role.Set(leaf.RoleState{Role: leaf.RoleOff, Self: "mlab", Reason: "own alias not in order"})
	b, err = json.Marshal(r.m.Answer())
	require.NoError(t, err)
	golden(t, "api-fleet-off.json", b)
}
