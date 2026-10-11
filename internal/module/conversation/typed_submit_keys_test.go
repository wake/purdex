package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// Keys mode: when the text was typed but the guard refuses the Enter, the text is left in the terminal. The answer is 409
// text_left_in_terminal (the deck tells the user to look at the terminal), and the id stays recorded as unknown, so a repeat
// never types again. The keys ledger is in memory only (lost on a daemon restart).
func TestTypedSubmit_KeysEnterRefusedLeavesTextInTerminal(t *testing.T) {
	for name, enterErr := range map[string]error{"guard": tmux.ErrPaneBusy, "tmux": context.DeadlineExceeded} {
		te := newTEnv(t)
		te.owners.drop()
		te.mod.tune.keysAfter = time.Millisecond
		time.Sleep(5 * time.Millisecond)
		te.keys.errs = []error{nil, enterErr} // the text goes out, the Enter is refused
		answer(t, te.submit("kt1", "hello"), 409, "refused", "", "text_left_in_terminal")
		if te.keys.n() != 2 {
			t.Fatalf("%s: calls = %d", name, te.keys.n())
		}
		answer(t, te.submit("kt1", "hello"), 200, "unknown", "keys", "")
		if te.keys.n() != 2 {
			t.Fatalf("%s: a replay typed again: %d calls", name, te.keys.n())
		}
	}
}
