package watch

import (
	"fmt"
	"strings"
	"time"
)

// The texts of the [負載守門] messages the watcher reads (host-load-control spec §12.3). Each names the event ids, so the watcher
// can deal with them by id (`pdx watch ack <id>… --note`).

const (
	messagePrefix = "[負載守門]"
	argvShow      = 120 // runes of a command shown in a message
	backlogShow   = 5   // events a backlog summary lists
)

func kindLabel(k Kind) string {
	switch k {
	case KindUnmanagedHeavy:
		return "不在管制內的重指令"
	case KindPressureHigh:
		return "記憶體壓力過高"
	case KindSlotOrphaned:
		return "名額被孤兒行程佔住"
	case KindSlotLong:
		return "名額佔用過久"
	}
	return string(k)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// eventLine is one event in a sentence, without its id.
func eventLine(r Record) string {
	switch r.Kind {
	case KindUnmanagedHeavy:
		s := fmt.Sprintf("%s 有不在管制內的重指令（%s）：%s｜峰值 %.1f 核心／%d MB，持續 %s", r.Host, r.Label, clip(strings.Join(r.Argv, " "), argvShow),
			r.PeakCores, r.PeakMB, (time.Duration(r.DurationS * float64(time.Second))).Round(time.Second))
		if r.Cwd != "" {
			s += "｜cwd " + r.Cwd
		}
		if r.New {
			s += "｜第一次出現"
		}
		return s
	case KindPressureHigh:
		return fmt.Sprintf("%s 整機記憶體壓力過高：%s", r.Host, r.Detail)
	case KindSlotOrphaned:
		return fmt.Sprintf("%s 的名額 %s 被孤兒行程佔住（gate pid %d，鎖持有者 %v）", r.Host, r.Slot, r.HolderPID, r.Holders)
	case KindSlotLong:
		return fmt.Sprintf("%s 的名額 %s 已被佔用很久（gate pid %d）", r.Host, r.Slot, r.HolderPID)
	}
	return fmt.Sprintf("%s：%s", r.Host, r.Kind)
}

// EventText is the message of one event.
func EventText(r Record) string {
	return messagePrefix + " " + eventLine(r) + "（事件 " + r.ID + "）"
}

// FoldText is the message that counts the events of one kind folded into the window before it.
func FoldText(host string, kind Kind, ids []string, count int) string {
	shown := ids
	if len(shown) > backlogShow {
		shown = shown[len(shown)-backlogShow:]
	}
	return fmt.Sprintf("%s %s 在 %d 分鐘內又有 %d 件「%s」（最新事件：%s）。用 pdx watch log --host %s --kind %s 查看。",
		messagePrefix, host, int(FoldWindow/time.Minute), count, kindLabel(kind), strings.Join(shown, "、"), host, kind)
}

// BacklogText is the summary a new or woken watcher first gets: counts by kind, the newest events, and how to see all. A large
// backlog comes in parts (part of parts), each about its own events.
func BacklogText(host string, recs []Record, part, nparts int) string {
	counts := map[Kind]int{}
	var kinds []Kind
	for _, r := range recs {
		if counts[r.Kind] == 0 {
			kinds = append(kinds, r.Kind)
		}
		counts[r.Kind]++
	}
	var kindParts []string
	for _, k := range kinds {
		kindParts = append(kindParts, fmt.Sprintf("%s ×%d", kindLabel(k), counts[k]))
	}
	var b strings.Builder
	where := ""
	if nparts > 1 {
		where = fmt.Sprintf("（第 %d／%d 段）", part, nparts)
	}
	fmt.Fprintf(&b, "%s%s %s 有 %d 件事件還沒送給守門員（%s）。最新 %d 件：", messagePrefix, where, host, len(recs), strings.Join(kindParts, "、"), min(backlogShow, len(recs)))
	from := max(0, len(recs)-backlogShow)
	for _, r := range recs[from:] {
		fmt.Fprintf(&b, "\n- %s %s", r.ID, eventLine(r))
	}
	b.WriteString("\n全部用 pdx watch log --all 查看；處理完用 pdx watch ack <id>… --note \"<一行>\"。")
	return b.String()
}

// EventSinkKey is the service-registry key of the delivery queue, which the module that records an event hands it to.
const EventSinkKey = "watch.events.sink"

// EventSink takes a recorded event for delivery to the watcher.
type EventSink interface {
	Enqueue(rec Record)
}
