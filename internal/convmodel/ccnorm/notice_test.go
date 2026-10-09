package ccnorm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func informational(uuid string, sec float64, content any, opts ...opt) []byte {
	o := common("system", uuid, sec)
	o["subtype"] = "informational"
	o["content"] = content
	o["level"] = "notice"
	o["isMeta"] = false
	return line(o, opts...)
}

func TestTurnDuration_DurationMS(t *testing.T) {
	cases := []struct {
		name string
		row  []byte
		want *int64
	}{
		{"valid", turnDuration("d", 3, 9000), i64p(9000)},
		{"zero", turnDuration("d", 3, 0), i64p(0)},
		{"missing", turnDuration("d", 3, 1, without("durationMs")), nil},
		{"negative", turnDuration("d", 3, 1, with("durationMs", -5)), nil},
		{"string", turnDuration("d", 3, 1, with("durationMs", "9000")), nil},
	}
	for _, tc := range cases {
		c := conv(t, userRow("u1", 1, "go"), assistantText("a1", 2, "ok"), tc.row)
		got := c.Turns[0].DurationMS
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: duration_ms = %v, want %v", tc.name, got, tc.want)
		}
		if c.Turns[0].Outcome != convmodel.OutcomeDone {
			t.Errorf("%s: outcome = %q, want done", tc.name, c.Turns[0].Outcome)
		}
	}
	// the last of several wins
	c := conv(t, userRow("u1", 1, "go"), turnDuration("d1", 2, 100), turnDuration("d2", 3, 250))
	if d := c.Turns[0].DurationMS; d == nil || *d != 250 {
		t.Errorf("last duration = %v, want 250", d)
	}
	b, _ := json.Marshal(c.Turns[0])
	if !strings.Contains(string(b), `"duration_ms":250`) {
		t.Errorf("json = %s", b)
	}
	b, _ = json.Marshal(conv(t, userRow("u1", 1, "go")).Turns[0])
	if strings.Contains(string(b), "duration_ms") {
		t.Errorf("json without duration has duration_ms: %s", b)
	}
}

func TestNotice_InOpenTurn(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"), informational("n1", 2, "plugin reloaded"), assistantText("a1", 3, "ok"))
	items := c.Turns[0].Items
	if len(items) != 3 || items[1].System == nil {
		t.Fatalf("items = %v", sigs(items))
	}
	s := items[1].System
	if s.Kind != convmodel.SystemNotice || s.ID != "n1" || s.At != ms(2) ||
		string(s.Detail) != `{"text":"plugin reloaded","level":"notice"}` {
		t.Errorf("system = %+v detail %s", s, s.Detail)
	}
	b, _ := json.Marshal(items[1])
	if !strings.Contains(string(b), `"kind":"notice"`) {
		t.Errorf("json = %s", b)
	}
	// level absent is omitted; text escaping like command_output
	c = conv(t, userRow("u1", 1, "go"), informational("n1", 2, "a < b", without("level")))
	if d := string(c.Turns[0].Items[1].System.Detail); d != `{"text":"a < b"}` {
		t.Errorf("detail = %s", d)
	}
}

func TestNotice_BetweenTurnsGoesToPreviousTurn(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "one"), turnDuration("d1", 2, 10),
		informational("n1", 3, "between"),
		userRow("u2", 4, "two"), turnDuration("d2", 5, 10),
	)
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d", len(c.Turns))
	}
	last := c.Turns[0].Items[len(c.Turns[0].Items)-1]
	if last.System == nil || last.System.Kind != convmodel.SystemNotice {
		t.Errorf("turn 0 items = %v", sigs(c.Turns[0].Items))
	}
	if len(c.Turns[1].Items) != 1 {
		t.Errorf("turn 1 items = %v", sigs(c.Turns[1].Items))
	}
}

func TestNotice_BeforeFirstTurnSkipped(t *testing.T) {
	n := norm(t, informational("n1", 1, "early"), userRow("u1", 2, "go"))
	if n.Stats().Skipped["orphan_notice"] != 1 || len(n.Conversation().Turns[0].Items) != 1 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

func TestNotice_TruncatedAndNonString(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"), informational("n1", 2, strings.Repeat("a", convmodel.MaxText+10)))
	var d struct {
		Text      string
		Truncated bool
	}
	if err := json.Unmarshal(c.Turns[0].Items[1].System.Detail, &d); err != nil || !d.Truncated || len(d.Text) != convmodel.MaxText {
		t.Errorf("detail truncated = %v len %d err %v", d.Truncated, len(d.Text), err)
	}
	n := norm(t, userRow("u1", 1, "go"), informational("n1", 2, 42), informational("n2", 3, []string{"x"}))
	if len(n.Conversation().Turns[0].Items) != 1 || n.Stats().Skipped["notice:content"] != 2 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

func i64p(v int64) *int64 { return &v }
