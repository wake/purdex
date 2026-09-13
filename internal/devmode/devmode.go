// Package devmode answers one question — are developer features on? — from
// the PDX_DEV_MODE environment variable. Purdex is single-user, so the
// default is ON; only an explicit PDX_DEV_MODE=0 turns them off (spec D6).
// The env is read on every call so tests can flip it with t.Setenv.
package devmode

import "os"

// Enabled reports whether dev features (dev-update endpoints, verbose probe
// logging) are on. Unset, "1", or anything except "0" ⇒ true.
func Enabled() bool { return os.Getenv("PDX_DEV_MODE") != "0" }
