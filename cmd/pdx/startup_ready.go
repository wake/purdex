package main

import (
	"fmt"
	"time"
)

// startupReadyLine is the one-line boot summary logged just before the
// daemon starts listening (#1767). init/start are the InitModules and
// StartModules wall times; total runs from the top of runServe.
func startupReadyLine(total, init, start time.Duration) string {
	return fmt.Sprintf("startup: ready in %dms (init=%dms start=%dms, process start to ready)",
		total.Milliseconds(), init.Milliseconds(), start.Milliseconds())
}
