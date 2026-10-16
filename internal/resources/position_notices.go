package resources

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The notices of the daemon to a session (amendment A §2.7, §2.10): what the outbox says when a position is offered,
// when its idle window named requests, and when it ends with something unanswered.

// NoticeRow is one named request of a position, as a notice tells it.
type NoticeRow struct {
	ID        string
	Mode      string // CallQueue | CallAsk
	Command   string
	Cwd       string
	CreatedAt time.Time // when the request was made (an ask row: when it was started in the background)
}

// MaxNoticeRows is how many requests one notice names; the rest are counted (plan Q8).
const MaxNoticeRows = 10

// OfferNotice is the one message of an offer (or of an idle window that named rows): a line per named request, a queue
// row's T-offer or an ask row's T-ask, queue rows first. positionID is what `pdx lease cancel` takes for a queue row;
// an ask row is answered by its own id. loc is the zone of the hour an ask line shows. Empty when no row is named.
//
// The ask line says whose command it is (M-D1: told bare, the model declined to confirm something a plugin "sent"): the
// one the session started in the background, postponed because the host was busy; confirm starts it as planned.
func OfferNotice(positionID string, rows []NoticeRow, loc *time.Location) string {
	var queue, ask []NoticeRow
	for _, r := range rows {
		if r.Mode == CallAsk {
			ask = append(ask, r)
		} else {
			queue = append(queue, r)
		}
	}
	var lines []string
	for _, r := range queue {
		lines = append(lines, "輪到你了：`"+clipCommand(r.Command)+"`（`"+r.Cwd+"`）。3 分鐘內照原樣重跑就會立刻執行；之後 3 分鐘內接著下的重指令也會直接跑。不跑請 `pdx lease cancel "+positionID+"`。")
	}
	for _, r := range ask {
		lines = append(lines, "你先前用 run_in_background 啟動的背景指令 `"+clipCommand(r.Command)+"`（`"+r.Cwd+"`，"+r.CreatedAt.In(loc).Format("15:04")+" 啟動）因為主機忙而延後，現在輪到它了。"+
			"這是你自己要求執行的同一個指令；`pdx lease confirm "+r.ID+"` 只是讓它照原計畫開始，不是新的指令。不要就 `pdx lease cancel "+r.ID+"`；3 分鐘沒回應會取消。")
	}
	if len(lines) == 0 {
		return ""
	}
	// At most MaxNoticeRows lines and MaxNoticeText bytes (the queue drops a longer text, and the offer would never
	// reach the session): the lines that do not fit are counted in the last one.
	shown, size := 0, 0
	for _, l := range lines {
		if shown >= MaxNoticeRows || size+len(l)+1+noticeMoreRoom > MaxNoticeText {
			break
		}
		shown++
		size += len(l) + 1
	}
	if shown == 0 {
		shown = 1 // a lone line is bounded by clipCommand and the directory limit
	}
	if more := len(lines) - shown; more > 0 {
		lines = append(lines[:shown], fmt.Sprintf("（另有 %d 個請求，用 pdx lease ls 看）", more))
	}
	return strings.Join(lines, "\n")
}

// MaxNoticeText is the most bytes one notice may have (promptq.MaxNoticeBytes; a test keeps the two equal).
const MaxNoticeText = 4000

// noticeMoreRoom is kept free for the "另有 N 個請求" line.
const noticeMoreRoom = 120

// clipCommand is the first 200 bytes of a command, cut at a character boundary.
func clipCommand(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// CancelledNotice is T-cancelled: sent only when the session had something pending that it did not answer.
const CancelledNotice = "3 分鐘沒有動作，你在佇列裡的位置已經放掉；之後要跑請重新下指令。"

// DrainedNotice is T-drained: the host stopped queueing while the session had something pending.
const DrainedNotice = "主機資源改回不排隊模式，你在佇列裡的位置已經放掉；現在直接重下指令就會執行。"
