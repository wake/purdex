package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PayloadCap is the largest section payload accepted, measured on the payload
// bytes alone — not on the request body that carries them.
const PayloadCap = 5 << 20

const nameMaxRunes = 64

// ErrPayloadTooLarge is returned by validatePayload when the payload exceeds
// PayloadCap. It is a sentinel so the handler can answer 413 instead of 400.
var ErrPayloadTooLarge = errors.New("payload exceeds 5 MiB")

var (
	profileIDPattern = regexp.MustCompile(`^p_[0-9a-f]{12}$`)
	// clientIDPattern is the client-id alphabet the SPA mints, so a client keeps one identity.
	clientIDPattern = regexp.MustCompile(`^c_[0-9a-f]{12}$`)
	// sectionPattern checks tabs.<id> structurally only: the daemon never
	// learns what a workspace is.
	sectionPattern = regexp.MustCompile(`^(hosts|settings|workspaces|tabs\.[A-Za-z0-9_-]{1,64})$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func validateProfileID(id string) error {
	if !profileIDPattern.MatchString(id) {
		return errors.New("profileId must match p_ + 12 lowercase hex")
	}
	return nil
}

func validateClientID(id string) error {
	if !clientIDPattern.MatchString(id) {
		return errors.New("clientId must match c_ + 12 lowercase hex")
	}
	return nil
}

// validateName trims a profile name and returns the trimmed value when it is
// 1–64 printable runes.
func validateName(name string) (string, error) {
	return validateDisplayName("name", name)
}

// validateDeviceName applies the same rule as validateName to a client's
// device name.
func validateDeviceName(name string) (string, error) {
	return validateDisplayName("deviceName", name)
}

// validateDisplayName trims value and returns the trimmed value when it is
// valid UTF-8, 1–64 runes long (runes, not bytes) and every rune is printable.
// unicode.IsPrint rejects control characters (C0, DEL, C1), format characters
// such as zero-width space and the bidi overrides, and every space other than
// U+0020.
func validateDisplayName(field, value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New(field + " must be valid UTF-8")
	}
	trimmed := strings.TrimSpace(value)
	n := utf8.RuneCountInString(trimmed)
	if n == 0 {
		return "", errors.New(field + " is required")
	}
	if n > nameMaxRunes {
		return "", errors.New(field + " too long")
	}
	for _, r := range trimmed {
		if !unicode.IsPrint(r) {
			return "", errors.New(field + " must be printable")
		}
	}
	return trimmed, nil
}

func validateSection(section string) error {
	if !sectionPattern.MatchString(section) {
		return errors.New("section must be hosts, settings, workspaces or tabs.<id>")
	}
	return nil
}

func validateHash(hash string) error {
	return validateSHA256Hex("hash", hash)
}

func validateFingerprint(fingerprint string) error {
	return validateSHA256Hex("fingerprint", fingerprint)
}

func validateSHA256Hex(field, value string) error {
	if !sha256Pattern.MatchString(value) {
		return errors.New(field + " must be 64 lowercase hex")
	}
	return nil
}

func validateOrdinal(ordinal int64) error {
	if ordinal < 1 {
		return errors.New("ordinal must be >= 1")
	}
	return nil
}

func validateBaseRev(baseRev int64) error {
	if baseRev < 0 {
		return errors.New("baseRev must be >= 0")
	}
	return nil
}

// validatePayload checks raw is one JSON object of at most PayloadCap bytes.
// Size is checked first, on the bytes as sent, so an oversized payload is
// never parsed. It never rewrites raw.
func validatePayload(raw json.RawMessage) error {
	if len(raw) > PayloadCap {
		return ErrPayloadTooLarge
	}
	// The leading-byte check rejects arrays, scalars and JSON null; json.Valid
	// then rejects malformed input and anything trailing the object.
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 || t[0] != '{' || !json.Valid(raw) {
		return errors.New("payload must be a JSON object")
	}
	return nil
}
