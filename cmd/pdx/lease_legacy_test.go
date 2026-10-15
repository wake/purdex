package main

// The tests of the old, daemon-lease `pdx lease run` (lease_run_test.go, lease_queued_test.go, …) drive it through runLeaseCmd.
// `pdx lease run` is the gate's alias now, so they are pointed at the old implementation, which stays until L2 removes it and
// these tests with it. The alias has its own tests (lease_run_alias_test.go).
func init() { leaseRun = runLeaseRun }
