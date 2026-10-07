package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/wake/purdex/internal/team"
)

// KeyTeam is the host_config row of the team settings (lead-team-relay
// spec §7.2 step 4): per host, read by the team module when it launches a
// member. P4b-2 adds the repo-selection fields to the same row.
const KeyTeam = "team"

// TeamSettingsKey is the service-registry key under which Init publishes
// the module as a TeamSettingsReader for the team module.
const TeamSettingsKey = "hostconfig.team-settings"

// memberCommandMaxBytes bounds team.member_command.
const memberCommandMaxBytes = 512

// TeamSettings is the stored shape and the GET field `team.items`.
//
// MemberCommand is shell text the host's owner wrote (PUT /api/hostconfig/team
// is admin-only: peers.HostRoutePolicy refuses every host principal). The
// daemon types it into the member's shell unquoted and appends its own,
// quoted, flags after it (team module launchLine), so it must end with the
// command that takes those flags. Validation keeps it to one line; what the
// line does is the owner's choice.
type TeamSettings struct {
	MemberCommand string `json:"member_command"`
}

// DefaultTeamSettings is what a host that never wrote the row reads as.
var DefaultTeamSettings = TeamSettings{MemberCommand: team.DefaultMemberCommand}

// teamSettingsJSON is DefaultTeamSettings as the GET answers it for a
// never-written key (emptyFor).
var teamSettingsJSON = func() string {
	b, err := json.Marshal(DefaultTeamSettings)
	if err != nil {
		panic(err) // a struct of strings always encodes
	}
	return string(b)
}()

// TeamSettingsReader is what the team module type-asserts on the registry value.
type TeamSettingsReader interface {
	TeamSettings() (TeamSettings, error)
}

// normalizeTeam validates a PUT body (and a stored value): a JSON object
// whose only field is member_command; a field left out keeps its default.
// The command is trimmed and must then be non-blank, at most 512 bytes,
// free of control characters (a newline would end the typed line early)
// and must not end with a backslash (it would escape the space before the
// appended --plugin-dir and swallow it into one word).
func normalizeTeam(raw json.RawMessage) (TeamSettings, error) {
	if firstByte(raw) != '{' {
		return TeamSettings{}, errors.New("items must be a JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return TeamSettings{}, errors.New("items must be a JSON object")
	}
	for k := range fields {
		if k != "member_command" {
			return TeamSettings{}, errors.New("unknown team field " + k + "; only member_command")
		}
	}
	out := DefaultTeamSettings
	v, present := fields["member_command"]
	if !present {
		return out, nil
	}
	var s string
	if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &s) != nil {
		return TeamSettings{}, errors.New("member_command must be a string")
	}
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return TeamSettings{}, errors.New("member_command must not be blank")
	case len(s) > memberCommandMaxBytes:
		return TeamSettings{}, fmt.Errorf("member_command is longer than %d bytes", memberCommandMaxBytes)
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return TeamSettings{}, errors.New("member_command must be one line without control characters")
	case strings.HasSuffix(s, `\`):
		return TeamSettings{}, errors.New("member_command must not end with a backslash")
	}
	out.MemberCommand = s
	return out, nil
}

// TeamSettings reads the stored settings, defaults for a never-written key.
// A stored value that no longer validates is an error, not a silent
// default: the team module then fails the spawn rather than launch a
// command its owner did not write.
func (m *Module) TeamSettings() (TeamSettings, error) {
	e, err := m.store.Get(KeyTeam)
	if err != nil {
		return TeamSettings{}, err
	}
	if e.Value == nil {
		return DefaultTeamSettings, nil
	}
	return normalizeTeam(e.Value)
}
