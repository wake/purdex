package qualify

import (
	"fmt"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func res(host string, exit int, sum *wire.Summary) *wire.Result {
	return &wire.Result{Exit: &exit, Host: host, Summary: sum}
}

// digestOf is a well-formed digest standing for "the passed tests are set n".
func digestOf(n int) string { return fmt.Sprintf("sha256:%064x", n) }

func sum(passed, failed int, failures ...string) *wire.Summary {
	s := &wire.Summary{Passed: passed, Failed: failed, PassedDigest: digestOf(passed)}
	for _, f := range failures {
		s.Failures = append(s.Failures, wire.Failure{File: "a.ts", Name: f})
	}
	return s
}

func pair(cand, ref *wire.Result) (Half, Half) {
	return Half{Want: "mserv", Result: cand}, Half{Want: "air26", Result: ref}
}

func TestComparedHalvesThatAgreeMatch(t *testing.T) {
	c, r := pair(res("mserv", 0, sum(10, 0)), res("air26", 0, sum(10, 0)))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("%+v", v)
	}
	c, r = pair(res("mserv", 0, nil), res("air26", 0, nil))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("no summary on either side: %+v", v)
	}
	// skipped tests are neither passes nor failures
	cs, rs := sum(10, 0), sum(10, 0)
	cs.Skipped, rs.Skipped = 7, 0
	c, r = pair(res("mserv", 0, cs), res("air26", 0, rs))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("skipped differ: %+v", v)
	}
}

func TestComparedHalvesThatDisagreeAreAMismatch(t *testing.T) {
	for name, tc := range map[string]struct{ cand, ref *wire.Result }{
		"candidate exits non-zero":   {res("mserv", 1, sum(10, 0)), res("air26", 0, sum(10, 0))},
		"passed counts differ":       {res("mserv", 0, sum(9, 0)), res("air26", 0, sum(10, 0))},
		"failed counts differ":       {res("mserv", 0, sum(10, 1, "x")), res("air26", 0, sum(10, 0))},
		"only the candidate summary": {res("mserv", 0, sum(10, 0)), res("air26", 0, nil)},
		"only the reference summary": {res("mserv", 0, nil), res("air26", 0, sum(10, 0))},
	} {
		c, r := pair(tc.cand, tc.ref)
		if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

// Equal counts but other failing tests are a disagreement; the same failures in another order are not.
func TestTheFailureSetIsComparedNotItsOrder(t *testing.T) {
	c := res("mserv", 0, sum(5, 2, "a", "b"))
	r := res("air26", 0, sum(5, 2, "b", "a"))
	if v := Compare(Half{Want: "mserv", Result: c}, Half{Want: "air26", Result: r}, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("same failures, other order: %+v", v)
	}
	r = res("air26", 0, sum(5, 2, "a", "z"))
	if v := Compare(Half{Want: "mserv", Result: c}, Half{Want: "air26", Result: r}, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("other failures: %+v", v)
	}
}

// A half that ran on another host than it was meant for makes the pair void: no label, no disagreement.
func TestAHalfOnTheWrongHostVoidsThePair(t *testing.T) {
	c, r := pair(res("air26", 0, sum(10, 0)), res("air26", 0, sum(10, 0))) // the candidate half was taken by the reference host
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
		t.Fatalf("candidate half on the reference: %+v", v)
	}
	c, r = pair(res("mserv", 0, sum(10, 0)), res("mlab", 0, sum(10, 0)))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
		t.Fatalf("reference half on a third host: %+v", v)
	}
	// even when the results differ, a wrong-host pair is void and not a mismatch
	c, r = pair(res("mlab", 1, sum(1, 9)), res("air26", 0, sum(10, 0)))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
		t.Fatalf("%+v", v)
	}
}

func TestAHalfThatNeverRanVoidsThePair(t *testing.T) {
	ran := res("air26", 0, sum(10, 0))
	for name, cand := range map[string]*wire.Result{
		"no result":     nil,
		"cancelled/exp": {Host: "mserv", Reason: "queue_timeout"},
	} {
		c, r := pair(cand, ran)
		if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
			t.Errorf("candidate %s: %+v", name, v)
		}
		c, r = pair(res("mserv", 0, sum(10, 0)), cand)
		if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
			t.Errorf("reference %s: %+v", name, v)
		}
	}
}

// The spec compares "pass/fail sets and exit state": a qualify run that fails the same way on both hosts matches (the host reproduces the
// reference, failures included), and one that fails on only one of them does not.
func TestTheSameFailureOnBothHostsMatches(t *testing.T) {
	c, r := pair(res("mserv", 1, sum(9, 1, "x")), res("air26", 1, sum(9, 1, "x")))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("%+v", v)
	}
	c, r = pair(res("mserv", 0, sum(10, 0)), res("air26", 1, sum(9, 1, "x")))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("the reference fails and the candidate passes: %+v", v)
	}
	c, r = pair(res("mserv", 1, sum(9, 1, "y")), res("air26", 1, sum(9, 1, "x")))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("two different failures: %+v", v)
	}
}

// A summary keeps only the first failures it saw: when either half lists fewer failures than it counts, the sets cannot be compared, so
// the pair proves nothing (void), whether the visible parts agree or not.
func TestATruncatedFailureListVoidsThePair(t *testing.T) {
	trunc := func(failures ...string) *wire.Summary {
		s := sum(0, 0, failures...)
		s.Failed = len(failures) + 40 // 40 more failures than are listed
		return s
	}
	c, r := pair(res("mserv", 1, trunc("a", "b")), res("air26", 1, trunc("a", "b")))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
		t.Fatalf("agreeing truncated lists: %+v", v)
	}
	c, r = pair(res("mserv", 1, trunc("b", "a")), res("air26", 1, trunc("c", "d")))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeVoid {
		t.Fatalf("different truncated lists: %+v", v)
	}
}

// Two hosts that ran different tests and passed as many of them are told apart by the digest: this is what the counts alone could not do.
func TestDifferentPassedTestsWithTheSameCountAreAMismatch(t *testing.T) {
	cs, rs := sum(2, 0), sum(2, 0)
	cs.PassedDigest = digestOf(1001) // tests A and B
	rs.PassedDigest = digestOf(1002) // tests C and D
	c, r := pair(res("mserv", 0, cs), res("air26", 0, rs))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("%+v", v)
	}
}

// A digest missing on either half (an older host, an older result, a parser with none) is a mismatch: never a fall back to the counts.
func TestAMissingDigestIsAMismatchNeverACountFallback(t *testing.T) {
	none := func() *wire.Summary { s := sum(10, 0); s.PassedDigest = ""; return s }
	for name, tc := range map[string]struct{ cand, ref *wire.Summary }{
		"the candidate has none": {none(), sum(10, 0)},
		"the reference has none": {sum(10, 0), none()},
		"neither has one":        {none(), none()},
	} {
		c, r := pair(res("mserv", 0, tc.cand), res("air26", 0, tc.ref))
		if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
			t.Errorf("%s: %+v", name, v)
		}
	}
	// no summary at all (a task without a parser) has no pass set to name: exit state alone decides, as before
	c, r := pair(res("mserv", 0, nil), res("air26", 0, nil))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("%+v", v)
	}
}

// A task with no test list (tsc, lint, build) reports a summary of zeros and no digest on both halves: only the exit state can differ.
func TestHalvesThatCountNoTestMatchOnTheExitState(t *testing.T) {
	zero := func() *wire.Summary { return &wire.Summary{} }
	c, r := pair(res("mserv", 0, zero()), res("air26", 0, zero()))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("%+v", v)
	}
	c, r = pair(res("mserv", 1, zero()), res("air26", 2, zero()))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMatch {
		t.Fatalf("both failing the same way: %+v", v)
	}
	c, r = pair(res("mserv", 0, zero()), res("air26", 1, zero()))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("exit states differ: %+v", v)
	}
	// one half counting a test brings back the digest rule
	c, r = pair(res("mserv", 0, zero()), res("air26", 0, sum(10, 0)))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("one half counted tests: %+v", v)
	}
	c, r = pair(res("mserv", 0, sum(10, 0)), res("air26", 0, zero()))
	if v := Compare(c, r, "plain"); v.Outcome != OutcomeMismatch {
		t.Fatalf("one half counted tests: %+v", v)
	}
}

// A task with no test list reports an empty summary on purpose, and only a plain summary may (kind "" is plain). For a kind that parses a
// report, an empty summary is what a missing or unreadable report looks like, so two of them never match on the exit alone.
func TestTheExitAloneDecidesOnlyForAPlainSummary(t *testing.T) {
	for _, kind := range []string{"plain", ""} {
		c, r := pair(res("mserv", 0, &wire.Summary{}), res("air26", 0, &wire.Summary{}))
		if v := Compare(c, r, kind); v.Outcome != OutcomeMatch {
			t.Errorf("%q, same exit: %+v", kind, v)
		}
		c, r = pair(res("mserv", 0, &wire.Summary{}), res("air26", 1, &wire.Summary{}))
		if v := Compare(c, r, kind); v.Outcome != OutcomeMismatch {
			t.Errorf("%q, different exit: %+v", kind, v)
		}
	}
	for _, kind := range []string{"junit", "vitest-json", "go-json"} {
		c, r := pair(res("mserv", 0, &wire.Summary{}), res("air26", 0, &wire.Summary{}))
		if v := Compare(c, r, kind); v.Outcome != OutcomeMismatch {
			t.Errorf("%q, nothing parsed on either half: %+v", kind, v)
		}
	}
}
