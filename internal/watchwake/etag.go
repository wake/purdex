package watchwake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Marshal is the canonical JSON of a document: the fields in the order of the types, the lists as [] and never null. It is what the store
// keeps and what Etag hashes, so the same document is the same bytes whatever order a client sent its keys in.
func Marshal(s Settings) []byte {
	s.ensureLists()
	b, err := json.Marshal(s)
	if err != nil { // plain strings, numbers and bools: cannot fail
		panic("watchwake: " + err.Error())
	}
	return b
}

// Etag is the first 16 hex digits of the sha256 of the canonical JSON. It is of the normalized document (not of what was sent) and is not stored.
func Etag(s Settings) string {
	sum := sha256.Sum256(Marshal(s))
	return hex.EncodeToString(sum[:8])
}
