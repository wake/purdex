package hostconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/team"
)

// #1889: the lenient readers, one per collection, behind the GET and a PUT's
// answers. The strict normalize* tests stay as they were: each rule exists
// once and serves both modes.

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// A stored value a PUT would take reads as exactly what that PUT stored, with
// no marker.
func TestRead_AValidValueReadsAsNormalized(t *testing.T) {
	for _, c := range []struct {
		name, raw string
		read      func(json.RawMessage) readout
		normalize func(json.RawMessage) (any, error)
	}{
		{"projects", `[{"id":"p1","name":"  Purdex ","slug":"purdex","path":" ~/w "},{"id":"p2","name":"Root","slug":"r-2","path":"/"}]`,
			readProjects, func(r json.RawMessage) (any, error) { return normalizeProjects(r) }},
		{"empty projects", `[]`, readProjects, func(r json.RawMessage) (any, error) { return normalizeProjects(r) }},
		{"commands", `[{"id":"c1","name":" Claude ","command":"cld","icon":{"kind":"agent","value":"cc-bot"}},` +
			`{"id":"c2","name":"Shell","command":"echo hi && ls","icon":{"kind":"phosphor","value":"Terminal"}}]`,
			readCommands, func(r json.RawMessage) (any, error) { return normalizeCommands(r) }},
		{"quick replies", `[{"id":"go","text":"  go on \n"},{"id":"t","text":"請跑測試"}]`,
			readQuickReplies, func(r json.RawMessage) (any, error) { return normalizeQuickReplies(r) }},
		{"resume templates", `{"cc":{"exact":"cld --resume {id}","fallback":""},"codex":{"exact":"","fallback":"cx"}}`,
			readResumeTemplates, func(r json.RawMessage) (any, error) { return normalizeResumeTemplates(r) }},
		{"empty resume templates", `{}`, readResumeTemplates, func(r json.RawMessage) (any, error) { return normalizeResumeTemplates(r) }},
		{"relay", `{"self_solo":false,"prompt_write":"W {{path}}","prompt_fix":" \n ","prompt_seed":"  seed  "}`,
			readRelay, func(r json.RawMessage) (any, error) { return normalizeRelay(r) }},
		{"relay left out", `{}`, readRelay, func(r json.RawMessage) (any, error) { return normalizeRelay(r) }},
		{"team", `{"member_command":" claude --x "}`, readTeam, func(r json.RawMessage) (any, error) { return normalizeTeam(r) }},
		{"team left out", `{}`, readTeam, func(r json.RawMessage) (any, error) { return normalizeTeam(r) }},
		{"resources", `{"mode":"advise","kinds":{"build":20},"warmup_s":0}`,
			readResources, func(r json.RawMessage) (any, error) { return normalizeResources(r) }},
		{"resources left out", `{}`, readResources, func(r json.RawMessage) (any, error) { return normalizeResources(r) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			want, err := c.normalize(json.RawMessage(c.raw))
			require.NoError(t, err)
			r := c.read(json.RawMessage(c.raw))
			assert.NoError(t, r.invalid)
			assert.Nil(t, r.dropped)
			assert.Equal(t, want, r.items)
		})
	}
}

// Each rule a PUT applies drops exactly the row that breaks it, with its
// reason; the rows around it are kept, in order, as the PUT stores them. A
// duplicate of a KEPT row is dropped.
func TestReadProjects_DropsTheRowThatBreaksARule(t *testing.T) {
	const first, last = `{"id":"p0","name":" A ","slug":"s0","path":"/a"}`, `{"id":"p9","name":"Z","slug":"s9","path":" ~/z "}`
	want := []Project{{ID: "p0", Name: "A", Slug: "s0", Path: "/a"}, {ID: "p9", Name: "Z", Slug: "s9", Path: "~/z"}}
	for row, why := range map[string]string{
		`"nope"`: `not a project`,
		`null`:   `not a project`,
		`["p1"]`: `not a project`,
		`{"id":"p1","name":3,"slug":"s1","path":"/"}`:                                 `not a project`,
		`{"id":"a b","name":"n","slug":"s1","path":"/"}`:                              `invalid id "a b"`,
		`{"id":"p0","name":"n","slug":"s1","path":"/"}`:                               `duplicate id "p0"`,
		`{"id":"p1","name":"  ","slug":"s1","path":"/"}`:                              `project name is required`,
		`{"id":"p1","name":"` + strings.Repeat("a", 65) + `","slug":"s1","path":"/"}`: `project name too long`,
		`{"id":"p1","name":"n","slug":"BAD","path":"/"}`:                              `invalid slug "BAD"`,
		`{"id":"p1","name":"n","slug":"s0","path":"/"}`:                               `duplicate slug "s0"`,
		`{"id":"p1","name":"n","slug":"s1","path":" "}`:                               `invalid path for project "p1"`,
		`{"id":"p1","name":"n","slug":"s1","path":"/a\u0000b"}`:                       `invalid path for project "p1"`,
		`{"id":"p1","name":"n","slug":"s1","path":"rel/x"}`:                           `path must be absolute or start with ~/ (project "p1")`,
	} {
		r := readProjects(json.RawMessage("[" + first + "," + row + "," + last + "]"))
		require.NoError(t, r.invalid, row)
		assert.Equal(t, want, r.items, row)
		assert.Equal(t, &Dropped{Count: 1, Reasons: []string{"item 1: " + why}}, r.dropped, row)
	}
}

func TestReadCommands_DropsTheRowThatBreaksARule(t *testing.T) {
	const first = `{"id":"c0","name":" A ","command":"a","icon":{"kind":"agent","value":"codex"}}`
	const last = `{"id":"c9","name":"Z","command":"z","icon":{"kind":"phosphor","value":"Rocket"}}`
	want := []Command{
		{ID: "c0", Name: "A", Command: "a", Icon: CommandIcon{Kind: "agent", Value: "codex"}},
		{ID: "c9", Name: "Z", Command: "z", Icon: CommandIcon{Kind: "phosphor", Value: "Rocket"}},
	}
	icon := `"icon":{"kind":"agent","value":"codex"}`
	for row, why := range map[string]string{
		`"nope"`: `not a command`,
		`{"id":"c1","name":"n","command":"x","icon":"Rocket"}`:                               `not a command`,
		`{"id":"c 1","name":"n","command":"x",` + icon + `}`:                                 `invalid id "c 1"`,
		`{"id":"c0","name":"n","command":"x",` + icon + `}`:                                  `duplicate id "c0"`,
		`{"id":"c1","name":"","command":"x",` + icon + `}`:                                   `command name is required`,
		`{"id":"c1","name":"n","command":"",` + icon + `}`:                                   `invalid command for "c1"`,
		`{"id":"c1","name":"n","command":"` + strings.Repeat("x", 4097) + `",` + icon + `}`:  `invalid command for "c1"`,
		`{"id":"c1","name":"n","command":"a\u0000",` + icon + `}`:                            `invalid command for "c1"`,
		`{"id":"c1","name":"n","command":"x","icon":{"kind":"emoji","value":"x"}}`:           `invalid icon kind "emoji"`,
		`{"id":"c1","name":"n","command":"x","icon":{"kind":"agent","value":"gemini"}}`:      `invalid agent icon "gemini"`,
		`{"id":"c1","name":"n","command":"x","icon":{"kind":"phosphor","value":"terminal"}}`: `invalid phosphor icon "terminal"`,
		`{"id":"c1","name":"n","command":"x","icon":null}`:                                   `invalid icon kind ""`,
	} {
		r := readCommands(json.RawMessage("[" + first + "," + row + "," + last + "]"))
		require.NoError(t, r.invalid, row)
		assert.Equal(t, want, r.items, row)
		assert.Equal(t, &Dropped{Count: 1, Reasons: []string{"item 1: " + why}}, r.dropped, row)
	}
}

func TestReadQuickReplies_DropsTheRowThatBreaksARule(t *testing.T) {
	const first, last = `{"id":"q0","text":" a "}`, `{"id":"q9","text":"z"}`
	want := []QuickReply{{ID: "q0", Text: "a"}, {ID: "q9", Text: "z"}}
	for row, why := range map[string]string{
		`1`:                       `not a quick reply`,
		`{"id":"q1","text":42}`:   `not a quick reply`,
		`{"id":"q1"}`:             `invalid text for quick reply "q1"`,
		`{"id":"q1","text":"  "}`: `invalid text for quick reply "q1"`,
		`{"id":"q1","text":"` + strings.Repeat("x", 1001) + `"}`: `invalid text for quick reply "q1"`,
		`{"id":"q1","text":"a\u0000b"}`:                          `invalid text for quick reply "q1"`,
		`{"id":"q0","text":"x"}`:                                 `duplicate id "q0"`,
		`{"id":"","text":"x"}`:                                   `invalid id ""`,
	} {
		r := readQuickReplies(json.RawMessage("[" + first + "," + row + "," + last + "]"))
		require.NoError(t, r.invalid, row)
		assert.Equal(t, want, r.items, row)
		assert.Equal(t, &Dropped{Count: 1, Reasons: []string{"item 1: " + why}}, r.dropped, row)
	}
}

// A dropped row reserves nothing: its id (and slug) do not make a later row a
// duplicate. Each dropped row here fails AFTER its id (and slug) passed.
func TestRead_ADroppedRowReservesNothing(t *testing.T) {
	r := readProjects(json.RawMessage(`[{"id":"p1","name":"n","slug":"s1","path":"rel"},{"id":"p1","name":"n","slug":"s1","path":"/"}]`))
	assert.Equal(t, []Project{{ID: "p1", Name: "n", Slug: "s1", Path: "/"}}, r.items)
	assert.Equal(t, &Dropped{Count: 1, Reasons: []string{`item 0: path must be absolute or start with ~/ (project "p1")`}}, r.dropped)

	r = readCommands(json.RawMessage(`[{"id":"c1","name":"n","command":"x","icon":{"kind":"emoji","value":"x"}},` +
		`{"id":"c1","name":"n","command":"y","icon":{"kind":"agent","value":"codex"}}]`))
	assert.Equal(t, []Command{{ID: "c1", Name: "n", Command: "y", Icon: CommandIcon{Kind: "agent", Value: "codex"}}}, r.items)

	r = readQuickReplies(json.RawMessage(`[{"id":"q1","text":""},{"id":"q1","text":"ok"}]`))
	assert.Equal(t, []QuickReply{{ID: "q1", Text: "ok"}}, r.items)
}

// Over a list's max, the first max VALID rows are kept and the rest dropped.
func TestRead_ListMaxKeepsTheFirstValidRows(t *testing.T) {
	rows := []string{`{"id":"bad id","name":"n","slug":"x","path":"/"}`}
	for i := 0; i <= maxItems; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":"p%d","name":"n","slug":"s%d","path":"/"}`, i, i))
	}
	r := readProjects(json.RawMessage("[" + strings.Join(rows, ",") + "]"))
	got := r.items.([]Project)
	require.Len(t, got, maxItems)
	assert.Equal(t, "p0", got[0].ID)
	assert.Equal(t, fmt.Sprintf("p%d", maxItems-1), got[maxItems-1].ID)
	assert.Equal(t, &Dropped{Count: 2, Reasons: []string{`item 0: invalid id "bad id"`, "item 201: at most 200 projects"}}, r.dropped)

	cmds := make([]string, maxItems+1)
	for i := range cmds {
		cmds[i] = fmt.Sprintf(`{"id":"c%d","name":"n","command":"x","icon":{"kind":"agent","value":"codex"}}`, i)
	}
	r = readCommands(json.RawMessage("[" + strings.Join(cmds, ",") + "]"))
	assert.Len(t, r.items, maxItems)
	assert.Equal(t, &Dropped{Count: 1, Reasons: []string{"item 200: at most 200 commands"}}, r.dropped)

	r = readQuickReplies(json.RawMessage(quickReplyList(maxQuickReplies + 2)))
	assert.Len(t, r.items, maxQuickReplies)
	assert.Equal(t, &Dropped{Count: 2, Reasons: []string{"item 20: at most 20 quick replies", "item 21: at most 20 quick replies"}}, r.dropped)
}

// A value that is not JSON, or not the collection's container, is invalid:
// the empty value, no rows dropped.
func TestRead_NotTheContainerIsInvalid(t *testing.T) {
	lists := map[string]func(json.RawMessage) readout{"projects": readProjects, "commands": readCommands, "quick replies": readQuickReplies}
	for _, raw := range []string{`[{"id":`, `{}`, `null`, `"x"`, ``, `[] []`} {
		for name, read := range lists {
			r := read(json.RawMessage(raw))
			assert.Error(t, r.invalid, "%s %q", name, raw)
			assert.Nil(t, r.dropped, "%s %q", name, raw)
			assert.Equal(t, `[]`, jsonOf(t, r.items), "%s %q", name, raw)
		}
	}
	for _, raw := range []string{`[]`, `null`, `{"cc":`, `"x"`, ``} {
		r := readResumeTemplates(json.RawMessage(raw))
		assert.Error(t, r.invalid, raw)
		assert.Nil(t, r.dropped, raw)
		assert.Equal(t, `{}`, jsonOf(t, r.items), raw)
	}
}

func TestReadResumeTemplates_DropsTheEntryThatBreaksARule(t *testing.T) {
	cc := ResumeTemplatePair{Exact: "cld --resume {id}", Fallback: "cld -c"}
	for entry, why := range map[string]string{
		`"CC":{"exact":"","fallback":""}`:   `invalid agent type "CC"`,
		`"codex":"oops"`:                    `not a template pair for "codex"`,
		`"codex":null`:                      `not a template pair for "codex"`,
		`"codex":{"exact":5,"fallback":""}`: `not a template pair for "codex"`,
		`"codex":{"exact":"` + strings.Repeat("x", 4097) + `","fallback":""}`: `invalid template for "codex"`,
		`"codex":{"exact":"","fallback":"a\u0000"}`:                           `invalid template for "codex"`,
	} {
		r := readResumeTemplates(json.RawMessage(`{"cc":{"exact":"cld --resume {id}","fallback":"cld -c"},` + entry + `}`))
		require.NoError(t, r.invalid, entry)
		assert.Equal(t, map[string]ResumeTemplatePair{"cc": cc}, r.items, entry)
		assert.Equal(t, &Dropped{Count: 1, Reasons: []string{why}}, r.dropped, entry)
	}
}

// Go's map order is random, so the 32-agent cap keeps the first 32 VALID
// agents in sorted key order: every GET answers the same set.
func TestReadResumeTemplates_CapKeepsTheFirstValidAgentsInSortedOrder(t *testing.T) {
	entries := []string{`"BAD":{"exact":"","fallback":""}`} // sorts before every lower-case key
	for i := maxResumeAgents + 1; i >= 0; i-- {             // 34 valid agents, written out of order
		entries = append(entries, fmt.Sprintf(`"a%02d":{"exact":"","fallback":""}`, i))
	}
	raw := json.RawMessage("{" + strings.Join(entries, ",") + "}")
	for range 5 {
		r := readResumeTemplates(raw)
		got := r.items.(map[string]ResumeTemplatePair)
		require.Len(t, got, maxResumeAgents)
		assert.Contains(t, got, "a00")
		assert.Contains(t, got, "a31")
		assert.NotContains(t, got, "a32")
		assert.Equal(t, &Dropped{Count: 3, Reasons: []string{
			`invalid agent type "BAD"`, `at most 32 agents; "a32" left out`, `at most 32 agents; "a33" left out`,
		}}, r.dropped)
	}
}

// The relay switches fail closed, as RelaySwitches() reads them: a value
// whose fields or switches do not read is invalid and answers both off —
// never the defaults (on).
func TestReadRelay_SwitchesFailClosed(t *testing.T) {
	for _, raw := range []string{
		`{"self_solo":true,"bogus":1}`, `{"self_leaad":false}`, `{"self_lead":null}`, `{"self_solo":"yes"}`,
		`[true]`, `null`, `{"self_solo":`, ``,
	} {
		r := readRelay(json.RawMessage(raw))
		assert.Error(t, r.invalid, raw)
		assert.Nil(t, r.dropped, raw)
		assert.JSONEq(t, `{"self_solo":false,"self_lead":false}`, jsonOf(t, r.items), raw)
	}
}

// A prompt body relayPromptsOf refuses is dropped on its own ("" = the
// default); the switches and the other bodies are kept.
func TestReadRelay_ABadPromptBodyIsDroppedAlone(t *testing.T) {
	for body, why := range map[string]string{
		`null`:                "prompt_fix must be a string",
		`3`:                   "prompt_fix must be a string",
		`"see [pdx-relay x]"`: "prompt_fix: a relay prompt may not contain [pdx-relay: the machine tag is the mod's",
		`"a\rb"`:              "prompt_fix: a relay prompt may hold no control character but newline and tab (found U+000D)",
		`"` + strings.Repeat("a", team.RelayPromptMaxBytes+1) + `"`: "prompt_fix: a relay prompt may be at most 16384 bytes",
		string([]byte{'"', 'o', 'k', 0xff, '"'}):                    "prompt_fix: a relay prompt must be UTF-8",
	} {
		raw := `{"self_solo":false,"prompt_write":"W","prompt_fix":` + body + `,"prompt_seed":"  "}`
		r := readRelay(json.RawMessage(raw))
		require.NoError(t, r.invalid, "%.40s", body)
		assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true, PromptWrite: "W"}, r.items, "%.40s", body)
		assert.Equal(t, &Dropped{Count: 1, Reasons: []string{why}}, r.dropped, "%.40s", body)
	}

	r := readRelay(json.RawMessage(`{"self_lead":false,"prompt_write":null,"prompt_seed":"[pdx-relay"}`))
	assert.Equal(t, RelaySwitches{SelfSolo: true, SelfLead: false}, r.items)
	assert.Equal(t, 2, r.dropped.Count)
}

// The team row is one setting: a value normalizeTeam refuses is invalid and
// answers {} — never the default command, which its owner did not write.
func TestReadTeam_InvalidAnswersNoCommand(t *testing.T) {
	for _, raw := range []string{`{"member_command":"claude; rm"}`, `{"member_command":null}`, `{"member_comand":"x"}`, `[]`, `{`, ``} {
		r := readTeam(json.RawMessage(raw))
		assert.Error(t, r.invalid, raw)
		assert.Nil(t, r.dropped, raw)
		assert.Equal(t, `{}`, jsonOf(t, r.items), raw)
	}
}

// The count is exact; the reasons are the first ten, each at most 200 bytes,
// cut on a rune boundary.
func TestRead_ReasonsAreCappedAtTenAnd200Bytes(t *testing.T) {
	rows := make([]string, 15)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":"q%d","text":""}`, i)
	}
	r := readQuickReplies(json.RawMessage("[" + strings.Join(rows, ",") + "]"))
	assert.Equal(t, 15, r.dropped.Count)
	require.Len(t, r.dropped.Reasons, maxDroppedReasons)
	assert.Equal(t, `item 9: invalid text for quick reply "q9"`, r.dropped.Reasons[9])

	// `item 0: invalid id "x` is 21 bytes, so byte 197 falls inside a rune.
	r = readProjects(json.RawMessage(`[{"id":"x` + strings.Repeat("接", 300) + `","name":"n","slug":"s","path":"/"}]`))
	why := r.dropped.Reasons[0]
	assert.LessOrEqual(t, len(why), droppedReasonMaxBytes)
	assert.True(t, utf8.ValidString(why), why)
	assert.True(t, strings.HasPrefix(why, `item 0: invalid id "x接接`), why)
	assert.True(t, strings.HasSuffix(why, "接…"), why)
}
