package fleetmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/module/hostconfig"
)

// rig is a Measure on fakes: a hand-driven clock, a FakeLocal, a FakeRole, the real events broadcaster.
type mrig struct {
	t      *testing.T
	m      *Measure
	fc     *fakeClock
	local  *leaf.FakeLocal
	role   *leaf.FakeRole
	events *core.EventsBroadcaster

	mu      sync.Mutex
	probes  []hostconfig.ProbeHost
	fleet   hostconfig.FleetConfig
	probeRv atomic.Int64
	fleetRv atomic.Int64

	probeReads, fleetReads atomic.Int32
	ssh                    string
	sshDir                 string
}

func newMRig(t *testing.T, script string) *mrig {
	t.Helper()
	r := &mrig{t: t, fc: newFakeClock(), local: leaf.NewFakeLocal(), events: core.NewEventsBroadcaster()}
	r.role = leaf.NewFakeRole(leaf.RoleState{Role: leaf.RolePrimary, Term: 3, Primary: "mlab", Self: "mlab"})
	r.fleet = hostconfig.FleetConfig{Order: []string{"air26", "mlab"}, PeerSSHAlias: "a26", Enabled: true}
	r.ssh, r.sshDir = fakeSSH(t, script)
	r.m = NewMeasure(Deps{
		Home: t.TempDir(), Clock: r.fc, Local: r.local, Role: r.role, Events: r.events,
		Probe: func() ([]hostconfig.ProbeHost, int64, error) {
			r.probeReads.Add(1)
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]hostconfig.ProbeHost(nil), r.probes...), r.probeRv.Load(), nil
		},
		Fleet: func() (hostconfig.FleetConfig, int64, error) {
			r.fleetReads.Add(1)
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.fleet, r.fleetRv.Load(), nil
		},
		Revisions:  func() (int64, int64, error) { return r.probeRv.Load(), r.fleetRv.Load(), nil },
		SelfHostID: func() string { return "mlab:abc123" },
		PeerHostID: func(a string) string {
			if a == "air26" {
				return "air26:def456"
			}
			return ""
		},
		Disk: func() (float64, float64, bool) { return 40, 120, true },
		Session: sessionCfg{
			LookPath:  func(string) (string, error) { return r.ssh, nil },
			Valid:     func(string, string) bool { return true },
			TermGrace: 300 * time.Millisecond,
		},
	})
	return r
}

func (r *mrig) start() {
	r.t.Helper()
	require.NoError(r.t, r.m.Start(r.t.Context()))
	r.t.Cleanup(r.m.Stop)
}

func (r *mrig) setProbes(rows ...hostconfig.ProbeHost) {
	r.mu.Lock()
	r.probes = rows
	r.mu.Unlock()
	r.probeRv.Add(1)
}

func (r *mrig) tick() { r.m.tick(r.t.Context()) }

func (r *mrig) setLocal(cpu float64, level int) {
	r.local.SetFast(leaf.LocalFast{At: r.fc.Now(), CPU: cpu, Mem: 50, Level: level, NCPU: 10, Load1: 2})
}

func (r *mrig) hosts() []HostJSON { return r.m.Answer().Hosts }

func (r *mrig) host(id string) *HostJSON {
	for _, h := range r.hosts() {
		if h.ID == id {
			h := h
			return &h
		}
	}
	return nil
}

const silent = `exec sleep 600`
const oneFrame = `cat "$D/frames.txt"; exec sleep 600`

// The own host is read from the LocalSource at each tick, with the disk of the fleet's mount; the zone and the composite load
// are worked out from it.
func TestOwnHostFromTheLocalSource(t *testing.T) {
	r := newMRig(t, silent)
	r.start()
	r.tick()
	h := r.host("mlab:abc123")
	require.NotNil(t, h)
	assert.Equal(t, "offline", h.Status, "no reading yet")
	assert.Nil(t, h.Fast)

	r.setLocal(95, 1)
	r.tick()
	h = r.host("mlab:abc123")
	require.NotNil(t, h.Fast)
	assert.Equal(t, "ok", h.Status)
	assert.Equal(t, KindDaemon, h.Kind)
	assert.Equal(t, "mlab", h.Alias)
	assert.Equal(t, 95.0, h.Fast.CPU)
	assert.Equal(t, 40.0, h.Fast.Disk)
	assert.Equal(t, 120.0, h.Fast.DiskFreeGB)
	assert.Equal(t, "normal", h.Fast.Zone, "CPU 95 with no memory pressure is busy, fine")
	assert.Equal(t, 95.0, h.Fast.Load)

	r.setLocal(10, 4)
	r.fc.Advance(time.Second)
	r.setLocal(10, 4)
	r.tick()
	assert.Equal(t, "danger", r.host("mlab:abc123").Fast.Zone)
}

// The host list: this daemon, the other daemon host (id from the pairing), then the probe hosts, each with its own id.
func TestHostListOrderAndIds(t *testing.T) {
	r := newMRig(t, silent)
	r.setProbes(hostconfig.ProbeHost{ID: "ab12cd34", Name: "air-2019", SSHAlias: "a19", Icon: "laptop", IconWeight: "bold",
		Colors: json.RawMessage(`{"console":{"main":{"color":"#112233","alpha":100}}}`)})
	r.start()
	hs := r.hosts()
	require.Len(t, hs, 3)
	assert.Equal(t, []string{"mlab:abc123", "air26:def456", "ab12cd34"}, []string{hs[0].ID, hs[1].ID, hs[2].ID})
	assert.Equal(t, []string{KindDaemon, KindDaemon, KindProbe}, []string{hs[0].Kind, hs[1].Kind, hs[2].Kind})
	assert.Equal(t, "air26", hs[1].Alias)
	assert.Equal(t, "air-2019", hs[2].Name)
	assert.Equal(t, "a19", hs[2].Alias)
	assert.JSONEq(t, `{"console":{"main":{"color":"#112233","alpha":100}}}`, string(hs[2].Colors))
}

// A remote host's frames reach the row; the queue summary is made for daemon hosts only.
func TestRemoteReadingReachesTheRow(t *testing.T) {
	r := newMRig(t, oneFrame)
	r.start()
	eventually(t, "reading", func() bool { h := r.host("air26:def456"); return h != nil && h.Status == "ok" && h.Fast != nil })
	h := r.host("air26:def456")
	assert.Equal(t, "Darwin", h.OS)
	assert.NotNil(t, h.LastOKAt)
	assert.NotNil(t, h.Slow, "the fixture's first frame carries the slow part")
	assert.NotEmpty(t, h.Slow.TopCPU)
}

// The config is re-read only when a revision moved, never per tick (the readers re-read ~/.ssh/config).
func TestConfigIsReadOnlyWhenARevisionMoves(t *testing.T) {
	r := newMRig(t, silent)
	r.start()
	p0, f0 := r.probeReads.Load(), r.fleetReads.Load()
	for i := 0; i < 5; i++ {
		r.fc.Advance(time.Second)
		r.tick()
	}
	assert.Equal(t, p0, r.probeReads.Load(), "probe hosts re-read without a revision change")
	assert.Equal(t, f0, r.fleetReads.Load(), "fleet row re-read without a revision change")

	r.setProbes(hostconfig.ProbeHost{ID: "ab12cd34", Name: "x", SSHAlias: "a19"})
	r.tick()
	assert.Equal(t, p0+1, r.probeReads.Load())
	assert.Len(t, r.hosts(), 3)
}

// New probe hosts start, removed ones stop, an alias change restarts, a change of look only does not.
func TestProbeHostChangesStartStopRestart(t *testing.T) {
	r := newMRig(t, `echo $$ >> "$D/pids"; `+silent)
	r.fleet.PeerSSHAlias = "" // only the probe hosts have sessions
	r.start()
	assert.Equal(t, 0, startsOf(r.sshDir))

	r.setProbes(hostconfig.ProbeHost{ID: "ab12cd34", Name: "x", SSHAlias: "a19"})
	r.tick()
	eventually(t, "started", func() bool { return startsOf(r.sshDir) == 1 })

	r.setProbes(hostconfig.ProbeHost{ID: "ab12cd34", Name: "renamed", SSHAlias: "a19", Icon: "cpu"})
	r.tick()
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, startsOf(r.sshDir), "a look-only change restarted the session")
	assert.Equal(t, "renamed", r.host("ab12cd34").Name)
	assert.Equal(t, "cpu", r.host("ab12cd34").Icon)

	r.setProbes(hostconfig.ProbeHost{ID: "ab12cd34", Name: "renamed", SSHAlias: "a19b"})
	r.tick()
	eventually(t, "restarted on an alias change", func() bool { return startsOf(r.sshDir) == 2 })

	pid := pidOf(t, r.sshDir, "pid")
	r.setProbes()
	r.tick()
	assert.Len(t, r.hosts(), 1)
	eventually(t, "stopped", func() bool { return !alive(pid) })
}

// Once there has been a reading, the age is answered for good and keeps growing, however every source fails afterwards. A
// primary that has read nothing yet answers "no reading".
func TestNewestAgeNeverFallsBackToNull(t *testing.T) {
	r := newMRig(t, `echo "x: Connection refused" >&2; exit 255`)
	r.start()
	_, ok := r.m.NewestAge()
	assert.False(t, ok, "nothing read yet")

	r.setLocal(5, 1)
	r.tick()
	age, ok := r.m.NewestAge()
	require.True(t, ok)
	assert.Equal(t, time.Duration(0), age)

	// Now every source fails: the local source stops, the ssh sessions keep dying.
	prev := age
	for i := 0; i < 5; i++ {
		r.fc.Advance(20 * time.Second)
		r.tick()
		age, ok = r.m.NewestAge()
		require.True(t, ok, "must stay (age, true) while all sources fail")
		assert.Greater(t, age, prev)
		prev = age
	}
	assert.Equal(t, 100*time.Second, age)
}

// A restart of the measuring (standby, then primary) starts again from no reading.
func TestNewestAgeStartsOverAfterARestart(t *testing.T) {
	r := newMRig(t, silent)
	r.start()
	r.setLocal(5, 1)
	r.tick()
	_, ok := r.m.NewestAge()
	require.True(t, ok)
	r.role.Set(leaf.RoleState{Role: leaf.RoleStandby, Term: 4, Primary: "air26", Self: "mlab"})
	r.role.Set(leaf.RoleState{Role: leaf.RolePrimary, Term: 5, Primary: "mlab", Self: "mlab"})
	_, ok = r.m.NewestAge()
	assert.False(t, ok)
}

func (r *mrig) subscribe(features ...string) *core.EventSubscriber {
	sub := r.events.AddTestSubscriberWith(features...)
	r.t.Cleanup(func() { r.events.RemoveTestSubscriber(sub) })
	return sub
}

func pushOf(t *testing.T, sub *core.EventSubscriber) (PushPayload, bool) {
	t.Helper()
	b, ok := sub.TakeNewest()
	if !ok {
		return PushPayload{}, false
	}
	var ev core.HostEvent
	require.NoError(t, json.Unmarshal(b, &ev))
	assert.Equal(t, "fleet.changed", ev.Type)
	var p PushPayload
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &p))
	return p, true
}

// One push per tick to the opted-in subscribers: the term, the primary, the fast part of every host; the slow part only in
// the push right after it changed.
func TestPushPerTickSlowOnlyWhenChanged(t *testing.T) {
	r := newMRig(t, silent)
	r.fleet.PeerSSHAlias = ""
	r.start()
	sub := r.subscribe(core.FeatureFleetV1)
	plain := r.subscribe()

	r.setLocal(30, 1)
	r.local.SetSlow(leaf.LocalSlow{At: r.fc.Now(), TopCPU: []leaf.Proc{{PID: 1, Name: "a", CPU: 9}}})
	r.tick()
	p, ok := pushOf(t, sub)
	require.True(t, ok)
	assert.Equal(t, int64(3), p.Term)
	assert.Equal(t, "mlab", p.Primary)
	require.Len(t, p.Hosts, 1)
	assert.Equal(t, "mlab:abc123", p.Hosts[0].ID)
	require.NotNil(t, p.Hosts[0].Fast)
	assert.NotNil(t, p.Hosts[0].Slow, "the slow part is new")

	r.fc.Advance(time.Second)
	r.setLocal(31, 1)
	r.tick()
	p, ok = pushOf(t, sub)
	require.True(t, ok, "one push every tick")
	assert.Equal(t, 31.0, p.Hosts[0].Fast.CPU)
	assert.Nil(t, p.Hosts[0].Slow, "the slow part did not change")

	r.fc.Advance(4 * time.Second)
	r.local.SetSlow(leaf.LocalSlow{At: r.fc.Now(), TopCPU: []leaf.Proc{{PID: 2, Name: "b", CPU: 8}}})
	r.setLocal(32, 1)
	r.tick()
	p, _ = pushOf(t, sub)
	require.NotNil(t, p.Hosts[0].Slow)
	assert.Equal(t, 2, p.Hosts[0].Slow.TopCPU[0].PID)

	_, ok = plain.TakeNewest()
	assert.False(t, ok, "a subscriber that did not opt in got a fleet frame")
	assert.Empty(t, plain.SendCh())
}

// A standby or an off daemon pushes nothing and measures nothing; the measuring starts and stops with the role.
func TestStandbyAndOffPushNothing(t *testing.T) {
	r := newMRig(t, silent)
	r.start()
	sub := r.subscribe(core.FeatureFleetV1)
	assert.Equal(t, 1, r.local.Starts())

	r.role.Set(leaf.RoleState{Role: leaf.RoleStandby, Term: 4, Primary: "air26", Self: "mlab"})
	assert.Equal(t, 1, r.local.Stops(), "the local source stopped")
	r.m.push() // even a stray call sends nothing
	_, ok := pushOf(t, sub)
	assert.False(t, ok, "a standby pushed")

	r.role.Set(leaf.RoleState{Role: leaf.RoleOff, Self: "mlab", Reason: "own alias not in order"})
	r.m.push()
	_, ok = pushOf(t, sub)
	assert.False(t, ok, "an off daemon pushed")
}

// OnStandby then OnPrimary in quick succession never runs two sessions for one host: the old one is waited for.
func TestQuickStandbyThenPrimaryNeverDoublesASession(t *testing.T) {
	script := `for p in $(cat "$D/pids" 2>/dev/null); do kill -0 $p 2>/dev/null && echo overlap >> "$D/overlap"; done
echo $$ >> "$D/pids"
trap 'sleep 0.5; exit 0' TERM
sleep 600 &
wait`
	r := newMRig(t, script)
	r.start()
	eventually(t, "first session", func() bool { return startsOf(r.sshDir) == 1 })
	for i := 0; i < 3; i++ {
		r.role.Set(leaf.RoleState{Role: leaf.RoleStandby, Term: 4, Primary: "air26", Self: "mlab"})
		r.role.Set(leaf.RoleState{Role: leaf.RolePrimary, Term: 5, Primary: "mlab", Self: "mlab"})
	}
	eventually(t, "sessions again", func() bool { return startsOf(r.sshDir) >= 2 })
	_, err := os.Stat(filepath.Join(r.sshDir, "overlap"))
	assert.True(t, os.IsNotExist(err), "two sessions of one host were alive at once")
}

// GET /api/fleet in its three shapes, and the alias list.
func TestAPIShapes(t *testing.T) {
	r := newMRig(t, silent)
	r.fleet.PeerSSHAlias = ""
	r.start()
	mux := http.NewServeMux()
	r.m.Routes(mux)
	get := func(path string) map[string]any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, rec.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	r.setLocal(5, 1)
	r.local.SetSlow(leaf.LocalSlow{At: r.fc.Now(), Gate: &gate.Status{
		QueueHeavy: []gate.Waiter{{Class: "heavy", Place: 1, Argv: []string{"go", "test"}, Cwd: "/w/proj", Since: r.fc.Now()}},
		Heavy:      []gate.Holder{{Token: "SECRET-TOKEN", Locked: []gate.Straggler{{PID: 777}}}},
		Settings:   []gate.Setting{{Key: "slots.heavy", Value: "2"}},
	}})
	r.tick()
	prim := get("/api/fleet")
	assert.Equal(t, "primary", prim["role"])
	assert.EqualValues(t, 3, prim["term"])
	assert.NotEmpty(t, prim["at"])
	hosts := prim["hosts"].([]any)
	require.Len(t, hosts, 1)
	slow := hosts[0].(map[string]any)["slow"].(map[string]any)
	q := slow["queue"].(map[string]any)
	assert.EqualValues(t, 1, q["heavy_used"])
	assert.NotContains(t, slow, "Gate")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/fleet", nil))
	assert.NotContains(t, rec.Body.String(), "SECRET-TOKEN", "the gate status itself must not leave the daemon")
	assert.NotContains(t, rec.Body.String(), "locked")

	r.role.Set(leaf.RoleState{Role: leaf.RoleStandby, Term: 4, Primary: "air26", Self: "mlab"})
	assert.Equal(t, map[string]any{"role": "standby", "term": float64(4), "primary": "air26"}, get("/api/fleet"))

	r.role.Set(leaf.RoleState{Role: leaf.RoleOff, Self: "mlab", Reason: "own alias not in order"})
	assert.Equal(t, map[string]any{"role": "off", "reason": "own alias not in order"}, get("/api/fleet"))

	require.NoError(t, os.MkdirAll(filepath.Join(r.m.d.Home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(r.m.d.Home, ".ssh", "config"), []byte("Host mserv a19\n  HostName x\nHost *\n  User y\n"), 0o600))
	assert.Equal(t, map[string]any{"aliases": []any{"mserv", "a19"}}, get("/api/fleet/ssh-aliases"))
}

var _ = context.Background
