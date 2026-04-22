package agent

import "sort"

// CoverageRow describes a single provider's Status declaration state.
type CoverageRow struct {
	AgentType string
	Declares  bool
	Declared  []Status
}

// Coverage produces a matrix of each registered provider's declared Status
// support, sorted by AgentType. Providers not implementing StatusSupporter
// are reported with Declares=false and Declared=nil. Providers implementing
// it have Declared normalized to a non-nil defensive copy (even for empty
// declarations). Returns nil for an empty registry.
func Coverage(r *Registry) []CoverageRow {
	providers := r.All()
	if len(providers) == 0 {
		return nil
	}
	rows := make([]CoverageRow, 0, len(providers))
	for _, p := range providers {
		row := CoverageRow{AgentType: p.Type()}
		if ss, ok := p.(StatusSupporter); ok {
			row.Declares = true
			src := ss.SupportedStatuses()
			// Normalize nil to empty slice + defensive copy so callers cannot
			// mutate the provider's internal slice.
			dst := make([]Status, len(src))
			copy(dst, src)
			row.Declared = dst
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].AgentType < rows[j].AgentType
	})
	return rows
}
