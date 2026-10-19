package peers

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/store"
)

// The wiring of the picking rules (namerules.go) into the naming passes: the lead flags of the personas, the name history of
// this host, and the familiar sets of the other hosts become a nameRuleInput under the pool lock.

// NameHistoryReader is the name history the rules read (store.NameHistoryStore): who has been a lead, who worked with whom.
type NameHistoryReader interface {
	Leads(ctx context.Context) ([]store.NameLead, error)
	Pairs(ctx context.Context) ([]store.NamePair, error)
	// Familiar is the codes this host has seen, most recent first, at most limit (the envelope's names.familiar).
	Familiar(ctx context.Context, limit int) ([]string, error)
}

// WithNameHistory wires the name history; nil (the default) means the rules see none, which is the v1 behaviour for a build
// without it.
func (m *Module) WithNameHistory(h NameHistoryReader) *Module {
	m.nameHistory = h
	m.nameWriter, _ = h.(NameHistoryWriter)
	return m
}

// leadCodes is the set of persona codes whose persona is lead:true, as of the last collection the pool followed.
func (m *Module) leadCodes() map[string]bool {
	if p := m.leads.Load(); p != nil {
		return *p
	}
	return nil
}

// ruleInput builds the input of the rules for the free names. A history that cannot be read does not stop the naming: the
// pick goes on as if there were none, and says so. Called with the pool lock held.
func (m *Module) ruleInput(ctx context.Context, free []string) nameRuleInput {
	in := nameRuleInput{Free: free, Lead: m.leadCodes(), Familiar: map[string]bool{}, AnyPairs: map[string]int{}, Leads: map[string]nameStat{}, LeadPairs: map[string]map[string]nameStat{}}
	if m.namePicker != nil {
		in.Pick = m.namePicker.Pick
	}
	if m.nameHistory != nil {
		leads, lerr := m.nameHistory.Leads(ctx)
		pairs, perr := m.nameHistory.Pairs(ctx)
		if err := errors.Join(lerr, perr); err != nil {
			m.logf("peers: name pool: cannot read the name history, picking as if there were none: %v", err)
		} else {
			for _, l := range leads {
				in.Familiar[l.Code] = true
				in.Leads[l.Code] = nameStat{Count: l.Count, LastAt: l.LastAt}
			}
			for _, p := range pairs {
				in.Familiar[p.MemberCode] = true
				in.AnyPairs[p.MemberCode] += p.Count
				if in.LeadPairs[p.LeadCode] == nil {
					in.LeadPairs[p.LeadCode] = map[string]nameStat{}
				}
				in.LeadPairs[p.LeadCode][p.MemberCode] = nameStat{Count: p.Count, LastAt: p.LastAt}
			}
		}
	}
	if m.familiar != nil {
		for c := range m.familiar.RemoteFamiliar() {
			in.Familiar[c] = true
		}
	}
	return in
}

// pickOther is rule 3, for a conversation that is not a team member and not a lead's card: ok is false when nothing is free.
// The free names (the pickable pool minus held) and the lead flags are read under one read lock, so a collection that is being
// applied is seen whole or not at all (bindPersonas holds the write side while it publishes both).
func (m *Module) pickOther(ctx context.Context, picked map[string]bool) (string, bool, error) {
	in, err := m.inputWithHeld(ctx, picked)
	if err != nil {
		return "", false, err
	}
	pick, ok := pickForOther(in)
	return pick, ok, nil
}

// inputWithHeld reads the held names (plus picked, the names this pass already took) and builds the rule input under one read
// lock of the pool generation: the names that live sessions hold are recognised by the pool of that generation, and so are the
// free names, so a collection published between the two cannot make a persona code that a live session already ends in look
// free. Called with the pool lock held.
func (m *Module) inputWithHeld(ctx context.Context, picked map[string]bool) (nameRuleInput, error) {
	m.poolGen.RLock()
	defer m.poolGen.RUnlock()
	held, err := m.heldPoolNames(ctx, picked)
	if err != nil {
		return nameRuleInput{}, err
	}
	return m.inputForLocked(ctx, held), nil
}

// inputForLocked is inputFor for a caller that already holds the generation read lock (it must not take it again: a writer
// waiting between the two would deadlock them).
func (m *Module) inputForLocked(ctx context.Context, held map[string]bool) nameRuleInput {
	return m.ruleInput(ctx, freePoolNames(held))
}
