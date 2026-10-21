package labels

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func s(v string) *string { return &v }

func rec(tools wire.Tools) wire.Facts {
	return wire.Facts{Platform: "darwin", Arch: "arm64", OSVersion: "26.6.2", Tools: tools}
}

func TestFromFactsLabelsAndFingerprint(t *testing.T) {
	l, fp := FromFacts(rec(wire.Tools{
		Go: s("1.26.0"), Node: s("24.14.0"), Pnpm: s("10.23.0"), PHP: []string{"7.4.33", "8.4.21", "8.0.30"}, Xcode: s("16.2"),
	}))
	want := []string{"arch:arm64", "os:darwin", "tool:go1.26", "tool:node24", "tool:php74", "tool:php80", "tool:php84", "tool:pnpm10", "tool:xcode16"}
	if !reflect.DeepEqual(l, want) {
		t.Fatalf("labels %v", l)
	}
	wantFP := map[string]string{
		"os_version": "26.6.2", "go": "1.26.0", "node": "24.14.0", "pnpm": "10.23.0", "xcode": "16.2", "php": "7.4.33,8.0.30,8.4.21",
	}
	if !reflect.DeepEqual(fp, wantFP) {
		t.Fatalf("fingerprint %v", fp)
	}
}

// A tool that is missing, empty or unreadable gives no label and no error; the
// other labels stand.
func TestFromFactsSkipsWhatIsNotThere(t *testing.T) {
	l, fp := FromFacts(rec(wire.Tools{Go: s("not a version"), Node: s(""), PHP: []string{"weird", "8.4.21"}}))
	want := []string{"arch:arm64", "os:darwin", "tool:php84"}
	if !reflect.DeepEqual(l, want) {
		t.Fatalf("labels %v", l)
	}
	if _, ok := fp["go"]; ok {
		t.Errorf("an unreadable version is not in the fingerprint: %v", fp)
	}
	if fp["php"] != "8.4.21" || fp["os_version"] != "26.6.2" {
		t.Errorf("fingerprint %v", fp)
	}
	// no platform in the record (nothing sampled yet): nothing is invented
	l, fp = FromFacts(wire.Facts{})
	if len(l) != 0 || len(fp) != 0 {
		t.Fatalf("empty record: %v %v", l, fp)
	}
}

// A version whose needed parts do not read is refused whole: it must not turn
// into a plausible label (a missing minor is not minor 0).
func TestFromFactsRefusesMalformedVersions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools wire.Tools
	}{
		{"go without minor", wire.Tools{Go: s("1")}},
		{"go bad minor", wire.Tools{Go: s("1.bad")}},
		{"go negative", wire.Tools{Go: s("1.-2.0")}},
		{"go signed", wire.Tools{Go: s("+1.26")}},
		{"go overflow", wire.Tools{Go: s("1.99999999999999999999")}},
		{"php bad minor", wire.Tools{PHP: []string{"8.x"}}},
		{"php no minor", wire.Tools{PHP: []string{"8"}}},
		{"go trailing garbage", wire.Tools{Go: s("1.26bad")}},
		{"php trailing garbage", wire.Tools{PHP: []string{"8.4garbage"}}},
		{"go odd prerelease", wire.Tools{Go: s("1.26rcx")}},
		{"node negative", wire.Tools{Node: s("-24.1.0")}},
		{"node huge", wire.Tools{Node: s("99999999999999999999.0")}},
	} {
		l, fp := FromFacts(rec(tc.tools))
		for _, x := range l {
			if strings.HasPrefix(x, "tool:") {
				t.Errorf("%s: label %q", tc.name, x)
			}
		}
		for _, k := range []string{"go", "node", "php"} {
			if _, ok := fp[k]; ok {
				t.Errorf("%s: fingerprint %s=%q", tc.name, k, fp[k])
			}
		}
	}
	// Go's own pre-release spelling is a version
	if l, _ := FromFacts(rec(wire.Tools{Go: s("1.26rc1")})); !slices.Contains(l, "tool:go1.26") {
		t.Errorf("go1.26rc1: %v", l)
	}
	// a pre-release suffix on a good version is fine
	if l, _ := FromFacts(rec(wire.Tools{Node: s("24.14.0-rc1")})); !slices.Contains(l, "tool:node24") {
		t.Errorf("suffix after the numbers: %v", l)
	}
}

func TestTrackerReportsOnlyARealLabelChange(t *testing.T) {
	var tr Tracker
	a := rec(wire.Tools{Node: s("24.14.0")})
	if !tr.Update(a) {
		t.Fatal("the first labels are a change")
	}
	if tr.Update(a) {
		t.Fatal("the same record is no change")
	}
	// a patch bump moves the fingerprint, not the labels: no labels event
	b := rec(wire.Tools{Node: s("24.15.0")})
	if tr.Update(b) {
		t.Fatal("a patch version changes no label")
	}
	if _, fp := tr.Current(); fp["node"] != "24.15.0" {
		t.Fatalf("fingerprint follows: %v", fp)
	}
	if !tr.Update(rec(wire.Tools{Node: s("25.0.0")})) {
		t.Fatal("a new major is a label change")
	}
	if l, _ := tr.Current(); !reflect.DeepEqual(l, []string{"arch:arm64", "os:darwin", "tool:node25"}) {
		t.Fatalf("%v", l)
	}
}

// Current hands out copies: a caller cannot change what the tracker holds.
func TestTrackerCurrentIsACopy(t *testing.T) {
	var tr Tracker
	tr.Update(rec(wire.Tools{Node: s("24.14.0")}))
	l, fp := tr.Current()
	l[0], fp["node"] = "x", "x"
	l2, fp2 := tr.Current()
	if l2[0] == "x" || fp2["node"] == "x" {
		t.Fatal("Current leaked its state")
	}
}

func TestQualifyClass(t *testing.T) {
	for in, want := range map[wire.JobClass]wire.JobClass{
		wire.ClassRelease: wire.ClassMerge, wire.ClassMerge: wire.ClassMerge, wire.ClassDev: wire.ClassDev, "": wire.ClassDev, "bogus": wire.ClassDev,
	} {
		if got := QualifyClass(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// tool:claude has no version in the label (a task needs the tool, not a release of it); the fingerprint holds the major only, because Claude Code
// releases a patch nearly every day and a patch must not take back the labels earned under it (drift.Changed).
func TestFromFactsClaudeLabelHasNoVersion(t *testing.T) {
	l, fp := FromFacts(rec(wire.Tools{Claude: s("2.1.306")}))
	if want := []string{"arch:arm64", "os:darwin", "tool:claude"}; !reflect.DeepEqual(l, want) {
		t.Fatalf("labels %v", l)
	}
	if fp["claude"] != "2" {
		t.Errorf("fingerprint %v", fp)
	}
	l, fp = FromFacts(rec(wire.Tools{Claude: s("2.1.400")}))
	if fp["claude"] != "2" || len(l) != 3 {
		t.Errorf("a patch moved the fingerprint: %v %v", l, fp)
	}
	if l, _ := FromFacts(rec(wire.Tools{Claude: s("not a version")})); slices.Contains(l, "tool:claude") {
		t.Errorf("an unreadable version still gave a label: %v", l)
	}
}
