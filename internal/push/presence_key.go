package push

// AtComputerKey is the service-registry key under which the push module publishes whether the person is at a computer now (some Mac's
// v2 presence report says so), for the modules that only need that one fact (the load watcher's patrol).
const AtComputerKey = "push.at-computer"

// AtComputerReader is the value under AtComputerKey.
type AtComputerReader interface {
	AtComputer() bool
}

// PresenceStateReader is the optional second face of the value under AtComputerKey (the push module's presence implements both): the same
// fact in three values. known is false when no unexpired v2 report says anything (no Mac is reporting); atComputer is false then too, but that
// does not mean the person is away. A reader that needs the difference (the watcher's user_back) asks with a type assertion; a value that is
// only an AtComputerReader is "not known".
type PresenceStateReader interface {
	PresenceState() (atComputer, known bool)
}
