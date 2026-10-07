package main

import (
	"fmt"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// startupReadyLine is the one-line boot summary logged just before the
// daemon starts listening (#1767). init/start are the InitModules and
// StartModules wall times; total runs from the top of runServe; ex is the
// tmux/ps fork tally accumulated since process start.
func startupReadyLine(total, init, start time.Duration, ex execstat.Stats) string {
	return fmt.Sprintf("startup: ready in %dms (init=%dms start=%dms, process start to ready) exec: %s",
		total.Milliseconds(), init.Milliseconds(), start.Milliseconds(), ex)
}
