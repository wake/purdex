// Package locale guarantees the daemon process exports a UTF-8 character
// locale before it execs tmux.
//
// tmux sanitises format output per the *client's* locale: without a UTF-8
// codeset every non-printable byte — including the TAB the daemon uses as a
// field separator — is replaced by "_", which breaks every tab-separated
// parser. The tmux server the daemon spawns also inherits this environment,
// so fixing it here fixes every pane shell and agent transitively.
package locale

import (
	"os"
	"strings"
)

// DefaultLocale is exported as LANG when no locale is set at all. LANG (not
// LC_ALL) so a pane shell's rc file can still override it.
const DefaultLocale = "en_US.UTF-8"

// Action describes what EnsureUTF8 did.
type Action int

const (
	// Kept: a UTF-8 locale was already in effect; nothing changed.
	Kept Action = iota
	// Set: no locale was set; LANG was exported to DefaultLocale.
	Set
	// Warned: an explicit non-UTF-8 locale is in effect; left untouched.
	Warned
)

func (a Action) String() string {
	switch a {
	case Kept:
		return "kept"
	case Set:
		return "set"
	case Warned:
		return "warned"
	default:
		return "unknown"
	}
}

// Result reports the action taken and the locale value in effect afterwards.
type Result struct {
	Action Action
	Value  string
}

// EnsureUTF8 makes sure the process exports a UTF-8 character locale.
// Returns what it did so the caller can log it.
//
// Effective LC_CTYPE follows POSIX precedence: LC_ALL > LC_CTYPE > LANG
// (empty = unset). The UTF-8 test is the same string match tmux performs in
// tmux.c ("UTF-8" / "UTF8", case-insensitive) — it never consults
// nl_langinfo, so the locale need not be installed.
func EnsureUTF8() Result {
	effective := ""
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			effective = v
			break
		}
	}

	switch {
	case effective == "":
		os.Setenv("LANG", DefaultLocale)
		return Result{Action: Set, Value: DefaultLocale}
	case isUTF8(effective):
		return Result{Action: Kept, Value: effective}
	default:
		return Result{Action: Warned, Value: effective}
	}
}

// isUTF8 reports whether a locale string names a UTF-8 codeset.
func isUTF8(v string) bool {
	u := strings.ToUpper(v)
	return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
}
