package summary

import "testing"

// The digest covers the failed tests as well as the passed ones, by the same identity (suite, file, class, name): two reports with no
// passed test and one failure each, the failure in a different suite, are not the same result. The counts and the failure lists of the
// summary alone could not tell them apart (a Failure names no suite).
func TestJunitDigestTellsWhichSuiteFailed(t *testing.T) {
	doc := func(unit, integration string) []byte {
		return []byte(`<testsuites><testsuite name="unit">` + unit + `</testsuite><testsuite name="integration">` + integration + `</testsuite></testsuites>`)
	}
	fail := `<testcase classname="C" name="t"><failure message="x"/></testcase>`
	skip := `<testcase classname="C" name="t"><skipped/></testcase>`
	// host A: unit failed, integration skipped; host B: the other way round
	a := digestOf(t, "junit", Input{Junit: doc(fail, skip)})
	b := digestOf(t, "junit", Input{Junit: doc(skip, fail)})
	if a == b {
		t.Fatalf("a failure in unit and a failure in integration have one digest: %s", a)
	}
	// and the same report is the same digest
	if again := digestOf(t, "junit", Input{Junit: doc(fail, skip)}); again != a {
		t.Fatal("not stable")
	}
}

// With the same tests passing, a different test failing is another digest; the same ones failing, the same digest. A skipped test is not
// compared (qualification does not), so it does not move the digest.
func TestDigestCoversWhichTestsFailed(t *testing.T) {
	run := func(events ...string) string { return digestOf(t, "go-json", Input{Stdout: goStream(events...)}) }
	skipEv := `{"Action":"skip","Package":"p","Test":"S"}`
	a := run(goPassEv("p", "T1"), goFailEv("p", "T2"))
	if b := run(goPassEv("p", "T1"), goFailEv("p", "T3")); a == b {
		t.Error("another test failing left the digest as it was")
	}
	if b := run(goFailEv("p", "T2"), goPassEv("p", "T1")); a != b {
		t.Error("the order changed the digest")
	}
	if b := run(goPassEv("p", "T1"), goFailEv("p", "T2"), skipEv); a != b {
		t.Error("a skipped test moved the digest")
	}
	// a package that failed on its own (a build failure) is a failure too
	build := `{"Action":"fail","Package":"q"}`
	if b := run(goPassEv("p", "T1"), goFailEv("p", "T2"), build); a == b {
		t.Error("a package that failed to build left the digest as it was")
	}
	// vitest: a suite that failed with no failed test of its own
	vt := func(status string) string {
		return `{"testResults":[{"name":"/w/a.test.ts","status":"` + status + `","assertionResults":[{"fullName":"x","status":"passed"}]}]}`
	}
	vOK := digestOf(t, "vitest-json", Input{Report: []byte(vt("passed")), Root: "/w"})
	vBad := digestOf(t, "vitest-json", Input{Report: []byte(vt("failed")), Root: "/w"})
	if vOK == vBad {
		t.Error("a suite that failed on its own left the digest as it was")
	}
}
