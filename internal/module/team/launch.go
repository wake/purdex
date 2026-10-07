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
//	<member_command> --plugin-dir '<dir>'[ --model '<m>'][ --effort <e>]
//
// The trust boundary, since the line runs in the member's shell:
//   - memberCommand is the owner's shell text (team.member_command, an
//     admin-only PUT), typed as it is; it must end with the command that
//     takes the flags. It is re-checked to be one non-blank line
//     (hostconfig.ValidateMemberCommand), so it cannot end the line early.
//   - What the daemon appends is single-quoted for a POSIX-family shell
//     (sh, bash, zsh): pluginDir (an absolute path of one line) and the model
//     (ValidModel; "[1m]" would glob). The effort is a bare enum. A refused
//     model or effort is an error and composes nothing.
func launchLine(memberCommand, pluginDir, model, effort string) (string, error) {
	cmd := strings.TrimSpace(memberCommand)
	if err := hostconfig.ValidateMemberCommand(cmd); err != nil {
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
	line := cmd + " --plugin-dir " + shellQuote(pluginDir)
	if model != "" {
		line += " --model " + shellQuote(model)
	}
	if effort != "" {
		line += " --effort " + effort
	}
	return line, nil
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
