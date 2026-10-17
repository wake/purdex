package gate

import (
	"sort"
	"time"
)

// Takes says whether the host's capability takes the command (plan contract 11, X2b item 1): the command's words (the runner
// skipped, as a conflict rule reads them) begin with one of Can and with none of Cannot. A host with no Can takes nothing, so a
// host that was only added runs nothing until the watcher has written what it can do.
func (h AssistHost) Takes(argv []string) bool {
	words := commandWords(argv)
	for _, p := range h.Cannot {
		if matchesPrefix(patternWords(p), words) {
			return false
		}
	}
	for _, p := range h.Can {
		if matchesPrefix(patternWords(p), words) {
			return true
		}
	}
	return false
}

// PickInput is what the walk knows about one waiting ticket.
type PickInput struct {
	// Hosts are the assist hosts that are Usable for the ticket's session and repo (the daemon's answer): on, ready for the repo,
	// allowed. Keyed by alias.
	Hosts map[string]AssistHost
	Argv  []string
	Class Class
	Repo  string
	// WouldWait says the ticket would wait here (all mlab slots taken, or others in front).
	WouldWait bool
	// Reserved says mlab is reserved (a release is running): whatever can go out goes out.
	Reserved bool
	Now      time.Time
}

// PickAssist is the order in which to try the assist hosts for a ticket, best first; empty keeps the ticket on mlab. A host is a
// candidate when its capability takes the command and it is not held off for the repo. The capability (written by the watcher) is what
// sends the main tests to air26 and the PHP tests to mserv; the order, by alias, only settles which of two hosts that both take a
// command is tried first. A heavy ticket goes out whenever there
// is a candidate (an assist host is the preferred place, decision 5); a light one only when it would otherwise wait on mlab; while
// mlab is reserved both go. The estimate comparison of the first design is not in the rule (user 10-17: air26, then mlab).
func PickAssist(dir string, in PickInput) []string {
	if in.Class == ClassLight && !in.WouldWait && !in.Reserved {
		return nil
	}
	var names []string
	for name, h := range in.Hosts {
		if !h.Takes(in.Argv) {
			continue
		}
		if _, held := HeldHost(dir, name, in.Repo, in.Now); held {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
