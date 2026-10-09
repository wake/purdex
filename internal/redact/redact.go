// Package redact masks credentials in text the daemon is about to send to a model or store (session workbook spec §8).
// It is the runtime subset of the patterns in convmodel/ccnorm/scrub, plus the Purdex token prefixes, with no import of
// the normaliser.
package redact

import "regexp"

// Mask replaces every secret.
const Mask = "[redacted]"

var (
	// the prefix patterns, tried before the long-run rule; each one swallows its whole token
	prefixed = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bBearer\s+[^\s"'` + "`" + `]+`),
		regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9_]{8,}`),
		regexp.MustCompile(`(?i)\bxox[a-z]-[A-Za-z0-9\-]{4,}`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bpdx[pd]_[A-Za-z0-9_\-]+`),
	}
	// a run of 32 or more letters and digits is redacted when it mixes both (D8): a key, a hash, a session secret
	longRun  = regexp.MustCompile(`[A-Za-z0-9]{32,}`)
	hasAlpha = regexp.MustCompile(`[A-Za-z]`)
	hasDigit = regexp.MustCompile(`[0-9]`)
)

// String returns s with secrets replaced by Mask. Applying it again changes nothing.
func String(s string) string {
	for _, re := range prefixed {
		s = re.ReplaceAllString(s, Mask)
	}
	return longRun.ReplaceAllStringFunc(s, func(run string) string {
		if hasAlpha.MatchString(run) && hasDigit.MatchString(run) {
			return Mask
		}
		return run
	})
}
