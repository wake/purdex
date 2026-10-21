package qualify

import "slices"

// ManifestError is a clone whose manifest could not be read or parsed: Source.Tasks joins one per clone, so the view can name the repos
// whose tasks it does not list.
type ManifestError struct {
	Repo string
	Err  error
}

func (e *ManifestError) Error() string { return e.Repo + ": " + e.Err.Error() }
func (e *ManifestError) Unwrap() error { return e.Err }

// UnreadableRepos is the repos named by the ManifestErrors inside err (an errors.Join of them, wrapped or not), sorted and without repeats.
// An error that is not a ManifestError names no repo.
func UnreadableRepos(err error) []string {
	var out []string
	var walk func(error)
	walk = func(err error) {
		switch e := err.(type) {
		case nil:
		case *ManifestError:
			out = append(out, e.Repo)
		case interface{ Unwrap() []error }:
			for _, c := range e.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(e.Unwrap())
		}
	}
	walk(err)
	slices.Sort(out)
	return slices.Compact(out)
}
