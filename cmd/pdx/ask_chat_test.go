package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// A chat reply (answered_remote with hook.message) passes through `pdx ask
// wait` untouched: a multi-line message with a tab survives the round trip.
func TestRunAskCmd_WaitPrintsTheChatReplyVerbatim(t *testing.T) {
	msg := "第一行\n\t第二行 \"quoted\"\n第三行"
	d := newFakeAskDaemon(t)
	d.waits = []team.AskWaitResponse{{State: team.AskAnsweredRemote, Hook: &team.HookDecision{Message: msg}}}
	code, stdout, stderr := driveAsk(t, d, time.Now, "wait", "ask-1")
	if code != ExitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var got team.AskWaitResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if got.State != team.AskAnsweredRemote || got.Hook == nil || got.Hook.Message != msg || got.Hook.Answers != nil {
		t.Fatalf("got %+v hook=%+v, want message %q", got, got.Hook, msg)
	}
}
