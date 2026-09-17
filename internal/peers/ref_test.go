// internal/peers/ref_test.go
package peers

import (
	"errors"
	"regexp"
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

// ValidSuffix outlives Suffix/Sanitize: v4 mints no suffix, but wire.go still
// validates one a v2 or v3 sender puts on the wire (spec §5.6).
func TestValidSuffix(t *testing.T) {
	if long := strings.Repeat("a", 40) + "-" + strings.Repeat("b", 24); !ValidSuffix(long) {
		t.Errorf("65-char bound: ValidSuffix(%q) = false", long)
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

// TestRefID_DeterministicAndCollisionFree pins the property the whole address
// design rests on (spec §5.1): the ref is a pure function of the sessionId, so
// a resume or a daemon restart lands on the same one, and two different
// conversations do not land on one.
func TestRefID_DeterministicAndCollisionFree(t *testing.T) {
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
		got := RefID(sid)
		for range 8 {
			if again := RefID(sid); again != got {
				t.Fatalf("RefID(%q) not deterministic: %q then %q", sid, got, again)
			}
		}
		if other, dup := seen[got]; dup {
			t.Errorf("RefID collision: %q and %q both give %q", other, sid, got)
		}
		seen[got] = sid
	}
}

// TestRefID_Form checks every output lands in the ref
// namespace, including the inputs most likely to break an encoder: the
// empty string, a very long one, and bytes that are not valid UTF-8.
func TestRefID_Form(t *testing.T) {
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
		got := RefID(in)
		if len(got) != canonicalN+1 {
			t.Errorf("RefID(%q) = %q: len %d, want %d", in, got, len(got), canonicalN+1)
		}
		if !IsRef(got) {
			t.Errorf("RefID(%q) = %q, want ^_[0-9a-z]{6}$", in, got)
		}
	}

	// The zero-padding path: a value below 36^5 renders with a leading
	// '0', which only a fixed-width encoder produces.
	padded := false
	for i := range 200 {
		if RefID("pad-probe-" + strconv.Itoa(i))[1] == '0' {
			padded = true
			break
		}
	}
	if !padded {
		t.Error("no zero-padded vector in 200 probes — padding path not exercised")
	}
}

func TestIsRef(t *testing.T) {
	ok := []string{"_000000", "_3k9f2m", "_zzzzzz", "_0a1b2c"}
	for _, s := range ok {
		if !IsRef(s) {
			t.Errorf("IsRef(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",              // empty
		"_",             // bare underscore
		"_k3x9q",        // 5 digits
		"_k3x9qz1",      // 7 digits: one too many, must not pass
		"_k3x9qz123",    // 9 digits
		"3k9f2m",        // no leading underscore
		"purdex-tester", // a user label
		"_3K9F2M",       // uppercase
		"_3k9f2-",       // hyphen
		"_3k9f_m",       // underscore inside
		"__3k9f2",       // second underscore
		"_3k9f2m:x",     // a whole address, not a bare id
		" _3k9f2m",      // leading space
		"_3k9f2m\n",     // trailing newline
	}
	for _, s := range bad {
		if IsRef(s) {
			t.Errorf("IsRef(%q) = true, want false", s)
		}
	}
}

// TestRefID_DisjointFromUserLabels is the namespace guarantee of
// spec §5.1, asserted in both directions: no ref can ever be claimed as a
// label, and no claimable label can ever be read as a ref.
func TestRefID_DisjointFromUserLabels(t *testing.T) {
	for i := range 500 {
		id := RefID("disjointness-probe-" + strconv.Itoa(i))
		if err := ValidateUserLabel(id); err == nil {
			t.Fatalf("ValidateUserLabel(%q) accepted a ref", id)
		}
	}
	for _, id := range []string{"_000000", "_zzzzzz", "_3k9f2m"} {
		if err := ValidateUserLabel(id); err == nil {
			t.Errorf("ValidateUserLabel(%q) accepted a ref", id)
		}
	}

	labels := []string{
		"ab", "a1", "0abc", "purdex-tester", "purdex-tester-2",
		"cc", "tmux", // reserved words: still not addresses
		strings.Repeat("a", 32), "abcdefgh", "12345678",
	}
	for _, s := range labels {
		if IsRef(s) {
			t.Errorf("IsRef(%q) = true for a label-shaped string", s)
		}
	}
}

func TestRefID_Shape(t *testing.T) {
	for _, sid := range []string{"a57f3d89-5850-4812-84f5-d24d6c561902", "", "x"} {
		got := RefID(sid)
		if !regexp.MustCompile(`^_[0-9a-z]{6}$`).MatchString(got) {
			t.Errorf("RefID(%q) = %q, want ^_[0-9a-z]{6}$", sid, got)
		}
		if !IsRef(got) {
			t.Errorf("IsRef(%q) = false, want true", got)
		}
	}
}

func TestRefID_Deterministic(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	if a, b := RefID(sid), RefID(sid); a != b {
		t.Errorf("RefID not deterministic: %q != %q", a, b)
	}
}

// TestRefID_PinnedVector fails loudly if a refactor changes every address on
// every host at once. The expectation is a LITERAL on purpose: a vector that
// recomputes its own expectation asserts nothing.
func TestRefID_PinnedVector(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	const want = "_4psn4f"
	if got := RefID(sid); got != want {
		t.Errorf("RefID(%q) = %q, want %q", sid, got, want)
	}
}

func TestIsRef_Rejects(t *testing.T) {
	for _, s := range []string{"", "_", "_q34psn4f", "q34psn", "_Q34PSN", "_q34ps", "purdex-b0"} {
		if IsRef(s) {
			t.Errorf("IsRef(%q) = true, want false", s)
		}
	}
}

func TestRoutableName_AcceptsObservedCorpus(t *testing.T) {
	// Every registry name on mini-lab, 2026-09-17.
	for _, s := range []string{
		"purdex-b0", "purdex-53", "purdex-03", "nexen-f2", "nexen-ec",
		"ai-chat-story-3a", "at-inwin-plugin-2e", "invoice-plane-89",
		"firefly-be", "csp-plugin-5e", "mlab-c8", "air19-e2", "istdc-a5", "barbox-a6",
	} {
		if !RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

// Each of these would make a name address unparseable or ambiguous.
func TestRoutableName_RejectsAddressSyntax(t *testing.T) {
	for _, s := range []string{
		"", "a", "-lead", "has/slash", "has:colon", "has space", "has[bracket]",
		"_leading-underscore", "UPPER", "tráiler", strings.Repeat("a", 65),
	} {
		if RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false", s)
		}
	}
}

// The whole point: a six-digit name would shadow the bare-ref input form for
// anyone copying bracket text.
func TestRoutableName_RejectsRefShaped(t *testing.T) {
	for _, s := range []string{"q34psn", "abc123", "000000", "zzzzzz"} {
		if RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false (ref-shaped)", s)
		}
	}
	for _, s := range []string{"abc12", "abc1234"} {
		if !RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

func TestRoutableName_BoundaryLengths(t *testing.T) {
	if !RoutableName("ab") {
		t.Error("2 chars rejected")
	}
	if !RoutableName(strings.Repeat("a", 64)) {
		t.Error("64 chars rejected")
	}
}

func TestValidateTitle_Accepts(t *testing.T) {
	for _, s := range []string{
		"Purdex Tester 01", "purdex-tester", "測試 01", "cc", "tmux", strings.Repeat("a", 64),
	} {
		if err := ValidateTitle(s); err != nil {
			t.Errorf("ValidateTitle(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateTitle_Rejects(t *testing.T) {
	// NBSP and U+3000 are listed for the same reason as \t: strings.Fields
	// collapses them, so NormalizeTitle would read them as a space that
	// ValidateTitle never let through. Keeping them refused is what makes the
	// two functions agree on what "whitespace" means.
	for _, s := range []string{
		"", strings.Repeat("a", 65), "has\ttab", "has\nnewline", "esc\x1b[31m",
		"nbsp here", "ideographic　space", "\x00nul",
	} {
		err := ValidateTitle(s)
		if err == nil {
			t.Errorf("ValidateTitle(%q) = nil, want an error", s)
			continue
		}
		if !errors.Is(err, ErrTitleInvalid) {
			t.Errorf("ValidateTitle(%q) = %v, want it to wrap ErrTitleInvalid", s, err)
		}
	}
}

// The limit is BYTES, not runes: storage and the wire both measure bytes.
func TestValidateTitle_ByteBoundary(t *testing.T) {
	if err := ValidateTitle(strings.Repeat("測", 21)); err != nil { // 63 bytes
		t.Errorf("63 bytes rejected: %v", err)
	}
	if err := ValidateTitle(strings.Repeat("測", 22)); err == nil { // 66 bytes
		t.Error("66 bytes accepted, want rejected")
	}
}

func TestNormalizeTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Purdex Tester", "purdex tester"},
		{"purdex  tester", "purdex tester"},
		{"  Purdex\tTester  ", "purdex tester"},
	} {
		if got := NormalizeTitle(tc.in); got != tc.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
