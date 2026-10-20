package jobsmod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// asMap is JSON read into generic maps, for a comparison key by key.
func asMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m), string(b))
	return m
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// Four kinds of job, as wwd records them: a queue job, a declared fast-lane task, an adhoc fast-lane command, one that has ended.
func slotJobs(t *testing.T) []wire.Job {
	queued := jobOf(t, "j_queue", wire.StateQueued)
	light := jobOf(t, "j_light", wire.StateRunning)
	light.Local, light.Cacheable = true, false // a fast-lane run is never cacheable
	heavyLocal := jobOf(t, "j_adhoc", wire.StateRunning)
	heavyLocal.Local, heavyLocal.AdHoc, heavyLocal.Cacheable = true, true, false
	done := jobOf(t, "j_done", wire.StateSucceeded)
	return []wire.Job{queued, light, heavyLocal, done}
}

// S11: every job in the list says whether it takes a heavy or a light slot — and nothing else about the answer changes: the jobs' own
// fields, the cursor and the epoch are what wwd sent, key by key, and the only new key is slot.
func TestList_EveryJobCarriesItsSlotAndNothingElseChanges(t *testing.T) {
	jobs := slotJobs(t)
	original := wire.JobsListResponse{Jobs: jobs, Cursor: "7", Epoch: epoch}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, original) })
	rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs")
	require.Equal(t, 200, rr.Code, rr.Body.String())

	got := asMap(t, rr.Body.Bytes())
	want := asMap(t, marshal(t, original)) // what the route wrote before there was a slot
	gotJobs, wantJobs := got["jobs"].([]any), want["jobs"].([]any)
	require.Len(t, gotJobs, len(jobs))
	slots := map[string]string{"j_queue": "heavy", "j_light": "light", "j_adhoc": "heavy", "j_done": "heavy"}
	for i, gj := range gotJobs {
		g := gj.(map[string]any)
		assert.Equal(t, slots[g["id"].(string)], g["slot"], g["id"])
		delete(g, "slot")
		assert.Equal(t, wantJobs[i], g, "job %v: every key but slot is as wwd sent it", g["id"])
	}
	delete(got, "jobs")
	delete(want, "jobs")
	assert.Equal(t, want, got, "cursor, epoch and every other key of the answer are unchanged")
}

// The same for one job: the job's keys (slot added), the log and every other key of the detail.
func TestJob_DetailCarriesTheSlotAndNothingElseChanges(t *testing.T) {
	for _, j := range slotJobs(t) {
		j := j
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, j) })
		mux.HandleFunc("GET /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("the log\n")) })
		rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs/"+j.ID+"?host=air19")
		require.Equal(t, 200, rr.Code, rr.Body.String())

		var log *string
		if j.State.Terminal() {
			s := "the log\n"
			log = &s
		}
		want := asMap(t, marshal(t, wire.JobDetail{Job: j, Log: log}))
		got := asMap(t, rr.Body.Bytes())
		job := got["job"].(map[string]any)
		wantSlot := "heavy"
		if j.Local && !j.AdHoc {
			wantSlot = "light"
		}
		assert.Equal(t, wantSlot, job["slot"], j.ID)
		delete(job, "slot")
		assert.Equal(t, want, got, "%s: every key but job.slot is as before (job, log)", j.ID)
	}
}

// An answer that can't be turned into a pool job — no jobs — is still an empty array, as before.
func TestList_NoJobsStaysAnEmptyArrayWithTheSlotType(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, wire.JobsListResponse{Jobs: nil, Cursor: "0", Epoch: epoch})
	})
	rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"jobs":[]`)
}
