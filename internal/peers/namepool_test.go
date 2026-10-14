package peers

import (
	"regexp"
	"strings"
	"testing"
)

func TestNamePool_ThirtyShortLowercaseNamesThatNeverShareTheirFirstThreeLetters(t *testing.T) {
	if len(NamePool) != 30 {
		t.Fatalf("pool has %d names, want 30", len(NamePool))
	}
	shape := regexp.MustCompile(`^[a-z]{3,5}$`)
	seen := map[string]string{}
	for _, n := range NamePool {
		if !shape.MatchString(n) {
			t.Errorf("pool name %q is not 3-5 lowercase ASCII letters", n)
		}
		if other, dup := seen[n[:3]]; dup {
			t.Errorf("%q and %q share their first three letters", n, other)
		}
		seen[n[:3]] = n
	}
}

func TestNamePool_NoNameCanBeMistakenForARefOrRefSuffix(t *testing.T) {
	for _, n := range NamePool {
		// A ref suffix is exactly two characters; the pool's shortest name is
		// three, so PoolNameOf never mixes the two kinds.
		if len(n) <= 2 {
			t.Errorf("pool name %q is as short as a ref suffix", n)
		}
		if IsRef(n) || IsRef("_"+n) {
			t.Errorf("pool name %q is ref-shaped", n)
		}
	}
}

func TestVirtualNameWith_EveryPoolNameMakesARoutableNameEvenOnA61ByteBase(t *testing.T) {
	base := strings.Repeat("a", 61)
	for _, n := range NamePool {
		got, ok := VirtualNameWith(base, n)
		if !ok {
			t.Errorf("VirtualNameWith(61-byte base, %q) refused", n)
			continue
		}
		if len(got) > 64 || !RoutableName(got) {
			t.Errorf("VirtualNameWith(61-byte base, %q) = %q is not routable", n, got)
		}
		if !strings.HasSuffix(got, "-"+n) {
			t.Errorf("VirtualNameWith(61-byte base, %q) = %q lost its suffix", n, got)
		}
		// The cut is at 64 - 1 - len(suffix), not at the ref suffix's 64 - 3.
		if want := strings.Repeat("a", 64-1-len(n)) + "-" + n; got != want {
			t.Errorf("VirtualNameWith(61-byte base, %q) = %q, want %q", n, got, want)
		}
	}
}

func TestVirtualNameWith_ABaseCutOnADashDropsTheDash(t *testing.T) {
	// "freya" leaves 58 bytes of base; the dash at byte 57 is trimmed.
	base := strings.Repeat("b", 57) + "-" + strings.Repeat("c", 10)
	got, ok := VirtualNameWith(base, "freya")
	if !ok || got != strings.Repeat("b", 57)+"-freya" {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestVirtualNameWith_RefusesABadBaseOrAnEmptyOrOddSuffix(t *testing.T) {
	for _, c := range []struct{ base, suffix string }{
		{"", "adam"},
		{"-ab", "adam"},
		{"Purdex", "adam"},
		{"a b", "adam"},
		{"purdex", ""},
		{"purdex", "Adam"},
		{"purdex", "a-b"},
		{"purdex", "a/b"},
		{"purdex", strings.Repeat("z", 70)},
	} {
		if got, ok := VirtualNameWith(c.base, c.suffix); ok {
			t.Errorf("VirtualNameWith(%q, %q) = %q, want refused", c.base, c.suffix, got)
		}
	}
}

func TestVirtualName_StillCutsAt61ForTheRefSuffix(t *testing.T) {
	// Byte-for-byte the old result: the ref suffix keeps the 64 - 3 cut.
	got, ok := VirtualName(strings.Repeat("a", 100), "_k3m9qz")
	if !ok || got != strings.Repeat("a", 61)+"-k3" {
		t.Errorf("VirtualName = %q, %v", got, ok)
	}
}

func TestPoolNameOf_FindsThePoolSegmentAfterTheLastDash(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"purdex-3f-adam", "adam", true},
		{"mlab-freya", "freya", true},
		{"purdex-3f-vq", "", false},   // a ref suffix
		{"adam", "", false},           // no dash
		{"anna-tools-k3", "", false},  // a base that merely contains a pool word
		{"purdex-adam-k3", "", false}, // the pool word is not the last segment
		{"purdex-adamx", "", false},   // not a whole segment
		{"purdex-ADAM", "", false},    // case matters
		{"purdex-", "", false},
		{"", "", false},
	} {
		got, ok := PoolNameOf(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("PoolNameOf(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
