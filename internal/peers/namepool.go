package peers

import "strings"

// NamePool is the fixed list of short human names a new conversation's
// virtual name ends with, "<base>-<name>" (name pool spec §3): common employee
// names from many countries, one male and one female per region.
//
// Lowercase ASCII, 3-5 letters, no two sharing their first three letters. A
// ref suffix is exactly two characters, so a pool name and a ref suffix can
// never be mistaken for one another (PoolNameOf relies on that). The order
// does not matter.
var NamePool = [...]string{
	"adam", "emma", "kenji", "yuki", "wei", "mei", "joon", "hana", "raj", "priya",
	"diego", "lucia", "louis", "chloe", "jonas", "anna", "marco", "elena", "omar", "layla",
	"erik", "freya", "ivan", "olga", "kofi", "amara", "tiago", "julia", "budi", "linh",
}

var poolNames = func() map[string]bool {
	m := make(map[string]bool, len(NamePool))
	for _, n := range NamePool {
		m[n] = true
	}
	return m
}()

// PoolNameOf reports the pool name a virtual name ends with: the segment
// after its last '-', when that segment is a whole pool name. A base that
// merely contains a pool word ("anna-tools-k3") and a ref suffix
// ("purdex-3f-vq") are not pool names.
func PoolNameOf(virtualName string) (string, bool) {
	i := strings.LastIndexByte(virtualName, '-')
	if i < 0 {
		return "", false
	}
	if n := virtualName[i+1:]; poolNames[n] {
		return n, true
	}
	return "", false
}
