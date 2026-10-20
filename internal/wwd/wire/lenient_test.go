package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withUnknownFields is the JSON of a golden file with a field this build has never heard of added to the top level object and to every
// object below it (arrays of objects included): what a newer wwd's record looks like to this reader.
func withUnknownFields(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(x any, key string)
	walk = func(x any, key string) {
		switch x := x.(type) {
		case map[string]any:
			for k, c := range x { // the children it has now, before the new field is put among them
				walk(c, k)
			}
			if key == "fingerprint" {
				return // the one free-form map of strings: a key added there is an entry, not a new field
			}
			x["zz_new_field"] = map[string]any{"nested": []any{1, "two"}}
		case []any:
			for _, c := range x {
				walk(c, key)
			}
		}
	}
	walk(v, "")
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// readsLeniently checks one reader: the golden file with unknown fields everywhere is refused by Decode (so the injection is real) and
// taken by DecodeLenient, and what it takes is what the plain golden file gives.
func readsLeniently[T any](t *testing.T, file string) {
	t.Helper()
	plain, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	newer := withUnknownFields(t, file)
	if _, err := Decode[T](newer); err == nil {
		t.Fatalf("%s: strict Decode took unknown fields: the test does not prove anything", file)
	}
	got, err := DecodeLenient[T](newer)
	if err != nil {
		t.Fatalf("%s: DecodeLenient refused a newer record: %v", file, err)
	}
	want, err := DecodeLenient[T](plain)
	if err != nil {
		t.Fatalf("%s: the golden file itself: %v", file, err)
	}
	// the record the deployed build writes (the golden file) reads the same either way: the new reader loses nothing of the old shape
	strict, err := Decode[T](plain)
	if err != nil {
		t.Fatalf("%s: strict Decode of the golden file: %v", file, err)
	}
	if s, w := mustJSON(t, strict), mustJSON(t, want); s != w {
		t.Fatalf("%s: lenient and strict read the golden file differently:\n lenient %s\n strict  %s", file, w, s)
	}
	g, w := mustJSON(t, got), mustJSON(t, want)
	if g != w {
		t.Fatalf("%s: a newer record reads differently from the old one:\n got %s\nwant %s", file, g, w)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every type a reader takes from a peer, a response or an event, as a table: the policy is that a field added later is not a break.
func TestEveryReadTypeToleratesUnknownFields(t *testing.T) {
	t.Run("info", func(t *testing.T) { readsLeniently[Info](t, "info.json") })
	t.Run("facts linux", func(t *testing.T) { readsLeniently[Facts](t, "facts_linux.json") })
	t.Run("facts darwin", func(t *testing.T) { readsLeniently[Facts](t, "facts_darwin.json") })
	t.Run("facts v2", func(t *testing.T) { readsLeniently[Facts](t, "facts_v2_darwin.json") })
	t.Run("pool", func(t *testing.T) { readsLeniently[Pool](t, "pool.json") })
	t.Run("facts history", func(t *testing.T) { readsLeniently[FactsHistoryResponse](t, "facts_history_response.json") })
	t.Run("jobs list", func(t *testing.T) { readsLeniently[JobsListResponse](t, "jobs_list_response.json") })
	t.Run("job detail", func(t *testing.T) { readsLeniently[JobDetail](t, "job_detail.json") })
	t.Run("job queued", func(t *testing.T) { readsLeniently[Job](t, "job_queued.json") })
	t.Run("job running", func(t *testing.T) { readsLeniently[Job](t, "job_running.json") })
	t.Run("job done", func(t *testing.T) { readsLeniently[Job](t, "job_done.json") })
	t.Run("submission response", func(t *testing.T) { readsLeniently[JobSubmissionResponse](t, "submission_response.json") })
	t.Run("claim response", func(t *testing.T) { readsLeniently[ClaimResponse](t, "claim_response.json") })
	t.Run("heartbeat response", func(t *testing.T) { readsLeniently[HeartbeatResponse](t, "heartbeat_response.json") })
	t.Run("local run response", func(t *testing.T) { readsLeniently[LocalRunResponse](t, "local_run_response.json") })
	t.Run("locks busy", func(t *testing.T) { readsLeniently[LocksBusy](t, "locks_busy.json") })
	t.Run("conflict 409", func(t *testing.T) { readsLeniently[Conflict409](t, "conflict409.json") })
	t.Run("cursor ahead", func(t *testing.T) { readsLeniently[CursorAhead](t, "cursor_ahead.json") })
	t.Run("worker settings", func(t *testing.T) { readsLeniently[WorkerSettings](t, "worker_settings.json") })
	t.Run("alert list", func(t *testing.T) { readsLeniently[AlertList](t, "alert_list.json") })
}

// An event's payload is read leniently too (DecodeData is what every consumer of the stream calls).
func TestEventPayloadsToleratePeerFieldsToo(t *testing.T) {
	for _, file := range []string{"event_facts.json", "event_job.json", "event_labels.json", "event_exception.json", "event_worker.json", "event_alert.json"} {
		ev, err := DecodeLenient[Event](withUnknownFields(t, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		// the payload of the injected record is the plain one with unknown fields inside it as well
		var inner map[string]any
		if err := json.Unmarshal(ev.Data, &inner); err != nil {
			t.Fatal(err)
		}
		if _, ok := inner["zz_new_field"]; !ok {
			t.Fatalf("%s: the payload lost the injected field before the test could read it", file)
		}
		if _, err := ev.DecodeData(); err != nil {
			t.Errorf("%s: DecodeData refused a newer payload: %v", file, err)
		}
	}
}

// What stays: the value checks and the framing. Tolerating an unknown field is not tolerating a wrong one.
func TestLenientDecodeStillValidatesAndFrames(t *testing.T) {
	good := `{"host_id":"h","alias":"a","version":"v","proto":3,"platform":"darwin","arch":"arm64","worker":{"enabled":true},"labels":[],"fingerprint":{}}`
	if _, err := DecodeLenient[Info]([]byte(good)); err != nil {
		t.Fatalf("the base record: %v", err)
	}
	for name, body := range map[string]string{
		"a value out of range":              strings.Replace(good, `"proto":3`, `"proto":99`, 1),
		"an unknown field plus a bad value": strings.Replace(good, `"proto":3`, `"proto":0,"zz":1`, 1),
		"trailing data":                     good + ` {}`,
		"not json":                          `{"host_id":`,
		"wrong type for a known field":      strings.Replace(good, `"alias":"a"`, `"alias":7`, 1),
		"empty":                             ``,
	} {
		if _, err := DecodeLenient[Info]([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// and the strict Decode is what it was
	if _, err := Decode[Info]([]byte(strings.Replace(good, `"proto":3`, `"proto":3,"zz":1`, 1))); err == nil {
		t.Error("strict Decode took an unknown field")
	}
}

// frozenDerived764 is derived as the deployed 764 reads it (a copy made when DecodeLenient came in, not the live type): its reader is the
// strict one. This pins the constraint of the build that carries DecodeLenient, the "conversion build" of docs/wwd-deploy.md: a record with
// a field that build does not write is refused by a strict reader of the 764 kind, so the conversion build itself writes no new field, and a
// later build adds fields only once every host reads leniently.
type frozenDerived764 struct {
	Zone         Zone  `json:"zone"`
	ZoneOverride Zone  `json:"zone_override,omitempty"`
	Headroom     Slots `json:"headroom"`
	SessionsRoom int   `json:"sessions_room"`
}

func TestAStrictReaderOfTheOldKindRefusesAFieldItDoesNotKnow(t *testing.T) {
	strict := func(js string) error {
		dec := json.NewDecoder(strings.NewReader(js))
		dec.DisallowUnknownFields()
		var d frozenDerived764
		return dec.Decode(&d)
	}
	const known = `{"zone":"normal","headroom":{"heavy":1,"light":2},"sessions_room":3}`
	const newer = `{"zone":"normal","headroom":{"heavy":1,"light":2},"sessions_room":3,"a_later_field":true}`
	if err := strict(known); err != nil {
		t.Fatalf("the shape 764 writes: %v", err)
	}
	if err := strict(newer); err == nil {
		t.Fatal("a strict reader took a field it does not know: the constraint of the conversion build is not what the deploy notes say")
	}
	// and the lenient reader of this build takes both, which is what lets later builds add fields
	for _, js := range []string{known, newer} {
		if _, err := DecodeLenient[Derived]([]byte(js)); err != nil {
			t.Fatalf("DecodeLenient[Derived](%s): %v", js, err)
		}
	}
}
