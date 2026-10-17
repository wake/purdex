package queue

import (
	"crypto/rand"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newJobID is "j_" and a ULID: 48 bits of the origin's clock in milliseconds first, then 80 random bits, in Crockford base32 (26
// characters). Ids sort by submission time and are unique without asking the database.
func newJobID(now time.Time) string {
	var rnd [10]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		panic("queue: crypto/rand failed: " + err.Error())
	}
	var out [26]byte
	t := uint64(now.UnixMilli())
	for i := 9; i >= 0; i-- {
		out[i] = crockford[t&31]
		t >>= 5
	}
	// the 80 random bits, five at a time
	var acc uint32
	var bits uint
	pos := 10
	for _, b := range rnd {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out[pos] = crockford[(acc>>bits)&31]
			pos++
		}
	}
	return "j_" + string(out[:])
}
