// internal/peers/label_test.go
package peers

import (
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
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

func TestSanitizeLabel(t *testing.T) {
	cases := []struct {
		in    string
		label string
		ok    bool
	}{
		{"purdex1", "purdex1", true},
		{"AI-Chat4", "ai-chat4", true},
		{"my_proj.2", "my-proj-2", true},
		// Lossy on purpose: this input and the one above collapse to the
		// same label. Spec §3.3 rule 2 makes both sessions fall back.
		{"my proj 2", "my-proj-2", true},
		{"--lead--", "lead", true},
		{"a", "", false},
		{"", "", false},
		{"專案", "", false},
		{"cc", "", false},
		{"tmux", "", false},
		{"CC", "", false},
		{strings.Repeat("a", 33), strings.Repeat("a", 32), true},
		// The 32-byte cut lands on a '-', which the second trim removes.
		{strings.Repeat("a-", 20), strings.Repeat("a-", 15) + "a", true},
		{strings.Repeat("a", 32) + "-x", strings.Repeat("a", 32), true},
	}
	for _, c := range cases {
		label, ok := SanitizeLabel(c.in)
		if label != c.label || ok != c.ok {
			t.Errorf("SanitizeLabel(%q) = %q,%v want %q,%v", c.in, label, ok, c.label, c.ok)
		}
	}
}

// The user label regexp validates every accepted output, so the shape is
// proven rather than the construction trusted (spec §3.1).
func TestSanitizeLabel_AcceptedOutputIsAValidUserLabel(t *testing.T) {
	corpus := []string{
		"purdex1", "AI-Chat4", "my_proj.2", "my proj 2", "--lead--",
		"a", "", "專案", "cc", "tmux", "CC", "Tmux", "c-c",
		strings.Repeat("a", 33), strings.Repeat("a-", 20),
		strings.Repeat("a", 32) + "-x", strings.Repeat("ab", 100),
		"-", "--", "---", "0", "0a", "-9",
		"a\x00b", "a\tb", "a\nb", "\x7f", "\x01\x02",
		"a:b", "a/b", "a.b", "a_b", "a b",
		"🎉", "pro🎉ject", "側欄", "session#3", "SESSION",
	}
	for _, in := range corpus {
		label, ok := SanitizeLabel(in)
		if !ok {
			if label != "" {
				t.Errorf("SanitizeLabel(%q) rejected but returned %q", in, label)
			}
			continue
		}
		if err := ValidateUserLabel(label); err != nil {
			t.Errorf("SanitizeLabel(%q) = %q: %v", in, label, err)
		}
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
			name:    "two unnamed conversations in one tmux session",
			entries: []Entry{liveEntry(1, "A", "purdex1"), liveEntry(2, "B", "purdex1")},
			want:    DefaultLabels{},
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
			name:    "two tmux names that sanitize to the same candidate",
			entries: []Entry{liveEntry(1, "A", "my_proj.2"), liveEntry(2, "B", "my proj 2")},
			want:    DefaultLabels{},
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
		liveEntry(8, "G", "my_proj.2"),
		liveEntry(9, "H", "my proj 2"),
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
