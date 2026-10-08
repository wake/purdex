package ccnorm_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
)

// -update rewrites expected.json of every case directory on disk and rebuilds
// MANIFEST.json (see testdata/conversation/v1/README.md, "Adding a case").
// facts.json is never touched: it is the hand-written oracle.
var update = flag.Bool("update", false, "rewrite expected.json files and MANIFEST.json under testdata/conversation/v1")

var fixtureRoot = filepath.Join("..", "..", "..", "testdata", "conversation", "v1")

const casesDir = "cc-transcript"

type manifest struct {
	Version int            `json:"version"`
	Cases   []manifestCase `json:"cases"`
}

type manifestCase struct {
	Name        string          `json:"name"`
	Source      string          `json:"source"`
	CCVersion   string          `json:"cc_version"`
	Description string          `json:"description"`
	Input       string          `json:"input"`
	Expected    string          `json:"expected"`
	Facts       string          `json:"facts"`
	SHA256      hashes          `json:"sha256"`
	Children    []manifestChild `json:"children,omitempty"`
}

type hashes struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Facts    string `json:"facts"`
}

// manifestChild is a subagent file of a case: children/<agent id>.input.jsonl
// normalized with NormalizeSubagent. Apps ignore it (daemon test data).
type manifestChild struct {
	AgentID  string `json:"agent_id"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	SHA256   struct {
		Input    string `json:"input"`
		Expected string `json:"expected"`
	} `json:"sha256"`
}

func readFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(readFile(t, "MANIFEST.json"), &m); err != nil {
		t.Fatalf("MANIFEST.json: %v", err)
	}
	if m.Version != 1 || len(m.Cases) == 0 {
		t.Fatalf("MANIFEST.json: version %d, %d cases", m.Version, len(m.Cases))
	}
	return m
}

// normalize feeds a transcript from offset 0, line by line, the way the
// daemon does, and closes the session when live is false.
func normalize(t *testing.T, input []byte, live bool) convmodel.Conversation {
	t.Helper()
	n := ccnorm.New(ccnorm.Options{SessionID: scrub.FixtureSessionID})
	var off int64
	lines := bytes.Split(input, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		if _, err := n.Feed(off, l); err != nil {
			t.Fatalf("feed at %d: %v", off, err)
		}
		off += int64(len(l)) + 1
	}
	if !live {
		n.SetLive(false)
	}
	return n.Conversation()
}

// encode is the canonical pretty form of a value: two-space indent, struct
// order, no HTML escaping, one trailing newline.
func encode(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// expectedFile is the wire form of expected.json.
type expectedFile struct {
	Live         bool                   `json:"live"`
	Conversation convmodel.Conversation `json:"conversation"`
}

func subagentExpected(t *testing.T, input []byte, agentID string) []byte {
	t.Helper()
	items, _, err := ccnorm.NormalizeSubagent(bytes.NewReader(input), agentID)
	if err != nil {
		t.Fatal(err)
	}
	return encode(t, struct {
		Items []convmodel.Item `json:"items"`
	}{items})
}

// ---- -update --------------------------------------------------------------

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

// ccVersion is the `version` of the first row that has one.
func ccVersion(input []byte) string {
	for _, l := range bytes.Split(input, []byte("\n")) {
		var r struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(l, &r) == nil && r.Version != "" {
			return r.Version
		}
	}
	return ""
}

func updateFixtures(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fixtureRoot, casesDir))
	if err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: 1}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := casesDir + "/" + name
		input := readFile(t, dir+"/input.jsonl")
		var facts struct {
			Live bool `json:"live"`
		}
		if err := json.Unmarshal(readFile(t, dir+"/facts.json"), &facts); err != nil {
			t.Fatalf("%s/facts.json: %v (hand-write it before running -update)", name, err)
		}
		c := manifestCase{
			Name: name, Source: "cc-transcript", CCVersion: ccVersion(input),
			Input: dir + "/input.jsonl", Expected: dir + "/expected.json", Facts: dir + "/facts.json",
		}
		if d, err := os.ReadFile(filepath.Join(fixtureRoot, dir, "DESCRIPTION")); err == nil {
			c.Description = firstLine(d)
		} else {
			c.Description = firstLine(readFile(t, dir+"/README.md"))
		}
		expected := encode(t, expectedFile{facts.Live, normalize(t, input, facts.Live)})
		writeFile(t, c.Expected, expected)
		c.SHA256 = hashes{sum(input), sum(expected), sum(readFile(t, c.Facts))}

		kids, _ := filepath.Glob(filepath.Join(fixtureRoot, dir, "children", "*.input.jsonl"))
		sort.Strings(kids)
		for _, k := range kids {
			id := strings.TrimSuffix(filepath.Base(k), ".input.jsonl")
			kin := readFile(t, dir+"/children/"+id+".input.jsonl")
			kexp := subagentExpected(t, kin, id)
			writeFile(t, dir+"/children/"+id+".expected.json", kexp)
			ch := manifestChild{AgentID: id, Input: dir + "/children/" + id + ".input.jsonl", Expected: dir + "/children/" + id + ".expected.json"}
			ch.SHA256.Input, ch.SHA256.Expected = sum(kin), sum(kexp)
			c.Children = append(c.Children, ch)
		}
		m.Cases = append(m.Cases, c)
	}
	sort.Slice(m.Cases, func(i, j int) bool { return m.Cases[i].Name < m.Cases[j].Name })
	writeFile(t, "MANIFEST.json", encode(t, m))
}

func writeFile(t *testing.T, rel string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- tests ----------------------------------------------------------------

// TestGolden normalizes every case's input.jsonl and compares the result with
// expected.json byte for byte.
func TestGolden(t *testing.T) {
	if *update {
		updateFixtures(t)
	}
	for _, c := range loadManifest(t).Cases {
		t.Run(c.Name, func(t *testing.T) {
			var f struct {
				Live bool `json:"live"`
			}
			if err := json.Unmarshal(readFile(t, c.Facts), &f); err != nil {
				t.Fatal(err)
			}
			conv := normalize(t, readFile(t, c.Input), f.Live)
			if err := conv.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
			want := readFile(t, c.Expected)
			if got := encode(t, expectedFile{f.Live, conv}); !bytes.Equal(got, want) {
				t.Errorf("%s differs from the normalizer output (%d vs %d bytes); first difference:\n%s\nrun with -update if the change is intended, then re-check facts.json",
					c.Expected, len(want), len(got), firstDiff(want, got))
			}
			for _, ch := range c.Children {
				kin := readFile(t, ch.Input)
				if got, want := subagentExpected(t, kin, ch.AgentID), readFile(t, ch.Expected); !bytes.Equal(got, want) {
					t.Errorf("%s differs from the NormalizeSubagent output:\n%s", ch.Expected, firstDiff(want, got))
				}
			}
		})
	}
}

// firstDiff shows the first line where two files differ.
func firstDiff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + itoa(i+1) + "\n  file: " + a + "\n  now:  " + b
		}
	}
	return "(no difference found)"
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestManifest_Sha256Match: the hashes the Apps pin are the hashes of the
// files; every case directory on disk is in the MANIFEST and the other way
// round.
func TestManifest_Sha256Match(t *testing.T) {
	m := loadManifest(t)
	inManifest := map[string]bool{}
	for _, c := range m.Cases {
		inManifest[c.Name] = true
		for _, h := range []struct{ what, path, want string }{
			{"input", c.Input, c.SHA256.Input}, {"expected", c.Expected, c.SHA256.Expected}, {"facts", c.Facts, c.SHA256.Facts},
		} {
			if got := sum(readFile(t, h.path)); got != h.want {
				t.Errorf("%s: sha256 of %s is %s, MANIFEST says %s (run -update after changing a file)", c.Name, h.what, got, h.want)
			}
		}
		for _, ch := range c.Children {
			if got := sum(readFile(t, ch.Input)); got != ch.SHA256.Input {
				t.Errorf("%s: sha256 of %s differs from MANIFEST", c.Name, ch.Input)
			}
			if got := sum(readFile(t, ch.Expected)); got != ch.SHA256.Expected {
				t.Errorf("%s: sha256 of %s differs from MANIFEST", c.Name, ch.Expected)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(fixtureRoot, casesDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !inManifest[e.Name()] {
			t.Errorf("case directory %s is not in MANIFEST.json (run -update)", e.Name())
		}
	}
	for i := 1; i < len(m.Cases); i++ {
		if m.Cases[i-1].Name >= m.Cases[i].Name {
			t.Errorf("MANIFEST cases are not sorted by name at %s", m.Cases[i].Name)
		}
	}
}
