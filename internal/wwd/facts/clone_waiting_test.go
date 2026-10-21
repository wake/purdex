package facts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A copy of a record shares nothing with it: the waiting block is a pointer (#3941).
func TestClone_WaitingIsNotShared(t *testing.T) {
	f := wire.Facts{}
	f.Worker.Waiting = &wire.Waiting{Count: 1, OldestID: "j_1"}
	c := Clone(f)
	c.Worker.Waiting.Count = 9
	if f.Worker.Waiting.Count != 1 {
		t.Error("the copy shares the waiting block")
	}
}
