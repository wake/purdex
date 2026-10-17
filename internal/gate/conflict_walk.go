package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// The rules of a gate directory and the walk of the queue they shape (spec §5.3, plan GC.3-GC.4). The rules are read inside the
// class's admit lock, so a rule added before a take is honoured by that take; a command already running is never touched.

const conflictsFile = "conflicts.json"

type conflictsDoc struct {
	Rules []Conflict `json:"rules"`
}

// LoadConflicts reads the rules of a gate directory. No file is no rules. A file that cannot be read or makes no sense is no rules
// too, with the reason in the second answer: a broken file never strands a waiter.
func LoadConflicts(dir string) (rules []Conflict, problem string) {
	b, err := os.ReadFile(filepath.Join(dir, conflictsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, "conflicts.json unreadable: " + err.Error()
	}
	var doc conflictsDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, "conflicts.json is not JSON: " + err.Error()
	}
	var ok []Conflict
	for i, r := range doc.Rules {
		if (r.Class != ClassHeavy && r.Class != ClassLight) || len(patternWords(r.A)) == 0 || len(patternWords(r.B)) == 0 {
			return nil, fmt.Sprintf("conflicts.json: rule %d is not a rule (class %q, patterns %q and %q)", i+1, string(r.Class), r.A, r.B)
		}
		if err := validateReason(r.Reason); err != nil {
			return nil, fmt.Sprintf("conflicts.json: rule %d has a bad reason: %v", i+1, err)
		}
		ok = append(ok, r)
	}
	return ok, ""
}

// SaveConflicts replaces the rules of a gate directory atomically (a temp file renamed over it).
func SaveConflicts(dir string, rules []Conflict) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dirError(err)
	}
	if rules == nil {
		rules = []Conflict{}
	}
	b, err := json.MarshalIndent(conflictsDoc{Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, conflictsFile)
	tmp := path + "." + strconv.Itoa(os.Getpid()) + ".new"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return dirError(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return dirError(err)
	}
	return nil
}

// ConflictBlock says why a ticket does not take a slot though there is room: the holder or the ticket ahead that its command may not
// run beside, and the rule.
type ConflictBlock struct {
	With string   `json:"with"`           // "slot heavy-1" or "ticket <name>"
	Argv []string `json:"argv,omitempty"` // what that holder or ticket runs
	Rule Conflict `json:"rule"`
}

// walkResult is what the walk decided for every ticket that is not held.
type walkResult struct {
	Served  map[string]bool
	Blocked map[string]*ConflictBlock
}

// walkQueue walks the tickets in serving order (the held ones are skipped: they are ahead of nobody). A ticket is blocked when a
// holder of the class, or any ticket before it, runs a command a rule keeps apart from its own; a blocked ticket keeps its place and
// takes no capacity. Otherwise it is served while capacity is left (taking one), else it waits for capacity. selfName/selfArgv give
// the asking ticket's own command from memory (its file is not read back); an unreadable ticket carries no argv and matches nothing.
// extra is a hypothetical ticket (name "", words only) inserted after the last front/normal ticket, for the would-wait answer.
func walkQueue(class Class, q []QueueEntry, holders []Holder, rules []Conflict, capacity int, selfName string, selfArgv []string, extra []string) walkResult {
	res := walkResult{Served: map[string]bool{}, Blocked: map[string]*ConflictBlock{}}
	type seen struct {
		label string
		argv  []string
		words []string
	}
	var held []seen
	for _, h := range holders {
		if h.NoRecord || len(h.Argv) == 0 {
			continue // a holder with no argv matches no pattern
		}
		held = append(held, seen{label: "slot " + string(h.Class) + "-" + strconv.Itoa(h.Slot), argv: h.Argv, words: commandWords(h.Argv)})
	}
	var ahead []seen
	conflictWith := func(words []string) *ConflictBlock {
		if len(rules) == 0 || len(words) == 0 {
			return nil
		}
		for _, group := range [][]seen{held, ahead} {
			for _, o := range group {
				for _, r := range rules {
					if r.collides(class, words, o.words) {
						return &ConflictBlock{With: o.label, Argv: o.argv, Rule: r}
					}
				}
			}
		}
		return nil
	}
	step := func(name string, argv []string) {
		words := commandWords(argv)
		if b := conflictWith(words); b != nil {
			if name != "" {
				res.Blocked[name] = b
			} else {
				res.Blocked[""] = b
			}
		} else if capacity > 0 {
			capacity--
			res.Served[name] = true
		}
		if len(words) > 0 {
			ahead = append(ahead, seen{label: "ticket " + name, argv: argv, words: words})
		}
	}
	extraDone := extra == nil
	for _, e := range q {
		if e.Rank.Rank == RankHold {
			continue
		}
		if !extraDone && e.Rank.Rank == RankBack {
			step("", extra)
			extraDone = true
		}
		argv := e.Argv
		if e.Name == selfName {
			argv = selfArgv
		}
		step(e.Name, argv)
	}
	if !extraDone {
		step("", extra)
	}
	return res
}

// waitersWithConflicts is Waiters with the conflict each waiter is held by, found by the same walk a take makes (capacity: the free
// slots, less the holders). Read-only, like the rest of the status.
func (p *Pool) waitersWithConflicts(holders []Holder, rules []Conflict) []Waiter {
	return p.waitersWithConflictsFrom(p.peekSnapshot(), holders, rules)
}

// waitersWithConflictsFrom is waitersWithConflicts over a snapshot the caller already has.
func (p *Pool) waitersWithConflictsFrom(snap Snapshot, holders []Holder, rules []Conflict) []Waiter {
	ws := p.waitersFrom(snap)
	if len(rules) == 0 || len(ws) == 0 {
		return ws
	}
	res := walkQueue(p.Class, snap.Queue, holders, rules, max(p.Size-len(holders), 0), "", nil, nil)
	for i := range ws {
		if b := res.Blocked[ws[i].Ticket]; b != nil {
			ws[i].ConflictWith, ws[i].ConflictRule, ws[i].ConflictReason = b.With, b.Rule.A+" × "+b.Rule.B, b.Rule.Reason
		}
	}
	return ws
}
