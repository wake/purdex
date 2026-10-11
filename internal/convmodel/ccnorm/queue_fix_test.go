package ccnorm

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Measured on the 60 largest local transcripts: 1428 remove rows, 1413 match their enqueue byte for byte; the other 15
// are peer/task messages (absorbed_mid_turn) whose removed frame is re-serialised: the same body, but the wrapper
// attributes differ (the enqueued one carries a hop-chain attribute). Matching therefore falls back to the body.
func TestQueue_RemovePeerMatchesByBodyWhenWrapperDiffers(t *testing.T) {
	enq := `<cross-session-message from="uds:/x" hop-chain="abc123" from-name="mlab/a-b">` + "\nhello\n" + `</cross-session-message>`
	rm := `<cross-session-message from="uds:/x" from-name="mlab/a-b">` + "\nhello\n" + `</cross-session-message>`
	n := norm(t, qin(1, "enqueue", enq), qin(2, "enqueue", "mine"),
		qop(3, "remove", with("content", rm), with("reason", "absorbed_mid_turn")))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:mine"}) {
		t.Fatalf("queued = %v", got)
	}
	if n.Stats().Skipped["queue:remove-unmatched"] != 0 || n.Stats().Skipped["queue:remove-fifo"] != 0 {
		t.Fatalf("skipped = %v", n.Stats().Skipped)
	}
}

func TestQueue_RemoveMatchesIgnoringWhitespaceDifferences(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "do  this\n thing "), qin(2, "enqueue", "other"),
		qin(3, "remove", "do this thing\n"))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:other"}) {
		t.Fatalf("queued = %v", got)
	}
}

func TestQueue_RemoveWithNoMatchTakesOldestOfSameClass(t *testing.T) {
	// an unmatched task-notification remove (1 of 1428 measured) removes the oldest system entry, never a user one
	n := norm(t, qin(1, "enqueue", "mine"), qin(2, "enqueue", sysMsg), qin(3, "enqueue", peerMsg),
		qin(4, "remove", "<task-notification><task-id>other</task-id></task-notification>"))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:mine", "peer:hello"}) {
		t.Fatalf("queued = %v", got)
	}
	if n.Stats().Skipped["queue:remove-fifo"] != 1 {
		t.Fatalf("skipped = %v", n.Stats().Skipped)
	}
	// no entry of the class at all: counted unmatched, queue untouched
	n = norm(t, qin(1, "enqueue", "mine"), qin(2, "remove", sysMsg))
	if len(queuedOf(n)) != 1 || n.Stats().Skipped["queue:remove-unmatched"] != 1 {
		t.Fatalf("queued %+v skipped %v", queuedOf(n), n.Stats().Skipped)
	}
}

// Measured: a local slash command row has no promptSource at all; the typed/suggestion_accepted rows are the fresh prompts
// (suggestion_accepted: 45 of 45 rows arrive with no turn running).
func TestQueue_ClearRule(t *testing.T) {
	running := func(extra ...[]byte) [][]byte {
		return append([][]byte{userRow("u0", 0.1, "go"), assistantRow("a0", 0.2, "m", textBlock("working"))}, extra...)
	}
	ended := func(extra ...[]byte) [][]byte {
		return append(running(turnDuration("d0", 0.3, 100)), extra...)
	}
	enq := qin(1, "enqueue", "later")
	cases := []struct {
		name  string
		lines [][]byte
		keep  bool
	}{
		{"typed prompt opening a turn clears", ended(enq, userRow("u1", 2, "new", promptSource("typed"))), false},
		{"suggestion accepted opening a turn clears", ended(enq, userRow("u1", 2, "new", promptSource("suggestion_accepted"))), false},
		{"typed prompt while the turn runs keeps", running(enq, userRow("u1", 2, "new")), true},
		{"slash command (no promptSource) keeps", ended(enq, userRow("u1", 2, "<command-name>/cost</command-name>", without("promptSource"))), true},
		{"slash command mid-turn keeps", running(enq, userRow("u1", 2, "<command-name>/cost</command-name>")), true},
		{"bash input mid-turn keeps", running(enq, userRow("u1", 2, "<bash-input>ls</bash-input>")), true},
		{"missing promptSource keeps", ended(enq, userRow("u1", 2, "plain", without("promptSource"))), true},
		{"empty promptSource keeps", ended(enq, userRow("u1", 2, "plain", promptSource(""))), true},
		{"older row shape keeps", ended(enq, oldUserRow("u1", 2, "plain")), true},
		{"queued row keeps", ended(enq, userRow("u1", 2, "x", promptSource("queued"))), true},
		{"meta row keeps", ended(enq, userRow("u1", 2, "x", isMeta())), true},
		{"tool_result keeps", ended(enq, toolResultRow("u1", 2, "t1", "out")), true},
		{"compact summary keeps", ended(enq, compactSummary("u1", 2, "summary")), true},
		{"sidechain row keeps", ended(enq, userRow("u1", 2, "x", sidechain())), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := norm(t, c.lines...)
			if got := len(queuedOf(n)) == 1; got != c.keep {
				t.Fatalf("queue kept = %v, want %v (%+v)", got, c.keep, queuedOf(n))
			}
		})
	}
}

func TestQueue_OversizeLineClearsQueue(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"))
	if _, err := n.Feed(n.Next(), []byte(strings.Repeat("x", maxLineBytes+1))); err != nil {
		t.Fatal(err)
	}
	if len(queuedOf(n)) != 0 {
		t.Fatalf("Feed oversize left %+v", queuedOf(n))
	}
	n = norm(t, qin(1, "enqueue", "a"))
	if err := n.Skip(n.Next(), maxLineBytes+5); err != nil {
		t.Fatal(err)
	}
	if len(queuedOf(n)) != 0 {
		t.Fatalf("Skip oversize left %+v", queuedOf(n))
	}
}

func TestQueue_SourceByTag(t *testing.T) {
	for in, want := range map[string]string{
		"<task-notification>x</task-notification>":         "system",
		"  <local-command-stdout>x</local-command-stdout>": "system",
		"\n<system-reminder>x</system-reminder>":           "system",
		"<foo-bar attr=\"1\">x":                            "user",
		"<div> broken":                                     "user",
		"<Button> 壞了":                                      "user",
		"<?php echo 1;":                                    "user",
		"<task-notificationx>y":                            "user",
		"<cross-session-message from-name=\"a\">x</cross-session-message>": "peer",
		"fix a < b please": "user",
		"< 5 is small":     "user",
		"a <tag> later":    "user",
		"":                 "user",
	} {
		if got := queueSource(in); got != want {
			t.Errorf("queueSource(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestQueue_SourceWhitelistedHarnessTags(t *testing.T) {
	for _, tag := range []string{"task-notification", "system-reminder", "command-name", "command-message", "command-args",
		"local-command-stdout", "local-command-stderr", "local-command-caveat", "bash-input", "bash-stdout", "bash-stderr",
		"user-prompt-submit-hook"} {
		for _, in := range []string{"<" + tag + ">x</" + tag + ">", "  \n<" + tag + " a=\"1\">x"} {
			if got := queueSource(in); got != "system" {
				t.Errorf("queueSource(%q) = %s, want system", in, got)
			}
		}
	}
}

func TestQueue_RemoveUserFallbackOnlyWhenSingleUserEntry(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "only"), qin(2, "remove", "something else"))
	if len(queuedOf(n)) != 0 || n.Stats().Skipped["queue:remove-fifo"] != 1 {
		t.Fatalf("queued %+v skipped %v", queuedOf(n), n.Stats().Skipped)
	}
	n = norm(t, qin(1, "enqueue", "one"), qin(2, "enqueue", "two"), qin(3, "remove", "something else"))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:one", "user:two"}) || n.Stats().Skipped["queue:remove-unmatched"] != 1 {
		t.Fatalf("queued %v skipped %v", got, n.Stats().Skipped)
	}
}

func TestQueue_PeerEntryCarriesBodyAndSender(t *testing.T) {
	n := norm(t, qin(1, "enqueue", `<cross-session-message from="uds:/x" from-name="mlab/purdex-b0">`+"\nplease look\n"+`</cross-session-message>`))
	q := queuedOf(n)
	if len(q) != 1 || q[0] != (Queued{Text: "please look", At: ms(1), Source: "peer", From: "mlab/purdex-b0"}) {
		t.Fatalf("queued = %+v", q)
	}
}

func TestQueue_StoredEntriesAreBounded(t *testing.T) {
	long := strings.Repeat("字", maxQueuedText*10)
	n := norm(t, qin(1, "enqueue", long))
	if got := utf8.RuneCountInString(n.queue[0].Text); got != maxQueuedText {
		t.Fatalf("stored text runes = %d", got)
	}
	feed(t, n, qin(2, "remove", long))
	if len(queuedOf(n)) != 0 {
		t.Fatal("remove did not match by hash")
	}
}

func TestQueue_UnknownOpKeyIsBounded(t *testing.T) {
	n := norm(t, qop(1, "bogus-"+strings.Repeat("z", 500)))
	found := false
	for k := range n.Stats().Skipped {
		if strings.HasPrefix(k, "queue:op:") {
			found = true
			if len(k) > len("queue:op:")+64 {
				t.Fatalf("skip key of %d bytes", len(k))
			}
		}
	}
	if !found {
		t.Fatal("no op skip counted")
	}
}
