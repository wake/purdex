package hostconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// Lead-team-relay spec §8.7 (a): the relay switches are a host config
// section; both default to true, a PUT may set either, the reader the team
// module uses answers the stored value or the defaults.
func TestRelaySwitches_DefaultsPutAndReader(t *testing.T) {
	m := newTestModule(t)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, DefaultRelaySwitches, sw)

	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())
	sw, err = m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true}, sw)

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"relay":{"items":{"self_solo":false,"self_lead":true},"revision":1}`)

	// Stale revision: 409 with the server copy.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_lead":false},"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())

	// Not an object, or not booleans: 400, nothing stored.
	for _, body := range []string{`{"items":[true],"baseRevision":1}`, `{"items":{"self_solo":"yes"},"baseRevision":1}`,
		`{"items":{"self_solo":null},"baseRevision":1}`, `{"items":{"self_lead":null},"baseRevision":1}`, `{"items":{"self_lead":1},"baseRevision":1}`,
		`{"items":{"self_leaad":false},"baseRevision":1}`, `{"items":{"self_lead":false,"extra":1},"baseRevision":1}`} {
		rr = serve(m, http.MethodPut, "/api/hostconfig/relay", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, body)
	}
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision)
}

// Init publishes the module under RelaySwitchesKey as the RelaySwitchReader
// the team module type-asserts (spec §8.7 (a)); the plan's mutation gate
// "drop RelaySwitchesKey from Init → red" lands here, since the handler
// tests build their Module without Init.
func TestInit_RegistersRelaySwitchReader(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(RelaySwitchesKey)
	require.True(t, ok, "Init must register under RelaySwitchesKey")
	reader, ok := svc.(RelaySwitchReader)
	require.True(t, ok, "registry value must be a RelaySwitchReader, got %T", svc)
	sw, err := reader.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, DefaultRelaySwitches, sw)
	// ... and under RelayPromptsKey as the RelayPromptReader (spec §8.8).
	svc, ok = c.Registry.Get(RelayPromptsKey)
	require.True(t, ok, "Init must register under RelayPromptsKey")
	prompts, ok := svc.(RelayPromptReader)
	require.True(t, ok, "registry value must be a RelayPromptReader, got %T", svc)
	p, err := prompts.RelayPrompts()
	require.NoError(t, err)
	assert.Equal(t, team.RelayPromptBodies{}, p)
}

// Spec §8.8, U21 (a)/(d): prompt_write|fix|seed are strings (null and other
// types refused, as for the switches); whitespace only is stored as ""
// (the default); a body over 16 KiB, with the tag or a control character
// is a ValidationError (400) and nothing is stored; an unknown key is
// still refused, naming the five known ones.
func TestNormalizeRelay_PromptFields(t *testing.T) {
	got, err := normalizeRelay(json.RawMessage(`{"self_lead":false,"prompt_write":"寫 {{path}}\n\tok","prompt_fix":" \n\t ","prompt_seed":"  seed  "}`))
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: true, SelfLead: false, PromptWrite: "寫 {{path}}\n\tok", PromptSeed: "  seed  "}, got)

	for _, raw := range []string{
		`{"prompt_write":null}`, `{"prompt_fix":1}`, `{"prompt_seed":["x"]}`, `{"prompt_write":{}}`, `{"prompt_seed":true}`,
		`{"prompt_write":"` + strings.Repeat("a", team.RelayPromptMaxBytes+1) + `"}`,
		`{"prompt_fix":"x [pdx-relay op=1 n=2]"}`, `{"prompt_seed":"a\rb"}`, `{"prompt_write":"a\u0000b"}`, `{"prompt_write":"a\u0085b"}`,
	} {
		_, err := normalizeRelay(json.RawMessage(raw))
		assert.Error(t, err, "%.60s", raw)
	}
	_, err = normalizeRelay(json.RawMessage(`{"prompt_writ":"x"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "self_solo, self_lead, prompt_write, prompt_fix and prompt_seed")

	m := newTestModule(t)
	for _, items := range []string{`{"prompt_fix":"[pdx-relay"}`, `{"prompt_seed":"a\u007fb"}`, `{"prompt_write":null}`,
		`{"prompt_write":"` + strings.Repeat("a", team.RelayPromptMaxBytes+1) + `"}`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, "%.60s", items)
	}
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Nil(t, e.Value, "a refused PUT stores nothing")

	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"prompt_write":"`+strings.Repeat("a", team.RelayPromptMaxBytes)+`"},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, "16 384 bytes are allowed")
}

// encoding/json decodes an invalid byte to U+FFFD, so the raw bytes of the
// field are checked before decoding (plan review). Mutation gate: check
// only the decoded string → a U+FFFD body is stored → red.
func TestNormalizeRelay_InvalidUTF8InTheRawBodyIs400(t *testing.T) {
	m := newTestModule(t)
	body := string([]byte(`{"items":{"self_solo":true,"prompt_write":"ok `)) + string([]byte{0xff, 0xfe}) + ` end"},"baseRevision":0}`
	require.False(t, utf8.ValidString(body), "the fixture must carry raw invalid bytes")
	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", body)
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "prompt_write")
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Nil(t, e.Value, "nothing stored, not a U+FFFD body")
	assert.Equal(t, int64(0), e.Revision)
}

// omitempty keeps a row without bodies exactly as before P9a: the
// never-written default and a switches-only PUT both read as
// relaySwitchesJSON byte for byte. Mutation gate: remove omitempty → red.
func TestGetHostConfig_RelayDefaultUnchanged(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.Contains(t, rr.Body.String(), `"relay":{"items":`+relaySwitchesJSON+`,"revision":0}`)
	b, err := json.Marshal(DefaultRelaySwitches)
	require.NoError(t, err)
	assert.Equal(t, relaySwitchesJSON, string(b))
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Equal(t, relaySwitchesJSON, string(e.Value))
}

// The reader the team module serves GET /api/relay/prompts from: "" for
// every unset body, the stored text otherwise; a body PUT keeps the
// switches it was sent with.
func TestRelayPrompts_DefaultsAndStored(t *testing.T) {
	m := newTestModule(t)
	p, err := m.RelayPrompts()
	require.NoError(t, err)
	assert.Equal(t, team.RelayPromptBodies{}, p)

	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false,"self_lead":true,"prompt_write":"W {{path}}","prompt_seed":"S"},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true,"prompt_write":"W {{path}}","prompt_seed":"S"},"revision":1}`, rr.Body.String())
	p, err = m.RelayPrompts()
	require.NoError(t, err)
	assert.Equal(t, team.RelayPromptBodies{Write: "W {{path}}", Seed: "S"}, p)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true}, sw, "the switch reader answers the switches only")
}

// A stored body that no longer validates (written around the PUT) must not
// make the switches an error — that would turn self relay's begin into a
// 503 — while the prompts reader reports it. Mutation gate: let
// RelaySwitches() validate the bodies → red.
func TestRelaySwitches_IgnoreABadStoredPrompt(t *testing.T) {
	m := newTestModule(t)
	_, stored, err := m.store.Put(KeyRelay, 0, func() (json.RawMessage, error) {
		return json.RawMessage(`{"self_solo":false,"self_lead":true,"prompt_fix":"[pdx-relay op=x"}`), nil
	})
	require.NoError(t, err)
	require.True(t, stored)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true}, sw)
	_, err = m.RelayPrompts()
	assert.Error(t, err)
}

// 還原預設 is a PUT whose field is "": the row then holds no prompt_* key.
func TestPutRelay_RestoreDefaultClearsTheKey(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":true,"self_lead":true,"prompt_fix":"F"},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":true,"self_lead":true,"prompt_fix":""},"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, `{"items":`+relaySwitchesJSON+`,"revision":2}`+"\n", rr.Body.String())
	p, err := m.RelayPrompts()
	require.NoError(t, err)
	assert.Equal(t, team.RelayPromptBodies{}, p)
}
