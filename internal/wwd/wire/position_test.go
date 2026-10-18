package wire

import "testing"

// A job's manual place is checked where a job is decoded: a position a job may not carry, or a number that does not fit the place, is
// refused (a peer's answer is not trusted to schedule on).
func TestJobManualPlaceIsValidated(t *testing.T) {
	good := map[string]string{
		"none":  `"manual": "", "manual_seq": 0`,
		"hold":  `"manual": "hold", "manual_seq": 0`,
		"front": `"manual": "front", "manual_seq": -3`,
		"back":  `"manual": "back", "manual_seq": 2`,
	}
	for name, repl := range good {
		if _, err := Decode[Job](replaceIn(t, "job_queued.json", `"manual": "", "manual_seq": 0`, repl)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]string{
		"normal is a request, not a place": `"manual": "normal", "manual_seq": 0`,
		"unknown":                          `"manual": "sideways", "manual_seq": 0`,
		"unknown with a number":            `"manual": "sideways", "manual_seq": -1`,
		"normal with a number":             `"manual": "normal", "manual_seq": 2`,
		"hold with a number":               `"manual": "hold", "manual_seq": 1`,
		"none with a number":               `"manual": "", "manual_seq": 1`,
		"front with zero":                  `"manual": "front", "manual_seq": 0`,
		"front with a positive number":     `"manual": "front", "manual_seq": 4`,
		"back with zero":                   `"manual": "back", "manual_seq": 0`,
		"back with a negative number":      `"manual": "back", "manual_seq": -1`,
	}
	for name, repl := range bad {
		if _, err := Decode[Job](replaceIn(t, "job_queued.json", `"manual": "", "manual_seq": 0`, repl)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestPositionRequestIsValidated(t *testing.T) {
	for _, p := range []Position{PositionFront, PositionBack, PositionHold, PositionNormal} {
		if err := (PositionRequest{Position: p}).Validate(); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []Position{"", "sideways", "FRONT"} {
		if err := (PositionRequest{Position: p}).Validate(); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
	if _, err := Decode[PositionRequest]([]byte(`{"position":"front","x":1}`)); err == nil {
		t.Error("an unknown field was accepted")
	}
}
