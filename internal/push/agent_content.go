package push

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/notifylabel"
	"github.com/wake/purdex/internal/reads"
)

// NotifyLabel is the persona, task group and tmux name an agent push's title is built from (notify-title spec §1).
type NotifyLabel = notifylabel.Label

// AgentInput is what the content of an agent push is built from: the session, the event (the frame's raw_event_name)
// and the frame's detail.
type AgentInput struct {
	HostLabel   string
	SessionCode string
	SessionID   string
	SessionName string // the tmux session name; "" falls back to the code
	// Label is the persona and task group the daemon worked out; the zero value is today's title "{host}: {session}".
	Label     NotifyLabel
	EventName string
	Detail    map[string]any
	// Workbook is the session workbook's line for the Stop this push is about (WB-3); nil = today's push. Only a Stop or a
	// StopFailure uses it.
	Workbook *WorkbookLine
	// Member: the session is known to be a team member; the payload then says so (purdex.member).
	Member bool
	// EventCursor is the hook cursor of the event (agent.NotifyEvent.Event.Epoch / Seq); an empty epoch leaves it out.
	EventCursor reads.Cursor
}

// WorkbookLine is the part of a workbook entry that a Stop push uses.
type WorkbookLine struct {
	Thing   string // the title's subject
	Push    string // the body; blank = today's body
	ConvKey string
	EntryID int64
}

// eventAliases: cc broadcasts PdxXxx; the notification rules key on the legacy names (the Mac's normalizeEventName).
var eventAliases = map[string]string{
	"PdxNotification":      "Notification",
	"PdxPermissionRequest": "PermissionRequest",
	"PdxStop":              "Stop",
	"PdxStopFailure":       "StopFailure",
}

// NormalizeEventName collapses the four user-facing notification events to their legacy form.
func NormalizeEventName(raw string) string {
	if n, ok := eventAliases[raw]; ok {
		return n
	}
	return raw
}

func init() {
	for locale, add := range map[string]map[string]string{
		"zh-TW": {
			"agent_title":      "%s：%s",
			"permission_tool":  "需要授權：%s",
			"permission_none":  "需要授權：未知工具",
			"permission_ask":   "需要授權核准",
			"elicitation":      "需要輸入資訊（MCP）",
			"notification_new": "新通知",
			"stop_done":        "任務完成",
			"stop_failed":      "任務異常中斷",
		},
		"en": {
			"agent_title":      "%s: %s",
			"permission_tool":  "Permission required: %s",
			"permission_none":  "Permission required: unknown tool",
			"permission_ask":   "Permission approval required",
			"elicitation":      "Input required (MCP)",
			"notification_new": "New notification",
			"stop_done":        "Task completed",
			"stop_failed":      "Task stopped unexpectedly",
		},
	} {
		for k, v := range add {
			texts[locale][k] = v
		}
	}
}

// workbookTitle appends "・{thing}" to the title; when the whole is over the title limit it is the thing that is cut (with an
// ellipsis), never the host or the session name. A title already over the limit on its own is cut as today.
func workbookTitle(title, thing string) string {
	thing = Normalise(thing, maxNamedRunes) // model text on a lock screen: no newlines, control or bidi characters
	if thing == "" {
		return cutRunes(title, maxTitleRunes)
	}
	base := cutRunes(title, maxTitleRunes)
	room := maxTitleRunes - utf8.RuneCountInString(base) - 1 // the "・"
	if room <= 0 {
		return base
	}
	return base + "・" + cutRunes(thing, room)
}

// NotifyTitle is the title of an agent push (notify-title spec §1). With a persona name: "{name} — {group}", or without a
// group "{name} — {host}：{tmux}"; without a name today's "{host}：{tmux}". A workbook thing follows as "・{thing}". When the
// whole is over the limit the thing is cut first (and dropped when nothing is left for it), then the group or the host and
// tmux part; the name is never cut here (it is normalised to the length of a session's title, as the group is). The host
// label is the receiving device's own name for the host, so it is an argument, not part of the label.
func NotifyTitle(label NotifyLabel, hostLabel, thing, locale string) string {
	t := table(locale)
	row := label.Tmux
	if hostLabel != "" {
		row = fmt.Sprintf(t["agent_title"], hostLabel, label.Tmux)
	}
	name := Normalise(label.Name, maxNamedRunes)
	if name == "" {
		return workbookTitle(row, thing)
	}
	mid := Normalise(label.Group, maxNamedRunes)
	if mid == "" {
		mid = row
	}
	prefix := name + " — "
	if room := maxTitleRunes - utf8.RuneCountInString(prefix); utf8.RuneCountInString(mid) > room {
		if room <= 0 {
			return name
		}
		return prefix + cutRunes(mid, room)
	}
	return workbookTitle(prefix+mid, thing)
}

func detailString(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return s
}

// AgentContent is the push for an agent event, or false when this event has none (spec §5.3, the Mac's
// buildNotificationContent minus WorkerTerminated). A probe or sweep frame names its reason as the event, which is no
// event here, so it never has content. The body is normalised for a lock screen; the title is "{host}: {session}"; a
// newer push for the same session replaces the older one on the phone (collapse id agent-<code>).
func AgentContent(in AgentInput, locale string) (Content, bool) {
	t := table(locale)
	ev := NormalizeEventName(in.EventName)
	var candidates []string // the first that survives normalisation is the body
	switch ev {
	case "Notification":
		candidates = []string{detailString(in.Detail, "message")}
		switch detailString(in.Detail, "notification_type") {
		case "permission_prompt":
			candidates = append(candidates, t["permission_ask"])
		case "elicitation_dialog":
			candidates = append(candidates, t["elicitation"])
		}
		candidates = append(candidates, t["notification_new"])
	case "PermissionRequest":
		if tool := detailString(in.Detail, "tool_name"); tool != "" {
			candidates = []string{fmt.Sprintf(t["permission_tool"], tool)}
		}
		candidates = append(candidates, t["permission_none"])
	case "Stop":
		candidates = []string{detailString(in.Detail, "last_assistant_message"), t["stop_done"]}
	case "StopFailure":
		candidates = []string{detailString(in.Detail, "error_details"), detailString(in.Detail, "error"), t["stop_failed"]}
	default:
		return Content{}, false
	}
	wb := in.Workbook
	if ev != "Stop" && ev != "StopFailure" {
		wb = nil
	}
	if wb != nil && strings.TrimSpace(wb.Push) != "" { // the workbook's line is the body; today's candidates stay as the fallback
		candidates = append([]string{wb.Push}, candidates...)
	}
	body := ""
	for _, c := range candidates {
		if body = Normalise(c, maxBodyRunes); body != "" {
			break
		}
	}
	name := in.SessionName
	if name == "" {
		name = in.SessionCode
	}
	label := in.Label
	label.Tmux = name
	title := NotifyTitle(label, in.HostLabel, "", locale)
	c := Content{
		Title: cutRunes(title, maxTitleRunes), Body: body, Kind: "agent",
		SessionCode: in.SessionCode, SessionID: in.SessionID, SessionName: name, Event: ev,
		CollapseID: "agent-" + in.SessionCode, Member: in.Member, EventCursor: in.EventCursor,
	}
	if wb != nil {
		c.Title = NotifyTitle(label, in.HostLabel, wb.Thing, locale)
		c.WorkbookConv, c.WorkbookEntry = wb.ConvKey, wb.EntryID
	}
	return c, true
}
