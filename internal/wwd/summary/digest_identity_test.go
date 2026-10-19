package summary

import "testing"

// Two different files outside the workspace that rel folds to one base name must not be one test: such a report gets no digest.
func TestVitestFilesOutsideTheWorkspaceGiveNoDigest(t *testing.T) {
	report := func(dir string) []byte {
		return []byte(`{"testResults":[{"name":"` + dir + `/foo.test.ts","status":"passed","assertionResults":[{"fullName":"works","status":"passed"}]}]}`)
	}
	for _, dir := range []string{"/vendor/a", "/tmp/b"} {
		s, err := Parse("vitest-json", Input{Report: report(dir), Root: "/work", Digest: true})
		if err != nil || s.PassedDigest != "" {
			t.Errorf("%s: digest %q err %v: a file outside the workspace is not told apart from another by its base name", dir, s.PassedDigest, err)
		}
	}
	// inside the workspace it is fine
	s, err := Parse("vitest-json", Input{Report: report("/work/src"), Root: "/work", Digest: true})
	if err != nil || s.PassedDigest == "" {
		t.Errorf("inside: digest %q err %v", s.PassedDigest, err)
	}
}

// The same class and name in two suites are two tests, and one passing in a suite and not in another is another set.
func TestJunitDigestTellsSuitesApart(t *testing.T) {
	doc := func(inner string) []byte { return []byte(`<testsuites>` + inner + `</testsuites>`) }
	two := doc(`<testsuite name="unit"><testcase classname="A" name="t"/></testsuite><testsuite name="integration"><testcase classname="A" name="t"/></testsuite>`)
	onlyUnit := doc(`<testsuite name="unit"><testcase classname="A" name="t"/></testsuite><testsuite name="integration"><testcase classname="A" name="t"><failure message="x"/></testcase></testsuite>`)
	onlyIntegration := doc(`<testsuite name="unit"><testcase classname="A" name="t"><failure message="x"/></testcase></testsuite><testsuite name="integration"><testcase classname="A" name="t"/></testsuite>`)
	a := digestOf(t, "junit", Input{Junit: two})
	b := digestOf(t, "junit", Input{Junit: onlyUnit})
	c := digestOf(t, "junit", Input{Junit: onlyIntegration})
	if a == b || a == c || b == c {
		t.Fatalf("suites are not told apart: %s %s %s", a, b, c)
	}
	// nested suites: the whole path counts
	nested := digestOf(t, "junit", Input{Junit: doc(`<testsuite name="o"><testsuite name="i"><testcase classname="A" name="t"/></testsuite></testsuite>`)})
	flat := digestOf(t, "junit", Input{Junit: doc(`<testsuite name="i"><testcase classname="A" name="t"/></testsuite>`)})
	if nested == flat {
		t.Fatal("a suite inside another is the same as one on its own")
	}
	// the same document gives the same digest (suite names are what both hosts read)
	if again := digestOf(t, "junit", Input{Junit: two}); again != a {
		t.Fatal("not stable")
	}
}

// A case with no name, or a file outside the workspace, cannot be told apart: no digest. A failed or skipped one needs no identity.
func TestJunitCasesThatCannotBeToldApartGiveNoDigest(t *testing.T) {
	doc := func(inner string) []byte { return []byte(`<testsuite name="s">` + inner + `</testsuite>`) }
	for name, inner := range map[string]string{
		"no name":               `<testcase classname="A"/><testcase classname="B"/>`,
		"a file outside":        `<testcase classname="A" name="t" file="/elsewhere/x.php"/>`,
		"no name beside a good": `<testcase classname="A" name="t"/><testcase classname="B"/>`,
	} {
		s, err := Parse("junit", Input{Junit: doc(inner), Root: "/work", Digest: true})
		if err != nil || s.PassedDigest != "" {
			t.Errorf("%s: digest %q err %v", name, s.PassedDigest, err)
		}
	}
	s, err := Parse("junit", Input{Junit: doc(`<testcase classname="A" name="ok" file="/work/x.php"/><testcase name=""><failure message="x"/></testcase><testcase><skipped/></testcase>`), Root: "/work", Digest: true})
	if err != nil || s.PassedDigest == "" {
		t.Errorf("a nameless failed or skipped case is not a passed test: digest %q err %v", s.PassedDigest, err)
	}
}
