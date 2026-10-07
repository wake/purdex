package teammod

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// launchLine is the line the spawn runner types into a member's window 0
// (spec §7.2 step 4, U20 (a)), without the newline the runner adds:
//
//	NAME='v'… 'cmd' 'arg'… --plugin-dir '<dir>'[ --model '<m>'][ --effort <e>]
//
// Everything is single-quoted for a POSIX-family shell (sh, bash, zsh) but
// the flag names, the effort enum and assignment names, so nothing in the
// line is read as anything but literal words:
//   - memberCommand (team.member_command, the owner's text through an
//     admin-only PUT) is parsed again here as one simple command
//     (hostconfig.ParseMemberCommand) and each word re-quoted, so it cannot
//     comment out, redirect or end early the flags after it (R2 finding 1);
//   - pluginDir must be an absolute path of one line; the model passes
//     ValidModel ("[1m]" would glob unquoted) and the effort ValidEffort.
//
// Anything refused is an error and composes nothing.
func launchLine(memberCommand, pluginDir, model, effort string) (string, error) {
	argv, err := hostconfig.ParseMemberCommand(strings.TrimSpace(memberCommand))
	if err != nil {
		return "", fmt.Errorf("launch line: %w", err)
	}
	if !filepath.IsAbs(pluginDir) || !utf8.ValidString(pluginDir) || strings.IndexFunc(pluginDir, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("launch line: plugin dir %q must be an absolute path of one line", pluginDir)
	}
	if model != "" && !team.ValidModel(model) {
		return "", fmt.Errorf("launch line: invalid model %q", model)
	}
	if effort != "" && !team.ValidEffort(effort) {
		return "", fmt.Errorf("launch line: invalid effort %q", effort)
	}
	words := make([]string, 0, len(argv.Env)+len(argv.Args)+6)
	for _, kv := range argv.Env {
		name, value, _ := strings.Cut(kv, "=") // name is [A-Za-z_][A-Za-z0-9_]*
		words = append(words, name+"="+shellQuote(value))
	}
	for _, a := range argv.Args {
		words = append(words, shellQuote(a))
	}
	words = append(words, "--plugin-dir", shellQuote(pluginDir))
	if model != "" {
		words = append(words, "--model", shellQuote(model))
	}
	if effort != "" {
		words = append(words, "--effort", effort)
	}
	return strings.Join(words, " "), nil
}

// shellQuote single-quotes s for a POSIX-family shell. Inside single quotes
// nothing is special but the quote itself, which becomes (close, escaped
// quote, reopen):
//
//	'\''
//
// The rule of internal/agent/cc's unexported shellSingleQuote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
