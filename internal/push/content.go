package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxBodyRunes  = 240
	maxTitleRunes = 120
	maxNamedRunes = 40 // a session's title inside a longer title
	maxPayload    = 4096
)

// Approval is the part of a team approval that push content is built from (the module converts from team.Approval).
type Approval struct {
	ID      string
	Kind    string
	Payload json.RawMessage
	Origin  ApprovalOrigin
}

// ApprovalOrigin names the requesting session; the title shows the first of Title, Name, Ref that is set (as the Mac's
// approval title does).
type ApprovalOrigin struct {
	Title, Name, Ref string
}

// Content is what one push says. CollapseID makes a newer push of the same thing replace the older one on the phone.
type Content struct {
	Title, Body string
	Kind        string // lead | self_relay | hook_ask | agent
	ApprovalID  string
	SessionCode string
	SessionID   string
	SessionName string
	Event       string
	CollapseID  string
	// OpenApprovals is how many approvals of the pushed kinds are open on this host; the sender fills it in at send time
	// (the iOS notification extension sets the app-icon badge from it, spec §6).
	OpenApprovals int
}

var (
	reImage  = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reFence  = regexp.MustCompile("(?m)^\\s*```[^\\n]*$")
	reQuote  = regexp.MustCompile(`(?m)^\s*>+\s?`)
	reHead   = regexp.MustCompile(`(?m)^\s*#{1,6}\s+`)
	reBullet = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)])\s+`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// Normalise makes a Markdown text fit a lock screen (push spec §5.3): link and image syntax reduced to their text,
// code fences, backticks, bold markers, heading hashes, quote marks and list bullets dropped, every run of whitespace
// one space, cut at maxRunes with an ellipsis. The same rule the Mac's own notification content is to adopt.
func Normalise(s string, maxRunes int) string {
	s = reImage.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1")
	s = reFence.ReplaceAllString(s, "")
	s = reHead.ReplaceAllString(s, "")
	s = reQuote.ReplaceAllString(s, "")
	s = reBullet.ReplaceAllString(s, "")
	s = strings.NewReplacer("`", "", "**", "", "__", "").Replace(s)
	s = strings.Map(plainRune, s)
	s = strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
	return cutRunes(s, maxRunes)
}

// plainRune keeps a text honest on a lock screen: every kind of space (a line or paragraph separator included) becomes a
// plain space, and control and format characters (direction marks such as RLO / LRI / PDI, zero-width marks, the BOM)
// are dropped, so a title written by a session cannot reorder or hide the words around it.
func plainRune(r rune) rune {
	switch {
	case unicode.IsSpace(r):
		return ' '
	case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		return -1
	}
	return r
}

func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func originLabel(o ApprovalOrigin) string {
	switch {
	case o.Title != "":
		return o.Title
	case o.Name != "":
		return o.Name
	default:
		return o.Ref
	}
}

var texts = map[string]map[string]string{
	"zh-TW": {
		"lead":            "%s：%s 申請成為 lead",
		"self_relay":      "%s：%s 申請接力（已用 %d%%）",
		"self_relay_by":   "核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）",
		"member_relay":    "%s：%s 要幫 member %s 接力（context %d%%）",
		"member_relay_by": "member 額度用完，要核准嗎？",
		"hook_ask":        "%s：%s 在等你回答",
	},
	"en": {
		"lead":            "%s: %s requests to become lead",
		"self_relay":      "%s: %s requests a relay (%d%% used)",
		"self_relay_by":   "Once approved, this session writes its relay file, clears, and takes over in place (about 1 minute)",
		"member_relay":    "%s: %s wants to relay member %s (context %d%%)",
		"member_relay_by": "The member quota is used up. Approve?",
		"hook_ask":        "%s: %s is waiting for your answer",
	},
}

func table(locale string) map[string]string {
	if t, ok := texts[locale]; ok {
		return t
	}
	return texts["zh-TW"]
}

// ApprovalContent is the push for a newly opened approval, or false when this approval is not pushed: only lead,
// self_relay, member_relay and an answerable hook_ask (not terminal_only) are (spec §5.1).
func ApprovalContent(a Approval, hostLabel, locale string) (Content, bool) {
	t := table(locale)
	who := originLabel(a.Origin)
	c := Content{Kind: a.Kind, ApprovalID: a.ID, CollapseID: a.ID}
	switch a.Kind {
	case "lead":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		c.Title = fmt.Sprintf(t["lead"], hostLabel, who)
		c.Body = Normalise(p.Reason, maxBodyRunes)
	case "self_relay":
		var p struct {
			Used float64 `json:"used_percentage"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		c.Title = fmt.Sprintf(t["self_relay"], hostLabel, who, int(math.Round(p.Used)))
		c.Body = t["self_relay_by"]
	case "member_relay":
		// A lead's member relay that waits for a person because the lead's member pool is out (RQ-2): who asks, for whom.
		// The titles are written by the sessions, so they are normalised and cut like any text from outside.
		var p struct {
			LeadTitle   string  `json:"lead_title"`
			MemberTitle string  `json:"member_title"`
			MemberRef   string  `json:"member_ref"`
			Used        float64 `json:"used_percentage"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		lead := Normalise(p.LeadTitle, maxNamedRunes)
		if lead == "" {
			lead = Normalise(who, maxNamedRunes) // the origin's own label, written by the session too
		}
		if lead == "" {
			lead = Normalise(a.Origin.Ref, maxNamedRunes)
		}
		member := Normalise(p.MemberTitle, maxNamedRunes)
		if member == "" {
			member = Normalise(p.MemberRef, maxNamedRunes)
		}
		c.Title = fmt.Sprintf(t["member_relay"], hostLabel, lead, member, int(math.Round(p.Used)))
		c.Body = t["member_relay_by"]
	case "hook_ask":
		var p struct {
			TerminalOnly bool            `json:"terminal_only"`
			Questions    json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		if p.TerminalOnly {
			return Content{}, false
		}
		var qs []struct {
			Question string `json:"question"`
		}
		_ = json.Unmarshal(p.Questions, &qs)
		c.Title = fmt.Sprintf(t["hook_ask"], hostLabel, who)
		if len(qs) > 0 {
			c.Body = Normalise(qs[0].Question, maxBodyRunes)
		}
	default:
		return Content{}, false
	}
	c.Title = cutRunes(c.Title, maxTitleRunes)
	return c, true
}

// Payload is the APNs JSON body (spec §6), under 4 KiB: the body is cut further if the whole is over. Empty fields of
// the purdex block are left out.
func (c Content) Payload(hostID string) ([]byte, error) {
	body := c.Body
	for {
		raw, err := c.payload(hostID, body)
		if err != nil {
			return nil, err
		}
		if len(raw) <= maxPayload {
			return raw, nil
		}
		if body == "" { // only the fixed fields are left and they are still over: refuse here, not at APNs
			return nil, errors.New("push payload exceeds 4096 bytes")
		}
		r := []rune(body)
		body = string(r[:len(r)*3/4])
	}
}

func (c Content) payload(hostID, body string) ([]byte, error) {
	purdex := map[string]any{"host_id": hostID, "kind": c.Kind}
	for k, v := range map[string]string{"approval_id": c.ApprovalID, "session_code": c.SessionCode, "session_id": c.SessionID, "session_title": c.SessionName, "event": c.Event} {
		if v != "" {
			purdex[k] = v
		}
	}
	purdex["open_approvals"] = c.OpenApprovals
	return json.Marshal(map[string]any{
		"aps": map[string]any{
			"alert":              map[string]string{"title": c.Title, "body": body},
			"sound":              "default",
			"thread-id":          hostID,
			"interruption-level": "time-sensitive",
			"mutable-content":    1,
		},
		"purdex": purdex,
	})
}
