package reuse

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func infoOf(labels []string, fp map[string]string) wire.Info {
	return wire.Info{Alias: "mserv", Labels: labels, Fingerprint: fp}
}

// The real earned label, read from a host's /v1/info, decides condition (3) end to end.
func TestHostNowFromInfoDrivesTheLookup(t *testing.T) {
	label := "task:purdex/tsc"
	fp := map[string]string{"go": "1.26.0"}
	q := query()
	// a qualified host (advertises the label): a member's result answers the lead on mlab
	h := HostNowFromInfo(infoOf([]string{"os:linux", label}, fp), label, false, true)
	if !h.Earned || h.Reference || !Answers(entry(), q, h) {
		t.Fatalf("earned host: %+v", h)
	}
	// the same host after its toolchain changed: the label is revoked (not advertised) and the toolchain is another one
	h = HostNowFromInfo(infoOf([]string{"os:linux"}, map[string]string{"go": "1.26.1"}), label, false, true)
	if h.Earned || Answers(entry(), q, h) {
		t.Fatalf("a drifted host answered another host: %+v", h)
	}
	// no standing and the same toolchain: only itself
	h = HostNowFromInfo(infoOf([]string{"os:linux"}, fp), label, false, true)
	if Answers(entry(), q, h) {
		t.Fatal("a host with no standing answered another host")
	}
	q.Host = "mserv"
	if !Answers(entry(), q, h) {
		t.Fatal("a host with no standing did not answer itself")
	}
	// the reference host: valid while its fingerprint is the one of the entry
	h = HostNowFromInfo(infoOf([]string{"os:linux", label}, fp), label, true, true)
	q.Host = "mlab"
	if !h.Reference || h.Earned || !Answers(entry(), q, h) {
		t.Fatalf("reference host: %+v", h)
	}
	h = HostNowFromInfo(infoOf([]string{"os:linux", label}, map[string]string{"go": "1.27.0"}), label, true, true)
	if Answers(entry(), q, h) {
		t.Fatal("a reference host that drifted answered")
	}
}

// A quarantined label is not advertised, so /v1/info alone cannot tell it from "never had it". Unless the caller vouches that the host
// is not quarantined, the host does not answer even itself; with the vouching it does.
func TestUnvouchedHostWithoutTheLabelAnswersNobody(t *testing.T) {
	label := "task:purdex/tsc"
	fp := map[string]string{"go": "1.26.0"}
	q := query()
	q.Host = "mserv"
	info := infoOf([]string{"os:linux"}, fp) // the label is missing: never earned, or quarantined
	h := HostNowFromInfo(info, label, false, false)
	if !h.StandingUnverified || Answers(entry(), q, h) {
		t.Fatalf("an unverified standing answered: %+v", h)
	}
	h = HostNowFromInfo(info, label, false, true)
	if h.StandingUnverified || !Answers(entry(), q, h) {
		t.Fatalf("a vouched host with no standing did not answer itself: %+v", h)
	}
	// an advertised label needs no vouching: it is earned
	h = HostNowFromInfo(infoOf([]string{"os:linux", label}, fp), label, false, false)
	if h.StandingUnverified || !h.Earned {
		t.Fatalf("%+v", h)
	}
}

func TestHostNowFromInfoCopiesWhatItIsGiven(t *testing.T) {
	info := infoOf([]string{"os:linux"}, map[string]string{"go": "1"})
	h := HostNowFromInfo(info, "task:a/b", false, true)
	info.Labels[0], info.Fingerprint["go"] = "x", "2"
	if h.Labels[0] != "os:linux" || h.CurrentFingerprint["go"] != "1" {
		t.Fatalf("%+v", h)
	}
}

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
	h := HostNow{Labels: []string{"os:linux"}, CurrentFingerprint: map[string]string{"go": "1.26.0"}}
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

// Condition (4) also compares the toolchain: a host with no standing that upgraded Go is not answered with a result from before.
func TestUnqualifiedHostToolchainChangeIsAnotherJob(t *testing.T) {
	q := query()
	q.Host = "mserv"
	h := HostNow{Labels: []string{"os:linux"}, CurrentFingerprint: map[string]string{"go": "1.26.0"}}
	if !Answers(entry(), q, h) {
		t.Fatal("an unchanged toolchain did not hit")
	}
	h.CurrentFingerprint = map[string]string{"go": "1.26.1"}
	if Answers(entry(), q, h) {
		t.Fatal("a changed toolchain hit a result from before the change")
	}
	h.CurrentFingerprint = nil
	if Answers(entry(), q, h) {
		t.Fatal("an unknown current toolchain hit")
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

// A quarantined label's results are in dispute: they answer nobody, not even the host that made them.
func TestQuarantinedHostDoesNotAnswerItself(t *testing.T) {
	h := HostNow{Labels: []string{"os:linux"}, Quarantined: true}
	for _, host := range []string{"mserv", "mlab"} {
		q := query()
		q.Host = host
		if Answers(entry(), q, h) {
			t.Fatalf("a quarantined host's result answered %s", host)
		}
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
	if Answers(entry(), q, HostNow{Labels: []string{"os:linux"}, CurrentFingerprint: map[string]string{"go": "1.26.0"}}) {
		t.Fatal("another tree answered")
	}
}
