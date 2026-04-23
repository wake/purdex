package agent

import "sort"

// DeriveSupportedStatuses computes the lexicographically sorted union of
// HookEventSpec.EmitsStatus across a slice of specs. It is the helper that
// backs each provider's SupportedStatuses() implementation once the event
// catalog (HookInstaller.Events()) becomes the single source of truth for
// which Status values DeriveStatus may emit.
//
// Invariants:
//   - Empty-status specs (detail-only events such as SubagentStart/Stop)
//     contribute nothing to the union.
//   - A Status appearing in multiple specs, or repeated within a single spec,
//     is collapsed to one occurrence.
//   - Returns a non-nil empty slice for empty input. Callers can treat the
//     result as a freshly-allocated slice safe to hand to external consumers.
//   - Ordering is lexicographic on the underlying Status string so callers
//     that compare results (drift test, Inspector UI) get deterministic
//     output independent of spec ordering.
func DeriveSupportedStatuses(specs []HookEventSpec) []Status {
	set := make(map[Status]struct{})
	for _, spec := range specs {
		for _, s := range spec.EmitsStatus {
			if s == "" {
				continue
			}
			set[s] = struct{}{}
		}
	}
	out := make([]Status, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}
