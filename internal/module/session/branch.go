package session

import (
	"os"

	"github.com/wake/purdex/internal/gitbranch"
)

var branchStopAt = func() string {
	home, _ := os.UserHomeDir()
	return home
}()

func branchOf(cwd string) string { return gitbranch.Branch(cwd, branchStopAt) }
