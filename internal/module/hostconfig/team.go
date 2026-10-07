package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/team"
)

// KeyTeam is the host_config row of the team settings (lead-team-relay
// spec §7.2 step 4); P4b-2 adds the repo-selection fields to it.
const KeyTeam = "team"

// TeamSettingsKey is the registry key of the module as a TeamSettingsReader.
const TeamSettingsKey = "hostconfig.team-settings"

const memberCommandMaxBytes = 512

// TeamSettings is the stored shape and the GET field `team.items`.
// MemberCommand is shell text the host's owner wrote (the PUT is admin-only:
// peers.HostRoutePolicy refuses every host principal). The daemon types it
// unquoted and appends its own quoted flags (team module launchLine), so it
// must end with the command that takes them; validation keeps it one line.
type TeamSettings struct {
	MemberCommand string `json:"member_command"`
}

// DefaultTeamSettings is what a host that never wrote the row reads as.
var DefaultTeamSettings = TeamSettings{MemberCommand: team.DefaultMemberCommand}

// teamSettingsJSON is DefaultTeamSettings as the GET answers it for a
// never-written key (emptyFor); the default needs no JSON escaping.
const teamSettingsJSON = `{"member_command":"` + team.DefaultMemberCommand + `"}`

// TeamSettingsReader is what the team module type-asserts on the registry value.
type TeamSettingsReader interface {
	TeamSettings() (TeamSettings, error)
}

// normalizeTeam validates a PUT body (and a stored value): a JSON object
// whose only field is member_command, a string (null refused) that is
// trimmed and must then pass ValidateMemberCommand; a field left out keeps
// its default.
func normalizeTeam(raw json.RawMessage) (TeamSettings, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return TeamSettings{}, errors.New("items must be a JSON object")
	}
	for k := range fields {
		if k != "member_command" {
			return TeamSettings{}, errors.New("unknown team field " + k + "; only member_command")
		}
	}
	out := DefaultTeamSettings
	if v, present := fields["member_command"]; present {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &out.MemberCommand) != nil {
			return TeamSettings{}, errors.New("member_command must be a string")
		}
		out.MemberCommand = strings.TrimSpace(out.MemberCommand)
		if err := ValidateMemberCommand(out.MemberCommand); err != nil {
			return TeamSettings{}, err
		}
	}
	return out, nil
}

// ValidateMemberCommand is the rule for a trimmed member_command, shared by
// normalizeTeam and the team module's launch line (which checks again right
// before typing it): non-blank, at most 512 bytes of UTF-8, no control
// character (a newline would end the typed line early), and no trailing
// backslash (it would escape the space before the appended --plugin-dir).
func ValidateMemberCommand(s string) error {
	switch {
	case s == "" || strings.TrimSpace(s) != s:
		return errors.New("member_command must be trimmed and not blank")
	case len(s) > memberCommandMaxBytes:
		return fmt.Errorf("member_command is longer than %d bytes", memberCommandMaxBytes)
	case !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0:
		return errors.New("member_command must be one line of UTF-8 without control characters")
	case strings.HasSuffix(s, `\`):
		return errors.New("member_command must not end with a backslash")
	}
	return nil
}

// TeamSettings reads the stored settings, defaults for a never-written key.
// A stored value that no longer validates is an error, not a silent
// default: the spawn then fails rather than launch a command its owner did
// not write.
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
