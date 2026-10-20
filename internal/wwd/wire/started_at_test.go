package wire

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func queuedJob(t *testing.T) Job {
	t.Helper()
	j, err := Decode[Job](fixture(t, "job_queued.json"))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// started_at is sent when there is one and left out when there is not, so a record of a job that has not started is the bytes it always was;
// it comes back as the same instant.
func TestJobStartedAtIsOmittedWhenAbsentAndRoundTripsWhenPresent(t *testing.T) {
	j := queuedJob(t)
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("started_at")) {
		t.Fatalf("a job that has not started has no started_at: %s", b)
	}

	at := time.Date(2026, 10, 20, 8, 0, 3, 120_000_000, time.UTC)
	j.StartedAt = &at
	b, err = json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"started_at":"2026-10-20T08:00:03.12Z"`)) {
		t.Fatalf("started_at is in the record: %s", b)
	}
	back, err := Decode[Job](b)
	if err != nil {
		t.Fatal(err)
	}
	if back.StartedAt == nil || !back.StartedAt.Equal(at) {
		t.Fatalf("started_at = %v, want %v", back.StartedAt, at)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
}

// The compatibility rule written as something that runs. A reader built before the field was added (a copy of Job without it, read the way
// that build read a job: strictly) refuses a record that has started_at, which is why the field is sent only after every reader of jobs is
// a build that skips what it does not know (DecodeLenient, 765): the origin's own peers, the Purdex daemons, the worker that reads another
// host's queue. A reader of 765 or later reads it. Going back to a build older than 765 is going back past this.
func TestAReaderFromBeforeStartedAtRefusesTheRecordAndALenientOneReadsIt(t *testing.T) {
	j := queuedJob(t)
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	j.StartedAt = &at
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}

	// the Job of before: the same fields and tags, without StartedAt
	var fields []reflect.StructField
	for i := 0; i < reflect.TypeOf(Job{}).NumField(); i++ {
		f := reflect.TypeOf(Job{}).Field(i)
		if f.Name != "StartedAt" {
			fields = append(fields, reflect.StructField{Name: f.Name, Type: f.Type, Tag: f.Tag})
		}
	}
	if len(fields) != reflect.TypeOf(Job{}).NumField()-1 {
		t.Fatal("Job has no StartedAt field to leave out")
	}
	old := reflect.New(reflect.StructOf(fields)).Interface()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	err = dec.Decode(old)
	if err == nil || !strings.Contains(err.Error(), "started_at") {
		t.Fatalf("a strict reader from before the field must refuse the record, got %v", err)
	}

	got, err := DecodeLenient[Job](b)
	if err != nil {
		t.Fatalf("a lenient reader reads it: %v", err)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(at) {
		t.Fatalf("started_at = %v", got.StartedAt)
	}
}

// A jobs list and a job event carry it like the job does: whatever reads those with DecodeLenient reads it.
func TestStartedAtInAListAndInAnEvent(t *testing.T) {
	j := queuedJob(t)
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	j.StartedAt = &at
	list, err := json.Marshal(JobsListResponse{Jobs: []Job{j}, Cursor: "12", Epoch: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := DecodeLenient[JobsListResponse](list); err != nil || len(r.Jobs) != 1 || r.Jobs[0].StartedAt == nil {
		t.Fatalf("list: %+v %v", r, err)
	}
	ev, err := json.Marshal(JobEvent{Host: "mlab", Job: j})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := DecodeLenient[JobEvent](ev); err != nil || e.Job.StartedAt == nil {
		t.Fatalf("event: %+v %v", e, err)
	}
}
