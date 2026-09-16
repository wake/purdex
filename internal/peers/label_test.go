// internal/peers/label_test.go
package peers

import (
	"errors"
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
