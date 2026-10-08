package team

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/peers"
)

// ErrTeamNameInvalid is what NormaliseTeamName wraps for a name that breaks
// the rule; the daemon answers 400 bad_request, the CLI exits before asking.
var ErrTeamNameInvalid = errors.New("team name invalid")

// NormaliseTeamName is the one rule for a team name (spec D-N2): leading and
// trailing white space is trimmed, an empty result means "no name", anything
// else must pass the title rule (1..64 bytes, valid UTF-8, every rune
// printable — a name is printed into terminal tables).
func NormaliseTeamName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if err := peers.ValidateTitle(s); err != nil {
		return "", fmt.Errorf("%w: %s", ErrTeamNameInvalid, strings.TrimPrefix(err.Error(), peers.ErrTitleInvalid.Error()+": "))
	}
	return s, nil
}
