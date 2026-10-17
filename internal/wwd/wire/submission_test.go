package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// POST /v1/jobs (local socket) takes a JobSubmission: what the submitter fixes at submit time, including the three properties
// copied from the manifest (interruptible, queue_timeout_s, adhoc), so the origin never rereads a manifest (WWD-3b).
func TestJobSubmissionRoundTripsAndCarriesTheManifestProperties(t *testing.T) {
	s := roundTrip[JobSubmission](t, "job_submission.json")
	if !s.Interruptible || s.QueueTimeoutS != 3600 || s.AdHoc {
		t.Fatalf("manifest properties lost: %+v", s)
	}
	if s.Task != "spa-related" || s.Submitter.Ref != "_abc123" {
		t.Fatalf("fields lost: %+v", s)
	}
}

// A submission has no id, state, claim, result or created_at: the origin owns those (a client cannot pick its job's id or state).
func TestJobSubmissionRejectsServerOwnedFields(t *testing.T) {
	for _, field := range []string{`"id":"j_1"`, `"state":"running"`, `"claim":null`, `"result":null`, `"created_at":"2026-10-18T05:00:00+08:00"`, `"origin":"x"`} {
		raw := strings.Replace(string(fixture(t, "job_submission.json")), "{", "{"+field+",", 1)
		if _, err := Decode[JobSubmission]([]byte(raw)); err == nil {
			t.Errorf("a submission with %s was accepted", field)
		}
	}
}

func TestJobSubmissionValidation(t *testing.T) {
	good := string(fixture(t, "job_submission.json"))
	cases := map[string]string{
		"bad class":          strings.Replace(good, `"class": "merge"`, `"class": "urgent"`, 1),
		"no task":            strings.Replace(good, `"task": "spa-related"`, `"task": ""`, 1),
		"negative queue":     strings.Replace(good, `"queue_timeout_s": 3600`, `"queue_timeout_s": -1`, 1),
		"negative timeout":   strings.Replace(good, `"timeout_s": 1800`, `"timeout_s": -5`, 1),
		"no snapshot commit": strings.Replace(good, `"commit": "c0ffee"`, `"commit": ""`, 1),
		"no repo name":       strings.Replace(good, `"name": "purdex"`, `"name": ""`, 1),
	}
	for name, raw := range cases {
		if raw == good {
			t.Fatalf("%s: the replacement changed nothing", name)
		}
		if _, err := Decode[JobSubmission]([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// queue_timeout_s 0 means the default (2 h), not an error
	if _, err := Decode[JobSubmission]([]byte(strings.Replace(good, `"queue_timeout_s": 3600`, `"queue_timeout_s": 0`, 1))); err != nil {
		t.Errorf("queue_timeout_s 0 (default) rejected: %v", err)
	}
}

// Job carries the same three properties back (the App and `pdx jobs` show them), and the keys are never omitted.
func TestJobCarriesInterruptibleQueueTimeoutAndAdHoc(t *testing.T) {
	j := roundTrip[Job](t, "job_queued.json")
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"interruptible", "queue_timeout_s", "adhoc"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Job omits %q", k)
		}
	}
	if j.Interruptible || j.AdHoc || j.QueueTimeoutS != 0 {
		t.Fatalf("job_queued.json: unexpected values %+v", j)
	}
}

// The reason of a Result is a free string: the classes of a failed run (lost, oom, timeout, infra) are not an enumeration here.
func TestResultReasonIsAnOpenString(t *testing.T) {
	for _, reason := range []string{"lost", "oom", "timeout", "infra", "queue_timeout", "cancelled_by_user"} {
		raw := `{"exit":null,"summary":null,"log":"","host":"air26","started_at":null,"finished_at":null,"cache_hit":false,"reason":"` + reason + `","superseded_by":null}`
		r, err := Decode[Result]([]byte(raw))
		if err != nil || r.Reason != reason {
			t.Errorf("reason %q: %v %q", reason, err, r.Reason)
		}
	}
}
