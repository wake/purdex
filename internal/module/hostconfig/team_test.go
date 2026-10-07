package hostconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// Spec §7.2 step 4: team.member_command defaults to cld-yolo's expansion; a
// field left out keeps it. Unknown fields, null, and a command
// ParseMemberCommand refuses are refused: it is typed into a shell.
func TestNormalizeTeam_DefaultsUnknownFieldNullAndBlank(t *testing.T) {
	assert.Equal(t, team.DefaultMemberCommand, DefaultTeamSettings.MemberCommand)
	for raw, want := range map[string]string{
		`{}`:                                  team.DefaultMemberCommand,
		`{"member_command":"  cld --x  "}`:    "cld --x",
		`{"member_command":"printf '%s\\n'"}`: `printf '%s\n'`, // backslash-n is two bytes, not a newline
		`{"member_command":"` + strings.Repeat("a", 512) + `"}`: strings.Repeat("a", 512),
	} {
		got, err := normalizeTeam(json.RawMessage(raw))
		require.NoError(t, err, raw)
		assert.Equal(t, TeamSettings{MemberCommand: want}, got, raw)
	}
	for _, raw := range []string{
		`[]`, `"claude"`, `null`, `{"member_command":null}`, `{"member_command":1}`,
		`{"member_command":""}`, `{"member_command":"   "}`,
		`{"member_command":"claude\nrm -rf ~"}`, `{"member_command":"claude\r--x"}`, `{"member_command":"a\tb"}`,
		`{"member_command":"a\u0000b"}`, `{"member_command":"a\u007fb"}`, `{"member_command":"a\u0085b"}`,
		`{"member_command":"claude \\"}`, `{"member_command":"claude #"}`, `{"member_command":"claude; rm"}`,
		`{"member_command":"` + strings.Repeat("a", 513) + `"}`,
		`{"membercommand":"claude"}`, `{"member_command":"claude","extra":1}`,
	} {
		_, err := normalizeTeam(json.RawMessage(raw))
		assert.Error(t, err, raw)
	}
}

// member_command is one simple command (R2 finding 1): leading NAME=value
// assignments, then the command and its words, split on spaces, with single
// quotes and double quotes (no $, ` or \ inside). Anything that would make
// the shell read the appended flags differently is refused: a comment, an
// operator, a redirection, an expansion, a glob, an escape, an unterminated
// quote, no command.
func TestParseMemberCommand_OneSimpleCommand(t *testing.T) {
	for s, want := range map[string]MemberArgv{
		team.DefaultMemberCommand:  {Args: []string{"claude", "--dangerously-skip-permissions"}},
		"  cld   --x  ":            {Args: []string{"cld", "--x"}},
		"FOO=bar B_2='x y' claude": {Env: []string{"FOO=bar", "B_2=x y"}, Args: []string{"claude"}},
		`claude --append-system-prompt 'be terse; no # $(x) ~' "a b" '' it"'"s a=b`: {
			Args: []string{"claude", "--append-system-prompt", "be terse; no # $(x) ~", "a b", "", "it's", "a=b"}},
		`'FOO=bar' claude`:        {Args: []string{"FOO=bar", "claude"}}, // a quoted name is no assignment (as in sh)
		`F"OO"=bar claude`:        {Args: []string{"FOO=bar", "claude"}},
		`claude x]y a%b @c +d ,e`: {Args: []string{"claude", "x]y", "a%b", "@c", "+d", ",e"}},
	} {
		got, err := ParseMemberCommand(strings.TrimSpace(s))
		require.NoError(t, err, s)
		assert.Equal(t, want, got, s)
	}
	for _, s := range []string{
		"claude #", "claude >/tmp/log #", "claude > /tmp/x", "claude; rm", "claude | tee", "claude $(x)", "claude &",
		"claude *", "claude a?", "claude [x]", "claude {a,b}", "claude !x", "(claude)", "claude `x`", "claude $HOME",
		"~/bin/claude", "claude --x=~/y", "claude a\\ b", "claude 'x", `claude "x`, `claude "$HOME"`, "claude \"`x`\"",
		`claude "a\b"`, "claude <x", "FOO=bar", "FOO=bar ''", "'' --x",
		"", " claude", "claude ", "claude\nrm", "a\xffb", strings.Repeat("a", 513),
	} {
		_, err := ParseMemberCommand(s)
		assert.Error(t, err, s)
	}
}

// A host that never wrote the key reads the default, both on the GET and
// through the reader the team module uses (P4-5).
func TestGetHostConfig_TeamDefault(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.Contains(t, rr.Body.String(), `"team":{"items":{"member_command":"claude --dangerously-skip-permissions"},"revision":0}`)
	ts, err := m.TeamSettings()
	require.NoError(t, err)
	assert.Equal(t, DefaultTeamSettings, ts)
}

// PUT /api/hostconfig/team stores the normalized settings under the
// revision CAS; the reader then answers them. A stored value that no
// longer validates is an error, never a silent default: the spawn runner
// would otherwise launch a command its owner did not write.
func TestTeamSettings_PutAndReader(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/team", `{"items":{"member_command":" claude --verbose "},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"member_command":"claude --verbose"},"revision":1}`, rr.Body.String())
	ts, err := m.TeamSettings()
	require.NoError(t, err)
	assert.Equal(t, TeamSettings{MemberCommand: "claude --verbose"}, ts)

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"team":{"items":{"member_command":"claude --verbose"},"revision":1}`)

	// Stale revision: 409 with the server copy.
	rr = serve(m, http.MethodPut, "/api/hostconfig/team", `{"items":{},"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"member_command":"claude --verbose"},"revision":1}`, rr.Body.String())

	// Invalid: 400, nothing stored.
	for _, body := range []string{`{"items":{"member_command":"a\nb"},"baseRevision":1}`, `{"items":{"member_command":null},"baseRevision":1}`,
		`{"items":{"member_comand":"x"},"baseRevision":1}`, `{"items":[],"baseRevision":1}`} {
		rr = serve(m, http.MethodPut, "/api/hostconfig/team", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, body)
	}
	// A stored value that fails validation (written around the PUT) is an error.
	_, stored, err := m.store.Put(KeyTeam, 1, func() (json.RawMessage, error) { return json.RawMessage(`{"member_command":"a\nb"}`), nil })
	require.True(t, stored, "the 400s stored nothing: the revision is still 1")
	require.NoError(t, err)
	_, err = m.TeamSettings()
	assert.Error(t, err)
}

// Init publishes the module under TeamSettingsKey as the TeamSettingsReader
// the team module type-asserts (P4-5); the handler tests build their Module
// without Init, so this is where dropping the registration goes red.
func TestInit_RegistersTeamSettingsReader(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(TeamSettingsKey)
	require.True(t, ok, "Init must register under TeamSettingsKey")
	reader, ok := svc.(TeamSettingsReader)
	require.True(t, ok, "registry value must be a TeamSettingsReader, got %T", svc)
	ts, err := reader.TeamSettings()
	require.NoError(t, err)
	assert.Equal(t, DefaultTeamSettings, ts)
}
