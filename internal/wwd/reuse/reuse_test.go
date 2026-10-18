package reuse

import "testing"

func baseParts() KeyParts {
	return KeyParts{
		Tree: "t1", Task: "tsc", Args: []string{"a", "b"}, DefHash: "d1", OS: "linux", Arch: "amd64",
		Toolchain:     map[string]string{"os_version": "26.04", "go": "1.26.0", "node": "24.14.0"},
		Qualification: map[string]string{"go": "1.26.0"},
	}
}

func TestKeyIsStableAcrossMapOrderAndNilVersusEmpty(t *testing.T) {
	a := baseParts()
	b := baseParts()
	b.Toolchain = map[string]string{"node": "24.14.0", "go": "1.26.0", "os_version": "26.04"}
	if Key(a) != Key(b) {
		t.Fatal("the same parts gave two keys")
	}
	a.Qualification, b.Qualification = nil, map[string]string{}
	a.Args, b.Args = nil, []string{}
	if Key(a) != Key(b) {
		t.Fatal("nil and empty are one key")
	}
}

// Each part of the key changes the key (spec §4.5).
func TestKeyChangesWithEveryPart(t *testing.T) {
	base := Key(baseParts())
	for name, mod := range map[string]func(*KeyParts){
		"tree":     func(p *KeyParts) { p.Tree = "t2" },
		"task":     func(p *KeyParts) { p.Task = "lint" },
		"args":     func(p *KeyParts) { p.Args = []string{"b", "a"} },
		"def hash": func(p *KeyParts) { p.DefHash = "d2" },
		"os":       func(p *KeyParts) { p.OS = "darwin" },
		"arch":     func(p *KeyParts) { p.Arch = "arm64" },
		"os version": func(p *KeyParts) {
			p.Toolchain = map[string]string{"os_version": "26.10", "go": "1.26.0", "node": "24.14.0"}
		},
		"tool version": func(p *KeyParts) {
			p.Toolchain = map[string]string{"os_version": "26.04", "go": "1.26.1", "node": "24.14.0"}
		},
		"qualification": func(p *KeyParts) { p.Qualification = map[string]string{"go": "1.26.1"} },
	} {
		p := baseParts()
		mod(&p)
		if Key(p) == base {
			t.Errorf("%s does not change the key", name)
		}
	}
}

func entry() Entry {
	return Entry{Tree: "t1", Task: "tsc", Args: []string{"x"}, DefHash: "d1", Host: "mserv", Fingerprint: map[string]string{"go": "1.26.0"}}
}

func query() Query {
	return Query{Tree: "t1", Task: "tsc", Args: []string{"x"}, DefHash: "d1", Labels: []string{"os:linux"}, Host: "mlab"}
}

func validHost() HostNow {
	return HostNow{Labels: []string{"os:linux", "arch:amd64"}, Earned: true, EarnedFingerprint: map[string]string{"go": "1.26.0"}}
}

// Condition (1).
func TestSameWorkNeedsTreeTaskArgsAndDefinition(t *testing.T) {
	if !SameWork(entry(), query()) {
		t.Fatal("equal work does not match")
	}
	for name, mod := range map[string]func(*Query){
		"tree": func(q *Query) { q.Tree = "t2" }, "task": func(q *Query) { q.Task = "lint" },
		"args": func(q *Query) { q.Args = []string{"y"} }, "def": func(q *Query) { q.DefHash = "d2" },
	} {
		q := query()
		mod(&q)
		if SameWork(entry(), q) {
			t.Errorf("a different %s matches", name)
		}
	}
}

// Condition (2): a job that names os:linux never takes a darwin result.
func TestDarwinResultNeverAnswersALinuxJob(t *testing.T) {
	darwin := HostNow{Labels: []string{"os:darwin", "arch:arm64"}, Earned: true, EarnedFingerprint: map[string]string{"go": "1.26.0"}}
	if HostCoversLabels(darwin, []string{"os:linux"}) {
		t.Fatal("darwin covers os:linux")
	}
	if Answers(entry(), query(), darwin) {
		t.Fatal("a darwin result answered a linux job")
	}
	if !HostCoversLabels(darwin, nil) {
		t.Fatal("a job with no labels is covered by any host")
	}
}

// Condition (3).
func TestHostStillValid(t *testing.T) {
	e := entry()
	if !HostStillValid(e, validHost()) {
		t.Fatal("earned with the same fingerprint is valid")
	}
	drifted := validHost()
	drifted.EarnedFingerprint = map[string]string{"go": "1.26.1"}
	if HostStillValid(e, drifted) {
		t.Fatal("earned with another fingerprint is valid")
	}
	if HostStillValid(e, HostNow{}) {
		t.Fatal("a host with nothing is valid")
	}
	ref := HostNow{Reference: true, ReferenceFingerprint: map[string]string{"go": "1.26.0"}}
	if !HostStillValid(e, ref) {
		t.Fatal("a reference host with an unchanged fingerprint is valid")
	}
	ref.ReferenceFingerprint = map[string]string{"go": "1.27.0"}
	if HostStillValid(e, ref) {
		t.Fatal("a reference host whose fingerprint changed is valid")
	}
	if HostStillValid(Entry{Host: "h"}, HostNow{Earned: true}) {
		t.Fatal("an empty fingerprint proves nothing")
	}
}

// Condition (4).
func TestUnqualifiedHostAnswersOnlyItself(t *testing.T) {
	e := entry() // made on mserv
	h := HostNow{Labels: []string{"os:linux"}}
	q := query() // from mlab
	if Answers(e, q, h) {
		t.Fatal("an unqualified host's result answered another host")
	}
	q.Host = "mserv"
	if !Answers(e, q, h) {
		t.Fatal("an unqualified host's result did not answer itself")
	}
	if SameHostOnly(Entry{}, Query{}, HostNow{}) {
		t.Fatal("empty hosts are not the same host")
	}
}

// A host with standing whose fingerprint drifted answers nobody, not even itself: its entries are stale, not private.
func TestDriftedEarnedHostDoesNotAnswerItself(t *testing.T) {
	h := validHost()
	h.EarnedFingerprint = map[string]string{"go": "1.26.1"}
	q := query()
	q.Host = "mserv"
	if Answers(entry(), q, h) {
		t.Fatal("an earned host that drifted answered itself")
	}
}

func TestDriftedReferenceHostDoesNotAnswerItself(t *testing.T) {
	h := HostNow{Labels: []string{"os:linux"}, Reference: true, ReferenceFingerprint: map[string]string{"go": "1.27.0"}}
	q := query()
	q.Host = "mserv"
	if Answers(entry(), q, h) {
		t.Fatal("a reference host that drifted answered itself")
	}
}

// A member's result on a qualified mserv answers the lead's re-verification on mlab.
func TestMemberResultOnQualifiedHostAnswersLead(t *testing.T) {
	if !Answers(entry(), query(), validHost()) {
		t.Fatal("no hit for a qualified host's result")
	}
}

// (1) and (2) hold even for the same host: another snapshot is never answered.
func TestSameHostStillNeedsSameWork(t *testing.T) {
	q := query()
	q.Host, q.Tree = "mserv", "t2"
	if Answers(entry(), q, HostNow{Labels: []string{"os:linux"}}) {
		t.Fatal("another tree answered")
	}
}
