package agent

import (
	"fmt"

	"github.com/wake/purdex/internal/execstat"
)

// startExecLine reports the tmux/ps forks made since base (taken at the top
// of Start), so it covers only Start's own work (#1767). Observation only.
func startExecLine(base execstat.Stats) string {
	return fmt.Sprintf("[agent] start exec: %s", execstat.Take().Sub(base))
}
