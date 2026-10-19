package daemon

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/wire"
)

func faultSampler(h *fault.Hooks, level *int, owner *wire.OwnerState) *Sampler {
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Fault: h,
		Sources: fakeSources(level, owner), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
	})
}

// Without a hook nothing about a record or /v1/info changes: not a field, not a value.
func TestFaultOffLeavesRecordAndInfoUntouched(t *testing.T) {
	level := 1
	owner := &wire.OwnerState{Active: false, Reason: "idle"}
	for name, h := range map[string]*fault.Hooks{
		"nil":                 nil,
		"disabled":            fault.New(fault.Config{LogUploadFail: true, Zone: "danger"}, nil),
		"enabled, none named": fault.New(fault.Config{Enabled: true}, nil),
	} {
		s := faultSampler(h, &level, owner)
		s.Tick(s0)
		f, _ := s.Latest()
		b, _ := json.Marshal(f)
		if strings.Contains(string(b), "zone_override") {
			t.Errorf("%s: record carries zone_override: %s", name, b)
		}
		if f.Derived.Zone != wire.ZoneNormal || f.Derived.Headroom.Heavy != 1 {
			t.Errorf("%s: derived changed: %+v", name, f.Derived)
		}
		d := New(Options{HostID: "h1", Alias: "x", Version: "v0", Token: testToken, Fault: h})
		ib, _ := json.Marshal(d.info())
		if strings.Contains(string(ib), "fault") {
			t.Errorf("%s: info carries fault: %s", name, ib)
		}
	}
}

// H3 moves only the value a worker scores by; the real zone, which the Purdex gate feed and the app read, stays.
func TestFaultZoneOverridesOnlyThePickValue(t *testing.T) {
	level := 1
	s := faultSampler(fault.New(fault.Config{Enabled: true, Zone: "danger"}, nil), &level, nil)
	s.Tick(s0)
	f, _ := s.Latest()
	if f.Derived.Zone != wire.ZoneNormal || f.Derived.PickZone() != wire.ZoneDanger || f.Derived.Headroom.Heavy != 1 || f.Derived.SessionsRoom == 0 {
		t.Fatalf("derived: %+v", f.Derived)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
}

// H2 makes the owner block say active, which empties the headroom like a real active owner; the sessions room is not touched.
func TestFaultOwnerFileEmptiesHeadroomOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner-active")
	level := 1
	owner := &wire.OwnerState{Active: false, Reason: "idle"}
	s := faultSampler(fault.New(fault.Config{Enabled: true, OwnerActiveFile: file}, nil), &level, owner)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("no file: %+v", f.Derived)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Tick(s0.Add(5 * time.Second))
	f, _ := s.Latest()
	if f.Derived.Headroom != (wire.Slots{}) || f.Owner == nil || !f.Owner.Active || f.Derived.SessionsRoom == 0 {
		t.Fatalf("file: derived %+v owner %+v", f.Derived, f.Owner)
	}
}

func TestFaultInfoDeclaresWhatIsOn(t *testing.T) {
	h := fault.New(fault.Config{Enabled: true, LogUploadFail: true, Zone: "warning"}, nil)
	info := New(Options{HostID: "h1", Alias: "x", Version: "v0", Token: testToken, Fault: h}).info()
	if info.Fault == nil || !info.Fault.LogUploadFail || info.Fault.Zone != "warning" || info.Fault.OwnerActiveFile || info.Fault.SnapshotDelayS != 0 {
		t.Fatalf("fault: %+v", info.Fault)
	}
}

func TestFaultConfigOnlyFromTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[fault]\nenabled = true\nzone = \"danger\"\nsnapshot_delay_s = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Fault.Enabled || cfg.Fault.Zone != "danger" || cfg.Fault.SnapshotDelayS != 20 {
		t.Fatalf("fault: %+v", cfg.Fault)
	}
	if err := os.WriteFile(p, []byte("[fault]\nzone = \"danger\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p, t.TempDir()); err == nil {
		t.Fatal("a hook without enabled loaded")
	}
}

// H1 through the daemon as cmd/wwd wires it: a remote worker's log upload arrives on the TCP port, which is where the hook must be; the
// result upload on the same claim is not touched.
func TestFaultLogUploadFailOverTheRealTCPPort(t *testing.T) {
	h := newLaneHost(t, true, func(o *Options) {
		o.Fault = fault.New(fault.Config{Enabled: true, LogUploadFail: true}, nil)
	})
	_, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	auth := "Bearer " + testToken
	code, raw, _ := h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", auth, wire.ClaimRequest{Worker: "air26"})
	require.Equal(t, 200, code, string(raw))
	claim, err := wire.Decode[wire.ClaimResponse](raw)
	require.NoError(t, err)
	code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/start", auth, wire.StartRequest{ClaimID: claim.ClaimID})
	require.Equal(t, 200, code, string(raw))
	code, _, _ = h.tcpDo(t, "PUT", "/v1/jobs/"+sub.ID+"/log?claim_id="+claim.ClaimID, auth, nil)
	require.Equal(t, 503, code)
	code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/result", auth, wire.ResultRequest{ClaimID: claim.ClaimID, State: wire.StateSucceeded, Exit: new(int)})
	require.Equal(t, 200, code, string(raw))
}
