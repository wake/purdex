package qualify

import (
	"fmt"
	"sort"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Half is one result of a pair with the host it was meant for. The origin does not check labels at claim, so a half counts only when
// Result.Host is that host.
type Half struct {
	Want   string // the host this half was meant for
	Result *wire.Result
}

// Verdict is the outcome of a pair and, in words, why.
type Verdict struct {
	Outcome Outcome
	Why     string
}

func void(format string, a ...any) Verdict {
	return Verdict{Outcome: OutcomeVoid, Why: fmt.Sprintf(format, a...)}
}

// Compare judges a pair: host is the half run on the host that wants the label, reference the half run on the reference host. A pair
// is void (no label, no disagreement) when a half is missing or did not run to an exit, when a half ran on another host than it was
// meant for, or when a summary lists fewer failures than it counts (the failure sets cannot be compared). Otherwise both ran where
// they should: the pair matches when the exit state (zero or not) and the pass/fail sets are the same, and is a mismatch when they
// are not. A task whose qualify run fails the same way on both hosts matches: the host then reproduces the reference, failures
// included (the spec says "same pass/fail sets and exit state"; a qualify run that should pass is the manifest author's to choose).
// The sets are the counts of passed and failed tests and the set of failures; skipped tests are neither passes nor failures and are
// not compared.
func Compare(host, reference Half) Verdict {
	for _, h := range []struct {
		name string
		half Half
	}{{"candidate", host}, {"reference", reference}} {
		switch {
		case h.half.Result == nil:
			return void("the %s half has no result", h.name)
		case h.half.Result.Exit == nil:
			return void("the %s half did not run to an exit (%s)", h.name, reasonOf(h.half.Result))
		case h.half.Result.Host != h.half.Want:
			return void("the %s half ran on %q, not on %q", h.name, h.half.Result.Host, h.half.Want)
		}
	}
	if (*host.Result.Exit == 0) != (*reference.Result.Exit == 0) {
		return Verdict{Outcome: OutcomeMismatch, Why: fmt.Sprintf("exit %d on the candidate, %d on the reference", *host.Result.Exit, *reference.Result.Exit)}
	}
	hs, rs := host.Result.Summary, reference.Result.Summary
	switch {
	case hs == nil && rs == nil:
		return Verdict{Outcome: OutcomeMatch, Why: "the same exit state and neither reports a summary"}
	case hs == nil || rs == nil:
		return Verdict{Outcome: OutcomeMismatch, Why: "only one half reports a summary"}
	case hs.Passed != rs.Passed || hs.Failed != rs.Failed:
		return Verdict{Outcome: OutcomeMismatch, Why: fmt.Sprintf("passed/failed %d/%d on the candidate, %d/%d on the reference", hs.Passed, hs.Failed, rs.Passed, rs.Failed)}
	}
	if truncated(hs) || truncated(rs) {
		// the summary keeps the first failures only: two truncated lists are not the two sets, so their equality or difference shows nothing
		return void("a half lists fewer failures than it counts (%d of %d on the candidate, %d of %d on the reference): the sets cannot be compared",
			len(hs.Failures), hs.Failed, len(rs.Failures), rs.Failed)
	}
	if a, b := failureSet(hs), failureSet(rs); !equalSets(a, b) {
		return Verdict{Outcome: OutcomeMismatch, Why: "the failing tests differ"}
	}
	return Verdict{Outcome: OutcomeMatch, Why: "same exit and same pass/fail sets"}
}

// truncated says the summary counts more failures than it lists.
func truncated(s *wire.Summary) bool { return s.Failed > len(s.Failures) }

func reasonOf(r *wire.Result) string {
	if r.Reason != "" {
		return r.Reason
	}
	return "no reason given"
}

// failureSet identifies each failed test by file and name, sorted, so two orders of the same failures are equal.
func failureSet(s *wire.Summary) []string {
	out := make([]string, 0, len(s.Failures))
	for _, f := range s.Failures {
		out = append(out, f.File+"\x00"+f.Name)
	}
	sort.Strings(out)
	return out
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
