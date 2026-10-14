package main

// Process exit codes for the team commands (spec §14). 0, 1 and 2 are the
// existing convention every pdx command already follows; 10–14 report what
// the daemon decided, 20–21 report that it could not be asked. A skill and
// the relay mod branch on these numbers, so they are a wire contract.
const (
	ExitOK           = 0  // approved / done / accepted
	ExitError        = 1  // other runtime or API error
	ExitUsage        = 2  // usage error, before any config load
	ExitDenied       = 10 // the user denied
	ExitTimeout      = 11 // the request timed out (U7: counts as a denial)
	ExitCancelled    = 12 // cancelled by the requester, or abandoned
	ExitRefused      = 13 // refused by team rules: request_open, later already_lead …
	ExitMemberFailed = 14 // the member did not start or did not respond (P4+)
	// ExitQueued: `pdx lease acquire` / `run` did not get the lease and did not
	// run the command: the request is queued (its place kept; the session is told when
	// it is its turn), an ask request ended without running, or a newer call replaced
	// it. Not a failure: wait for the notice.
	ExitQueued      = 15
	ExitUnavailable = 20 // the daemon stayed unreachable through the 30 s grace
	ExitUnsupported = 21 // the daemon does not have this route (plain 404)
)
