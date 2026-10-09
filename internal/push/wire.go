// Package push holds the wire types and rules of phone push notifications (spec docs/specs/2026-10-09-push-spec.md):
// the device registration request and its validation, the stored device and its masked view, and the presence request
// the Mac App reports (used from PU-3). The module that serves them is internal/module/push.
package push

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BundleID is the one topic v1 accepts (spec §4.1).
const BundleID = "tw.protype.purdex"

const (
	maxDeviceNameRunes = 64
	maxHostLabelRunes  = 32
	maxTabs            = 500
	maxCodeLen         = 64
	maxAgentTypes      = 32
	maxEventsPerAgent  = 64
	maxNameLen         = 64
	minTokenLen        = 64
	maxTokenLen        = 200
)

// AgentPrefs mirrors the Mac's NotificationSettings for one agent type. A nil Enabled is the Mac default (on).
type AgentPrefs struct {
	Enabled          *bool           `json:"enabled,omitempty"`
	Events           map[string]bool `json:"events,omitempty"`
	NotifyWithoutTab bool            `json:"notify_without_tab,omitempty"`
}

// Prefs is what a phone wants pushed: per agent type, plus the session codes it has open as tabs on this host.
type Prefs struct {
	Agents map[string]AgentPrefs `json:"agents,omitempty"`
	Tabs   []string              `json:"tabs,omitempty"`
}

// DeviceRequest is the body of POST /api/push/devices.
type DeviceRequest struct {
	Token      string `json:"token"`
	BundleID   string `json:"bundle_id"`
	Env        string `json:"env"`
	Platform   string `json:"platform"`
	DeviceName string `json:"device_name"`
	HostLabel  string `json:"host_label"`
	Locale     string `json:"locale"`
	Prefs      Prefs  `json:"prefs"`
}

// Validate checks the request (spec §4.1) and normalises it in place: the token is lowercased, an unknown locale
// becomes zh-TW. The error texts never carry the token.
func (r *DeviceRequest) Validate() error {
	if n := len(r.Token); n < minTokenLen || n > maxTokenLen || !isHex(r.Token) {
		return errors.New("token: want 64-200 hex characters")
	}
	r.Token = lower(r.Token)
	if r.BundleID != BundleID {
		return errors.New("bundle_id: not a topic this host pushes for")
	}
	if r.Env != "sandbox" && r.Env != "production" {
		return errors.New("env: want sandbox or production")
	}
	if r.Platform != "ios" {
		return errors.New("platform: want ios")
	}
	if err := printable("device_name", r.DeviceName, maxDeviceNameRunes); err != nil {
		return err
	}
	if err := printable("host_label", r.HostLabel, maxHostLabelRunes); err != nil {
		return err
	}
	// Both come back in responses: neither may carry (a long enough slice of) the token itself.
	for _, v := range []string{r.DeviceName, r.HostLabel} {
		if containsTokenSlice(lower(v), r.Token) {
			return errors.New("device_name / host_label: must not contain the token")
		}
	}
	if r.Locale != "zh-TW" && r.Locale != "en" {
		r.Locale = "zh-TW"
	}
	return r.Prefs.validate()
}

func (p *Prefs) validate() error {
	if len(p.Tabs) > maxTabs {
		return fmt.Errorf("prefs.tabs: at most %d", maxTabs)
	}
	for _, code := range p.Tabs {
		if code == "" || len(code) > maxCodeLen || hasControl(code) {
			return errors.New("prefs.tabs: every code must be 1-64 printable characters")
		}
	}
	if len(p.Agents) > maxAgentTypes {
		return fmt.Errorf("prefs.agents: at most %d agent types", maxAgentTypes)
	}
	for name, a := range p.Agents {
		if name == "" || len(name) > maxNameLen || hasControl(name) {
			return errors.New("prefs.agents: every agent type must be 1-64 printable characters")
		}
		if len(a.Events) > maxEventsPerAgent {
			return fmt.Errorf("prefs.agents.events: at most %d events per agent type", maxEventsPerAgent)
		}
		for ev := range a.Events {
			if ev == "" || len(ev) > maxNameLen || hasControl(ev) {
				return errors.New("prefs.agents.events: every event name must be 1-64 printable characters")
			}
		}
	}
	return nil
}

// containsTokenSlice reports whether v holds 16 or more consecutive characters of token.
func containsTokenSlice(v, token string) bool {
	const n = 16
	for i := 0; i+n <= len(token); i++ {
		if strings.Contains(v, token[i:i+n]) {
			return true
		}
	}
	return false
}

// ValidDeviceID reports whether s has the shape DeviceID produces: 16 lowercase hex characters.
func ValidDeviceID(s string) bool {
	return len(s) == 16 && isHex(s) && s == lower(s)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || !unicode.IsPrint(r) && r != ' ' {
			return true
		}
	}
	return !utf8.ValidString(s)
}

func printable(field, s string, maxRunes int) error {
	if utf8.RuneCountInString(s) > maxRunes {
		return fmt.Errorf("%s: at most %d characters", field, maxRunes)
	}
	if hasControl(s) {
		return fmt.Errorf("%s: control characters are not allowed", field)
	}
	return nil
}

// DeviceID is the first 16 hex chars of the SHA-256 of the lowercased token: the handle every URL and log line uses
// instead of the token.
func DeviceID(token string) string {
	sum := sha256.Sum256([]byte(lower(token)))
	return hex.EncodeToString(sum[:])[:16]
}

// MaskToken shows the first 8 and last 4 characters of a token; a token too short to hide anything shows nothing.
func MaskToken(token string) string {
	if len(token) < 16 {
		return ""
	}
	return token[:8] + "…" + token[len(token)-4:]
}

// Device is a stored registration. Token is the full APNs token: never put it in a response, a log line or an error.
type Device struct {
	DeviceID   string
	Token      string
	BundleID   string
	Env        string
	Platform   string
	DeviceName string
	HostLabel  string
	Locale     string
	Prefs      Prefs
	CreatedAt  int64 // Unix ms
	UpdatedAt  int64
	LastSentAt int64
	LastError  string
}

// DeviceView is the debugging view of a Device (spec §4.3): the token masked, the tabs counted.
type DeviceView struct {
	DeviceID   string `json:"device_id"`
	Token      string `json:"token"`
	Env        string `json:"env"`
	DeviceName string `json:"device_name"`
	HostLabel  string `json:"host_label"`
	Locale     string `json:"locale"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	LastSentAt int64  `json:"last_sent_at"`
	LastError  string `json:"last_error"`
	TabsCount  int    `json:"tabs_count"`
}

func (d Device) View() DeviceView {
	return DeviceView{
		DeviceID: d.DeviceID, Token: MaskToken(d.Token), Env: d.Env, DeviceName: d.DeviceName, HostLabel: d.HostLabel,
		Locale: d.Locale, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, LastSentAt: d.LastSentAt,
		LastError: d.LastError, TabsCount: len(d.Prefs.Tabs),
	}
}

// PresenceSession is one session a Mac window shows; PresenceRequest is the body of PUT /api/push/presence (PU-3).
type PresenceSession struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type PresenceRequest struct {
	ClientID string            `json:"client_id"`
	Active   bool              `json:"active"`
	Sessions []PresenceSession `json:"sessions"`
	TTLMs    int               `json:"ttl_ms"`
}
