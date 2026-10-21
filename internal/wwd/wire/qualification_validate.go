package wire

import "fmt"

// Validate checks an answer as a reader must before it shows it: a host, task labels that are task labels (once each), a hold and a progress
// on every task, and records with a label and an outcome of a pair. It checks the shape only: the values of hold, progress and blocked.code
// are drawn as they come, so a newer wwd's word is no error.
func (v HostQualification) Validate() error {
	if v.Host == "" {
		return fmt.Errorf("qualification: no host")
	}
	seen := map[string]bool{}
	for i, t := range v.Tasks {
		switch {
		case !ValidTaskLabel(t.Label):
			return fmt.Errorf("qualification: tasks[%d] label %q", i, t.Label)
		case seen[t.Label]:
			return fmt.Errorf("qualification: tasks[%d] repeats %s", i, t.Label)
		case t.Hold == "" || t.Progress == "":
			return fmt.Errorf("qualification: tasks[%d] %s has no hold or progress", i, t.Label)
		}
		seen[t.Label] = true
	}
	for i, r := range v.Recent {
		switch {
		case !ValidTaskLabel(r.Label):
			return fmt.Errorf("qualification: recent[%d] label %q", i, r.Label)
		case r.Outcome != "match" && r.Outcome != "mismatch" && r.Outcome != "void":
			return fmt.Errorf("qualification: recent[%d] outcome %q", i, r.Outcome)
		}
	}
	return nil
}
