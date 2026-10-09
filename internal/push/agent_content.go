package push

import "fmt"

// AgentInput is what the content of an agent push is built from: the session, the event (the frame's raw_event_name)
// and the frame's detail.
type AgentInput struct {
	HostLabel   string
	SessionCode string
	SessionID   string
	SessionName string // the tmux session name; "" falls back to the code
	EventName   string
	Detail      map[string]any
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
	title := name
	if in.HostLabel != "" {
		title = fmt.Sprintf(t["agent_title"], in.HostLabel, name)
	}
	return Content{
		Title: cutRunes(title, maxTitleRunes), Body: body, Kind: "agent",
		SessionCode: in.SessionCode, SessionID: in.SessionID, SessionName: name, Event: ev,
		CollapseID: "agent-" + in.SessionCode,
	}, true
}
