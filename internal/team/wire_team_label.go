package team

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/textwidth"
)

// A team label (spec 2026-10-09 team-label D-L1..D-L3): the short name shown
// at the front of the team's tab group; the team name (wire_team_name.go) is
// the long one shown in the team panel.

// MaxTeamLabelWidth is the most a label may weigh: about five Chinese
// characters (each weighs 2). The weight is textwidth's, a product-defined
// count per code point, not a promise about any terminal.
const MaxTeamLabelWidth = 10

// ErrTeamLabelInvalid is wrapped by every error NormaliseTeamLabel returns.
var ErrTeamLabelInvalid = errors.New("team label invalid")

// NormaliseTeamLabel is the one rule for a team label (D-L1): leading and
// trailing white space is trimmed, an empty result means "no label", anything
// else must pass the title rule (valid UTF-8, printable, at most 64 bytes) and
// weigh at most MaxTeamLabelWidth.
func NormaliseTeamLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if err := peers.ValidateTitle(s); err != nil {
		return "", fmt.Errorf("%w: %s", ErrTeamLabelInvalid, strings.TrimPrefix(err.Error(), peers.ErrTitleInvalid.Error()+": "))
	}
	if textwidth.CellWidth(s) == 0 {
		// Printable, but nothing to see (a lone variation selector, bare
		// combining marks): it would stand for "no label" without being one.
		return "", fmt.Errorf("%w: no visible character", ErrTeamLabelInvalid)
	}
	if w := textwidth.CellWidth(s); w > MaxTeamLabelWidth {
		return "", fmt.Errorf("%w: weighs %d, at most %d (about five Chinese characters)", ErrTeamLabelInvalid, w, MaxTeamLabelWidth)
	}
	return s, nil
}

// DeriveTeamLabel is what a team without a label of its own gets from its name
// (D-L3): the part before the name's first separator, if that part is not
// empty and passes NormaliseTeamLabel; a name with no separator is its own
// first part. Otherwise "". It never cuts a name in the middle: a part that is
// too wide is not shortened, there is no label.
//
// The separators ： : ／ | ｜ － — count wherever they stand; the ASCII - and /
// count only with white space on both sides, because inside an English
// compound (resource-lease) or an identifier (I/O) they are not separators.
func DeriveTeamLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) { // the byte offsets below are those of valid UTF-8
		return ""
	}
	first := len(name)
	runes := []rune(name)
	offset := 0
	for i, r := range runes {
		sep := false
		switch r {
		case '：', ':', '／', '|', '｜', '－', '—':
			sep = true
		case '-', '/':
			sep = i > 0 && i+1 < len(runes) && unicode.IsSpace(runes[i-1]) && unicode.IsSpace(runes[i+1])
		}
		if sep {
			first = offset
			break
		}
		offset += utf8.RuneLen(r)
	}
	label, err := NormaliseTeamLabel(name[:first])
	if err != nil {
		return ""
	}
	return label
}
