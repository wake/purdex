// Package reuse is the pure part of result reuse (spec §4.5): the key a stored result is filed under and the four conditions under
// which a stored result answers a new submission. It reads no store and no clock; the store (5d-1b) and the submit path (5d-2) call it.
package reuse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/wake/purdex/internal/wwd/wire"
)

// KeyParts is everything the key is made of (spec §4.5): what was run, and the host and the qualification it was run under.
type KeyParts struct {
	Tree    string   // snapshot tree
	Task    string   // task name
	Args    []string // in order: the same words in another order are another run
	DefHash string   // hash of the parsed task definition (run, cwd, env, args kind, summary)
	OS      string
	Arch    string
	// Toolchain is the producing host's full fingerprint (OS version and every tool version the task's labels name);
	// Qualification is that host's qualification fingerprint for the task ("" keys for a reference host: nil/empty).
	Toolchain     map[string]string
	Qualification map[string]string
}

// keyVersion is the V of the key. It is raised when what a job runs with changes in a way the other parts do not show, so that a result
// stored before is no longer found: 2 since a job's environment carries USER and LOGNAME (#3919), and a success of the older environment
// must not answer a submission now. The lookup crosses hosts (daemon/reuse_lookup.go): during a rolling deploy the two versions file
// under different keys, so a host only misses a result of the other, it never hits one it should not.
const keyVersion = 2

// Key is the hex SHA-256 of the canonical encoding of the parts. Map keys are sorted by encoding/json, so the same parts give the
// same key whatever order the maps were built in.
func Key(p KeyParts) string { return keyAt(keyVersion, p) }

// keyAt is Key under the given version (a seam for the test that the version changes the key).
func keyAt(version int, p KeyParts) string {
	b, err := json.Marshal(struct {
		V             int               `json:"v"`
		Tree          string            `json:"tree"`
		Task          string            `json:"task"`
		Args          []string          `json:"args"`
		DefHash       string            `json:"def"`
		OS            string            `json:"os"`
		Arch          string            `json:"arch"`
		Toolchain     map[string]string `json:"toolchain"`
		Qualification map[string]string `json:"qual"`
	}{version, p.Tree, p.Task, nonNil(p.Args), p.DefHash, p.OS, p.Arch, nonNilMap(p.Toolchain), nonNilMap(p.Qualification)})
	if err != nil { // strings, slices and maps of strings cannot fail to encode
		panic("reuse: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// Entry is what a stored result remembers about the work and the host that made it.
type Entry struct {
	Tree        string
	Task        string
	Args        []string
	DefHash     string
	Host        string            // result.host
	Fingerprint map[string]string // the host's toolchain fingerprint when it made the result
}

// Query is a new submission.
type Query struct {
	Tree    string
	Task    string
	Args    []string
	DefHash string
	Labels  []string // the job's labels, e.g. os:linux
	Host    string   // the host the submission comes from
}

// HostNow is what is known of the entry's host at lookup time.
type HostNow struct {
	Labels []string // the labels it advertises now
	// Earned: it holds the task's earned label (state earned) and EarnedFingerprint is the one that label was earned with.
	Earned            bool
	EarnedFingerprint map[string]string
	// CurrentFingerprint is the host's toolchain fingerprint now (the same make-up as Entry.Fingerprint). Condition (4) needs it:
	// a host with no standing has no earned or reference fingerprint to compare, but a new toolchain is still another job.
	CurrentFingerprint map[string]string
	// Quarantined: it holds the task's label in state quarantined (it had standing and lost it to a disagreement). Not Earned, and
	// not "no standing" either: its entries answer nobody until it is qualified again.
	Quarantined bool
	// StandingUnverified: this lookup cannot tell a host that never held the label from one that lost it to a quarantine (a host's
	// public label list hides quarantined labels). Condition (4) then does not apply: only (3) can answer.
	StandingUnverified bool
	// Reference: it is the task's reference host and ReferenceFingerprint is its current fingerprint.
	Reference            bool
	ReferenceFingerprint map[string]string
}

// HostNowFromInfo is what a host's /v1/info says, read as HostNow for one task: taskLabel is task:<repo>/<task>, isReference says the
// host is that task's reference (from the manifest's qualify.reference). A non-reference host that advertises the task label has it
// earned: an advertised label has not drifted (a drift revokes it, WWD-5c-1), so the current toolchain fingerprint is the one it was
// earned with. The reference host holds the label without qualifying and counts as Reference, not Earned.
//
// A quarantined label is not advertised, so /v1/info alone cannot tell "never had it" from "lost it". The caller says what it knows:
// neverQuarantined is true only when it has an authoritative answer that the host's label is not quarantined (its own earned_labels for
// the local host; the origin's disagreement read for a peer, WWD-5c-2). Without that, a host that does not advertise the label has an
// unverified standing and condition (4) does not let it answer even itself, so a disputed result cannot come back through a missing
// piece of information. Deleting the entries on quarantine (store.DeleteCacheByLabel) is a cleanup, not what this rests on.
func HostNowFromInfo(info wire.Info, taskLabel string, isReference, neverQuarantined bool) HostNow {
	h := HostNow{Labels: slices.Clone(info.Labels), CurrentFingerprint: maps.Clone(info.Fingerprint)}
	switch {
	case isReference:
		h.Reference, h.ReferenceFingerprint = true, maps.Clone(info.Fingerprint)
	case slices.Contains(info.Labels, taskLabel):
		h.Earned, h.EarnedFingerprint = true, maps.Clone(info.Fingerprint)
	case !neverQuarantined:
		h.StandingUnverified = true
	}
	return h
}

// SameWork is condition (1): snapshot tree, task, args and definition hash are equal.
func SameWork(e Entry, q Query) bool {
	return e.Tree == q.Tree && e.Task == q.Task && e.DefHash == q.DefHash && slices.Equal(nonNil(e.Args), nonNil(q.Args))
}

// HostCoversLabels is condition (2): the labels of the host that produced the result include the job's labels, so a job that names
// os:linux never takes a darwin result.
func HostCoversLabels(h HostNow, jobLabels []string) bool {
	for _, l := range jobLabels {
		if !slices.Contains(h.Labels, l) {
			return false
		}
	}
	return true
}

// HostStillValid is condition (3): the host holds the earned label with the fingerprint the entry was made with, or it is the task's
// reference host with an unchanged fingerprint.
func HostStillValid(e Entry, h HostNow) bool {
	if h.Earned && sameFingerprint(h.EarnedFingerprint, e.Fingerprint) {
		return true
	}
	return h.Reference && sameFingerprint(h.ReferenceFingerprint, e.Fingerprint)
}

// SameHostOnly is condition (4): an entry whose host has no standing for the task (it holds no earned label and is not the
// reference) is answered only to a submission from that host. A host that does have standing but whose fingerprint no longer
// matches is not here: its entries are stale, not private, so they answer nobody. Nor is a quarantined host: its results are in
// dispute.
func SameHostOnly(e Entry, q Query, h HostNow) bool {
	return !h.Earned && !h.Reference && !h.Quarantined && !h.StandingUnverified && e.Host != "" && e.Host == q.Host &&
		sameFingerprint(h.CurrentFingerprint, e.Fingerprint)
}

// Answers says whether the entry answers the submission: (1) and (2) always, and then (3), or (4) when the host has no standing.
func Answers(e Entry, q Query, h HostNow) bool {
	if !SameWork(e, q) || !HostCoversLabels(h, q.Labels) {
		return false
	}
	return HostStillValid(e, h) || SameHostOnly(e, q, h)
}

// sameFingerprint is equality of two fingerprints; an empty fingerprint never matches (a host that reported nothing proves nothing).
func sameFingerprint(a, b map[string]string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// KeyForHost is the key a result made by the host described by h is filed under, for work with the given tree, task, args and definition
// hash: the key the writer files it under and the key a lookup asks for, so the two cannot differ. platform and arch are the host's
// (Info.Platform, Info.Arch). A host that holds the task's earned label is keyed by the fingerprint it earned it with; the reference
// host, which holds it without qualifying, has no qualification fingerprint.
func KeyForHost(tree, task string, args []string, defHash, platform, arch string, h HostNow) string {
	var qual map[string]string
	if h.Earned {
		qual = h.EarnedFingerprint
	}
	return Key(KeyParts{Tree: tree, Task: task, Args: args, DefHash: defHash, OS: platform, Arch: arch,
		Toolchain: h.CurrentFingerprint, Qualification: qual})
}
