package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
)

func digestOf(t *testing.T, kind string, in Input) string {
	t.Helper()
	in.Digest = true
	s, err := Parse(kind, in)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("%s: the digest is not of the wire form: %v", kind, err)
	}
	return s.PassedDigest
}

func goStream(events ...string) []byte {
	var b []byte
	for _, e := range events {
		b = append(b, e...)
		b = append(b, '\n')
	}
	return b
}

func goPassEv(pkg, test string) string {
	return fmt.Sprintf(`{"Action":"pass","Package":%q,"Test":%q}`, pkg, test)
}

func goFailEv(pkg, test string) string {
	return fmt.Sprintf(`{"Action":"fail","Package":%q,"Test":%q}`, pkg, test)
}

// The digest is what the spec says it is: "sha256:" and the hex of the SHA-256 of the sorted passed test ids, each followed by a newline.
// Written out here so the definition cannot drift unnoticed.
func TestPassedDigestIsTheHashOfTheSortedIDs(t *testing.T) {
	got := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p/b", "TestB"), goPassEv("p/a", "TestA"))})
	h := sha256.New()
	for _, id := range []string{strconv.Quote("p/a\x00TestA"), strconv.Quote("p/b\x00TestB")} {
		h.Write([]byte(id + "\n"))
	}
	if want := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

// Not asked for, not computed.
func TestNoDigestUnlessAskedFor(t *testing.T) {
	s, err := Parse("go-json", Input{Stdout: goStream(goPassEv("p", "T"))})
	if err != nil || s.PassedDigest != "" {
		t.Fatalf("digest %q err %v", s.PassedDigest, err)
	}
}

// The same tests passing is the same digest, whatever the order and however often a test ran; another set of passes, or a pass that
// became a failure, is another digest. Skipped and failed tests are not in the set.
func TestDigestIsOfTheSetOfPassedTests(t *testing.T) {
	a := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "T1"), goPassEv("p", "T2"), goFailEv("p", "T3"))})
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goFailEv("p", "T3"), goPassEv("p", "T2"), goPassEv("p", "T1"))}); a != b {
		t.Error("the order of the events changed the digest")
	}
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "T1"), goPassEv("p", "T2"), goPassEv("p", "T2"), goFailEv("p", "T3"))}); a != b {
		t.Error("a test that ran twice changed the digest")
	}
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "T1"), goFailEv("p", "T2"), goFailEv("p", "T3"))}); a == b {
		t.Error("a pass that became a failure left the digest as it was")
	}
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "T1"), goPassEv("p", "T2"), goPassEv("p", "T4"))}); a == b {
		t.Error("another test passing left the digest as it was")
	}
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("q", "T1"), goPassEv("p", "T2"), goFailEv("p", "T3"))}); a == b {
		t.Error("the same test name in another package is another test")
	}
	// the failed one's name is not in it, a skipped one's neither
	skipped := `{"Action":"skip","Package":"p","Test":"T9"}`
	if b := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "T1"), goPassEv("p", "T2"), goFailEv("p", "T3"), skipped)}); a != b {
		t.Error("a skipped test changed the digest")
	}
}

// A name with a newline in it is one test, not two: ids are quoted.
func TestDigestKeepsOneIDPerTestWhateverTheNameHolds(t *testing.T) {
	one := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "a\nb"))})
	two := digestOf(t, "go-json", Input{Stdout: goStream(goPassEv("p", "a"), goPassEv("p", "b"))})
	if one == two {
		t.Fatal("a test named 'a\\nb' has the digest of two tests 'a' and 'b'")
	}
}

// Nothing that cannot be trusted gets a digest: a damaged event, a status the parser does not know, a plain summary, an over-long set.
func TestNoDigestForAReportThatIsNotWhole(t *testing.T) {
	bad := goStream(goPassEv("p", "T1"), `{"Action":"pass","Package":"p","Te`)
	s, err := Parse("go-json", Input{Stdout: bad, Digest: true})
	if err == nil || s.PassedDigest != "" {
		t.Errorf("go-json damaged: digest %q err %v", s.PassedDigest, err)
	}
	vt := `{"testResults":[{"name":"/w/a.test.ts","status":"passed","assertionResults":[{"fullName":"x","status":"passed"},{"fullName":"y","status":"weird"}]}]}`
	s, err = Parse("vitest-json", Input{Report: []byte(vt), Root: "/w", Digest: true})
	if err == nil || s.PassedDigest != "" {
		t.Errorf("vitest unknown status: digest %q err %v", s.PassedDigest, err)
	}
	s, err = Parse("plain", Input{Log: []byte("ok\n"), Digest: true})
	if err != nil || s.PassedDigest != "" {
		t.Errorf("plain: digest %q err %v", s.PassedDigest, err)
	}
	p := newPassedSet(true)
	for i := 0; i <= maxPassedIDs; i++ {
		p.add("p", strconv.Itoa(i))
	}
	if p.digest() != "" {
		t.Error("a set over the limit still got a digest")
	}
}

// The limit is on distinct tests: a set that is exactly full still takes a repeat, and only a new test past it goes over.
func TestTheLimitCountsDistinctTestsOnly(t *testing.T) {
	p := newPassedSet(true)
	for i := 0; i < maxPassedIDs; i++ {
		p.add("p", strconv.Itoa(i))
	}
	p.add("p", "0") // a repeat of a test already in
	p.add("p", strconv.Itoa(maxPassedIDs-1))
	if p.digest() == "" {
		t.Fatal("a repeat on a full set took the digest away")
	}
	p.add("p", "one more")
	if p.digest() != "" {
		t.Fatal("the 250,001st distinct test kept the digest")
	}
}

// The workspace root does not enter the id: two hosts with different directories have the same digest for the same passes.
func TestVitestDigestIsOfTheRelativeFile(t *testing.T) {
	report := func(root string) string {
		return `{"testResults":[{"name":"` + root + `/src/a.test.ts","status":"passed","assertionResults":[{"fullName":"a works","status":"passed"},{"fullName":"b fails","status":"failed","failureMessages":["no"]}]}]}`
	}
	a := digestOf(t, "vitest-json", Input{Report: []byte(report("/work/1")), Root: "/work/1"})
	b := digestOf(t, "vitest-json", Input{Report: []byte(report("/other/2")), Root: "/other/2"})
	if a != b {
		t.Fatalf("the directory changed the digest: %s %s", a, b)
	}
}

func TestJunitDigest(t *testing.T) {
	xml := func(cases string) []byte {
		return []byte(`<testsuites><testsuite name="s">` + cases + `</testsuite></testsuites>`)
	}
	pass := `<testcase classname="A" name="one"/><testcase classname="A" name="two"/>`
	bad := `<testcase classname="A" name="bad"><failure message="x"/></testcase>`
	skip := `<testcase classname="A" name="skip"><skipped/></testcase>`
	a := digestOf(t, "junit", Input{Junit: xml(pass + bad + skip)})
	if b := digestOf(t, "junit", Input{Junit: xml(bad + pass)}); a != b {
		t.Error("order or a skipped case changed the digest")
	}
	if b := digestOf(t, "junit", Input{Junit: xml(`<testcase classname="A" name="one"/>` + bad)}); a == b {
		t.Error("a missing pass left the digest as it was")
	}
}
