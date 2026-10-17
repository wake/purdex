package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// git_dir is the repo's common git directory, an absolute path, part of every submission (ad-hoc ones too) and queue-private: a Job
// has no such key.
func TestSubmissionCarriesAnAbsoluteGitDirAndAJobDoesNot(t *testing.T) {
	s := roundTrip[JobSubmission](t, "job_submission.json")
	if s.GitDir != "/Users/wake/Workspace/wake/purdex/.git" {
		t.Fatalf("git_dir lost: %q", s.GitDir)
	}
	good := string(fixture(t, "job_submission.json"))
	for name, raw := range map[string]string{
		"empty":    strings.Replace(good, `"git_dir": "/Users/wake/Workspace/wake/purdex/.git"`, `"git_dir": ""`, 1),
		"relative": strings.Replace(good, `"git_dir": "/Users/wake/Workspace/wake/purdex/.git"`, `"git_dir": "../.git"`, 1),
		"dot":      strings.Replace(good, `"git_dir": "/Users/wake/Workspace/wake/purdex/.git"`, `"git_dir": "."`, 1),
	} {
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[JobSubmission]([]byte(raw)); err == nil {
			t.Errorf("a %s git_dir was accepted", name)
		}
	}
	// the Job has no git_dir: a received Job that carries one is refused, so the path can never travel to a peer by accident
	withDir := strings.Replace(string(fixture(t, "job_queued.json")), `"task":`, `"git_dir": "/x/.git", "task":`, 1)
	if _, err := Decode[Job]([]byte(withDir)); err == nil {
		t.Error("a Job carrying git_dir was accepted")
	}
	b, _ := json.Marshal(roundTrip[Job](t, "job_queued.json"))
	if strings.Contains(string(b), "git_dir") {
		t.Error("Job encodes a git_dir")
	}
}

// A fast-lane run registers with a LocalRunRequest: a submission plus the git dir, never cacheable (it has no snapshot tree to key on);
// the answer carries the claim the runner then heartbeats with.
func TestLocalRunRequestAndResponse(t *testing.T) {
	r := roundTrip[LocalRunRequest](t, "local_run_request.json")
	if r.Task != "spa-related" || r.GitDir == "" {
		t.Fatalf("fields lost: %+v", r)
	}
	if _, err := Decode[LocalRunRequest]([]byte(strings.Replace(string(fixture(t, "local_run_request.json")), `"cacheable": false`, `"cacheable": true`, 1))); err == nil {
		t.Error("a cacheable local run was accepted")
	}
	// it is a submission first: everything a submission must have, a local run must have
	goodReq := string(fixture(t, "local_run_request.json"))
	for name, raw := range map[string]string{
		"bad class":    strings.Replace(goodReq, `"class": "dev"`, `"class": "urgent"`, 1),
		"no task":      strings.Replace(goodReq, `"task": "spa-related"`, `"task": ""`, 1),
		"relative dir": strings.Replace(goodReq, `"git_dir": "/Users/wake/Workspace/wake/purdex/.git"`, `"git_dir": "x/.git"`, 1),
	} {
		if raw == goodReq {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[LocalRunRequest]([]byte(raw)); err == nil {
			t.Errorf("a local run with %s was accepted", name)
		}
	}
	resp := roundTrip[LocalRunResponse](t, "local_run_response.json")
	if !ValidClaimID(resp.ClaimID) || resp.LeaseS <= 0 || resp.RenewS <= 0 || resp.ID == "" {
		t.Fatalf("response lost fields: %+v", resp)
	}
	good := string(fixture(t, "local_run_response.json"))
	for name, raw := range map[string]string{
		"no id":          strings.Replace(good, `"id": "j_01JC"`, `"id": ""`, 1),
		"bad claim id":   strings.Replace(good, resp.ClaimID, "nope", 1),
		"zero lease":     strings.Replace(good, `"lease_s": 60`, `"lease_s": 0`, 1),
		"negative renew": strings.Replace(good, `"renew_s": 15`, `"renew_s": -1`, 1),
	} {
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[LocalRunResponse]([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A local run that finds its lock keys held gets a 409 whose body names the keys.
func TestLocksBusyBody(t *testing.T) {
	b := roundTrip[LocksBusy](t, "locks_busy.json")
	if b.Reason != "locks_busy" || len(b.Keys) != 1 || b.Keys[0] != "mysql:purdex" {
		t.Fatalf("unexpected %+v", b)
	}
	good := string(fixture(t, "locks_busy.json"))
	for name, raw := range map[string]string{
		"other reason": strings.Replace(good, `"locks_busy"`, `"busy"`, 1),
		"no keys":      strings.Replace(good, `["mysql:purdex"]`, `[]`, 1),
	} {
		if raw == good {
			t.Fatalf("%s: replacement changed nothing", name)
		}
		if _, err := Decode[LocksBusy]([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Job says it is a fast-lane job (the App and `pdx jobs` show it); a local job is never cacheable.
func TestJobLocalFlag(t *testing.T) {
	j := roundTrip[Job](t, "job_queued.json")
	if j.Local {
		t.Fatal("job_queued.json is not local")
	}
	b, _ := json.Marshal(j)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	if _, ok := m["local"]; !ok {
		t.Error("Job omits the local key")
	}
	local := strings.Replace(string(fixture(t, "job_queued.json")), `"local": false`, `"local": true`, 1)
	if local == string(fixture(t, "job_queued.json")) {
		t.Fatal("replacement changed nothing")
	}
	// job_queued.json is cacheable: as a local job it is refused; made non-cacheable it is accepted
	if _, err := Decode[Job]([]byte(local)); err == nil {
		t.Error("a local job that is cacheable was accepted")
	}
	noCache := strings.Replace(local, `"cacheable": true`, `"cacheable": false`, 1)
	if noCache == local {
		t.Fatal("replacement changed nothing")
	}
	j2, err := Decode[Job]([]byte(noCache))
	if err != nil {
		t.Fatalf("a local, non-cacheable job rejected: %v", err)
	}
	if !j2.Local {
		t.Error("local lost")
	}
}
