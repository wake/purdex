package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// maxPassedIDs is the most distinct tests (passed, failed and skipped together) one report may name and still get a digest. The ids are
// held to be sorted, so the count bounds the memory; a report beyond it gets no digest (qualification then reads it as a mismatch, never
// as a match).
const maxPassedIDs = 250_000

// passedSet collects the ids of the tests of a report, for Summary.PassedDigest (spec §7, WWD-5b-2w). It is off unless the caller asked
// for a digest (Input.Digest): then a parser adds the id of each test as it reads it and asks for the digest only once the whole report was
// read without a problem.
//
// The digest identifies which tests passed and which failed. Skipped tests are not in it (qualification does not compare them) but they
// are known, to see an id that is both.
type passedSet struct {
	on      bool
	over    bool
	tainted bool // a test of the report cannot be told apart from another: the set is not a set of tests, so there is no digest
	passed  map[string]struct{}
	failed  map[string]struct{}
	skipped map[string]struct{}
}

func newPassedSet(on bool) *passedSet {
	return &passedSet{on: on, passed: map[string]struct{}{}, failed: map[string]struct{}{}, skipped: map[string]struct{}{}}
}

// taint says the report holds a test whose identity is not unique across hosts (a path outside the workspace, folded for display; a
// test with no name). A wrong "same" is worse than a missing digest, which only sends the pair round again.
func (p *passedSet) taint() { p.tainted = true }

func idOf(parts []string) string { return strconv.Quote(strings.Join(parts, "\x00")) }

func (p *passedSet) size() int { return len(p.passed) + len(p.failed) + len(p.skipped) }

// record puts a test, named by parts, in one of the sets. The parts name it (a package and a test, a file and a title): they are quoted
// together, so an id is one line whatever the report put in a name, and two different tests never make one id. The same id again is the
// same test: the digest is of the sets, not of how often each test ran.
func (p *passedSet) record(into map[string]struct{}, parts []string) {
	if !p.on || p.over {
		return
	}
	id := idOf(parts)
	if _, seen := into[id]; seen {
		return
	}
	if p.size() >= maxPassedIDs { // a new id past the limit: only a different test counts toward it, never a repeat
		p.over, p.passed, p.failed, p.skipped = true, nil, nil, nil
		return
	}
	into[id] = struct{}{}
}

// add records a test that passed, failedTest one that failed (a build failure of a package, a suite that failed with no failed test of
// its own, count as one named for what failed) and skippedTest one that was skipped.
func (p *passedSet) add(parts ...string)         { p.record(p.passed, parts) }
func (p *passedSet) failedTest(parts ...string)  { p.record(p.failed, parts) }
func (p *passedSet) skippedTest(parts ...string) { p.record(p.skipped, parts) }

// digest is "sha256:" and the hex SHA-256 of the sorted lines "P <id>" for each passed test and "F <id>" for each failed one, each
// followed by a newline; "" when it was not asked for, the report named more tests than maxPassedIDs, or one id is in more than one set.
//
// An id in two sets cannot say which of the outcomes it was (two suites of one name, a test run twice that passed once): another host with
// the outcomes the other way round would have the same digest for another result. So that report has none.
func (p *passedSet) digest() string {
	if !p.on || p.over || p.tainted {
		return ""
	}
	lines := make([]string, 0, len(p.passed)+len(p.failed))
	for id := range p.passed {
		if _, also := p.failed[id]; also {
			return ""
		}
		if _, also := p.skipped[id]; also {
			return ""
		}
		lines = append(lines, "P "+id)
	}
	for id := range p.failed {
		if _, also := p.skipped[id]; also {
			return ""
		}
		lines = append(lines, "F "+id)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
