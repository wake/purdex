package resources

import (
	"strings"
	"testing"
	"time"
)

func TestAfterCommandEnd_NobodyWaitingNoLimit(t *testing.T) {
	end, kind, warned := AfterCommandEnd(5, 2*time.Hour, false, false)
	if end != AfterIdle || kind != "" || warned {
		t.Fatalf("got %v %q %v, want idle, no notice", end, kind, warned)
	}
}

func TestAfterCommandEnd_NoticeVoidWhenNobodyWaits(t *testing.T) {
	end, kind, warned := AfterCommandEnd(2, time.Minute, true, false)
	if end != AfterIdle || kind != "" || warned {
		t.Fatalf("got %v %q %v, want idle and warned=false", end, kind, warned)
	}
}

func TestAfterCommandEnd_ThreeShortCommandsAnnounceTheThird(t *testing.T) {
	if end, kind, warned := AfterCommandEnd(1, time.Minute, false, true); end != AfterIdle || kind != "" || warned {
		t.Fatalf("k=1: got %v %q %v", end, kind, warned)
	}
	if end, kind, warned := AfterCommandEnd(2, time.Minute, false, true); end != AfterIdleWarn || kind != NoticeThreeCommands || !warned {
		t.Fatalf("k=2: got %v %q %v", end, kind, warned)
	}
}

func TestAfterCommandEnd_OverThirtyMinutes(t *testing.T) {
	if end, _, _ := AfterCommandEnd(1, PositionMaxSpan-time.Millisecond, false, true); end != AfterIdle {
		t.Fatalf("29m59.999s: got %v, want idle", end)
	}
	end, kind, warned := AfterCommandEnd(1, PositionMaxSpan, false, true)
	if end != AfterIdleWarn || kind != NoticeOvertime || !warned {
		t.Fatalf("30m: got %v %q %v", end, kind, warned)
	}
}

func TestAfterCommandEnd_OneTwoOverThirty(t *testing.T) {
	if end, _, _ := AfterCommandEnd(1, 10*time.Minute, false, true); end != AfterIdle {
		t.Fatalf("k=1 at 10m: got %v, want idle", end)
	}
	if end, kind, _ := AfterCommandEnd(2, 31*time.Minute, false, true); end != AfterIdleWarn || kind != NoticeOvertime {
		t.Fatalf("k=2 at 31m: got %v %q", end, kind)
	}
}

func TestAfterCommandEnd_WaiterAfterTheThirdGetsOneExtension(t *testing.T) {
	end, kind, warned := AfterCommandEnd(3, time.Minute, false, true)
	if end != AfterIdleWarn || kind != NoticeThreeCommands || !warned {
		t.Fatalf("k=3 not warned: got %v %q %v", end, kind, warned)
	}
	if end, _, _ := AfterCommandEnd(4, time.Minute, true, true); end != AfterRelease {
		t.Fatalf("k=4 warned: got %v, want release", end)
	}
}

func TestAfterCommandEnd_WarnedReleases(t *testing.T) {
	end, kind, warned := AfterCommandEnd(1, time.Minute, true, true)
	if end != AfterRelease || kind != "" || !warned {
		t.Fatalf("got %v %q %v", end, kind, warned)
	}
}

func TestAfterCommandEnd_OvertimeWinsWhenBothHold(t *testing.T) {
	if _, kind, _ := AfterCommandEnd(2, time.Hour, false, true); kind != NoticeOvertime {
		t.Fatalf("got %q, want overtime", kind)
	}
}

func TestFinalNotice(t *testing.T) {
	if got := FinalNotice(NoticeOvertime); !strings.Contains(got, "執行時間過長") {
		t.Fatalf("overtime text: %q", got)
	}
	if got := FinalNotice(NoticeThreeCommands); !strings.Contains(got, "最後一個") {
		t.Fatalf("three_commands text: %q", got)
	}
	if got := FinalNotice(""); got != "" {
		t.Fatalf("empty kind has text %q", got)
	}
}

func TestCutCommand_RuneBoundaryAndControls(t *testing.T) {
	// 199 ASCII bytes then a 3-byte rune: the rune would straddle byte 200.
	in := strings.Repeat("a", 199) + "字"
	if got := CutCommand(in, 200); got != strings.Repeat("a", 199) {
		t.Fatalf("cut kept %d bytes", len(got))
	}
	if got := CutCommand("a\nb\tc\x00d", 200); got != "a b c d" {
		t.Fatalf("controls: %q", got)
	}
	if got := CutCommand("short", 200); got != "short" {
		t.Fatalf("short: %q", got)
	}
}
