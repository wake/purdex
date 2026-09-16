// internal/peers/label_test.go
package peers

import (
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
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

// Golden vectors: FIXED outputs computed once from the definition (FNV-1a
// 64 over the UTF-8 bytes, mod 36^6, base36 0-9a-z, 6 digits, left-padded
// with '0') with an independent implementation, and frozen here. Any
// change to the derivation is a wire change and must update these on
// purpose. "pad-39" is a vector whose value is < 36^5, so its rendering
// starts with '0' — that is the padding path.
func TestDefaultLabel_Golden(t *testing.T) {
	cases := map[string]string{
		"":                                     "_j4ux45",
		"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": "_you08b",
		"96c7a06c-4006-4a12-b163-de7fc00e1af0": "_v0h7yo",
		"pad-39":                               "_0hkg69",
	}
	for in, want := range cases {
		got := DefaultLabel(in)
		if got != want {
			t.Errorf("DefaultLabel(%q) = %q, want %q", in, got, want)
		}
		if len(got) != 7 || got[0] != '_' || !IsDefaultLabel(got) {
			t.Errorf("DefaultLabel(%q) = %q: not 7 chars / not default form", in, got)
		}
	}
	if DefaultLabel("pad-39")[1] != '0' {
		t.Error("padding vector does not start with '0' — padding path not exercised")
	}
}

// liveEntry is one live cc registry entry in tmux session tmuxName ("" ⇒
// outside tmux); only the fields ResolveDefaultLabels reads are set.
func liveEntry(pid int, sid, tmuxName string) Entry {
	e := Entry{PID: pid, SessionID: sid}
	if tmuxName != "" {
		e.Tmux = tmuxName + ":@0.%" + string(rune('0'+pid%10))
	}
	return e
}

func TestResolveDefaultLabels(t *testing.T) {
	cases := []struct {
		name      string
		entries   []Entry
		proxyPIDs map[int]bool
		labels    map[string]LabelInfo
		want      DefaultLabels
	}{
		{
			name:    "one conversation in one tmux session",
			entries: []Entry{liveEntry(1, "A", "purdex1")},
			want:    DefaultLabels{"A": "purdex1"},
		},
		{
			name:    "one conversation, two processes in the same tmux session",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "A", "purdex1")},
			want:    DefaultLabels{"A": "purdex1"},
		},
		{
			name:    "one conversation spanning two tmux sessions has no place",
			entries: []Entry{liveEntry(1, "A", "a1"), liveEntry(2, "A", "a2")},
			want:    DefaultLabels{},
		},
		{
			// Rule 1 is about the tmux sessions, not about which of them
			// happens to qualify: one live entry in a place that could
			// have been a candidate is still not a single place.
			name:    "one conversation spanning a qualifying and a non-qualifying tmux session",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "A", "my_proj.2")},
			want:    DefaultLabels{},
		},
		{
			name:    "two unnamed conversations in one tmux session",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "purdex1")},
			want:    DefaultLabels{},
		},
		{
			// Round-2 file-health finding (spec §11.3, §3.3 rule 2): B's
			// processes span two tmux sessions, so B has no candidate of
			// its own — but B still OCCUPIES purdex1, and a place holding
			// two live conversations names neither of them. Counting only
			// surviving candidates as competitors handed purdex1 to A.
			name:    "a conversation with no candidate still occupies its place",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "purdex1"), liveEntry(3, "B", "bb2")},
			want:    DefaultLabels{},
		},
		{
			// Occupancy is per entry, so a candidate-less occupant blocks
			// every place it has a process in, not just the first.
			name: "a candidate-less occupant blocks every place it is in",
			entries: []Entry{
				liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "purdex1"),
				liveEntry(3, "B", "bb2"), liveEntry(4, "C", "bb2"),
			},
			want: DefaultLabels{},
		},
		{
			name:    "a user-labelled conversation still competes for the place",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "purdex1")},
			labels:  map[string]LabelInfo{"B": {Label: "foo", Rev: 1}},
			want:    DefaultLabels{},
		},
		{
			name:    "another live session holds the candidate as a user label",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "bb2")},
			labels:  map[string]LabelInfo{"B": {Label: "purdex1", Rev: 1}},
			want:    DefaultLabels{"B": "bb2"},
		},
		{
			name:    "a dead session's user label is inert",
			entries: []Entry{liveEntry(1, "A", "purdex1")},
			labels:  map[string]LabelInfo{"dead": {Label: "purdex1", Rev: 1}},
			want:    DefaultLabels{"A": "purdex1"},
		},
		{
			name:    "a session's own user label does not block its own candidate",
			entries: []Entry{liveEntry(1, "A", "purdex1")},
			labels:  map[string]LabelInfo{"A": {Label: "purdex1", Rev: 1}},
			want:    DefaultLabels{"A": "purdex1"},
		},
		{
			name:    "outside tmux",
			entries: []Entry{liveEntry(1, "A", "")},
			want:    DefaultLabels{},
		},
		{
			name:    "a proxy entry is not in the population (IsProxy)",
			entries: []Entry{proxyOf(liveEntry(1, "P", "purdex1")), liveEntry(2, "A", "purdex1")},
			want:    DefaultLabels{"A": "purdex1"},
		},
		{
			name:      "a proxy entry is not in the population (proxyPIDs)",
			entries:   []Entry{liveEntry(1, "P", "purdex1"), liveEntry(2, "A", "purdex1")},
			proxyPIDs: map[int]bool{1: true},
			want:      DefaultLabels{"A": "purdex1"},
		},
		{
			// Spec §3.1 at the resolver level: the agent in "foo.bar" must
			// not take "foo-bar", which is the real name of the OTHER
			// session here — and would be the name of a real session the
			// resolver cannot even see when that session has no agent.
			name:    "a non-qualifying name never becomes another session's name",
			entries: []Entry{liveEntry(1, "A", "foo.bar"), liveEntry(2, "B", "foo-bar")},
			want:    DefaultLabels{"B": "foo-bar"},
		},
		{
			name:    "a released label row is not a competitor named \"\"",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "bb2")},
			labels:  map[string]LabelInfo{"A": {Label: "", Rev: 4}, "B": {Label: "", Rev: 2}},
			want:    DefaultLabels{"A": "purdex1", "B": "bb2"},
		},
		{
			name:    "the proxy filter runs before the distinct-name check",
			entries: []Entry{proxyOf(liveEntry(1, "A", "helper")), liveEntry(2, "A", "purdex1")},
			want:    DefaultLabels{"A": "purdex1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveDefaultLabels(c.entries, c.proxyPIDs, c.labels)
			if !maps.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func proxyOf(e Entry) Entry {
	e.IsProxy = true
	return e
}

// The resolver is a pure function of the population, so the registry's
// iteration order must not reach the result.
func TestResolveDefaultLabels_OrderIndependent(t *testing.T) {
	entries := []Entry{
		liveEntry(1, "A", "purdex1"),
		liveEntry(2, "A", "purdex1"),
		liveEntry(3, "B", "bb2"),
		liveEntry(4, "C", "bb2"),
		liveEntry(5, "D", "ai-chat4"),
		liveEntry(6, "E", ""),
		proxyOf(liveEntry(7, "F", "purdex1")),
		liveEntry(8, "G", "my_proj.2"), // does not qualify
		liveEntry(9, "H", "foo-bar"),
	}
	labels := map[string]LabelInfo{"D": {Label: "lead", Rev: 2}, "dead": {Label: "ai-chat4"}}
	want := ResolveDefaultLabels(entries, nil, labels)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20; i++ {
		shuffled := slices.Clone(entries)
		rng.Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		if got := ResolveDefaultLabels(shuffled, nil, labels); !maps.Equal(got, want) {
			t.Fatalf("shuffle %d: got %v, want %v", i, got, want)
		}
	}
}

func TestDefaultLabels_For(t *testing.T) {
	var nilMap DefaultLabels
	if got := nilMap.For("sid-1"); got != DefaultLabel("sid-1") {
		t.Errorf("nil map: got %q, want the hash", got)
	}
	d := DefaultLabels{"A": "purdex1", "B": ""}
	if got := d.For("A"); got != "purdex1" {
		t.Errorf("resolved: got %q", got)
	}
	if got := d.For("B"); got != DefaultLabel("B") {
		t.Errorf("empty value: got %q, want the hash", got)
	}
	if got := d.For("C"); got != DefaultLabel("C") {
		t.Errorf("absent sid: got %q, want the hash", got)
	}
}

// TestResolveDefaultLabels_Qualification pins spec §3.1 and the §6.1
// boundary list: a tmux session name becomes a default label ONLY when it
// already is a valid user label, and it is then the candidate verbatim.
// There is no folding, no substitution and no truncation, because a
// transformed name is a different string from the session it came from —
// and that other string may be the real name of another tmux session.
func TestResolveDefaultLabels_Qualification(t *testing.T) {
	qualifying := []string{"purdex1", "ab", "a1", "0abc", "purdex-tester-2", "bb2", strings.Repeat("a", 32)}
	for _, name := range qualifying {
		got := ResolveDefaultLabels([]Entry{liveEntry(1, "A", name)}, nil, nil)
		// The candidate is the tmux name itself, byte for byte: an
		// accepted name is never returned transformed.
		if !maps.Equal(got, DefaultLabels{"A": name}) {
			t.Errorf("tmux %q: got %v, want the name unchanged as A's default", name, got)
		}
	}
	rejected := []string{
		"AI-Chat4",              // uppercase
		"my_proj.2",             // '_' and '.'
		"foo.bar",               // would have sanitized into another session's name
		"a",                     // one byte
		"專案",                    // non-ASCII
		"",                      // outside tmux
		"cc",                    // reserved
		"tmux",                  // reserved
		strings.Repeat("a", 33), // 33 bytes
		"-lead",                 // leading '-'
	}
	for _, name := range rejected {
		got := ResolveDefaultLabels([]Entry{liveEntry(1, "A", name)}, nil, nil)
		if len(got) != 0 {
			t.Errorf("tmux %q: got %v, want no default at all (the session keeps its v2 hash)", name, got)
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
