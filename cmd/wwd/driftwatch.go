package main

import (
	"github.com/wake/purdex/internal/wwd/drift"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/store"
)

// newDriftWatcher is the watcher of spec §7 "Drift": at the facts cadence it compares what each earned task label was earned on with the
// host's fingerprint now. Needs and the reference tasks come from the manifests of the prepared clones (the same source that gives the
// reference host its labels); revoked is the daemon's RefreshLabels.
func newDriftWatcher(alias string, st *store.Store, refs *refTaskSource, revoked func()) *drift.Watcher {
	return drift.NewWatcher(drift.Config{
		Host:   alias,
		Earned: st,
		Cache:  st,
		Needs: func(label string) ([]string, bool) {
			for _, t := range refs.Tasks() {
				if labels.TaskLabel(t.Repo, t.Task) == label {
					return t.Needs, true
				}
			}
			return nil, false
		},
		Revoked: revoked,
		References: func() []drift.Reference {
			var out []drift.Reference
			for _, t := range refs.Tasks() {
				if t.Reference == alias {
					out = append(out, drift.Reference{Label: labels.TaskLabel(t.Repo, t.Task), Needs: t.Needs})
				}
			}
			return out
		},
	})
}
