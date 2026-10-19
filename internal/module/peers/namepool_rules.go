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
	in := nameRuleInput{Free: free, Lead: m.leadCodes(), Familiar: map[string]bool{}, AnyPairs: map[string]int{}, Leads: map[string]nameStat{}}
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
func (m *Module) pickOther(ctx context.Context, held map[string]bool) (string, bool) {
	return pickForOther(m.inputFor(ctx, held))
}

// inputFor is the rule input for the pickable pool minus held, read under the read lock of the pool generation.
func (m *Module) inputFor(ctx context.Context, held map[string]bool) nameRuleInput {
	m.poolGen.RLock()
	defer m.poolGen.RUnlock()
	return m.ruleInput(ctx, freePoolNames(held))
}
