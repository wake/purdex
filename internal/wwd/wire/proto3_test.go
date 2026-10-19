package wire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Proto 3 is one new reader: Summary.passed_digest. Decoders are strict, so an older reader fails on a field it does not know; the
// writers therefore stay off it until every pool host reports proto >= 3 (docs/wwd-deploy.md §5). These tests pin the two halves.

const goodDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSummaryDigestForm(t *testing.T) {
	for _, c := range []struct {
		d  string
		ok bool
	}{
		{"", true},
		{goodDigest, true},
		{"sha256:", false},
		{"sha256:" + strings.Repeat("A", 64), false}, // upper case
		{"sha1:" + strings.Repeat("a", 40), false},
		{goodDigest + "0", false},
		{" " + goodDigest, false},
	} {
		s := &Summary{PassedDigest: c.d}
		if err := s.Validate(); (err == nil) != c.ok {
			t.Errorf("%q: err = %v, want ok = %v", c.d, err, c.ok)
		}
	}
	var nilSummary *Summary
	if err := nilSummary.Validate(); err != nil {
		t.Fatalf("no summary: %v", err)
	}
}

// A worker cannot post a malformed digest.
func TestResultRequestRefusesABadDigest(t *testing.T) {
	body := func(d string) string {
		return `{"claim_id":"` + strings.Repeat("a", 32) + `","state":"succeeded","exit":0,"summary":{"passed":1,"failed":0,"skipped":0,"failures":[],"tail":[],"passed_digest":"` + d + `"},"started_at":null,"finished_at":null}`
	}
	if _, err := Decode[ResultRequest]([]byte(body(goodDigest))); err != nil {
		t.Fatalf("a good digest: %v", err)
	}
	if _, err := Decode[ResultRequest]([]byte(body("md5:abc"))); err == nil {
		t.Fatal("a bad digest was accepted")
	}
}

// A result read back from a peer is checked the way a posted one is: a job (and so a jobs list) whose result carries a malformed digest
// is refused at the boundary, and a good or absent one is not.
func TestAJobWithAMalformedDigestIsRefused(t *testing.T) {
	good := string(fixture(t, "job_done.json"))
	with := func(d string) string {
		return strings.Replace(good, `"tail": ["x"]}`, `"tail": ["x"], "passed_digest": "`+d+`"}`, 1)
	}
	if with(goodDigest) == good {
		t.Fatal("test setup: the fixture changed")
	}
	if _, err := Decode[Job]([]byte(with(goodDigest))); err != nil {
		t.Fatalf("a good digest: %v", err)
	}
	if _, err := Decode[Job]([]byte(good)); err != nil {
		t.Fatalf("no digest: %v", err)
	}
	if _, err := Decode[Job]([]byte(with("sha256:zz"))); err == nil {
		t.Fatal("a job with a malformed digest was accepted")
	}
	var nilResult *Result
	if err := nilResult.Validate(); err != nil {
		t.Fatalf("no result: %v", err)
	}
}

// The writer-release invariant: a summary with no digest is byte-for-byte the shape an older reader takes, so a writer that has nothing to
// say about the passed tests (or is held back by the gate) does not show an old reader a field it would reject. With a digest the same
// strict decode fails, which is why the writers wait.
func TestSummaryWithoutADigestIsTheOldShape(t *testing.T) {
	type oldSummary struct {
		Passed   int       `json:"passed"`
		Failed   int       `json:"failed"`
		Skipped  int       `json:"skipped"`
		Failures []Failure `json:"failures"`
		Tail     []string  `json:"tail"`
	}
	strict := func(b []byte) error {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		return dec.Decode(&oldSummary{})
	}
	empty, err := json.Marshal(Summary{Passed: 3, Failures: []Failure{}, Tail: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "passed_digest") {
		t.Fatalf("an empty digest is written: %s", empty)
	}
	if err := strict(empty); err != nil {
		t.Fatalf("an older strict reader refuses a summary with no digest: %v", err)
	}
	full, err := json.Marshal(Summary{Passed: 3, Failures: []Failure{}, Tail: []string{}, PassedDigest: goodDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := strict(full); err == nil {
		t.Fatal("an older strict reader took a digest: the two-phase rollout would not be needed")
	}
}

// New reader, old payload: a result written before the digest decodes with it empty.
func TestProto3ReaderTakesAResultWithoutADigest(t *testing.T) {
	r, err := Decode[ResultRequest]([]byte(`{"claim_id":"` + strings.Repeat("b", 32) + `","state":"succeeded","exit":0,"summary":{"passed":2,"failed":0,"skipped":0,"failures":[],"tail":[]},"started_at":null,"finished_at":null}`))
	if err != nil || r.Summary == nil || r.Summary.PassedDigest != "" || r.Summary.Passed != 2 {
		t.Fatalf("%+v %v", r.Summary, err)
	}
}
