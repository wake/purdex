// internal/peers/label_test.go
package peers

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidateUserLabel(t *testing.T) {
	ok := []string{"ab", "a1", "purdex-tester", "purdex-tester-2", "0abc", strings.Repeat("a", 32)}
	for _, s := range ok {
		if err := ValidateUserLabel(s); err != nil {
			t.Errorf("%q: unexpected error %v", s, err)
		}
	}
	bad := []string{"", "a", "-ab", "Ab", "a b", "a_b", "中文", "a:b", "a/b", strings.Repeat("a", 33), "_k3x9qz"}
	for _, s := range bad {
		if err := ValidateUserLabel(s); !errors.Is(err, ErrLabelInvalid) {
			t.Errorf("%q: got %v, want ErrLabelInvalid", s, err)
		}
	}
	for _, s := range []string{"cc", "tmux"} {
		if err := ValidateUserLabel(s); !errors.Is(err, ErrLabelReserved) {
			t.Errorf("%q: got %v, want ErrLabelReserved", s, err)
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"":                      "_",
		"mt0":                   "mt0",
		"purdex-49":             "purdex-49",
		"a b":                   "a_b",
		"側欄":                    "______", // 2 runes × 3 bytes, byte-wise
		"a:b/c":                 "a_b_c",
		strings.Repeat("z", 40): strings.Repeat("z", 32),
		"A.B_C-D":               "A.B_C-D",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuffix(t *testing.T) {
	if got := Suffix("mt0", "purdex-49"); got != "mt0-purdex-49" {
		t.Errorf("got %q", got)
	}
	if got := Suffix("", "purdex-49"); got != "purdex-49" {
		t.Errorf("outside tmux: got %q", got)
	}
	long := Suffix(strings.Repeat("a", 40), strings.Repeat("b", 40))
	if len(long) != 65 || !ValidSuffix(long) {
		t.Errorf("65-char bound: len %d valid %v", len(long), ValidSuffix(long))
	}
	if ValidSuffix("") || ValidSuffix(strings.Repeat("a", 66)) || ValidSuffix("a:b") {
		t.Error("ValidSuffix accepted an invalid value")
	}
}

func TestSplitSession(t *testing.T) {
	cases := []struct{ in, head, rest string }{
		{"purdex-tester", "purdex-tester", ""},
		{"purdex-tester:purdex-3f", "purdex-tester", "purdex-3f"},
		{"tmux:mt0", "tmux", "mt0"},
		{"a:b:c", "a", "b:c"},
		{":x", "", "x"},
	}
	for _, c := range cases {
		h, r := SplitSession(c.in)
		if h != c.head || r != c.rest {
			t.Errorf("SplitSession(%q) = %q,%q want %q,%q", c.in, h, r, c.head, c.rest)
		}
	}
}

// TestCanonicalID_Deterministic pins the property the whole v3 address
// design rests on (spec §3.1): the id is a pure function of the
// sessionId, so a resume or a daemon restart lands on the same address,
// and two different conversations do not land on one.
func TestCanonicalID_Deterministic(t *testing.T) {
	sids := []string{
		"",
		"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c",
		"96c7a06c-4006-4a12-b163-de7fc00e1af0",
		"pad-39",
		strings.Repeat("s", 4096),
		string([]byte{0xff, 0xfe, 0x80, 0x00, 'a'}),
	}

	seen := make(map[string]string, len(sids))
	for _, sid := range sids {
		got := CanonicalID(sid)
		for range 8 {
			if again := CanonicalID(sid); again != got {
				t.Fatalf("CanonicalID(%q) not deterministic: %q then %q", sid, got, again)
			}
		}
		if other, dup := seen[got]; dup {
			t.Errorf("CanonicalID collision: %q and %q both give %q", other, sid, got)
		}
		seen[got] = sid
	}
}

// TestCanonicalID_Form checks every output lands in the canonical
// namespace, including the inputs most likely to break an encoder: the
// empty string, a very long one, and bytes that are not valid UTF-8.
func TestCanonicalID_Form(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c",
		strings.Repeat("長", 2000),
		strings.Repeat("x", 100000),
		string([]byte{0x00}),
		string([]byte{0xc3, 0x28, 0xff, 0xfe}),
	}
	for _, in := range inputs {
		got := CanonicalID(in)
		if len(got) != canonicalN+1 {
			t.Errorf("CanonicalID(%q) = %q: len %d, want %d", in, got, len(got), canonicalN+1)
		}
		if !IsCanonicalID(got) {
			t.Errorf("CanonicalID(%q) = %q, want ^_[0-9a-z]{8}$", in, got)
		}
	}

	// The zero-padding path: a value below 36^7 renders with a leading
	// '0', which only a fixed-width encoder produces.
	padded := false
	for i := range 200 {
		if CanonicalID("pad-probe-" + strconv.Itoa(i))[1] == '0' {
			padded = true
			break
		}
	}
	if !padded {
		t.Error("no zero-padded vector in 200 probes — padding path not exercised")
	}
}

func TestIsCanonicalID(t *testing.T) {
	ok := []string{"_00000000", "_3k9f2mq4", "_zzzzzzzz", "_0a1b2c3d"}
	for _, s := range ok {
		if !IsCanonicalID(s) {
			t.Errorf("IsCanonicalID(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",              // empty
		"_",             // bare underscore
		"_k3x9qz",       // 6 digits: the v2 default label
		"_k3x9qz1",      // 7 digits: neither v2 nor v3, must not pass
		"_k3x9qz123",    // 9 digits
		"3k9f2mq4",      // no leading underscore
		"purdex-tester", // a user label
		"_3K9F2MQ4",     // uppercase
		"_3k9f2mq-",     // hyphen
		"_3k9f2m_4",     // underscore inside
		"__3k9f2mq",     // second underscore
		"_3k9f2mq4:x",   // a whole address, not a bare id
		" _3k9f2mq4",    // leading space
		"_3k9f2mq4\n",   // trailing newline
	}
	for _, s := range bad {
		if IsCanonicalID(s) {
			t.Errorf("IsCanonicalID(%q) = true, want false", s)
		}
	}
}

// TestCanonicalID_DisjointFromUserLabels is the namespace guarantee of
// spec §4.1, asserted in both directions: no canonical id can ever be
// claimed as a label, and no claimable label can ever be read as an
// address.
func TestCanonicalID_DisjointFromUserLabels(t *testing.T) {
	for i := range 500 {
		id := CanonicalID("disjointness-probe-" + strconv.Itoa(i))
		if err := ValidateUserLabel(id); err == nil {
			t.Fatalf("ValidateUserLabel(%q) accepted a canonical id", id)
		}
	}
	for _, id := range []string{"_00000000", "_zzzzzzzz", "_3k9f2mq4"} {
		if err := ValidateUserLabel(id); err == nil {
			t.Errorf("ValidateUserLabel(%q) accepted a canonical id", id)
		}
	}

	labels := []string{
		"ab", "a1", "0abc", "purdex-tester", "purdex-tester-2",
		"cc", "tmux", // reserved words: still not addresses
		strings.Repeat("a", 32), "abcdefgh", "12345678",
	}
	for _, s := range labels {
		if IsCanonicalID(s) {
			t.Errorf("IsCanonicalID(%q) = true for a label-shaped string", s)
		}
	}
}
