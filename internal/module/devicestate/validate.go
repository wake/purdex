package devicestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	deviceNameMaxRunes = 64
	appVersionMaxBytes = 64
)

var clientIDPattern = regexp.MustCompile(`^c_[0-9a-f]{12}$`)

func validateClientID(id string) error {
	if !clientIDPattern.MatchString(id) {
		return errors.New("clientId must match c_ + 12 lowercase hex")
	}
	return nil
}

// validateDeviceName trims name and returns the trimmed value when it is
// 1–64 runes long.
func validateDeviceName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	n := utf8.RuneCountInString(trimmed)
	if n == 0 {
		return "", errors.New("deviceName is required")
	}
	if n > deviceNameMaxRunes {
		return "", errors.New("deviceName too long")
	}
	return trimmed, nil
}

func validateAppVersion(v string) error {
	if len(v) > appVersionMaxBytes {
		return errors.New("appVersion too long")
	}
	return nil
}

func validateCapturedAt(ms int64) error {
	if ms <= 0 {
		return errors.New("capturedAt must be > 0")
	}
	return nil
}

// parsePayload checks raw is a version-1 WorkspaceSnapshot shape and returns
// the workspace and tab counts. It never rewrites raw.
func parsePayload(raw json.RawMessage) (workspaceCount, tabCount int, err error) {
	var obj map[string]json.RawMessage
	if !startsWith(raw, '{') || json.Unmarshal(raw, &obj) != nil {
		return 0, 0, errors.New("payload must be a JSON object")
	}

	var version float64
	if v, ok := obj["version"]; !ok || !startsWithNumber(v) || json.Unmarshal(v, &version) != nil || version != 1 {
		return 0, 0, errors.New("payload version must be 1")
	}

	var workspaces []json.RawMessage
	if v, ok := obj["workspaces"]; !ok || !startsWith(v, '[') || json.Unmarshal(v, &workspaces) != nil {
		return 0, 0, errors.New("payload workspaces must be an array")
	}

	var tabs map[string]json.RawMessage
	if v, ok := obj["tabs"]; !ok || !startsWith(v, '{') || json.Unmarshal(v, &tabs) != nil {
		return 0, 0, errors.New("payload tabs must be an object")
	}

	var tabOrder []json.RawMessage
	if v, ok := obj["tabOrder"]; !ok || !startsWith(v, '[') || json.Unmarshal(v, &tabOrder) != nil {
		return 0, 0, errors.New("payload tabOrder must be an array")
	}

	// activeTabId / activeWorkspaceId: present, and JSON null or string —
	// mirrors isWellFormedSnapshotV1 (spa/src/lib/snapshot/storage.ts).
	for _, key := range []string{"activeTabId", "activeWorkspaceId"} {
		if v, ok := obj[key]; !ok || !isNullOrString(v) {
			return 0, 0, errors.New("payload " + key + " must be null or a string")
		}
	}

	var sessionMeta map[string]json.RawMessage
	if v, ok := obj["sessionMeta"]; !ok || !startsWith(v, '{') || json.Unmarshal(v, &sessionMeta) != nil {
		return 0, 0, errors.New("payload sessionMeta must be an object")
	}

	return len(workspaces), len(tabs), nil
}

// startsWith reports whether the first non-whitespace byte of raw is c. This
// rejects JSON null, which json.Unmarshal would otherwise accept silently.
func startsWith(raw []byte, c byte) bool {
	t := bytes.TrimLeft(raw, " \t\r\n")
	return len(t) > 0 && t[0] == c
}

// isNullOrString reports whether raw is exactly JSON null or a JSON string.
func isNullOrString(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	if bytes.Equal(t, []byte("null")) {
		return true
	}
	var s string
	return startsWith(t, '"') && json.Unmarshal(t, &s) == nil
}

func startsWithNumber(raw []byte) bool {
	t := bytes.TrimLeft(raw, " \t\r\n")
	return len(t) > 0 && (t[0] == '-' || (t[0] >= '0' && t[0] <= '9'))
}
