package cc

import (
	"errors"
	"regexp"
)

var errNoSessionID = errors.New("session ID not found in pane content")

var sessionIDRegex = regexp.MustCompile(
	`Session ID:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`,
)

var cwdRegex = regexp.MustCompile(`(?m)^\s*cwd:\s*(\S.+?)\s*$`)

type StatusInfo struct {
	SessionID string
	Cwd       string
}

func ExtractSessionID(paneContent string) (string, error) {
	m := sessionIDRegex.FindStringSubmatch(paneContent)
	if len(m) < 2 {
		return "", errNoSessionID
	}
	return m[1], nil
}

func ExtractStatusInfo(paneContent string) (StatusInfo, error) {
	id, err := ExtractSessionID(paneContent)
	if err != nil {
		return StatusInfo{}, err
	}
	info := StatusInfo{SessionID: id}
	if m := cwdRegex.FindStringSubmatch(paneContent); len(m) >= 2 {
		info.Cwd = m[1]
	}
	return info, nil
}
