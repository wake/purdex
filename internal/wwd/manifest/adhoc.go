package manifest

import "github.com/wake/purdex/internal/wwd/wire"

// AdHocName is the task name of an undeclared command (spec §8.3).
const AdHocName = "adhoc"

// AdHocTask is the stand-in task of an undeclared command run through `pdx test --adhoc -- <argv>`: it has no command of its own,
// the caller's words are the whole argv (args = "pass", appended as given, never through a shell), it only runs on this host (local,
// any number of arguments), at the lowest priority class, with the default timeouts, and it declares no locks, labels, cache or
// supersede key — nothing is known about it.
func AdHocTask() *Task {
	return &Task{
		Name:         AdHocName,
		Cwd:          ".",
		Args:         ArgsPass,
		Local:        LocalPolicy{Allowed: true, Max: -1},
		Class:        wire.ClassDev,
		Timeout:      DefaultTimeout,
		QueueTimeout: DefaultQueueTimeout,
	}
}
