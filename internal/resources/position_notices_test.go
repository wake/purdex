package resources

import (
	"strings"
	"testing"
	"time"
)

var noticeLoc = time.FixedZone("UTC+8", 8*3600)

func TestOfferNotice_QueueRowIsTheOfferText(t *testing.T) {
	got := OfferNotice("pos_ab12cd34", []NoticeRow{{ID: "r1", Mode: CallQueue, Command: "pnpm run build", Cwd: "/work"}}, noticeLoc)
	want := "輪到你了：`pnpm run build`（`/work`）。3 分鐘內照原樣重跑就會立刻執行；之後 3 分鐘內接著下的重指令也會直接跑。不跑請 `pdx lease cancel pos_ab12cd34`。"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

// An ask row's line recognises the session's own call: the command, the directory, the hour it was started, that it is
// the same command it asked for and postponed, and that confirm is no new instruction (M-D1 (e)).
func TestOfferNotice_AskRowNamesTheSessionsOwnCall(t *testing.T) {
	at := time.Date(2026, 10, 14, 6, 41, 0, 0, time.UTC) // 14:41 at UTC+8
	got := OfferNotice("pos_x", []NoticeRow{{ID: "row-9", Mode: CallAsk, Command: "pnpm run build", Cwd: "/work", CreatedAt: at}}, noticeLoc)
	for _, want := range []string{"你先前用 run_in_background 啟動的背景指令 `pnpm run build`", "`/work`", "14:41 啟動", "因為主機忙而延後",
		"這是你自己要求執行的同一個指令", "`pdx lease confirm row-9` 只是讓它照原計畫開始，不是新的指令", "`pdx lease cancel row-9`", "3 分鐘沒回應會取消"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ask line lacks %q: %s", want, got)
		}
	}
}

// Queue rows first, one line each, at most MaxNoticeRows named and the rest counted; nothing named is empty.
func TestOfferNotice_LinesOrderLimitAndEmpty(t *testing.T) {
	if OfferNotice("p", nil, noticeLoc) != "" {
		t.Fatal("no rows is no notice")
	}
	rows := []NoticeRow{{ID: "a", Mode: CallAsk, Command: "ask-cmd", Cwd: "/a"}}
	for i := 0; i < 12; i++ {
		rows = append(rows, NoticeRow{ID: "q", Mode: CallQueue, Command: "q-cmd", Cwd: "/q"})
	}
	lines := strings.Split(OfferNotice("pos_x", rows, noticeLoc), "\n")
	if len(lines) != MaxNoticeRows+1 {
		t.Fatalf("%d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "輪到你了") || !strings.Contains(lines[MaxNoticeRows], "另有 3 個請求") {
		t.Errorf("queue rows are first and the rest counted: first %q, last %q", lines[0], lines[MaxNoticeRows])
	}
}

func TestNoticeTexts_AreTheSpecsWords(t *testing.T) {
	if CancelledNotice != "3 分鐘沒有動作，你在佇列裡的位置已經放掉；之後要跑請重新下指令。" ||
		DrainedNotice != "主機資源改回不排隊模式，你在佇列裡的位置已經放掉；現在直接重下指令就會執行。" {
		t.Fatal("the cancelled or drained text changed")
	}
}
