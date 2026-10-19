package peers

import "sort"

// The picking rules of name pool v2 (spec §6.2) as pure functions: the free names, the history and the persona flags come in,
// a name or an ordered list comes out. They hold no lock and read no store; the caller builds the input under the pool lock.
//
//	rule 1  a lead's name card   leadCandidates
//	rule 2  a member of lead L   pickForMember
//	rule 3  any other session    pickForOther

// nameStat is how many times a pair or a lead happened, and when last (unix ms).
type nameStat struct {
	Count  int
	LastAt int64
}

// nameRuleInput is everything the rules read.
type nameRuleInput struct {
	// Free is the pickable pool minus the held names, in pool order.
	Free []string
	// Lead is the set of codes whose persona is lead:true.
	Lead map[string]bool
	// Familiar is every code that has been a lead or a lead's member, on this host or (NP-3e) on another.
	Familiar map[string]bool
	// Pairs is, for rule 2, the member code → how often it worked with this member's lead.
	Pairs map[string]nameStat
	// AnyPairs is the member code → how often it worked with any lead (rules 2 and 3).
	AnyPairs map[string]int
	// Leads is, for rule 1, the code → how often it was a lead.
	Leads map[string]nameStat
	// Pick chooses one of the candidates it is given (the random step); nil, or an answer that is not a candidate, takes the
	// first.
	Pick func(candidates []string) string
}

func (in nameRuleInput) choose(candidates []string) string {
	if in.Pick != nil {
		p := in.Pick(candidates)
		for _, c := range candidates {
			if c == p {
				return p
			}
		}
	}
	return candidates[0]
}

// newcomers are the free names that are neither familiar nor lead:true.
func (in nameRuleInput) newcomers() []string {
	var out []string
	for _, n := range in.Free {
		if !in.Familiar[n] && !in.Lead[n] {
			out = append(out, n)
		}
	}
	return out
}

// leastPaired returns the names of cands with the fewest pairs with any lead (all of them on a tie), in pool order.
func (in nameRuleInput) leastPaired(cands []string) []string {
	var out []string
	best := -1
	for _, n := range cands {
		c := in.AnyPairs[n]
		switch {
		case best < 0 || c < best:
			best, out = c, []string{n}
		case c == best:
			out = append(out, n)
		}
	}
	return out
}

// pickAfterPairs is the part rules 2 and 3 share: a newcomer at random, else the name least paired with any lead, else the
// lead:true names last. ok is false when nothing is free.
func (in nameRuleInput) pickAfterPairs() (string, bool) {
	if c := in.newcomers(); len(c) > 0 {
		return in.choose(c), true
	}
	var plain, lead []string
	for _, n := range in.Free {
		if in.Lead[n] {
			lead = append(lead, n)
		} else {
			plain = append(plain, n)
		}
	}
	if c := in.leastPaired(plain); len(c) > 0 {
		return in.choose(c), true
	}
	if c := in.leastPaired(lead); len(c) > 0 {
		return in.choose(c), true
	}
	return "", false
}

// pickForMember is rule 2: a free name paired with the lead (most pairs first, then most recent, then pool order), then rule
// 3's order.
func pickForMember(in nameRuleInput) (string, bool) {
	var paired []string
	for _, n := range in.Free {
		if in.Pairs[n].Count > 0 {
			paired = append(paired, n)
		}
	}
	if len(paired) > 0 {
		sort.SliceStable(paired, func(i, j int) bool {
			a, b := in.Pairs[paired[i]], in.Pairs[paired[j]]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			return a.LastAt > b.LastAt
		})
		return paired[0], true
	}
	return in.pickAfterPairs()
}

// pickForOther is rule 3.
func pickForOther(in nameRuleInput) (string, bool) { return in.pickAfterPairs() }

// leadCandidates is rule 1: the names a lead's card offers, best first. keep is the session's current name (preselected, so
// it comes first even though the session itself holds it); then the free former leads, most recent first; then the free
// lead:true personas; then the other free names. A name appears once.
func leadCandidates(in nameRuleInput, keep string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	add(keep)
	var former []string
	for _, n := range in.Free {
		if in.Leads[n].Count > 0 {
			former = append(former, n)
		}
	}
	sort.SliceStable(former, func(i, j int) bool { return in.Leads[former[i]].LastAt > in.Leads[former[j]].LastAt })
	for _, n := range former {
		add(n)
	}
	for _, n := range in.Free {
		if in.Lead[n] {
			add(n)
		}
	}
	for _, n := range in.Free {
		add(n)
	}
	return out
}
