package ccnorm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
)

// facts is facts.json: written by hand from reading input.jsonl, never
// regenerated, and checked against the normalizer by TestFacts independently
// of expected.json. The format is documented in testdata/conversation/v1/README.md.
type facts struct {
	Live    bool         `json:"live"`
	Turns   int          `json:"turns"`
	PerTurn []factTurn   `json:"per_turn"`
	Steps   []factStep   `json:"steps"`
	Outputs []factOutput `json:"outputs"`
	Shapes  []string     `json:"shapes"`
}

type factTurn struct {
	ID         string `json:"id"`
	Outcome    string `json:"outcome"`
	UserSource string `json:"user_source"`
	ErrorKind  string `json:"error_kind,omitempty"`
}

type factStep struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Denial string `json:"denial,omitempty"`
}

type factOutput struct {
	Step       string `json:"step"`
	TotalLines int    `json:"total_lines"`
	TotalBytes int    `json:"total_bytes"`
	Keep       string `json:"keep"`
	Truncated  bool   `json:"truncated"`
}

func loadFactsFile(t *testing.T, rel string) facts {
	t.Helper()
	var f facts
	dec := json.NewDecoder(bytes.NewReader(readFile(t, rel)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return f
}

func loadFacts(t *testing.T, c manifestCase) facts { return loadFactsFile(t, c.Facts) }

// TestFacts is the independent oracle: the hand-written facts against the
// normalizer, without expected.json in between. A case's children/<id>.facts.json
// are checked the same way against NormalizeSubagent.
func TestFacts(t *testing.T) {
	for _, c := range loadManifest(t).Cases {
		t.Run(c.Name, func(t *testing.T) {
			f := loadFacts(t, c)
			conv := normalize(t, readFile(t, c.Input), f.Live)
			checkFacts(t, f, conv.Turns, false)
		})
		for _, ch := range c.Children {
			t.Run(c.Name+"/child-"+ch.AgentID, func(t *testing.T) {
				f := loadFactsFile(t, ch.Facts)
				items, _, err := ccnorm.NormalizeSubagent(bytes.NewReader(readFile(t, ch.Input)), ch.AgentID)
				if err != nil {
					t.Fatal(err)
				}
				tr := convmodel.Turn{Items: items}
				for _, it := range items {
					if it.User != nil {
						tr.ID = it.User.ID
						break
					}
				}
				checkFacts(t, f, []convmodel.Turn{tr}, true)
			})
		}
	}
}

// checkFacts compares facts with the turns the normalizer built. A child file
// is one pseudo-turn that has no outcome of its own (and is always read as
// live), so for a child (isChild) per_turn.outcome and live are not checked;
// per_turn.id is the id of its first user item.
func checkFacts(t *testing.T, f facts, turns []convmodel.Turn, isChild bool) {
	t.Helper()
	if len(turns) != f.Turns || len(f.PerTurn) != f.Turns {
		t.Fatalf("turns: normalizer %d, facts.turns %d, facts.per_turn %d", len(turns), f.Turns, len(f.PerTurn))
	}
	var steps []convmodel.Step
	for i, tr := range turns {
		want := f.PerTurn[i]
		if isChild {
			want.Outcome = ""
		}
		src := ""
		for _, it := range tr.Items {
			if it.User != nil {
				src = string(it.User.Source)
				break
			}
		}
		errKind := ""
		if tr.Error != nil {
			errKind = tr.Error.Kind
		}
		got := factTurn{tr.ID, string(tr.Outcome), src, errKind}
		if got != want {
			t.Errorf("turn %d: normalizer %+v, facts %+v", i, got, want)
		}
		for _, it := range tr.Items {
			if it.Step != nil {
				steps = append(steps, *it.Step)
			}
		}
	}
	if len(steps) != len(f.Steps) {
		t.Errorf("steps: normalizer %d, facts list %d (facts must list every step)", len(steps), len(f.Steps))
	}
	byID := map[string]convmodel.Step{}
	for _, s := range steps {
		byID[s.ID] = s
	}
	for i, want := range f.Steps {
		if i < len(steps) && steps[i].ID != want.ID {
			t.Errorf("step %d: normalizer %s, facts %s (order is the order of appearance)", i, steps[i].ID, want.ID)
		}
		s, ok := byID[want.ID]
		if !ok {
			t.Errorf("facts step %s does not exist", want.ID)
			continue
		}
		if got := (factStep{s.ID, string(s.Kind), string(s.Status), s.Denial}); got != want {
			t.Errorf("step %s: normalizer %+v, facts %+v", want.ID, got, want)
		}
	}
	listed := map[string]factOutput{}
	for _, o := range f.Outputs {
		listed[o.Step] = o
	}
	for _, s := range steps {
		o := s.Output
		if o == nil || !o.Truncated {
			continue
		}
		want, ok := listed[s.ID]
		if !ok {
			t.Errorf("step %s has a truncated output that facts.outputs does not list", s.ID)
			continue
		}
		if got := (factOutput{s.ID, o.TotalLines, o.TotalBytes, string(o.Keep), o.Truncated}); got != want {
			t.Errorf("output of %s: normalizer %+v, facts %+v", s.ID, got, want)
		}
	}
	for _, want := range f.Outputs {
		s, ok := byID[want.Step]
		if !ok || s.Output == nil {
			t.Errorf("facts output %s: no such step output", want.Step)
		} else if !want.Truncated && s.Output.Truncated {
			t.Errorf("facts output %s says not truncated", want.Step)
		} else if !want.Truncated && (s.Output.TotalLines != want.TotalLines || s.Output.TotalBytes != want.TotalBytes) {
			t.Errorf("output of %s: totals %d/%d, facts %d/%d", want.Step, s.Output.TotalLines, s.Output.TotalBytes, want.TotalLines, want.TotalBytes)
		}
	}
}

// private-data patterns a fixture must not contain. The sk- and xox rules
// need a boundary / dash: "task-notification" is not a key.
var privatePatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"home path", regexp.MustCompile(`/Users/`)},
	{"claude scratch path", regexp.MustCompile(`/private/tmp/claude-`)},
	{"temp folder", regexp.MustCompile(`/var/folders`)},
	{"tailnet address", regexp.MustCompile(`100\.64\.`)},
	{"e-mail address", regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)},
	{"bearer token", regexp.MustCompile(`Bearer `)},
	{"sk- key", regexp.MustCompile(`(^|[^A-Za-z0-9])sk-[A-Za-z0-9_\-]{8,}`)},
	{"GitHub token", regexp.MustCompile(`gh[po]_`)},
	{"Slack token", regexp.MustCompile(`xox[a-z]-`)},
	// encoded forms of the recording machine's paths (a dir under
	// .claude/projects, a /private/tmp/claude-N scratch path)
	{"claude scratch dir name", regexp.MustCompile(`claude-[0-9]{3}`)},
	{"encoded workspace path", regexp.MustCompile(`-Workspace-`)},
	{"encoded project dir", regexp.MustCompile(`user-purdex-`)},
}

var (
	reGuardUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	neutralDir  = ".claude/projects/" + scrub.FixtureProjectDir
)

// peerPatterns are private-data patterns that have one allowed fixture form:
// a pdx peer address is fine as host/fixture-peer, a recording host name is never fine, a peer socket as
// uds:/work/tmp/cc-socks/1.sock (what the scrubber writes).
var peerPatterns = []struct {
	name  string
	re    *regexp.Regexp
	allow func(match string) bool
}{
	{"recording host name", regexp.MustCompile(`(?i)\b(?:mlab|air26|air19|air-2026|air-2019)\b`),
		func(m string) bool { return false }},
	{"peer socket path", regexp.MustCompile("uds:[^\\s\"'`<>\\\\]*/[0-9]+\\.sock"),
		func(m string) bool { return m == scrub.FixtureSocket }},
	// an MCP tool name names a server of the recording host's setup; only the
	// neutral mcp__server__… (or a bare mcp__server) is fine
	{"mcp server name", regexp.MustCompile(`mcp__[A-Za-z0-9_*-]+`),
		func(m string) bool {
			server, _, _ := strings.Cut(strings.TrimPrefix(m, "mcp__"), "__")
			return server == scrub.FixtureMcpServer
		}},
	// the server column of a /context table row names it too
	{"mcp server column", regexp.MustCompile(`mcp__[A-Za-z0-9_*-]+ \| [A-Za-z0-9_-]+ \|`),
		func(m string) bool { return strings.HasSuffix(m, " | "+scrub.FixtureMcpServer+" |") }},
	// a path under .claude/projects names the recorded project dir and
	// session: only the neutral dir and the fixture session id are fine
	{".claude/projects path", regexp.MustCompile(`\.claude/projects/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._/-]*)?`),
		func(m string) bool {
			head, rest, _ := strings.Cut(strings.TrimPrefix(m, ".claude/projects/"), "/")
			if ".claude/projects/"+head != neutralDir {
				return false
			}
			for _, id := range reGuardUUID.FindAllString(rest, -1) {
				if id != scrub.FixtureSessionID {
					return false
				}
			}
			return true
		}},
}

var imageData = regexp.MustCompile(`"data":\s*"[A-Za-z0-9+/=]*"`)

// TestFixtures_NoPrivateData: no fixture file (inputs, expected, facts,
// READMEs; MANIFEST for the patterns but not the token rule, it holds hashes)
// carries a home path, a tailnet address, an e-mail address, a pdx peer
// address or peer socket path (other than the scrubbed forms), a
// secret-shaped string or a 32+ character token outside image data, an
// mcp__ name whose server is not `server`, or a .claude/projects path other
// than the neutral one.
func TestFixtures_NoPrivateData(t *testing.T) {
	checked := 0
	err := filepath.WalkDir(fixtureRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		text := string(imageData.ReplaceAll(b, []byte(`"data":""`)))
		for _, p := range privatePatterns {
			if loc := p.re.FindStringIndex(text); loc != nil {
				t.Errorf("%s contains a %s: …%s…", path, p.name, snippet(text, loc))
			}
		}
		for _, p := range peerPatterns {
			for _, loc := range p.re.FindAllStringIndex(text, -1) {
				if !p.allow(text[loc[0]:loc[1]]) {
					t.Errorf("%s contains a %s: …%s…", path, p.name, snippet(text, loc))
					break
				}
			}
		}
		if filepath.Base(path) != "MANIFEST.json" {
			for _, span := range scrub.TokenSpans(text) {
				t.Errorf("%s contains a token-shaped string: …%s…", path, snippet(text, span[:]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 4 {
		t.Fatalf("only %d fixture files found under %s", checked, fixtureRoot)
	}
}

// A /context listing in a local-command stdout names the recording host's
// plugins, skills and mcp servers; the scrubber omits long ones, and no fixture
// input may carry what is left of such a listing.
func TestFixtures_NoEnvironmentListingInLocalCommandOutput(t *testing.T) {
	var strs func(v any, out *[]string)
	strs = func(v any, out *[]string) {
		switch x := v.(type) {
		case string:
			*out = append(*out, x)
		case map[string]any:
			for _, e := range x {
				strs(e, out)
			}
		case []any:
			for _, e := range x {
				strs(e, out)
			}
		}
	}
	inputs, rowsSeen := 0, 0
	err := filepath.WalkDir(fixtureRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "input.jsonl" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		inputs++
		for n, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "local-command-stdout") {
				continue
			}
			var row any
			if json.Unmarshal([]byte(line), &row) != nil {
				t.Errorf("%s:%d is not JSON", path, n+1)
				continue
			}
			var all []string
			strs(row, &all)
			for _, s := range all {
				if !strings.Contains(s, "<local-command-stdout>") {
					continue
				}
				rowsSeen++
				for _, bad := range []string{"Plugin (", "mcp__"} {
					if strings.Contains(s, bad) {
						t.Errorf("%s:%d local-command stdout contains %q: %.80q", path, n+1, bad, s)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inputs < 4 || rowsSeen == 0 {
		t.Fatalf("looked at %d inputs and %d local-command stdout texts; the guard checks nothing", inputs, rowsSeen)
	}
}

// A /context expansion can also arrive as a plain isMeta user row; no fixture
// input may list the recording host's plugins anywhere.
func TestFixtures_NoPluginListingAnywhere(t *testing.T) {
	inputs := 0
	err := filepath.WalkDir(fixtureRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "input.jsonl" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		inputs++
		if i := strings.Index(string(b), "Plugin ("); i >= 0 {
			t.Errorf("%s contains a plugin listing: %.80q", path, string(b)[i:])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inputs < 4 {
		t.Fatalf("looked at %d inputs; the guard checks nothing", inputs)
	}
}

func snippet(text string, loc []int) string {
	from, to := max(loc[0]-20, 0), min(loc[1]+20, len(text))
	return strings.ReplaceAll(text[from:to], "\n", " ")
}

// requiredShapes are the rule shapes every fixture set must demonstrate.
// derived ones are computed from the structured part of facts.json; declared
// ones are claimed in facts.shapes by the author who read the input, and
// cross-checked against the structured data where it can.
var requiredShapes = []string{
	"denied:user-rejected", "denied:permission-rule", "denied:interrupted", "denied:cancelled", // derived
	"failed_step", "execute_keep_tail", "read_keep_head", // derived
	"source:user", "source:queued", "source:peer", "source:slash", "source:bash", "source:task", "source:scheduled", // derived; "resumed" is exempt until the transcript shows it (M-U1-4-a)
	"queued_prompt_row", "queued_absorbed", // declared
	"interrupted_marker", "interrupted_refusal", "interrupted_killed", // declared
	"failed_api_error", // declared
}

// evidence says what a declared shape needs from the structured facts.
func evidence(shape string, f facts) bool {
	has := func(pred func(factTurn) bool) bool { return slices.ContainsFunc(f.PerTurn, pred) }
	hasStep := func(pred func(factStep) bool) bool { return slices.ContainsFunc(f.Steps, pred) }
	interrupted := func(t factTurn) bool { return t.Outcome == "interrupted" }
	switch shape {
	case "queued_prompt_row":
		return has(func(t factTurn) bool { return t.UserSource == "queued" })
	case "interrupted_marker":
		return has(interrupted)
	case "interrupted_refusal":
		return has(interrupted) && hasStep(func(s factStep) bool { return s.Denial == "user-rejected" })
	case "interrupted_killed":
		return has(interrupted) && hasStep(func(s factStep) bool { return s.Denial == "interrupted" })
	case "failed_api_error":
		return has(func(t factTurn) bool { return t.Outcome == "failed" && t.ErrorKind != "" })
	}
	return true // queued_absorbed: an item inside a turn, no structured trace
}

func shapesOf(f facts) map[string]bool {
	seen := map[string]bool{}
	for _, s := range f.Steps {
		if s.Status == "denied" {
			seen["denied:"+s.Denial] = true
		}
		if s.Status == "failed" {
			seen["failed_step"] = true
		}
	}
	for _, o := range f.Outputs {
		for _, s := range f.Steps {
			if s.ID != o.Step || !o.Truncated {
				continue
			}
			if s.Kind == "execute" && o.Keep == "tail" {
				seen["execute_keep_tail"] = true
			}
			if s.Kind == "read" && o.Keep == "head" {
				seen["read_keep_head"] = true
			}
		}
	}
	for _, t := range f.PerTurn {
		if t.UserSource != "" {
			seen["source:"+t.UserSource] = true
		}
	}
	return seen
}

// TestFixtures_CoverRuleShapes: across all facts.json every rule shape in
// requiredShapes appears at least once.
func TestFixtures_CoverRuleShapes(t *testing.T) {
	covered := map[string]string{}
	for _, c := range loadManifest(t).Cases {
		f := loadFacts(t, c)
		for s := range shapesOf(f) {
			covered[s] = c.Name
		}
		for _, s := range f.Shapes {
			if !evidence(s, f) {
				t.Errorf("%s declares shape %q but its structured facts do not show it", c.Name, s)
			}
			covered[s] = c.Name
		}
	}
	var missing []string
	for _, s := range requiredShapes {
		if covered[s] == "" {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("no case demonstrates the rule shape(s): %s — add or extend a case (derived shapes come from facts.json steps / outputs / per_turn, declared ones from its \"shapes\" list)", strings.Join(missing, ", "))
	}
}

// ---- the scrubber keeps every field the normalizer reads --------------------

// readSet is the set of (row type, path) pairs of ReadFields that the rows of
// a transcript actually contain.
func readSet(t *testing.T, transcript []byte) map[string]bool {
	t.Helper()
	exact := map[string]bool{}
	subtree := map[string]bool{}
	for _, f := range ccnorm.ReadFields {
		exact[f.Row+"|"+f.Path] = true
		if f.Subtree {
			subtree[f.Row+"|"+f.Path] = true
		}
	}
	set := map[string]bool{}
	var walk func(row string, v any, path string)
	walk = func(row string, v any, path string) {
		for _, r := range []string{row, "*"} {
			if exact[r+"|"+path] {
				set[row+"|"+path] = true
				if subtree[r+"|"+path] {
					return
				}
			}
		}
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(row, e, p)
			}
		case []any:
			for _, e := range x {
				walk(row, e, path+"[]")
			}
		}
	}
	for _, l := range bytes.Split(transcript, []byte("\n")) {
		var m map[string]any
		if len(bytes.TrimSpace(l)) == 0 || json.Unmarshal(l, &m) != nil {
			continue
		}
		typ, _ := m["type"].(string)
		sub, _ := m["subtype"].(string)
		att := ""
		if a, ok := m["attachment"].(map[string]any); ok {
			att, _ = a["type"].(string)
		}
		if ccnorm.ReadsRow(typ, sub, att) {
			walk(typ, m, "")
		}
	}
	return set
}

func scrubBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if _, err := scrub.Scrub(bytes.NewReader(raw), &out, scrub.Options{Home: "/Users/wake", Users: []string{"wake"}}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func diffSets(before, after map[string]bool) string {
	var gone, added []string
	for k := range before {
		if !after[k] {
			gone = append(gone, k)
		}
	}
	for k := range after {
		if !before[k] {
			added = append(added, k)
		}
	}
	sort.Strings(gone)
	sort.Strings(added)
	if len(gone) == 0 && len(added) == 0 {
		return ""
	}
	return fmt.Sprintf("lost by the scrubber: %v; appeared: %v", gone, added)
}

// A hand-written raw transcript that exercises every ReadFields entry, with
// decoys the scrubber must drop. It is the always-on form of the test; the
// recorded transcripts (the iOS samples) are checked when PDX_RAW_TRANSCRIPTS
// names their directory.
const rawSample = `{"type":"custom-title","customTitle":"t"}
{"type":"ai-title","aiTitle":"t"}
{"type":"user","uuid":"u1","timestamp":"2026-10-07T13:00:00.000Z","isSidechain":false,"isMeta":false,"isCompactSummary":false,"entrypoint":"cli","agentId":"a1","origin":{"kind":"human","name":"n","producer":"p"},"turnOrigin":"human","promptSource":"typed","parentUuid":"p","message":{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}}
{"type":"user","uuid":"u2","interruptedMessageId":"m1","message":{"content":"[Request interrupted by user]"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-07T13:00:01.000Z","thinkingDurationMs":5,"effort":"high","perTurnEffort":"high","isApiErrorMessage":false,"error":"x","requestId":"r","message":{"model":"m","id":"msg","usage":{},"content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"text","text":"x"},{"type":"tool_use","id":"toolu_1","name":"Edit","input":{"file_path":"/work/a","old_string":"a","new_string":"b"},"caller":{}}]}}
{"type":"assistant","uuid":"a2","message":{"model":"m","content":"plain string content"}}
{"type":"user","uuid":"u3","toolDenialKind":"user-rejected","toolUseResult":{"structuredPatch":[{"oldStart":1,"lines":["+b"]}],"filePath":"/work/a","backgroundTaskId":"b","agentId":"g","description":"d","subagent_type":"s","isAsync":true,"stdout":"decoy"},"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":[{"type":"text","text":"no"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}}
{"type":"system","subtype":"turn_duration","uuid":"s1","timestamp":"2026-10-07T13:00:02.000Z","durationMs":2000,"content":"c"}
{"type":"system","subtype":"compact_boundary","uuid":"s2","compactMetadata":{"trigger":"manual","preTokens":1}}
{"type":"attachment","uuid":"q1","attachment":{"type":"queued_command","commandMode":"prompt","origin":{"kind":"human","name":"n"},"prompt":[{"type":"text","text":"later"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}}
`

func TestScrubber_KeepsEveryReadField(t *testing.T) {
	// 1. the sample touches every ReadFields entry, so none is untested
	raw := []byte(rawSample)
	before := readSet(t, raw)
	for _, f := range ccnorm.ReadFields {
		if !before[f.Row+"|"+f.Path] && f.Row != "*" {
			t.Errorf("the sample transcript never exercises ReadFields entry %s %s", f.Row, f.Path)
		}
	}
	if d := diffSets(before, readSet(t, scrubBytes(t, raw))); d != "" {
		t.Errorf("sample: %s", d)
	}
	// 2. every committed fixture input is a scrub fixed point: scrubbing it
	//    again loses no field
	for _, c := range loadManifest(t).Cases {
		in := readFile(t, c.Input)
		if d := diffSets(readSet(t, in), readSet(t, scrubBytes(t, in))); d != "" {
			t.Errorf("%s: %s", c.Name, d)
		}
	}
	// 3. recorded raw transcripts, when the directory is given
	if dir := os.Getenv("PDX_RAW_TRANSCRIPTS"); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		if len(files) == 0 {
			t.Fatalf("PDX_RAW_TRANSCRIPTS=%s holds no .jsonl", dir)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if d := diffSets(readSet(t, b), readSet(t, scrubBytes(t, b))); d != "" {
				t.Errorf("%s: %s", filepath.Base(f), d)
			}
		}
	}
}
