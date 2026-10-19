package wire

import (
	"testing"
	"time"
)

func TestAQualifyRequestNamesARepoAndATask(t *testing.T) {
	for _, ok := range []string{"purdex/tsc", "purdex/spa-related", "my.repo/a_b-c"} {
		if err := (QualifyRequest{Task: ok}).Validate(); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "tsc", "purdex/", "/tsc", "purdex/Tsc", "purdex/tsc/x", "task:purdex/tsc", "../x/tsc", "purdex/ts c", "a b/tsc"} {
		if err := (QualifyRequest{Task: bad}).Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAQualifyStatusIsCheckedBeforeItIsShown(t *testing.T) {
	good := QualifyStatus{
		InFlight: []QualifyStarted{{Label: "task:purdex/tsc", Candidate: "j_1", Reference: "j_2"}},
		Recent:   []QualifyRecord{{Label: "task:purdex/tsc", Outcome: "mismatch", Why: "exit 1 on the candidate, 0 on the reference", At: time.Unix(1, 0)}},
	}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Recent = []QualifyRecord{{Label: "task:purdex/tsc", Outcome: "maybe"}}
	if bad.Validate() == nil {
		t.Error("an outcome that is not one of the three was accepted")
	}
	bad = good
	bad.InFlight = []QualifyStarted{{Label: "os:linux"}}
	if bad.Validate() == nil {
		t.Error("a label that is not a task label was accepted")
	}
	// the body is decoded strictly like the others
	if _, err := Decode[QualifyRequest]([]byte(`{"task":"purdex/tsc","extra":1}`)); err == nil {
		t.Error("an unknown field was accepted")
	}
}
