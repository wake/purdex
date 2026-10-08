package resources

import "github.com/wake/purdex/internal/agent"

// RootSource lists the agent sessions whose process trees are attributed. The
// peers module's origin resolver is the production value: it reads the
// sessions registry, judging liveness from the one process snapshot the
// caller took, so listing costs no fork.
type RootSource interface {
	// ProcessRoots returns one Root per live, non-proxy session. The error
	// is a registry read failure only; a session whose process the snapshot
	// cannot vouch for is left out, not an error.
	ProcessRoots(snap *agent.ProcessSnapshot) ([]Root, error)
}
