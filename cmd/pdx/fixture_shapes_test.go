package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/peers"
)

// quotedLiteral finds the interpreted string literals in a Go source file.
// Raw (backquoted) literals are skipped deliberately: the ones in these tests
// hold JSON bodies whose addresses are already covered by the decoded
// fixtures, and matching across their newlines would only add false alarms.
var quotedLiteral = regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`)

// hostPart is the shape of an address's host half: a peer alias or a host id.
var hostPart = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// TestFixtures_HoldNoV3AddressShapes is a drift guard over this package's own
// test sources, and it exists because three separate batches of fixtures in
// this branch claimed to be v4 while holding v3 values.
//
// A fixture in the retired shape does not fail — it passes, and proves only
// that the renderer echoes whatever it was handed. Nothing about v4 is
// asserted by a test whose input could not occur under v4, so the assertion
// has to be made about the fixtures themselves.
//
// Two tells, both of them things v4 deleted:
//
//   - a ref longer than six base36 digits (v3's canonical was eight);
//   - an address whose session part carries a ':' — the retired
//     "<name>:<suffix>" form. "tmux:<name>" and the dead "cc:<name>" are the
//     two colon forms v4 keeps, so they are excluded by name.
func TestFixtures_HoldNoV3AddressShapes(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob *_test.go: %v (%d files)", err, len(files))
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			// A line that is ABOUT a retired form — the usage test asserting
			// the hint does not teach one — must be allowed to name it.
			if strings.Contains(line, "stale") || strings.Contains(line, "retired") {
				continue
			}
			for _, lit := range quotedLiteral.FindAllString(line, -1) {
				s := lit[1 : len(lit)-1]
				where := file + ":" + strconv.Itoa(i+1)

				if strings.HasPrefix(s, "_") && !peers.IsRef(s) && isBase36(s[1:]) {
					t.Errorf("%s: %q is a v3-length ref; a v4 ref is _ + 6 base36 digits", where, s)
				}
				host, session, ok := peers.SplitAddress(s)
				if !ok || !hostPart.MatchString(host) {
					continue
				}
				if !strings.Contains(session, ":") ||
					strings.HasPrefix(session, peers.LabelReservedTmux+":") ||
					strings.HasPrefix(session, peers.LabelReservedCC+":") {
					continue
				}
				t.Errorf("%s: %q carries the retired <name>:<suffix> form; v4 addresses are <host>/<name> or <host>/_<ref>", where, s)
			}
		}
	}
}

func isBase36(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}
