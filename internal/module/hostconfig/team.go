package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
// MemberCommand is one simple command the host's owner wrote (the PUT takes
// the admin token only). The launch line types its words re-quoted, then
// the daemon's own flags, which therefore always reach the command.
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
// trimmed and must then pass ParseMemberCommand; a field left out keeps
// its default. The text is stored as written, not re-quoted.
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
		if _, err := ParseMemberCommand(out.MemberCommand); err != nil {
			return TeamSettings{}, err
		}
	}
	return out, nil
}

// readTeam is normalizeTeam's lenient twin (the GET's view). The row is one
// setting, so a value that does not validate is invalid and answers {} —
// never the default command, which its owner did not write (TeamSettings()
// refuses it too).
func readTeam(raw json.RawMessage) readout {
	ts, err := normalizeTeam(raw)
	if err != nil {
		return readout{items: struct{}{}, invalid: err}
	}
	return readout{items: ts}
}

// MemberArgv is a parsed member_command: its leading NAME=value
// assignments, then the command word and its arguments.
type MemberArgv struct {
	Env  []string // "NAME=value", in order
	Args []string // Args[0] is the command; never empty
}

// memberForbidden are the bytes refused outside quotes. Each would make a
// shell read the line as other than one simple command of literal words: a
// comment, an operator, a redirection, an expansion (~ anywhere: bash and
// zsh also expand it after =), a glob, a history expansion or an escape.
const memberForbidden = "#;&|<>()`$\\{}*?[~!"

var assignName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseMemberCommand splits a trimmed member_command into one simple
// command (R2 finding 1): words split on spaces, made of plain bytes, '…'
// and "…" (no $, ` or \ inside). A leading word whose unquoted prefix is
// NAME= is an assignment. Blank, untrimmed, over 512 bytes, not UTF-8, a
// control character, an unterminated quote and no command word are
// refused too. The launch line re-quotes every word, so the flags appended
// after them are read as flags whatever the text.
func ParseMemberCommand(s string) (MemberArgv, error) {
	switch {
	case s == "" || strings.TrimSpace(s) != s:
		return MemberArgv{}, errors.New("member_command must be trimmed and not blank")
	case len(s) > memberCommandMaxBytes:
		return MemberArgv{}, fmt.Errorf("member_command is longer than %d bytes", memberCommandMaxBytes)
	case !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0:
		return MemberArgv{}, errors.New("member_command must be one line of UTF-8 without control characters")
	}
	var out MemberArgv
	var w strings.Builder
	inWord, quoted, eq := false, false, -1 // eq: offset of the first '=' while nothing before it was quoted
	flush := func() {
		if text := w.String(); inWord && len(out.Args) == 0 && eq > 0 && assignName.MatchString(text[:eq]) {
			out.Env = append(out.Env, text)
		} else if inWord {
			out.Args = append(out.Args, text)
		}
		w.Reset()
		inWord, quoted, eq = false, false, -1
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ':
			flush()
			i++
		case c == '\'' || c == '"':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				return MemberArgv{}, fmt.Errorf("member_command has an unterminated %c quote", c)
			}
			in := s[i+1 : i+1+j]
			if c == '"' && strings.ContainsAny(in, "$`\\") {
				return MemberArgv{}, errors.New("member_command: $, ` and \\ are refused inside double quotes")
			}
			w.WriteString(in)
			inWord, quoted = true, true
			i += j + 2
		case strings.IndexByte(memberForbidden, c) >= 0:
			return MemberArgv{}, fmt.Errorf("member_command: %q outside quotes would change how the shell reads the line", c)
		default:
			if c == '=' && eq < 0 && !quoted {
				eq = w.Len()
			}
			w.WriteByte(c)
			inWord = true
			i++
		}
	}
	flush()
	if len(out.Args) == 0 || out.Args[0] == "" {
		return MemberArgv{}, errors.New("member_command needs a command after its assignments")
	}
	return out, nil
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
