package resources

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixedSnapshot() Snapshot {
	return Snapshot{
		SampledAt: time.Date(2026, 10, 9, 3, 4, 5, 0, time.UTC),
		Available: true,
		Capacity:  Capacity,
		Host: HostUse{
			Measured:          90,
			CPU:               17.5,
			Mem:               76.25,
			Load1:             1.75,
			NCPU:              10,
			MemBytes:          17179869184,
			MemUsedBytes:      13100000000,
			Pressure:          2,
			MemorystatusLevel: 48,
			PcpuTotal:         312.5,
			Full:              true,
		},
		Sessions: []SessionUse{{
			SessionID: "s-1",
			PID:       4242,
			Tmux:      "work",
			Cwd:       "/tmp/x",
			CPU:       12.5,
			Mem:       3.5,
			Use:       13,
			RSSBytes:  600000000,
			Pcpu:      125,
			Procs:     6,
		}},
		Mode: "measure",
	}
}

func TestSnapshot_JSONShape(t *testing.T) {
	got, err := json.Marshal(fixedSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sampled_at":"2026-10-09T03:04:05Z","available":true,"capacity":100,` +
		`"host":{"measured":90,"cpu":17.5,"mem":76.25,"load1":1.75,"ncpu":10,` +
		`"mem_bytes":17179869184,"mem_used_bytes":13100000000,"pressure":2,` +
		`"memorystatus_level":48,"pcpu_total":312.5,"full":true},` +
		`"sessions":[{"session_id":"s-1","pid":4242,"tmux":"work","cwd":"/tmp/x",` +
		`"cpu":12.5,"mem":3.5,"use":13,"rss_bytes":600000000,"pcpu":125,"procs":6}],` +
		`"mode":"measure"}`
	if string(got) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(string(got), `"reason"`) {
		t.Errorf("reason must be omitted when empty: %s", got)
	}

	// A non-UTC time is still written in UTC; empty tmux and cwd vanish.
	s := fixedSnapshot()
	s.Available = false
	s.Reason = "sample_failed"
	s.SampledAt = time.Date(2026, 10, 9, 11, 4, 5, 0, time.FixedZone("x", 8*3600))
	s.Sessions = []SessionUse{{SessionID: "s-2", PID: 7}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, frag := range []string{`"sampled_at":"2026-10-09T03:04:05Z"`, `"reason":"sample_failed"`} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing %s in %s", frag, out)
		}
	}
	if strings.Contains(out, `"tmux"`) || strings.Contains(out, `"cwd"`) {
		t.Errorf("empty tmux/cwd must be omitted: %s", out)
	}
}

func TestSnapshot_ReservedLeaseFieldsAbsentInP0(t *testing.T) {
	got, err := json.Marshal(fixedSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"leases", "waiters"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q must not be emitted in P0", k)
		}
	}
	if Capacity != 100 || SampleInterval != 5*time.Second || PressureWarnFloor != 90 ||
		PressureCriticalFloor != 100 || MemFullPercent != 90 {
		t.Errorf("constants drifted from the plan")
	}
}
