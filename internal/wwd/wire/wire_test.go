package wire

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// semEqual compares two JSON documents by value (key order and whitespace
// ignored; times compared as strings, so offsets must be preserved).
func semEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

func roundTrip[T any](t *testing.T, name string) T {
	t.Helper()
	raw := fixture(t, name)
	v, err := Decode[T](raw)
	if err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !semEqual(t, raw, out) {
		t.Fatalf("%s: round trip differs\nfixture: %s\nencoded: %s", name, raw, out)
	}
	return v
}

func TestRoundTripFixtures(t *testing.T) {
	roundTrip[Facts](t, "facts_darwin.json")
	roundTrip[Facts](t, "facts_linux.json")
	roundTrip[Facts](t, "facts_minute_stats.json")
	roundTrip[Info](t, "info.json")
	roundTrip[Pool](t, "pool.json")
	roundTrip[FactsHistoryResponse](t, "facts_history_response.json")
	roundTrip[Job](t, "job_queued.json")
	roundTrip[Job](t, "job_running.json")
	roundTrip[Job](t, "job_done.json")
	roundTrip[ClaimRequest](t, "claim_request.json")
	roundTrip[ClaimResponse](t, "claim_response.json")
	roundTrip[StartRequest](t, "start_request.json")
	roundTrip[Lease](t, "lease.json")
	roundTrip[HeartbeatRequest](t, "heartbeat_request.json")
	roundTrip[HeartbeatResponse](t, "heartbeat_response.json")
	roundTrip[ReleaseRequest](t, "release_request.json")
	roundTrip[ResultRequest](t, "result_request.json")
	roundTrip[Conflict409](t, "conflict409.json")
	roundTrip[Summary](t, "summary.json")
	roundTrip[WorkerSettings](t, "worker_settings.json")
	roundTrip[Exception](t, "exception.json")
	roundTrip[PoolHostsResponse](t, "pool_hosts_response.json")
	roundTrip[PoolHostsResponse](t, "pool_hosts_response_reasons.json")
	roundTrip[JobsListResponse](t, "jobs_list_response.json")
	roundTrip[JobDetail](t, "job_detail.json")
	roundTrip[PoolChanged](t, "pool_changed.json")
	roundTrip[JobsChanged](t, "jobs_changed.json")
}

func TestRoundTripEvents(t *testing.T) {
	want := map[string]EventType{
		"event_facts.json": EventFacts, "event_job.json": EventJob, "event_labels.json": EventLabels,
		"event_exception.json": EventException, "event_worker.json": EventWorker,
	}
	for name, typ := range want {
		e := roundTrip[Event](t, name)
		if e.Type != typ {
			t.Fatalf("%s: type %q", name, e.Type)
		}
		v, err := e.DecodeData()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch typ {
		case EventFacts:
			_ = v.(Facts)
		case EventJob:
			_ = v.(JobEvent)
		case EventLabels:
			_ = v.(LabelsEvent)
		case EventException:
			_ = v.(ExceptionEvent)
		case EventWorker:
			_ = v.(WorkerEvent)
		}
	}
}

func TestEventRejectsBadTypeAndData(t *testing.T) {
	if _, err := Decode[Event]([]byte(`{"type":"bogus","data":{}}`)); err == nil {
		t.Fatal("bad type accepted")
	}
	// a field of a later build in the data is skipped (#3710); a wrong value in a known field is not
	if _, err := Decode[Event]([]byte(`{"type":"labels","data":{"host":"a","labels":[],"extra":1}}`)); err != nil {
		t.Fatalf("an unknown field in event data refused: %v", err)
	}
	if _, err := Decode[Event]([]byte(`{"type":"labels","data":{"host":"a","labels":"not a list","extra":1}}`)); err == nil {
		t.Fatal("a wrong value in event data accepted")
	}
	if _, err := Decode[Event]([]byte(`{"type":"labels"}`)); err == nil {
		t.Fatal("missing data accepted")
	}
}

func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	for _, js := range []string{
		`{"proto":1,"extra":true,"host_id":"a","alias":"a","version":"v","platform":"p","arch":"a","worker":{"enabled":true},"labels":[]}`,
		`{"claim_id":"0123456789abcdef0123456789abcdef","bogus":1}`,
		`{"lease_s":1,"renew_s":1,"x":2}`,
		`{"state":"queued","x":1}`,
	} {
		switch {
		case strings.Contains(js, "proto"):
			if _, err := Decode[Info]([]byte(js)); err == nil {
				t.Fatalf("Info accepted unknown field")
			}
		case strings.Contains(js, "claim_id"):
			if _, err := Decode[StartRequest]([]byte(js)); err == nil {
				t.Fatalf("StartRequest accepted unknown field")
			}
		case strings.Contains(js, "lease_s"):
			if _, err := Decode[Lease]([]byte(js)); err == nil {
				t.Fatalf("Lease accepted unknown field")
			}
		default:
			if _, err := Decode[Conflict409]([]byte(js)); err == nil {
				t.Fatalf("Conflict409 accepted unknown field")
			}
		}
	}
	// Nested unknown field inside facts.
	raw := strings.Replace(string(fixture(t, "facts_linux.json")), `"ncpu": 4`, `"ncpu": 4, "zzz": 1`, 1)
	if _, err := Decode[Facts]([]byte(raw)); err == nil {
		t.Fatal("nested unknown field accepted")
	}
	// Trailing data.
	if _, err := Decode[Lease]([]byte(`{"lease_s":1,"renew_s":1} {}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestEnumsValid(t *testing.T) {
	for _, z := range []Zone{ZoneNormal, ZoneWarning, ZoneDanger, ZoneUnknown} {
		if !z.Valid() {
			t.Fatal(z)
		}
	}
	for _, k := range []OwnerKind{OwnerJob, OwnerSession, OwnerCodex, OwnerResident, OwnerSystem} {
		if !k.Valid() {
			t.Fatal(k)
		}
	}
	for _, c := range []JobClass{ClassRelease, ClassMerge, ClassDev} {
		if !c.Valid() {
			t.Fatal(c)
		}
	}
	for _, s := range []JobState{StateQueued, StateClaimed, StateRunning, StateSucceeded, StateFailed,
		StateCancelled, StateSuperseded, StateExpired} {
		if !s.Valid() {
			t.Fatal(s)
		}
	}
	for _, r := range []ReleaseReason{ReleaseReclaim, ReleaseInfra} {
		if !r.Valid() {
			t.Fatal(r)
		}
	}
	for _, e := range []EventType{EventFacts, EventJob, EventLabels, EventException, EventWorker} {
		if !e.Valid() {
			t.Fatal(e)
		}
	}
	for _, h := range []Hold{HoldHeavy, HoldLight} {
		if !h.Valid() {
			t.Fatal(h)
		}
	}
	if Zone("bad").Valid() || OwnerKind("bad").Valid() || JobClass("bad").Valid() || JobState("bad").Valid() ||
		ReleaseReason("bad").Valid() || EventType("bad").Valid() || Hold("bad").Valid() ||
		Zone("").Valid() || JobState("").Valid() {
		t.Fatal("invalid enum accepted")
	}
	if !StateSucceeded.Terminal() || StateRunning.Terminal() || StateQueued.Terminal() {
		t.Fatal("Terminal")
	}
	if !StateCancelled.ResultState() || StateExpired.ResultState() || StateQueued.ResultState() {
		t.Fatal("ResultState")
	}
}

func replaceIn(t *testing.T, name, old, repl string) []byte {
	t.Helper()
	s := string(fixture(t, name))
	if !strings.Contains(s, old) {
		t.Fatalf("%s lacks %q", name, old)
	}
	return []byte(strings.Replace(s, old, repl, 1))
}

func TestDecodeRejectsBadEnums(t *testing.T) {
	if _, err := Decode[Facts](replaceIn(t, "facts_darwin.json", `"zone": "normal"`, `"zone": "hot"`)); err == nil {
		t.Fatal("bad zone")
	}
	if _, err := Decode[Facts](replaceIn(t, "facts_darwin.json", `"kind": "job"`, `"kind": "bogus"`)); err == nil {
		t.Fatal("bad owner kind")
	}
	if _, err := Decode[Facts](replaceIn(t, "facts_darwin.json", `"v": 1`, `"v": 3`)); err == nil {
		t.Fatal("bad v")
	}
	if _, err := Decode[Job](replaceIn(t, "job_queued.json", `"class": "merge"`, `"class": "urgent"`)); err == nil {
		t.Fatal("bad class")
	}
	if _, err := Decode[Job](replaceIn(t, "job_queued.json", `"state": "queued"`, `"state": "limbo"`)); err == nil {
		t.Fatal("bad state")
	}
	if _, err := Decode[ReleaseRequest](replaceIn(t, "release_request.json", `"infra"`, `"whim"`)); err == nil {
		t.Fatal("bad reason")
	}
	if _, err := Decode[ResultRequest](replaceIn(t, "result_request.json", `"failed"`, `"queued"`)); err == nil {
		t.Fatal("non-result state")
	}
	if _, err := Decode[Conflict409](replaceIn(t, "conflict409.json", `"claimed"`, `"nope"`)); err == nil {
		t.Fatal("bad conflict state")
	}
	if _, err := Decode[Exception](replaceIn(t, "exception.json", `"light"`, `"medium"`)); err == nil {
		t.Fatal("bad hold")
	}
	if _, err := Decode[Info](replaceIn(t, "info.json", `"proto": 1`, `"proto": 9`)); err == nil {
		t.Fatal("bad proto")
	}
	if _, err := Decode[FactsHistoryResponse](replaceIn(t, "facts_history_response.json", `"1m"`, `"2m"`)); err == nil {
		t.Fatal("bad step")
	}
	if _, err := Decode[JobsChanged](replaceIn(t, "jobs_changed.json", `"running"`, `"x"`)); err == nil {
		t.Fatal("bad jobs.changed state")
	}
}

func TestUnreadableFieldsSerializeAsNull(t *testing.T) {
	linux := roundTrip[Facts](t, "facts_linux.json")
	var m map[string]json.RawMessage
	out, _ := json.Marshal(linux)
	_ = json.Unmarshal(out, &m)
	if string(m["owner"]) != "null" {
		t.Fatalf("linux owner = %s, want null", m["owner"])
	}
	darwin := roundTrip[Facts](t, "facts_darwin.json")
	out, _ = json.Marshal(darwin)
	var d struct {
		Mem map[string]json.RawMessage `json:"mem"`
	}
	_ = json.Unmarshal(out, &d)
	psi, ok := d.Mem["psi"]
	if !ok || string(psi) != "null" {
		t.Fatalf("darwin mem.psi = %s (present=%v), want null", psi, ok)
	}
	// Zero Facts must still emit every nullable key.
	out, _ = json.Marshal(Facts{})
	for _, k := range []string{`"owner":null`, `"psi":null`, `"compressor_mb":null`, `"php":null`, `"xcode":null`} {
		if !bytes.Contains(out, []byte(k)) {
			t.Fatalf("zero Facts lacks %s: %s", k, out)
		}
	}
}

func TestClaimID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewClaimID()
		if len(id) != 32 || !ValidClaimID(id) {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "abc", strings.Repeat("a", 31), strings.Repeat("a", 33),
		strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 31) + " ", "\n" + strings.Repeat("a", 32)} {
		if ValidClaimID(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !ValidClaimID("0123456789abcdef0123456789abcdef") {
		t.Fatal("rejected good id")
	}
	if _, err := Decode[StartRequest]([]byte(`{"claim_id":"short"}`)); err == nil {
		t.Fatal("short claim_id accepted in request")
	}
}

func TestConstants(t *testing.T) {
	if Proto != 3 || FactsProto != 2 || MinProto != 1 || LeaseS != 60 || RenewS != 15 || Port != 7871 {
		t.Fatal("constants changed")
	}
}

func TestFactsHistoryQuery(t *testing.T) {
	since := time.Date(2026, 10, 18, 5, 0, 0, 0, time.FixedZone("", 8*3600))
	q := FactsHistoryQuery{Since: since, Step: Step1m}
	got, err := ParseFactsHistoryQuery(q.Values())
	if err != nil || !got.Since.Equal(since) || got.Step != Step1m {
		t.Fatalf("%v %v", got, err)
	}
	if g, err := ParseFactsHistoryQuery(url.Values{"since": {since.Format(time.RFC3339)}}); err != nil || g.Step != Step5s {
		t.Fatalf("default step: %v %v", g, err)
	}
	if _, err := ParseFactsHistoryQuery(url.Values{"since": {"yesterday"}}); err == nil {
		t.Fatal("bad since")
	}
	if _, err := ParseFactsHistoryQuery(url.Values{"since": {since.Format(time.RFC3339)}, "step": {"3s"}}); err == nil {
		t.Fatal("bad step")
	}
}

func TestJobNeverCarriesClaimID(t *testing.T) {
	b, _ := json.Marshal(roundTrip[Job](t, "job_done.json"))
	if bytes.Contains(b, []byte("claim_id")) {
		t.Fatal("Job exposes claim_id")
	}
}
