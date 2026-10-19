package summary

import "testing"

// A failed test, or a suite that failed on its own, in a file outside the workspace is not told apart from the same name in another
// directory (rel folds both to <outside-workspace>/<base>, and qualification compares a failure by its name and file): no digest.
func TestVitestFailuresOutsideTheWorkspaceGiveNoDigest(t *testing.T) {
	failed := func(dir string) []byte {
		return []byte(`{"testResults":[{"name":"` + dir + `/x.test.ts","status":"failed","assertionResults":[{"fullName":"breaks","status":"failed","failureMessages":["no"]}]}]}`)
	}
	suiteLevel := func(dir string) []byte {
		return []byte(`{"testResults":[{"name":"` + dir + `/x.test.ts","status":"failed","message":"cannot import","assertionResults":[]}]}`)
	}
	for name, report := range map[string][]byte{
		"a failed test, /a":  failed("/a"),
		"a failed test, /b":  failed("/b"),
		"a failed suite, /a": suiteLevel("/a"),
		"a failed suite, /b": suiteLevel("/b"),
	} {
		s, err := Parse("vitest-json", Input{Report: report, Root: "/work", Digest: true})
		if err != nil || s.PassedDigest != "" {
			t.Errorf("%s: digest %q err %v", name, s.PassedDigest, err)
		}
	}
	// inside the workspace the same failures have a digest, and it tells the two directories apart
	in := func(dir string) string {
		return digestOf(t, "vitest-json", Input{Report: failed("/work/" + dir), Root: "/work"})
	}
	if a, b := in("a"), in("b"); a == "" || a == b {
		t.Errorf("inside the workspace: %q %q", a, b)
	}
	// a suite of only skipped tests outside the workspace is not compared, so it does not cost the report its digest
	skipOnly := `{"testResults":[{"name":"/elsewhere/s.test.ts","status":"passed","assertionResults":[{"fullName":"later","status":"pending"}]},{"name":"/work/ok.test.ts","status":"passed","assertionResults":[{"fullName":"fine","status":"passed"}]}]}`
	if s, err := Parse("vitest-json", Input{Report: []byte(skipOnly), Root: "/work", Digest: true}); err != nil || s.PassedDigest == "" {
		t.Errorf("a suite of skipped tests outside: digest %q err %v", s.PassedDigest, err)
	}
}
