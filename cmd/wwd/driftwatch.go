package main

import (
	"github.com/wake/purdex/internal/wwd/drift"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/store"
)

// newDriftWatcher is the watcher of spec §7 "Drift": at the facts cadence it compares what each earned task label was earned on with the
// host's fingerprint now. The tasks come from the manifests of the prepared clones (the same source that gives the reference host its
// labels, read once per record and kept for a moment: see refTaskSource.ttl); revoked is the daemon's RefreshLabels.
func newDriftWatcher(alias string, st *store.Store, refs *refTaskSource, revoked func()) *drift.Watcher {
	return drift.NewWatcher(drift.Config{
		Host:    alias,
		Earned:  st,
		Cache:   st,
		Revoked: revoked,
		Tasks: func() []drift.Task {
			var out []drift.Task
			for _, t := range refs.Tasks() {
				out = append(out, drift.Task{Label: labels.TaskLabel(t.Repo, t.Task), Needs: t.Needs, IsReference: t.Reference == alias})
			}
			return out
		},
	})
}
