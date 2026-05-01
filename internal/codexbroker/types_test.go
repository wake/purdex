package codexbroker

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAnomalyCodes_ClosedList asserts that AllAnomalyCodes contains exactly the
// 15 codes defined in the spec §4.2 closed list. Adding a new code requires
// updating both the spec and this literal expected slice.
func TestAnomalyCodes_ClosedList(t *testing.T) {
	expected := []AnomalyCode{
		AnomalyBrokerJSONPidMismatch,
		AnomalyStateDirOrphan,
		AnomalyProcessOrphan,
		AnomalySocketOrphan,
		AnomalyCwdUnresolvable,
		AnomalyCwdMissing,
		AnomalyCwdTransientStatError,
		AnomalyLstartUnparseable,
		AnomalyArgvTruncated,
		AnomalyStateJSONUnreadable,
		AnomalyBrokerJSONUnreadable,
		AnomalyDuplicateRuntime,
		AnomalyBrokerKeyCollision,
		AnomalyStateDirNoMatch,
		AnomalyForeignOwner,
	}
	if len(AllAnomalyCodes) != len(expected) {
		t.Fatalf("AllAnomalyCodes length = %d, want %d", len(AllAnomalyCodes), len(expected))
	}
	got := make(map[AnomalyCode]struct{}, len(AllAnomalyCodes))
	for _, c := range AllAnomalyCodes {
		got[c] = struct{}{}
	}
	for _, c := range expected {
		if _, ok := got[c]; !ok {
			t.Errorf("expected anomaly %q not in AllAnomalyCodes", c)
		}
	}
}

// TestAnomalyCodes_Unique asserts no duplicate codes in AllAnomalyCodes.
func TestAnomalyCodes_Unique(t *testing.T) {
	seen := make(map[AnomalyCode]struct{}, len(AllAnomalyCodes))
	for _, c := range AllAnomalyCodes {
		if _, ok := seen[c]; ok {
			t.Errorf("duplicate anomaly code %q", c)
		}
		seen[c] = struct{}{}
	}
}

// TestBrokerRecord_RoundTrip round-trips a BrokerRecord through encoding/json
// without loss.
func TestBrokerRecord_RoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-1 * time.Hour)
	in := BrokerRecord{
		Key:               "abcdef0123456789",
		PID:               12345,
		Lstart:            now,
		PPID:              1,
		RSSBytes:          135 * 1024 * 1024,
		Cwd:               "/tmp/foo",
		CwdResolved:       "/private/tmp/foo",
		Endpoint:          "unix:/tmp/cxc-XXXX/broker.sock",
		SocketDir:         "/tmp/cxc-XXXX",
		PidFile:           "/tmp/cxc-XXXX/broker.pid",
		StateDir:          "/Users/x/.claude/plugins/data/codex-openai-codex/state/foo-abcdef0123456789",
		HasBrokerJSON:     true,
		BrokerJSONPID:     12345,
		StateJSONReadable: true,
		JobCounts:         JobCounts{Queued: 1, Running: 2, Completed: 3, Failed: 4, Cancelled: 5, Unknown: 6},
		LastJobUpdatedAt:  &last,
		CwdExists:         true,
		CwdStatErr:        "",
		Sources:           SourceProcess | SourceStateDir | SourceSocket,
		Anomalies: []Anomaly{
			{Code: AnomalyDuplicateRuntime, Detail: "two procs"},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out BrokerRecord
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Key != in.Key {
		t.Errorf("Key: got %q want %q", out.Key, in.Key)
	}
	if out.PID != in.PID {
		t.Errorf("PID: got %d want %d", out.PID, in.PID)
	}
	if !out.Lstart.Equal(in.Lstart) {
		t.Errorf("Lstart: got %v want %v", out.Lstart, in.Lstart)
	}
	if out.Sources != in.Sources {
		t.Errorf("Sources: got %v want %v", out.Sources, in.Sources)
	}
	if len(out.Anomalies) != 1 || out.Anomalies[0].Code != AnomalyDuplicateRuntime {
		t.Errorf("Anomalies round-trip lost: %+v", out.Anomalies)
	}
	if out.LastJobUpdatedAt == nil || !out.LastJobUpdatedAt.Equal(last) {
		t.Errorf("LastJobUpdatedAt round-trip lost: %v", out.LastJobUpdatedAt)
	}
	if out.JobCounts != in.JobCounts {
		t.Errorf("JobCounts: got %+v want %+v", out.JobCounts, in.JobCounts)
	}
}

// TestSourceMask_String exercises the human-readable rendering of SourceMask.
func TestSourceMask_String(t *testing.T) {
	tests := []struct {
		mask SourceMask
		want string
	}{
		{0, "none"},
		{SourceProcess, "process"},
		{SourceStateDir, "state-dir"},
		{SourceSocket, "socket"},
		{SourceProcess | SourceStateDir, "process|state-dir"},
		{SourceProcess | SourceStateDir | SourceSocket, "process|state-dir|socket"},
	}
	for _, tt := range tests {
		if got := tt.mask.String(); got != tt.want {
			t.Errorf("SourceMask(%d).String() = %q, want %q", tt.mask, got, tt.want)
		}
	}
}
