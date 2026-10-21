package wire

import (
	"strings"
	"testing"
)

// HostQualification.Validate is what a reader checks before it shows an answer: the shape, not the vocabulary (an unknown hold or progress
// value is drawn as it comes, so a newer wwd's word is no error).
func TestHostQualificationValidate(t *testing.T) {
	good := HostQualification{
		Host:   "mserv",
		Tasks:  []QualificationTask{{Label: "task:purdex/tsc", Hold: "reference", Progress: "idle"}},
		Recent: []QualifyRecord{{Label: "task:purdex/tsc", Outcome: "match"}},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	newer := good
	newer.Tasks = []QualificationTask{{Label: "task:purdex/tsc", Hold: "provisional", Progress: "someday"}}
	if err := newer.Validate(); err != nil {
		t.Fatalf("an unknown value must be accepted: %v", err)
	}
	for name, mutate := range map[string]func(*HostQualification){
		"no host":              func(v *HostQualification) { v.Host = "" },
		"a task without label": func(v *HostQualification) { v.Tasks = []QualificationTask{{Hold: "none", Progress: "ready"}} },
		"a label that is not a task label": func(v *HostQualification) {
			v.Tasks = []QualificationTask{{Label: "tool:node24", Hold: "none", Progress: "ready"}}
		},
		"no hold": func(v *HostQualification) {
			v.Tasks = []QualificationTask{{Label: "task:purdex/tsc", Progress: "ready"}}
		},
		"no progress": func(v *HostQualification) { v.Tasks = []QualificationTask{{Label: "task:purdex/tsc", Hold: "none"}} },
		"the same label twice": func(v *HostQualification) {
			tk := QualificationTask{Label: "task:purdex/tsc", Hold: "none", Progress: "ready"}
			v.Tasks = []QualificationTask{tk, tk}
		},
		"a record with a bad label":   func(v *HostQualification) { v.Recent = []QualifyRecord{{Label: "x", Outcome: "match"}} },
		"a record with a bad outcome": func(v *HostQualification) { v.Recent = []QualifyRecord{{Label: "task:purdex/tsc", Outcome: "maybe"}} },
	} {
		v := good
		mutate(&v)
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "qualification") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
