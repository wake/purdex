package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// maxPassedIDs is the most distinct passed tests one report may name and still get a digest. The ids are held to be sorted, so the
// count bounds the memory; a report beyond it gets no digest (qualification then reads it as a mismatch, never as a match).
const maxPassedIDs = 250_000

// passedSet collects the ids of the tests that passed, for Summary.PassedDigest (spec §7, WWD-5b-2w). It is off unless the caller asked
// for a digest (Input.Digest): then a parser adds the id of each test that passed and asks for the digest only once the whole report was
// read without a problem.
type passedSet struct {
	on      bool
	over    bool
	tainted bool // a test of the report cannot be told apart from another: the set is not a set of tests, so there is no digest
	ids     map[string]struct{}
}

// taint says the report holds a test whose identity is not unique across hosts (a path outside the workspace, folded for display; a
// test with no name). A wrong "same" is worse than a missing digest, which only sends the pair round again.
func (p *passedSet) taint() { p.tainted = true }

func newPassedSet(on bool) *passedSet {
	return &passedSet{on: on, ids: map[string]struct{}{}}
}

// add records a passed test. The parts name it (a package and a test, a file and a title): they are quoted together, so an id is one
// line whatever the report put in a name, and two different tests never make one id. The same id again is the same test: the digest
// is of the set of passed tests, not of how often each ran.
func (p *passedSet) add(parts ...string) {
	if !p.on || p.over {
		return
	}
	id := strconv.Quote(strings.Join(parts, "\x00"))
	if _, seen := p.ids[id]; seen {
		return
	}
	if len(p.ids) >= maxPassedIDs { // a new id past the limit: only a different test counts toward it, never a repeat
		p.over, p.ids = true, nil
		return
	}
	p.ids[id] = struct{}{}
}

// digest is "sha256:" and the hex SHA-256 of the sorted ids, each followed by a newline; "" when it was not asked for or the report
// named more tests than maxPassedIDs.
func (p *passedSet) digest() string {
	if !p.on || p.over || p.tainted {
		return ""
	}
	ids := make([]string, 0, len(p.ids))
	for id := range p.ids {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
