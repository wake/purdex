package push

// AtComputerKey is the service-registry key under which the push module publishes whether the person is at a computer now (some Mac's
// v2 presence report says so), for the modules that only need that one fact (the load watcher's patrol).
const AtComputerKey = "push.at-computer"

// AtComputerReader is the value under AtComputerKey.
type AtComputerReader interface {
	AtComputer() bool
}
