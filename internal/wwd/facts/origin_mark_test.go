package facts

import (
	"testing"

	wident "github.com/wake/purdex/internal/wwd/worker/ident"
)

func TestMarkFromEnv(t *testing.T) {
	tag := wident.OriginTag("air26")
	for name, tc := range map[string]struct {
		env     []string
		wantTag string
		wantID  string
		wantOK  bool
	}{
		"both":                {[]string{"A=1", "WWD_JOB=j_x", "WWD_ORIGIN=" + tag}, tag, "j_x", true},
		"old mark, no origin": {[]string{"WWD_JOB=j_x"}, "", "j_x", true},
		"origin but no job":   {[]string{"WWD_ORIGIN=" + tag}, "", "", false},
		"origin not a tag":    {[]string{"WWD_JOB=j_x", "WWD_ORIGIN=air26"}, "", "j_x", true},
		"origin too short":    {[]string{"WWD_JOB=j_x", "WWD_ORIGIN=ea225edf"}, "", "j_x", true},
		"origin upper case":   {[]string{"WWD_JOB=j_x", "WWD_ORIGIN=EA225EDF2A219EC6"}, "", "j_x", true},
		"job not an id":       {[]string{"WWD_JOB=x", "WWD_ORIGIN=" + tag}, "", "", false},
		"empty":               {nil, "", "", false},
	} {
		gotTag, gotID, ok := MarkFromEnv(tc.env)
		if gotTag != tc.wantTag || gotID != tc.wantID || ok != tc.wantOK {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", name, gotTag, gotID, ok, tc.wantTag, tc.wantID, tc.wantOK)
		}
	}
	// JobFromEnv stays as it was: the id alone
	if id, ok := JobFromEnv([]string{"WWD_JOB=j_x", "WWD_ORIGIN=" + tag}); !ok || id != "j_x" {
		t.Errorf("JobFromEnv = %q %v", id, ok)
	}
}

// The usage of two jobs with one id from two connections is two samples; a process with no origin (started before the upgrade) keeps
// the old key.
func TestJobUsageKeepsTwoOriginsWithOneIDApart(t *testing.T) {
	a, b := wident.OriginTag("air26"), wident.OriginTag("mserv")
	h, tr := newHost(), newJU()
	h.add(300, 100, "go", []string{"go", "test"})
	h.jobs[300], h.origins = "j_same", map[int]string{300: a}
	h.add(400, 100, "go", []string{"go", "test"})
	h.jobs[400], h.origins[400] = "j_same", b
	h.add(500, 100, "go", []string{"go", "test"}) // an old process: the mark has no origin
	h.jobs[500] = "j_old"
	h.rec(300).CPUPct, h.rec(400).CPUPct, h.rec(500).CPUPct = 100, 300, 200
	sample(tr, h, 0, "normal", nil)
	sample(tr, h, 1, "normal", nil)
	ga, ok := tr.TakeMark(a, "j_same")
	if !ok || ga.PeakCores != 1 {
		t.Fatalf("air26's: %+v %v", ga, ok)
	}
	gb, ok := tr.TakeMark(b, "j_same")
	if !ok || gb.PeakCores != 3 {
		t.Fatalf("mserv's: %+v %v", gb, ok)
	}
	if _, ok := tr.TakeMark(a, "j_same"); ok {
		t.Fatal("taken once")
	}
	// a job with no origin in its mark is asked for by its own tag after the upgrade, and found under the old key
	go2, ok := tr.TakeMark(a, "j_old")
	if !ok || go2.PeakCores != 2 {
		t.Fatalf("an old mark: %+v %v", go2, ok)
	}
}

func TestJobUsageTakeWithoutAnOriginFindsAnUnmarkedJob(t *testing.T) {
	h, tr := jobHost(), newJU()
	h.rec(300).CPUPct = 100
	sample(tr, h, 0, "normal", nil)
	if _, ok := tr.Take("j_a"); !ok {
		t.Fatal("Take(id) is the old call: it finds a job whose mark has no origin")
	}
}
