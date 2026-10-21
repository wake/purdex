package wire

import "testing"

// GET /api/pool/hosts/{alias}/qualification (WWD-7b2-0a): the App copies these two files as its fixtures. qualify.TestFixtureHostQualification
// writes and byte-checks them from the view; here they must read back through the strict decoder and round-trip, and an absent field stays absent.
func TestPoolHostQualificationFixtures(t *testing.T) {
	known := roundTrip[PoolHostQualification](t, "pool_host_qualification.json")
	if !known.Known || known.Host != "mserv" || len(known.Tasks) == 0 || len(known.Unreadable) != 1 || known.Recent == nil {
		t.Fatalf("known: %+v", known)
	}
	byLabel := map[string]QualificationTask{}
	for _, tk := range known.Tasks {
		byLabel[tk.Label] = tk
	}
	if tk := byLabel["task:purdex/n-waiting"]; tk.Last == nil || tk.Last.Why == "" || tk.RetryAt == nil || tk.Pair != nil || tk.Blocked != nil || tk.Earned != nil {
		t.Fatalf("waiting with a why: %+v", tk)
	}
	if tk := byLabel["task:purdex/q-waiting"]; tk.Last == nil || tk.Last.Why != "" || tk.Earned == nil {
		t.Fatalf("waiting without a why: %+v", tk)
	}
	if tk := byLabel["task:purdex/old-earned"]; !tk.Gone || tk.Reference != "" || tk.Progress != QualProgressIdle {
		t.Fatalf("gone: %+v", tk)
	}

	unknown := roundTrip[PoolHostQualification](t, "pool_host_qualification_unknown.json")
	if unknown.Known || unknown.Host != "mserv" || unknown.Tasks == nil || len(unknown.Tasks) != 0 || unknown.Recent == nil || len(unknown.Recent) != 0 {
		t.Fatalf("unknown: %+v", unknown)
	}
}

// The daemon's answer has no known; the Purdex layer adds it. The two share every other field.
func TestHostQualificationDecodesAWwdAnswer(t *testing.T) {
	v, err := Decode[HostQualification]([]byte(`{"host":"air26","tasks":[{"label":"task:purdex/tsc","reference":"air26","hold":"reference","progress":"idle"}],"recent":[]}`))
	if err != nil || len(v.Tasks) != 1 || v.Tasks[0].Hold != QualHoldReference {
		t.Fatalf("%+v %v", v, err)
	}
}
