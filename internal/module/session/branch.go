package session

import (
	"os"

	"github.com/wake/purdex/internal/gitbranch"
)

// branches resolves a session cwd to its git branch by reading .git/HEAD
// directly (no subprocess). It runs outside every module lock: the list
// path holds only the abandonable refill slot, never listCacheMu.
var branches = func() *gitbranch.Resolver {
	home, _ := os.UserHomeDir()
	return gitbranch.NewResolver(home)
}()

func branchOf(cwd string) string { return branches.Branch(cwd) }
