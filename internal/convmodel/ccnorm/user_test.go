package ccnorm

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

func userOf(t testing.TB, c convmodel.Conversation, turn, item int) *convmodel.UserMessage {
	t.Helper()
	items := itemsOf(t, c, turn)
	if item >= len(items) || items[item].User == nil {
		t.Fatalf("turn %d item %d is not a user item: %v\n%s", turn, item, sigs(items), dump(c))
	}
	return items[item].User
}

// ---- turn opening ---------------------------------------------------------

func TestTurns_OpenAtPromptRowWithTurnPosition(t *testing.T) {
	c := conv(t, userRow("u1", 1, "hello"), assistantText("a1", 2, "hi"), userRow("u2", 5, "again"))
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d, want 2\n%s", len(c.Turns), dump(c))
	}
	if c.Turns[0].ID != "u1" || c.Turns[1].ID != "u2" || c.Turns[1].Index != 1 {
		t.Errorf("turn ids/index: %q %q %d", c.Turns[0].ID, c.Turns[1].ID, c.Turns[1].Index)
	}
	if c.Turns[0].StartedAt != ms(1) {
		t.Errorf("started_at = %d, want %d", c.Turns[0].StartedAt, ms(1))
	}
	if u := userOf(t, c, 0, 0); u.ID != "u1" || u.Text != "hello" || u.Source != convmodel.SourceUser || u.At != ms(1) {
		t.Errorf("user item = %+v", u)
	}
}

func TestTurns_OpenAtPromptRowWithoutTurnPosition(t *testing.T) {
	// pre-2.1.284: no turnPosition, turnOrigin, origin or promptSource
	c := conv(t, oldUserRow("u1", 1, "hello"), assistantText("a1", 2, "hi"), oldUserRow("u2", 5, "again"))
	if len(c.Turns) != 2 || userOf(t, c, 1, 0).Text != "again" {
		t.Fatalf("turns = %d\n%s", len(c.Turns), dump(c))
	}
	if userOf(t, c, 0, 0).Source != convmodel.SourceUser {
		t.Error("an old-shape prompt row must be a user message")
	}
}

func TestTurns_LocalCommandIsATurn(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "hello"), assistantText("a1", 2, "hi"), turnDuration("d1", 3, 2000),
		localCommandRow("lc1", 10, "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args></command-args>"),
		localCommandRow("lc2", 10.1, "<local-command-stdout>Set model to opus</local-command-stdout>"),
		userRow("u2", 20, "next"),
	)
	if len(c.Turns) != 3 {
		t.Fatalf("turns = %d, want 3\n%s", len(c.Turns), dump(c))
	}
	cmd := c.Turns[1]
	if cmd.ID != "lc1" {
		t.Errorf("turn id = %q, want the command row's uuid", cmd.ID)
	}
	if got := sigs(cmd.Items); !equalStrings(got, []string{"user:slash:/model", "system:command_output"}) {
		t.Errorf("items = %v", got)
	}
	if cmd.Items[1].System.ID != "lc2" || string(cmd.Items[1].System.Detail) != `{"text":"Set model to opus"}` {
		t.Errorf("command_output = %+v", cmd.Items[1].System)
	}
	if cmd.Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome = %q: a command that never reaches the model has no model work to wait for", cmd.Outcome)
	}
}

func TestTurns_LocalCommandAsUserRowsIsATurn(t *testing.T) {
	// CC before 2.1.289 wrote local commands as user rows, no turnPosition
	c := conv(t,
		oldUserRow("lc1", 1, "<command-name>/exit</command-name>\n<command-message>exit</command-message>\n<command-args></command-args>"),
		oldUserRow("lc2", 1.1, "<local-command-stdout>bye</local-command-stdout>"),
		oldUserRow("lc0", 1.2, "<local-command-caveat>Caveat: ignore</local-command-caveat>"),
	)
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d, want 1\n%s", len(c.Turns), dump(c))
	}
	if got := sigs(c.Turns[0].Items); !equalStrings(got, []string{"user:slash:/exit", "system:command_output"}) {
		t.Errorf("items = %v", got)
	}
}

func TestTurns_BashModeIsOneTurn(t *testing.T) {
	// c2 lines 168-170: <bash-input> has no turnPosition, the <bash-stdout>
	// prompt row has one, and the model answers
	c := conv(t,
		userRow("b1", 1, "<bash-input>echo C2BANG</bash-input>", without("turnPosition"), without("origin"), without("promptSource"), without("turnOrigin")),
		userRow("b2", 1.1, "<bash-stdout>C2BANG</bash-stdout><bash-stderr></bash-stderr>", without("origin"), without("promptSource")),
		assistantText("a1", 2, "got it"),
		turnDuration("d1", 3, 1900),
	)
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d, want 1\n%s", len(c.Turns), dump(c))
	}
	items := c.Turns[0].Items
	if got := sigs(items); !equalStrings(got, []string{"user:bash:echo C2BANG", "system:command_output", "agent_text:got it"}) {
		t.Fatalf("items = %v", got)
	}
	if items[1].System.ID != "b2" || string(items[1].System.Detail) != `{"text":"C2BANG"}` {
		t.Errorf("command_output = %+v detail %s", items[1].System, items[1].System.Detail)
	}
	if c.Turns[0].Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome = %q", c.Turns[0].Outcome)
	}
}

func TestTurns_BashOutputKeepsStderr(t *testing.T) {
	c := conv(t,
		userRow("b1", 1, "<bash-input>ls nope</bash-input>", without("turnPosition")),
		userRow("b2", 1.1, "<bash-stdout></bash-stdout><bash-stderr>ls: nope: No such file</bash-stderr>"),
	)
	items := c.Turns[0].Items
	if len(items) != 2 || string(items[1].System.Detail) != `{"text":"ls: nope: No such file"}` {
		t.Errorf("items = %v detail = %s", sigs(items), items[len(items)-1].System.Detail)
	}
}

func TestTurns_AbsorbedQueuedPromptStaysInRunningTurn(t *testing.T) {
	// M-U1-7: a prompt sent during a turn is enqueued, then absorbed as a
	// queued_command attachment inside the running turn.
	c := conv(t,
		userRow("u1", 1, "write a story"),
		queueOp(3, "enqueue"),
		assistantText("a1", 4, "once upon a time"),
		queuedCommand("q1", 5, "also say QUEUED", obj{"kind": "human"}, "prompt"),
		queueOp(5, "remove"),
		assistantText("a2", 6, "QUEUED"),
		turnDuration("d1", 7, 6000),
	)
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d, want 1: an absorbed queued prompt does not open a turn\n%s", len(c.Turns), dump(c))
	}
	if got := sigs(c.Turns[0].Items); !equalStrings(got, []string{
		"user:user:write a story", "agent_text:once upon a time", "user:queued:also say QUEUED", "agent_text:QUEUED",
	}) {
		t.Errorf("items = %v", got)
	}
	if c.Turns[0].Items[2].User.ID != "q1" {
		t.Errorf("queued item id = %q, want the attachment row's uuid", c.Turns[0].Items[2].User.ID)
	}
}

func TestTurns_QueuedPromptRowOpensTheNextTurn(t *testing.T) {
	// f2b lines 60-67: waits for the turn to end, then is the next prompt row
	c := conv(t,
		userRow("u1", 1, "write a story"),
		queueOp(2, "enqueue"),
		assistantText("a1", 3, "story"),
		queueOp(4, "dequeue"),
		turnDuration("d1", 4, 3000),
		userRow("u2", 4.1, "say QUEUED2", promptSource("queued")),
		assistantText("a2", 5, "QUEUED2"),
		turnDuration("d2", 6, 1000),
	)
	if len(c.Turns) != 2 || userOf(t, c, 1, 0).Source != convmodel.SourceQueued {
		t.Fatalf("turns = %d\n%s", len(c.Turns), dump(c))
	}
}

func TestTurns_InterruptMarkerOpensNoTurn(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"), interruptRow("i1", 3, false))
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d, want 1\n%s", len(c.Turns), dump(c))
	}
	// the tool-use form carries no interruptedMessageId
	c = conv(t, userRow("u1", 1, "go"), interruptRow("i1", 3, true))
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d, want 1 for the tool-use marker\n%s", len(c.Turns), dump(c))
	}
}

func TestTurns_FileStartingMidConversationOpensUserlessTurn(t *testing.T) {
	c := conv(t,
		assistantText("a1", 1, "continuing"),
		toolResultRow("r1", 2, "toolu_1", "ok"),
		userRow("u1", 3, "next"),
	)
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d, want 2\n%s", len(c.Turns), dump(c))
	}
	first := c.Turns[0]
	if first.ID != "a1" || first.StartedAt != ms(1) {
		t.Errorf("userless turn id/start = %q/%d", first.ID, first.StartedAt)
	}
	if got := sigs(first.Items); !equalStrings(got, []string{"agent_text:continuing"}) {
		t.Errorf("items = %v: the first turn has no user item", got)
	}
	if c.Turns[1].Index != 1 {
		t.Error("index of the second turn")
	}
}

func TestTurns_IndexStableWhenMoreLinesArrive(t *testing.T) {
	lines := [][]byte{
		userRow("u1", 1, "a"), assistantText("a1", 2, "x"), turnDuration("d1", 3, 1),
		userRow("u2", 4, "b"), assistantText("a2", 5, "y"), turnDuration("d2", 6, 1),
		userRow("u3", 7, "c"),
	}
	n := New(Options{})
	seen := map[string]int{}
	for _, l := range lines {
		feed(t, n, l)
		for _, tr := range n.Conversation().Turns {
			if prev, ok := seen[tr.ID]; ok && prev != tr.Index {
				t.Fatalf("turn %q index moved %d → %d", tr.ID, prev, tr.Index)
			}
			seen[tr.ID] = tr.Index
		}
	}
	if seen["u1"] != 0 || seen["u2"] != 1 || seen["u3"] != 2 {
		t.Errorf("indexes = %v", seen)
	}
}

// ---- rows -----------------------------------------------------------------

func TestRows_SidechainSkipped(t *testing.T) {
	n := norm(t,
		userRow("s1", 1, "brief", sidechain()),
		assistantText("s2", 2, "sub", sidechain()),
	)
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["sidechain"] != 2 {
		t.Errorf("turns=%d skipped=%v", len(n.Conversation().Turns), n.Stats().Skipped)
	}
}

func TestRows_MetaSkippedExceptPeer(t *testing.T) {
	// an isMeta expansion row of a slash command is not a prompt
	n := norm(t,
		userRow("u1", 1, "<command-name>/x</command-name>"),
		userRow("m1", 1.1, "expanded skill text", isMeta(), without("origin"), without("promptSource"), without("turnOrigin"), without("turnPosition")),
	)
	c := validated(t, n)
	if len(c.Turns) != 1 || len(c.Turns[0].Items) != 1 {
		t.Errorf("a meta row became content: %s", dump(c))
	}
	if n.Stats().Skipped["meta"] != 1 {
		t.Errorf("Skipped = %v", n.Stats().Skipped)
	}

	// an isMeta peer message is kept
	c = conv(t, userRow("p1", 2, peerText("hello", "purdex-47"), isMeta(), originKind("peer"), turnOrigin("peer"), promptSource("system")))
	if len(c.Turns) != 1 || userOf(t, c, 0, 0).Source != convmodel.SourcePeer {
		t.Errorf("meta peer row dropped: %s", dump(c))
	}
}

func TestRows_MetaScheduledOpensTurn(t *testing.T) {
	// census: scheduled wake-ups are isMeta rows with turnPosition and no
	// origin; the source table needs them reachable
	c := conv(t, userRow("w1", 1, "check the build", isMeta(), without("origin"), turnOrigin("scheduled"), promptSource("system")))
	if len(c.Turns) != 1 || userOf(t, c, 0, 0).Source != convmodel.SourceScheduled {
		t.Errorf("scheduled row: %s", dump(c))
	}
}

func TestRows_CompactSummarySkipped(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), compactSummaryRow("cs1", 2))
	c := validated(t, n)
	if len(c.Turns) != 1 || len(c.Turns[0].Items) != 1 {
		t.Errorf("the compact summary became content: %s", dump(c))
	}
	if n.Stats().Skipped["compact_summary"] != 1 {
		t.Errorf("Skipped = %v", n.Stats().Skipped)
	}
}

// ---- sources --------------------------------------------------------------

// peerText is the text of a peer message as CC writes it (the wrapper is
// internal/peers/ccuds/wrapper.go's).
func peerText(body, fromName string) string {
	return "another claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"" +
		fromName + "\" from-mode=\"bypass\">\n" + body + "\n</cross-session-message>"
}

func taskNotification(summary string) string {
	return "<task-notification>\n<task-id>bnh3feck0</task-id>\n<status>completed</status>\n<summary>" + summary +
		"</summary>\n</task-notification>"
}

func TestSource_Typed(t *testing.T) {
	c := conv(t, userRow("u1", 1, "hello"))
	u := userOf(t, c, 0, 0)
	if u.Source != convmodel.SourceUser || u.From != nil || u.Text != "hello" {
		t.Errorf("user = %+v", u)
	}
}

func TestSource_SuggestionAcceptedIsUser(t *testing.T) {
	c := conv(t, userRow("u1", 1, "yes please", promptSource("suggestion_accepted")))
	if got := userOf(t, c, 0, 0).Source; got != convmodel.SourceUser {
		t.Errorf("source = %q, want user", got)
	}
}

func TestSource_QueuedPromptRow(t *testing.T) {
	// f2b line 67
	c := conv(t, userRow("u1", 1, "QUEUED2", promptSource("queued")))
	if got := userOf(t, c, 0, 0).Source; got != convmodel.SourceQueued {
		t.Errorf("source = %q, want queued", got)
	}
}

func TestSource_QueuedAttachmentFromHuman(t *testing.T) {
	for name, origin := range map[string]obj{"origin human": {"kind": "human"}, "no origin": nil} {
		c := conv(t, userRow("u1", 1, "go"), queuedCommand("q1", 2, "and this", origin, "prompt"))
		items := c.Turns[0].Items
		if len(c.Turns) != 1 || len(items) != 2 || items[1].User == nil || items[1].User.Source != convmodel.SourceQueued || items[1].User.Text != "and this" {
			t.Errorf("%s: %s", name, dump(c))
		}
	}
}

func TestSource_PeerMetaRowStripsWrapper(t *testing.T) {
	c := conv(t, userRow("p1", 1, peerText("hello\nworld", "a&amp;b"),
		isMeta(), with("origin", obj{"kind": "peer", "from": "uds:/tmp/cc-socks/1.sock", "msg_id": "m1"}), turnOrigin("peer"), promptSource("system")))
	u := userOf(t, c, 0, 0)
	if u.Source != convmodel.SourcePeer || u.Text != "hello\nworld" {
		t.Errorf("user = %+v", u)
	}
	if u.From == nil || u.From.Kind != "peer" || u.From.Name != "a&b" {
		t.Errorf("from = %+v, want {peer a&b} (the attribute is unescaped, the body is not)", u.From)
	}

	// a wrapper-less text is kept as is; the name falls back to origin.name
	c = conv(t, userRow("p2", 1, "plain", isMeta(), with("origin", obj{"kind": "peer", "name": "air26-1", "from": "uds:/x"}), turnOrigin("peer"), promptSource("system")))
	u = userOf(t, c, 0, 0)
	if u.Text != "plain" || u.From == nil || u.From.Name != "air26-1" {
		t.Errorf("user = %+v from %+v", u, u.From)
	}
}

func TestSource_PeerQueuedCommand(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"),
		queuedCommand("q1", 2, peerText("ping", "purdex-9"), obj{"kind": "peer", "from": "uds:/x", "msg_id": "m"}, "prompt"))
	if len(c.Turns) != 1 {
		t.Fatalf("turns = %d: %s", len(c.Turns), dump(c))
	}
	u := userOf(t, c, 0, 1)
	if u.Source != convmodel.SourcePeer || u.Text != "ping" || u.From == nil || u.From.Name != "purdex-9" {
		t.Errorf("user = %+v from %+v", u, u.From)
	}
}

func TestSource_TaskNotificationSummaryIsUnescaped(t *testing.T) {
	// the harness writes the summary HTML-escaped (measured on real transcripts: &amp; and &gt;)
	row := userRow("t1", 1, taskNotification(`Background command "cd x &amp;&amp; make &gt; out" completed`),
		with("origin", obj{"kind": "task-notification", "producer": "session-task"}), turnOrigin("task_notification"), promptSource("system"))
	if got := userOf(t, conv(t, row), 0, 0).Text; got != `Background command "cd x && make > out" completed` {
		t.Errorf("summary text = %q", got)
	}
}

func TestSource_TaskNotificationUsesSummary(t *testing.T) {
	row := userRow("t1", 1, taskNotification(`Background command "sleep" completed`),
		with("origin", obj{"kind": "task-notification", "producer": "session-task"}), turnOrigin("task_notification"), promptSource("system"))
	c := conv(t, row)
	u := userOf(t, c, 0, 0)
	if u.Source != convmodel.SourceTask || u.Text != `Background command "sleep" completed` {
		t.Errorf("user = %+v", u)
	}

	// no summary: the text without tags
	c = conv(t, userRow("t2", 1, "<task-notification>\n<task-id>x</task-id>\ndone\n</task-notification>",
		with("origin", obj{"kind": "task-notification"}), turnOrigin("task_notification"), promptSource("system")))
	if got := userOf(t, c, 0, 0).Text; got != "x\ndone" {
		t.Errorf("no-summary text = %q", got)
	}

	// a queued_command attachment: by origin, and by commandMode with no origin
	for name, origin := range map[string]obj{"origin": {"kind": "task-notification", "producer": "p"}, "mode only": nil} {
		c = conv(t, userRow("u1", 0.5, "go"), queuedCommand("q1", 2, taskNotification("bg done"), origin, "task-notification"))
		if len(c.Turns) != 1 || userOf(t, c, 0, 1).Source != convmodel.SourceTask || userOf(t, c, 0, 1).Text != "bg done" {
			t.Errorf("%s: %s", name, dump(c))
		}
	}

	// no origin at all, spelled only by turnOrigin (census: task_notification)
	c = conv(t, userRow("t3", 1, taskNotification("only turnOrigin"), without("origin"), turnOrigin("task_notification"), promptSource("system")))
	if got := userOf(t, c, 0, 0); got.Source != convmodel.SourceTask || got.Text != "only turnOrigin" {
		t.Errorf("user = %+v", got)
	}
}

func TestSource_Scheduled(t *testing.T) {
	c := conv(t, userRow("w1", 1, "run the check", isMeta(), without("origin"), turnOrigin("scheduled"), promptSource("system")))
	if u := userOf(t, c, 0, 0); u.Source != convmodel.SourceScheduled || u.Text != "run the check" {
		t.Errorf("user = %+v", u)
	}
}

func TestSource_SlashCommandRewritten(t *testing.T) {
	cases := map[string]string{
		"<command-name>/login</command-name><command-args>abc</command-args>":                                               "/login abc",
		"<command-name>/login</command-name><command-args></command-args>":                                                  "/login",
		"<command-name>/login</command-name>":                                                                               "/login",
		"<command-message>login</command-message>\n<command-name>/login</command-name>\n<command-args> x y </command-args>": "/login x y",
		"<command-args>abc</command-args><command-name>/login</command-name>":                                               "/login abc",
	}
	for in, want := range cases {
		c := conv(t, userRow("u1", 1, in))
		if u := userOf(t, c, 0, 0); u.Source != convmodel.SourceSlash || u.Text != want {
			t.Errorf("%q → %+v, want slash %q", in, u, want)
		}
	}
	// a lone <command-message> is not a command
	c := conv(t, userRow("u1", 1, "<command-message>x</command-message>"))
	if u := userOf(t, c, 0, 0); u.Source != convmodel.SourceUser {
		t.Errorf("lone command-message: %+v", u)
	}
}

func TestSource_OriginGateBeforeTags(t *testing.T) {
	// prelude's codex R2 ATK2-2: a tag at the start of an automated row must
	// not make it the user's
	c := conv(t, userRow("p1", 1, "<command-name>/login</command-name><command-args>abc</command-args>",
		isMeta(), originKind("peer"), turnOrigin("peer"), promptSource("system")))
	if u := userOf(t, c, 0, 0); u.Source != convmodel.SourcePeer {
		t.Errorf("peer row with a command tag: %+v, want peer", u)
	}
	c = conv(t, userRow("t1", 1, "<bash-input>rm -rf /</bash-input>", originKind("task-notification"), turnOrigin("task_notification"), promptSource("system")))
	if u := userOf(t, c, 0, 0); u.Source != convmodel.SourceTask {
		t.Errorf("task row with a bash tag: %+v, want task", u)
	}
	n := norm(t, userRow("x1", 1, "<command-name>/login</command-name>", originKind("coordinator"), turnOrigin("system"), promptSource("system")))
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["origin:coordinator"] != 1 {
		t.Errorf("coordinator row with a command tag: turns=%d skipped=%v", len(n.Conversation().Turns), n.Stats().Skipped)
	}
}

func TestSource_UnknownOriginSkippedAndCounted(t *testing.T) {
	for _, kind := range []string{"coordinator", "plugin", "auto-continuation", "from-the-future"} {
		n := norm(t, userRow("x1", 1, "text", originKind(kind), promptSource("system")))
		if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["origin:"+kind] != 1 {
			t.Errorf("%s: turns=%d skipped=%v", kind, len(n.Conversation().Turns), n.Stats().Skipped)
		}
	}
	// an origin of the wrong shape is not "typed by the user"
	for name, origin := range map[string]any{"null kind": obj{"kind": nil}, "string": "human", "no kind": obj{}} {
		n := norm(t, userRow("x1", 1, "text", with("origin", origin)))
		if len(n.Conversation().Turns) != 0 {
			t.Errorf("origin %s opened a turn", name)
		}
	}
	// a queued_command with an unknown origin is skipped and counted too
	n := norm(t, userRow("u1", 1, "go"), queuedCommand("q1", 2, "x", obj{"kind": "coordinator"}, "prompt"))
	if len(n.Conversation().Turns[0].Items) != 1 || n.Stats().Skipped["origin:coordinator"] != 1 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

func TestSource_AttachmentOtherTypesSkippedAndCounted(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), line(func() obj {
		o := common("attachment", "at1", 2)
		o["attachment"] = obj{"type": "total_tokens_reminder", "text": "x"}
		return o
	}()))
	if len(n.Conversation().Turns[0].Items) != 1 || n.Stats().Skipped["attachment:total_tokens_reminder"] != 1 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

func TestSource_PrecedenceMatrix(t *testing.T) {
	const skipped = ""
	type row struct {
		name                 string
		origin, turnOrig, ps string // "" = field absent
		meta                 bool
		want                 convmodel.Source // skipped = opens no turn
	}
	cases := []row{
		{"typed, all fields", "human", "human", "typed", false, convmodel.SourceUser},
		{"queued, all fields", "human", "human", "queued", false, convmodel.SourceQueued},
		{"nothing but typed", "", "", "typed", false, convmodel.SourceUser},
		{"nothing but queued", "", "", "queued", false, convmodel.SourceQueued},
		{"no fields (old row)", "", "", "", false, convmodel.SourceUser},
		{"suggestion accepted", "human", "", "suggestion_accepted", false, convmodel.SourceUser},
		{"sdk", "", "sdk", "sdk", false, convmodel.SourceUser},
		{"queued by turnOrigin human", "", "human", "queued", false, convmodel.SourceQueued},
		{"peer, all fields", "peer", "peer", "system", true, convmodel.SourcePeer},
		{"origin peer beats turnOrigin human", "peer", "human", "typed", true, convmodel.SourcePeer},
		{"origin human beats turnOrigin peer", "human", "peer", "typed", false, convmodel.SourceUser},
		{"origin human beats turnOrigin scheduled", "human", "scheduled", "typed", false, convmodel.SourceUser},
		{"task, both spellings", "task-notification", "task_notification", "system", false, convmodel.SourceTask},
		{"task, turnOrigin only", "", "task_notification", "system", false, convmodel.SourceTask},
		{"task, turnOrigin only, queued source", "", "task_notification", "queued", false, convmodel.SourceTask},
		{"task origin beats promptSource queued", "task-notification", "", "queued", false, convmodel.SourceTask},
		{"scheduled", "", "scheduled", "system", true, convmodel.SourceScheduled},
		{"coordinator", "coordinator", "system", "system", false, skipped},
		{"plugin", "plugin", "system", "system", false, skipped},
		{"auto-continuation, both spellings", "auto-continuation", "auto_continuation", "system", false, skipped},
		{"auto_continuation, turnOrigin only", "", "auto_continuation", "system", false, skipped},
		{"system turnOrigin", "", "system", "system", false, skipped},
		{"system promptSource only", "", "", "system", false, skipped},
		{"unknown origin kind", "from-the-future", "human", "typed", false, skipped},
		{"unknown turnOrigin", "", "from-the-future", "typed", false, skipped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []opt{without("origin"), without("turnOrigin"), without("promptSource")}
			if tc.origin != "" {
				opts = append(opts, originKind(tc.origin))
			}
			if tc.turnOrig != "" {
				opts = append(opts, turnOrigin(tc.turnOrig))
			}
			if tc.ps != "" {
				opts = append(opts, promptSource(tc.ps))
			}
			if tc.meta {
				opts = append(opts, isMeta())
			}
			text := "hello"
			switch tc.want {
			case convmodel.SourceTask:
				text = taskNotification("hello")
			case convmodel.SourcePeer:
				text = peerText("hello", "p")
			}
			n := norm(t, userRow("u1", 1, text, opts...))
			c := validated(t, n)
			if tc.want == skipped {
				if len(c.Turns) != 0 {
					t.Fatalf("opened a turn, want skipped: %s", dump(c))
				}
				return
			}
			if len(c.Turns) != 1 {
				t.Fatalf("turns = %d, want 1 (%v)", len(c.Turns), n.Stats().Skipped)
			}
			u := userOf(t, c, 0, 0)
			if u.Source != tc.want || u.Text != "hello" {
				t.Errorf("source = %q text = %q, want %q hello", u.Source, u.Text, tc.want)
			}
		})
	}
}

func TestSource_SkippedRowOpensNoTurn(t *testing.T) {
	// at the file start and in the middle of a conversation
	n := norm(t, userRow("x0", 0.5, "automated", originKind("coordinator"), promptSource("system")))
	if len(n.Conversation().Turns) != 0 {
		t.Fatal("a skipped row opened the first turn")
	}
	feed(t, n,
		userRow("u1", 1, "go"),
		userRow("x1", 2, "automated", originKind("plugin"), turnOrigin("system"), promptSource("system")),
		assistantText("a1", 3, "ok"),
	)
	c := validated(t, n)
	if len(c.Turns) != 1 || len(c.Turns[0].Items) != 2 {
		t.Errorf("a skipped row split the turn: %s", dump(c))
	}
}

// ---- user text and images -------------------------------------------------

func TestUser_ContentListJoinsTextBlocks(t *testing.T) {
	c := conv(t, userRow("u1", 1, "", blocksOf(obj{"type": "text", "text": "one"}, obj{"type": "text", "text": "two"})))
	if got := userOf(t, c, 0, 0).Text; got != "one\ntwo" {
		t.Errorf("text = %q", got)
	}
}

func TestUser_ImageBlocksBecomePlaceholders(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(make([]byte, 1000))
	img := obj{"type": "image", "source": obj{"type": "base64", "media_type": "image/png", "data": data + "\n"}}
	c := conv(t, userRow("u1", 1, "", blocksOf(obj{"type": "text", "text": "look [Image #1]"}, img)))
	u := userOf(t, c, 0, 0)
	if u.Text != "look [Image #1]" {
		t.Errorf("text = %q: the [Image #n] marker stays", u.Text)
	}
	if len(u.Images) != 1 || u.Images[0].MediaType != "image/png" || u.Images[0].Bytes != 1000 {
		t.Errorf("images = %+v, want one image/png of 1000 bytes (the decoded size)", u.Images)
	}
	if strings.Contains(jsonOf(t, c), data[:64]) {
		t.Error("the base64 data is in the output")
	}

	// an image-only prompt still opens a turn; a broken image has size 0
	c = conv(t, userRow("u2", 2, "", blocksOf(obj{"type": "image", "source": obj{"type": "url", "url": "https://x"}})))
	if len(c.Turns) != 1 || len(userOf(t, c, 0, 0).Images) != 1 || userOf(t, c, 0, 0).Images[0].Bytes != 0 {
		t.Errorf("image-only prompt: %s", dump(c))
	}
}

func TestUser_TextCappedAt64KiB(t *testing.T) {
	long := strings.Repeat("界", 30000) // 90,000 bytes
	c := conv(t, userRow("u1", 1, long))
	u := userOf(t, c, 0, 0)
	if !u.Truncated || len(u.Text) > convmodel.MaxText || len(u.Text) < convmodel.MaxText-3 || !utf8.ValidString(u.Text) {
		t.Errorf("len = %d truncated = %v valid = %v", len(u.Text), u.Truncated, utf8.ValidString(u.Text))
	}
	if !strings.HasPrefix(long, u.Text) {
		t.Error("the kept text is not the head")
	}

	exact := strings.Repeat("a", convmodel.MaxText)
	if u := userOf(t, conv(t, userRow("u2", 1, exact)), 0, 0); u.Truncated || len(u.Text) != convmodel.MaxText {
		t.Errorf("exactly the cap: len %d truncated %v", len(u.Text), u.Truncated)
	}
	over := strings.Repeat("a", convmodel.MaxText+1)
	if u := userOf(t, conv(t, userRow("u3", 1, over)), 0, 0); !u.Truncated || len(u.Text) != convmodel.MaxText {
		t.Errorf("one over the cap: len %d truncated %v", len(u.Text), u.Truncated)
	}
}
