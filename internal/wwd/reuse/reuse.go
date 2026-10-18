// Package reuse is the pure part of result reuse (spec §4.5): the key a stored result is filed under and the four conditions under
// which a stored result answers a new submission. It reads no store and no clock; the store (5d-1b) and the submit path (5d-2) call it.
package reuse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
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

// Key is the hex SHA-256 of the canonical encoding of the parts. Map keys are sorted by encoding/json, so the same parts give the
// same key whatever order the maps were built in.
func Key(p KeyParts) string {
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
	}{1, p.Tree, p.Task, nonNil(p.Args), p.DefHash, p.OS, p.Arch, nonNilMap(p.Toolchain), nonNilMap(p.Qualification)})
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
	// Quarantined: it holds the task's label in state quarantined (it had standing and lost it to a disagreement). Not Earned, and
	// not "no standing" either: its entries answer nobody until it is qualified again.
	Quarantined bool
	// Reference: it is the task's reference host and ReferenceFingerprint is its current fingerprint.
	Reference            bool
	ReferenceFingerprint map[string]string
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
	return !h.Earned && !h.Reference && !h.Quarantined && e.Host != "" && e.Host == q.Host
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
