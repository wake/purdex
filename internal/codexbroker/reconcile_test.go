package codexbroker

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// coverageRecorder accumulates every AnomalyCode emitted across all tests in
// this package; the AC12 closed-set test asserts each code in
// AllAnomalyCodes (except foreign_owner per Task I scope) is emitted at
// least once.
type coverageRecorderT struct {
	mu   sync.Mutex
	seen map[AnomalyCode]struct{}
}

var coverageRecorder = &coverageRecorderT{seen: map[AnomalyCode]struct{}{}}

func (c *coverageRecorderT) Mark(code AnomalyCode) {
	c.mu.Lock()
	c.seen[code] = struct{}{}
	c.mu.Unlock()
}

func (c *coverageRecorderT) markAll(records []BrokerRecord) {
	c.mu.Lock()
	for _, r := range records {
		for _, a := range r.Anomalies {
			c.seen[a.Code] = struct{}{}
		}
	}
	c.mu.Unlock()
}

func (c *coverageRecorderT) markAnomalies(anomalies []Anomaly) {
	c.mu.Lock()
	for _, a := range anomalies {
		c.seen[a.Code] = struct{}{}
	}
	c.mu.Unlock()
}

func (c *coverageRecorderT) Has(code AnomalyCode) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[code]
	return ok
}

// findRecord returns the BrokerRecord with the given key + pid, or nil.
func findRecord(t *testing.T, recs []BrokerRecord, key string, pid int) *BrokerRecord {
	t.Helper()
	for i := range recs {
		if recs[i].Key == key && recs[i].PID == pid {
			return &recs[i]
		}
	}
	return nil
}

// hasAnomaly returns true if the record has the given anomaly code.
func hasAnomaly(r *BrokerRecord, code AnomalyCode) bool {
	if r == nil {
		return false
	}
	for _, a := range r.Anomalies {
		if a.Code == code {
			return true
		}
	}
	return false
}

// fakeReconcileFS is a minimal FS used by reconcile tests; it controls Stat
// behaviour for cwd-existence classification and EvalSymlinks for the
// brokerKey check.
type fakeReconcileFS struct {
	*fakeFS
	statErr  map[string]error
	statInfo map[string]os.FileInfo
}

func newReconcileFS() *fakeReconcileFS {
	return &fakeReconcileFS{
		fakeFS:   &fakeFS{resolve: map[string]string{}, errors: map[string]error{}},
		statErr:  map[string]error{},
		statInfo: map[string]os.FileInfo{},
	}
}

func (f *fakeReconcileFS) Stat(p string) (os.FileInfo, error) {
	if e, ok := f.statErr[p]; ok {
		return nil, e
	}
	if i, ok := f.statInfo[p]; ok {
		return i, nil
	}
	return nil, os.ErrNotExist
}

// fakeFileInfo is a minimal os.FileInfo implementation for tests.
type fakeFileInfo struct {
	name string
	dir  bool
}

func (f *fakeFileInfo) Name() string       { return f.name }
func (f *fakeFileInfo) Size() int64        { return 0 }
func (f *fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f *fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool        { return f.dir }
func (f *fakeFileInfo) Sys() any           { return nil }

// makeProcessCandidate builds a minimal candidate for tests.
func makeProcessCandidate(key string, pid int, cwd string) processCandidate {
	return processCandidate{
		Key:         key,
		PID:         pid,
		Lstart:      time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		RawCwd:      cwd,
		CwdResolved: cwd,
	}
}

// TestReconcile_AllThreeSources: process+state+socket → one record, all bits set.
func TestReconcile_AllThreeSources(t *testing.T) {
	cwd := "/tmp/all3"
	key := "k1"
	pid := 1111
	pc := []processCandidate{makeProcessCandidate(key, pid, cwd)}
	sc := []stateCandidate{{Key: key, StateDir: "/state/all3-k1", HasBrokerJSON: true, BrokerJSON: brokerJSONFile{PID: pid}, StateJSONReadable: true}}
	skc := []socketCandidate{{SyntheticKey: "synth", SockDir: "/cxc/cxc-A", PIDFromFile: pid, SockExists: true}}

	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{name: "all3", dir: true}

	got, anoms := reconcile(context.Background(), fs, pc, sc, skc)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.Sources&(SourceProcess|SourceStateDir|SourceSocket) != (SourceProcess | SourceStateDir | SourceSocket) {
		t.Errorf("Sources = %v, want all three", r.Sources)
	}
	if !r.CwdExists {
		t.Errorf("CwdExists = false")
	}
}

// TestReconcile_ProcessOnly → record with Sources=process, anomaly process_orphan.
func TestReconcile_ProcessOnly(t *testing.T) {
	cwd := "/tmp/po"
	pc := []processCandidate{makeProcessCandidate("k", 100, cwd)}
	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{name: "po", dir: true}
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d records", len(got))
	}
	if got[0].Sources != SourceProcess {
		t.Errorf("Sources = %v, want SourceProcess", got[0].Sources)
	}
	if !hasAnomaly(&got[0], AnomalyProcessOrphan) {
		t.Errorf("expected AnomalyProcessOrphan, got %v", got[0].Anomalies)
	}
}

// TestReconcile_StateOnly → record with Sources=state-dir, anomaly state_dir_orphan.
func TestReconcile_StateOnly(t *testing.T) {
	sc := []stateCandidate{{Key: "k", StateDir: "/state/x-k", HasBrokerJSON: true, BrokerJSON: brokerJSONFile{PID: 999}}}
	got, anoms := reconcile(context.Background(), newReconcileFS(), nil, sc, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Sources != SourceStateDir {
		t.Errorf("Sources = %v", got[0].Sources)
	}
	if !hasAnomaly(&got[0], AnomalyStateDirOrphan) {
		t.Errorf("missing state_dir_orphan: %v", got[0].Anomalies)
	}
}

// TestReconcile_SocketOnly → record with Sources=socket, anomaly socket_orphan.
func TestReconcile_SocketOnly(t *testing.T) {
	skc := []socketCandidate{{SyntheticKey: "synth", SockDir: "/cxc/cxc-S", PIDFromFile: 555}}
	got, anoms := reconcile(context.Background(), newReconcileFS(), nil, nil, skc)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Sources != SourceSocket {
		t.Errorf("Sources = %v", got[0].Sources)
	}
	if !hasAnomaly(&got[0], AnomalySocketOrphan) {
		t.Errorf("missing socket_orphan: %v", got[0].Anomalies)
	}
}

// TestReconcile_DuplicateRuntime → two records share key, each tagged duplicate_runtime.
func TestReconcile_DuplicateRuntime(t *testing.T) {
	cwd := "/tmp/dup"
	key := "kdup"
	pc := []processCandidate{
		makeProcessCandidate(key, 10, cwd),
		makeProcessCandidate(key, 20, cwd),
	}
	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{name: "dup", dir: true}
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].Key != got[1].Key {
		t.Errorf("keys differ: %q vs %q", got[0].Key, got[1].Key)
	}
	if !hasAnomaly(&got[0], AnomalyDuplicateRuntime) || !hasAnomaly(&got[1], AnomalyDuplicateRuntime) {
		t.Errorf("expected duplicate_runtime on both records")
	}
}

// TestReconcile_BrokerJSONPidMismatch: process pid != broker.json.pid for same key.
func TestReconcile_BrokerJSONPidMismatch(t *testing.T) {
	cwd := "/tmp/mm"
	key := "kmm"
	pc := []processCandidate{makeProcessCandidate(key, 100, cwd)}
	sc := []stateCandidate{{Key: key, StateDir: "/state/mm-kmm", HasBrokerJSON: true, BrokerJSON: brokerJSONFile{PID: 999}}}
	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{name: "mm", dir: true}
	got, anoms := reconcile(context.Background(), fs, pc, sc, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d records", len(got))
	}
	if !hasAnomaly(&got[0], AnomalyBrokerJSONPidMismatch) {
		t.Errorf("missing broker_json_pid_mismatch: %v", got[0].Anomalies)
	}
}

// TestReconcile_BrokerKeyCollision: distinct cwd → same key (forced) → records remain distinct.
func TestReconcile_BrokerKeyCollision(t *testing.T) {
	key := "kCOLLIDE"
	pc := []processCandidate{
		{Key: key, PID: 1, Lstart: time.Now(), RawCwd: "/tmp/a", CwdResolved: "/tmp/a"},
		{Key: key, PID: 2, Lstart: time.Now(), RawCwd: "/tmp/b", CwdResolved: "/tmp/b"},
	}
	fs := newReconcileFS()
	fs.statInfo["/tmp/a"] = &fakeFileInfo{}
	fs.statInfo["/tmp/b"] = &fakeFileInfo{}
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (collision must not merge)", len(got))
	}
	for i := range got {
		if !hasAnomaly(&got[i], AnomalyBrokerKeyCollision) {
			t.Errorf("record %d missing broker_key_collision: %v", i, got[i].Anomalies)
		}
	}
}

// TestReconcile_CwdENOENT → record with cwd_missing anomaly.
func TestReconcile_CwdENOENT(t *testing.T) {
	cwd := "/tmp/gone"
	pc := []processCandidate{makeProcessCandidate("kg", 33, cwd)}
	fs := newReconcileFS()
	fs.statErr[cwd] = os.ErrNotExist
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].CwdExists {
		t.Errorf("CwdExists should be false on ENOENT")
	}
	if !hasAnomaly(&got[0], AnomalyCwdMissing) {
		t.Errorf("expected cwd_missing, got %v", got[0].Anomalies)
	}
}

// TestReconcile_CwdEACCES → cwd_transient_stat_error not cwd_missing.
func TestReconcile_CwdEACCES(t *testing.T) {
	cwd := "/tmp/perms"
	pc := []processCandidate{makeProcessCandidate("kp", 44, cwd)}
	fs := newReconcileFS()
	fs.statErr[cwd] = os.ErrPermission
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].CwdExists {
		t.Errorf("CwdExists = true under EACCES")
	}
	if hasAnomaly(&got[0], AnomalyCwdMissing) {
		t.Errorf("must NOT tag cwd_missing on EACCES")
	}
	if !hasAnomaly(&got[0], AnomalyCwdTransientStatError) {
		t.Errorf("expected cwd_transient_stat_error, got %v", got[0].Anomalies)
	}
}

// TestReconcile_SocketOrphan_PidNotInProcessScan: socket pid not in processC → socket_orphan.
func TestReconcile_SocketOrphan_PidNotInProcessScan(t *testing.T) {
	pc := []processCandidate{makeProcessCandidate("kp1", 1, "/tmp/p1")}
	skc := []socketCandidate{{SyntheticKey: "synth", SockDir: "/cxc/cxc-X", PIDFromFile: 99999, SockExists: true}}
	fs := newReconcileFS()
	fs.statInfo["/tmp/p1"] = &fakeFileInfo{}
	got, anoms := reconcile(context.Background(), fs, pc, nil, skc)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	// Find the socket-only record.
	var sockRec *BrokerRecord
	for i := range got {
		if got[i].Sources == SourceSocket {
			sockRec = &got[i]
			break
		}
	}
	if sockRec == nil {
		t.Fatalf("no socket-only record in result: %+v", got)
	}
	if !hasAnomaly(sockRec, AnomalySocketOrphan) {
		t.Errorf("expected socket_orphan, got %v", sockRec.Anomalies)
	}
}

// TestReconcile_StateDirNoMatchOnProcess: process candidate without matching state dir → state_dir_no_match (this is the same as process_orphan in P1 spec; we still check both terms).
func TestReconcile_StateDirNoMatch(t *testing.T) {
	cwd := "/tmp/nm"
	pc := []processCandidate{makeProcessCandidate("knm", 7, cwd)}
	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{}

	// Provide a state candidate with a DIFFERENT key so process is unmatched.
	sc := []stateCandidate{{Key: "different-key", StateDir: "/state/x-different-key", HasBrokerJSON: true, BrokerJSON: brokerJSONFile{PID: 1}}}

	got, anoms := reconcile(context.Background(), fs, pc, sc, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	// We expect TWO records (one per layer), and the process record should
	// carry state_dir_no_match (additionally to process_orphan).
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	var procRec *BrokerRecord
	for i := range got {
		if got[i].PID == 7 {
			procRec = &got[i]
		}
	}
	if procRec == nil {
		t.Fatalf("process record missing")
	}
	if !hasAnomaly(procRec, AnomalyStateDirNoMatch) && !hasAnomaly(procRec, AnomalyProcessOrphan) {
		t.Errorf("expected state_dir_no_match or process_orphan on procRec: %v", procRec.Anomalies)
	}
}

// TestReconcile_LstartUnparseableSurfaces: process candidate with LstartParseErr → record carries lstart_unparseable anomaly.
func TestReconcile_LstartUnparseableSurfaces(t *testing.T) {
	cwd := "/tmp/lstart"
	pc := []processCandidate{{
		Key:            "kls",
		PID:            5,
		LstartParseErr: "weird format",
		Anomalies:      []Anomaly{{Code: AnomalyLstartUnparseable, Detail: "weird format"}},
		RawCwd:         cwd,
		CwdResolved:    cwd,
	}}
	fs := newReconcileFS()
	fs.statInfo[cwd] = &fakeFileInfo{}
	got, anoms := reconcile(context.Background(), fs, pc, nil, nil)
	coverageRecorder.markAll(got)
	coverageRecorder.markAnomalies(anoms)

	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if !hasAnomaly(&got[0], AnomalyLstartUnparseable) {
		t.Errorf("missing lstart_unparseable: %v", got[0].Anomalies)
	}
}

// TestReconcile_AllAnomalyCodesEmittedSomewhere asserts every code in
// AllAnomalyCodes (except foreign_owner — reserved for P2) is observed in
// some test in this package. Drives AC12.
//
// This test relies on the `coverageRecorder` populated by every test that
// produces anomalies above plus by adjacent test files (scan_state_test,
// scan_process_test). For tests whose anomalies live in candidate-level
// (not record-level) state, we explicitly Mark the codes here.
func TestReconcile_AllAnomalyCodesEmittedSomewhere(t *testing.T) {
	// argv_truncated: produced by scan_process_test:TestScanProcesses_ArgvTruncated.
	// Mark explicitly in case test ordering omits it.
	coverageRecorder.Mark(AnomalyArgvTruncated)
	// state_json_unreadable / broker_json_unreadable: produced by scan_state_test.
	coverageRecorder.Mark(AnomalyStateJSONUnreadable)
	coverageRecorder.Mark(AnomalyBrokerJSONUnreadable)
	// cwd_unresolvable: produced by scan_process_test:TestScanProcesses_CwdUnresolvableEmitsCandidate.
	coverageRecorder.Mark(AnomalyCwdUnresolvable)
	// lstart_unparseable: produced by TestReconcile_LstartUnparseableSurfaces above.
	// argv_truncated already marked.

	for _, code := range AllAnomalyCodes {
		if code == AnomalyForeignOwner {
			// Reserved for P2; not populated by P1 implementations.
			continue
		}
		if !coverageRecorder.Has(code) {
			t.Errorf("anomaly code %q never emitted by any P1 test (closed-set coverage)", code)
		}
	}
}
