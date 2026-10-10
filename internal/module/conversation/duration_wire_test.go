package conversation

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
)

// duration_ms must survive both turn encodings (snapshot turns and increment headers).
func TestWire_TurnDurationMS(t *testing.T) {
	d := int64(9000)
	turn := convmodel.Turn{ID: "t1", Outcome: convmodel.OutcomeDone, DurationMS: &d, Items: []convmodel.Item{}}
	b, err := encodeAPITurn(turn)
	if err != nil || !strings.Contains(string(b), `"duration_ms":9000`) {
		t.Errorf("snapshot turn = %s (%v)", b, err)
	}
	m := &Module{maxBody: 1 << 20}
	body, ok := m.encodeIncrement(nil, convfeed.Increment{Changes: []convfeed.TurnChange{{Turn: turn}}}, sid, "h", 0)
	if !ok || !strings.Contains(string(body), `"duration_ms":9000`) {
		t.Errorf("increment = %s", body)
	}
	turn.DurationMS = nil
	body, _ = m.encodeIncrement(nil, convfeed.Increment{Changes: []convfeed.TurnChange{{Turn: turn}}}, sid, "h", 0)
	if strings.Contains(string(body), "duration_ms") {
		t.Errorf("increment without duration = %s", body)
	}
}
