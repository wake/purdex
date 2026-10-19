package summary

import "testing"

func noDigest(t *testing.T, name, kind string, in Input) {
	t.Helper()
	in.Digest = true
	s, err := Parse(kind, in)
	if err != nil || s.PassedDigest != "" {
		t.Errorf("%s: digest %q err %v: an id that passed and did not in one report must not make a digest", name, s.PassedDigest, err)
	}
}

// One id that both passed and did not (failed or skipped) in a report cannot say which was which: a host with the two the other way
// round would have the same digest for another outcome. So that report has none.
func TestAnIDThatBothPassedAndDidNotGivesNoDigest(t *testing.T) {
	// two sibling suites with no name, the same file, class and name in both: A passes the first, B the second
	suites := func(first, second string) []byte {
		return []byte(`<testsuites><testsuite>` + first + `</testsuite><testsuite>` + second + `</testsuite></testsuites>`)
	}
	pass := `<testcase classname="C" name="t"/>`
	fail := `<testcase classname="C" name="t"><failure message="x"/></testcase>`
	skip := `<testcase classname="C" name="t"><skipped/></testcase>`
	noDigest(t, "junit A: pass then fail", "junit", Input{Junit: suites(pass, fail)})
	noDigest(t, "junit B: fail then pass", "junit", Input{Junit: suites(fail, pass)})
	noDigest(t, "junit: pass and skipped", "junit", Input{Junit: suites(pass, skip)})
	// and the pass alone, twice, is still the one test that passed
	if d := digestOf(t, "junit", Input{Junit: suites(pass, pass)}); d == "" {
		t.Error("the same test passing in two suites of one name lost its digest")
	}

	// a go test run twice, passing once
	goRun := func(a, b string) []byte {
		return goStream(`{"Action":"`+a+`","Package":"p","Test":"T"}`, `{"Action":"`+b+`","Package":"p","Test":"T"}`)
	}
	noDigest(t, "go: pass then fail", "go-json", Input{Stdout: goRun("pass", "fail")})
	noDigest(t, "go: fail then pass", "go-json", Input{Stdout: goRun("fail", "pass")})
	noDigest(t, "go: skip then pass", "go-json", Input{Stdout: goRun("skip", "pass")})
	if d := digestOf(t, "go-json", Input{Stdout: goRun("pass", "pass")}); d == "" {
		t.Error("a go test that passed twice lost its digest")
	}

	// vitest: the same full name in one file, passed and failed
	vt := `{"testResults":[{"name":"/w/a.test.ts","status":"failed","assertionResults":[{"fullName":"x","status":"passed"},{"fullName":"x","status":"failed","failureMessages":["no"]}]}]}`
	noDigest(t, "vitest: passed and failed", "vitest-json", Input{Report: []byte(vt), Root: "/w"})
}
