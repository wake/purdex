// Package ident is how a worker names a job: by the connection it came in on and its id.
//
// A job id is only unique within the origin that made it (queue/id.go: 80 random bits per origin), and a worker takes jobs from its own
// queue and from every peer. So the identity of a job on a worker is (Source, ID), where Source is the name of the connection in the
// worker's own settings (loop.Origin.Host, pick.Candidate.Source): the local alias, or the alias of a [[peers]] entry. It is never the
// Origin field of the job's JSON, which the peer fills in itself.
//
// Pure functions, no I/O.
package ident

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Ref is a job as a worker names it.
type Ref struct {
	Source string
	ID     string
}

// Key is "source/id": for people, logs, lock owners and the quarantine's owner, which already have this form. An alias may hold a slash
// and an id never does, so ParseKey splits at the last one.
func (r Ref) Key() string { return r.Source + "/" + r.ID }

// Tag is OriginTag of the source.
func (r Ref) Tag() string { return OriginTag(r.Source) }

// ParseKey is the Ref of a Key. It refuses a key with an empty source or an empty id.
func ParseKey(key string) (Ref, bool) {
	i := strings.LastIndexByte(key, '/')
	if i <= 0 || i == len(key)-1 {
		return Ref{}, false
	}
	return Ref{Source: key[:i], ID: key[i+1:]}, true
}

// OriginTag is the first 16 hex characters of the sha256 of a source: for the places that take only a narrow character set (an
// environment variable's value, a systemd unit name), where an alias (which may hold a slash, spaces, anything printable) cannot go.
// 64 bits is enough against a peer that tries to choose a tag: it would have to invert the hash.
func OriginTag(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:16]
}
