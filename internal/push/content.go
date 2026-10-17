package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/reads"
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
	// SessionID is the origin's Claude Code session id; it names the badge key ("c:<id>") of a card whose tmux session does
	// not resolve, so several cards of one such session count once. "" = the approval has none.
	SessionID string
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
	// Category is the iOS notification category (aps.category): the actions the phone shows under the notification. "" = none.
	Category    string
	ApprovalID  string
	SessionCode string
	SessionID   string
	SessionName string
	Event       string
	CollapseID  string
	// OpenApprovals is how many approvals of the pushed kinds are open on this host, read by the sender at send time (the
	// iOS notification extension sets the app-icon badge from it, spec §6). nil = unknown (the read failed): the field is
	// left out of the payload, never sent as 0.
	OpenApprovals *int
	// OpenApprovalKeys are the badge keys of those approvals ("s:<session_code>" or "a:<approval_id>", sorted, de-duplicated,
	// at most 32). nil = unknown (left out); a non-nil empty slice is a known "none".
	OpenApprovalKeys []string
	// WorkbookConv / WorkbookEntry name the session workbook entry a Stop push was built from, so a phone can open it
	// (payload purdex.workbook = {conv_key, entry_id}); zero = none, the block is left out.
	WorkbookConv  string
	WorkbookEntry int64
	// Member marks an agent push of a team member (payload purdex.member = true), so a phone does not count its unread
	// event. false = not a member, or unknown: the field is left out, never sent as false.
	Member bool
	// Background marks a silent badge update (badge spec §3.1, BG-1b): sent as apns-push-type `background`, priority 5, with no
	// alert and no collapse id; its body is backgroundPayload. ClosedApprovalIDs are the approvals that closed since the last
	// one (at most 16 are sent); the phone clears their delivered notifications.
	Background        bool
	ClosedApprovalIDs []string
	// Reads are the session reads of other devices this push carries to a read_sync phone (badge spec §7.2): the sender
	// attaches up to maxReads pending ones, oldest first; Build says how many actually fitted. EventCursor is the hook
	// cursor of the event an agent push is about (purdex.event_cursor); an empty epoch leaves it out.
	Reads       []reads.Read
	EventCursor reads.Cursor
}

// maxReads is how many reads one push carries.
const maxReads = 16

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
		"lead":                 "%s：%s 申請成為 lead",
		"self_relay":           "%s：%s 申請接力（已用 %d%%）",
		"self_relay_by":        "核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）",
		"self_relay_idle":      "%s：%s 閒置 %d 分鐘，申請接力（已用 %d%%）",
		"self_relay_idle_by":   "趁快取還在時接力最省；不核准的話，Claude Code 稍後會自行壓縮",
		"self_relay_idle_half": "趁快取還在時接力最省；不核准的話，5 分鐘後會先寫好接力檔（不清空）",
		"member_relay":         "%s：%s 要幫 member %s 接力（context %d%%）",
		"member_relay_by":      "member 額度用完，要核准嗎？",
		"spawn_resources":      "%s：%s 要開 member，主機資源%s",
		"spawn_resources_body": "記憶體 %d%%、壓力 %s、load %.1f。放行或不放行？",
		"zone_warning":         "警戒",
		"zone_danger":          "危險",
		"pressure_1":           "正常",
		"pressure_2":           "警告",
		"pressure_4":           "嚴重",
		"hook_ask":             "%s：%s 在等你回答",
	},
	"en": {
		"lead":                 "%s: %s requests to become lead",
		"self_relay":           "%s: %s requests a relay (%d%% used)",
		"self_relay_by":        "Once approved, this session writes its relay file, clears, and takes over in place (about 1 minute)",
		"self_relay_idle":      "%s: %s has been idle %d min and requests a relay (%d%% used)",
		"self_relay_idle_by":   "Relaying while the cache is warm is cheapest; otherwise Claude Code compacts it later",
		"self_relay_idle_half": "Relaying while the cache is warm is cheapest; otherwise a handoff file is written in 5 minutes (nothing is cleared)",
		"member_relay":         "%s: %s wants to relay member %s (context %d%%)",
		"member_relay_by":      "The member quota is used up. Approve?",
		"spawn_resources":      "%s: %s wants to open a member, host resources %s",
		"spawn_resources_body": "Memory %d%%, pressure %s, load %.1f. Let it through?",
		"zone_warning":         "tight",
		"zone_danger":          "critical",
		"pressure_1":           "normal",
		"pressure_2":           "warning",
		"pressure_4":           "critical",
		"hook_ask":             "%s: %s is waiting for your answer",
	},
}

func table(locale string) map[string]string {
	if t, ok := texts[locale]; ok {
		return t
	}
	return texts["zh-TW"]
}

// CountsForBadge reports whether an open approval of this kind is a card the app-icon badge counts (badge spec §3.1): the
// approvals a person must act on, whether or not they get a visible push (ApprovalContent decides that alone). A paused
// session is not counted on its own: the card it waits on is.
func CountsForBadge(kind string) bool {
	switch kind {
	case "lead", "self_relay", "member_relay", "adopt", "hook_ask", "hook_permission":
		return true
	}
	return false
}

// ApprovalCategory is the notification category of an approval push the person can answer with a yes or a no (the 同意 /
// 拒絕 buttons): lead, self_relay and member_relay. A hook_ask needs answers or a message, so it has none (tap to open).
const ApprovalCategory = "PDX_APPROVAL"

// ApprovalContent is the push for a newly opened approval, or false when this approval is not pushed: only lead,
// self_relay, member_relay, spawn_resources and an answerable hook_ask (not terminal_only) are (spec §5.1; the module sends a
// spawn_resources card only when relay.push_spawn_cards is on).
func ApprovalContent(a Approval, hostLabel, locale string) (Content, bool) {
	t := table(locale)
	who := originLabel(a.Origin)
	c := Content{Kind: a.Kind, ApprovalID: a.ID, CollapseID: a.ID}
	switch a.Kind {
	case "lead", "self_relay", "member_relay", "spawn_resources":
		c.Category = ApprovalCategory
	}
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
			Used        float64 `json:"used_percentage"`
			Reason      string  `json:"reason"`
			IdleMinutes int     `json:"idle_minutes"`
			// HalfOnTimeout: the session's mod writes a half when the card times out (the daemon set it at begin, from the mod's protocol)
			HalfOnTimeout bool `json:"half_on_timeout"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		if p.Reason == "idle" && p.IdleMinutes >= 1 && p.IdleMinutes <= 59 { // an idle ask (idle relay spec §3.2) says how long and why now; a damaged one reads as an ordinary ask
			c.Title = fmt.Sprintf(t["self_relay_idle"], hostLabel, who, p.IdleMinutes, int(math.Round(p.Used)))
			c.Body = t["self_relay_idle_by"]
			if p.HalfOnTimeout {
				c.Body = t["self_relay_idle_half"]
			}
			break
		}
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
	case "spawn_resources":
		// A spawn that waits for the person because the host is in the warning or danger zone (RT-5b). The lead's title is
		// written by a session, so it is normalised and cut like the member relay's.
		var p struct {
			LeadTitle string `json:"lead_title"`
			LeadRef   string `json:"lead_ref"`
			Host      struct {
				Zone     string  `json:"zone"`
				Mem      float64 `json:"mem"`
				Pressure int     `json:"pressure"`
				Load1    float64 `json:"load1"`
			} `json:"host"`
		}
		_ = json.Unmarshal(a.Payload, &p)
		lead := Normalise(p.LeadTitle, maxNamedRunes)
		if lead == "" {
			lead = Normalise(p.LeadRef, maxNamedRunes)
		}
		if lead == "" {
			lead = Normalise(who, maxNamedRunes)
		}
		zone := t["zone_danger"]
		if p.Host.Zone == "warning" {
			zone = t["zone_warning"]
		}
		pressure := t["pressure_"+strconv.Itoa(p.Host.Pressure)]
		if pressure == "" {
			pressure = "?"
		}
		c.Title = fmt.Sprintf(t["spawn_resources"], hostLabel, lead, zone)
		c.Body = fmt.Sprintf(t["spawn_resources_body"], int(math.Round(p.Host.Mem)), pressure, p.Host.Load1)
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
	raw, _, err := c.Build(hostID)
	return raw, err
}

// Build is Payload that also says which of c.Reads the payload carries (at most maxReads, the oldest first). A visible push
// fits its reads in the 4 KiB budget first and trims the body to make room; only when the fixed fields do not fit even with
// an empty body are reads dropped, from the newest end, and a dropped read is not reported as carried (it stays pending).
// A push that cannot be built carries, and reports, nothing.
func (c Content) Build(hostID string) ([]byte, []reads.Read, error) {
	if c.Background {
		return c.backgroundBuild(hostID)
	}
	rs := firstReads(c.Reads)
	body := c.Body
	for {
		raw, err := c.payload(hostID, body, rs)
		if err != nil {
			return nil, nil, err
		}
		if len(raw) <= maxPayload {
			return raw, rs, nil
		}
		if body != "" {
			r := []rune(body)
			body = string(r[:len(r)*3/4])
			continue
		}
		if len(rs) == 0 { // only the fixed fields are left and they are still over: refuse here, not at APNs
			return nil, nil, errors.New("push payload exceeds 4096 bytes")
		}
		rs = rs[:len(rs)-1]
	}
}

// firstReads is the oldest maxReads of rs.
func firstReads(rs []reads.Read) []reads.Read {
	if len(rs) > maxReads {
		return rs[:maxReads]
	}
	return rs
}

func (c Content) payload(hostID, body string, rs []reads.Read) ([]byte, error) {
	purdex := map[string]any{"host_id": hostID, "kind": c.Kind}
	for k, v := range map[string]string{"approval_id": c.ApprovalID, "session_code": c.SessionCode, "session_id": c.SessionID, "session_title": c.SessionName, "event": c.Event} {
		if v != "" {
			purdex[k] = v
		}
	}
	if c.WorkbookEntry != 0 {
		purdex["workbook"] = map[string]any{"conv_key": c.WorkbookConv, "entry_id": c.WorkbookEntry}
	}
	if c.Member {
		purdex["member"] = true
	}
	if c.EventCursor.Epoch != "" {
		purdex["event_cursor"] = c.EventCursor
	}
	if len(rs) > 0 {
		purdex["reads"] = rs
	}
	if c.OpenApprovals != nil {
		purdex["open_approvals"] = *c.OpenApprovals
	}
	if c.OpenApprovalKeys != nil {
		purdex["open_approval_keys"] = c.OpenApprovalKeys
	}
	aps := map[string]any{
		"alert":              map[string]string{"title": c.Title, "body": body},
		"sound":              "default",
		"thread-id":          hostID,
		"interruption-level": "time-sensitive",
		"mutable-content":    1,
	}
	if c.Category != "" {
		aps["category"] = c.Category
	}
	return json.Marshal(map[string]any{"aps": aps, "purdex": purdex})
}
