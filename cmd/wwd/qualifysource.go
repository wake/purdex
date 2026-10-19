package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// qualifySource is what the qualification coordinator reads of this host (qualify.Source): the host as it is now, the tasks of the prepared
// clones that have a `qualify`, and the snapshot a pair runs against. The clones are the host's own (config [repos]); nothing here reaches
// another host.
type qualifySource struct {
	repos *clones.Source
	host  func() qualify.Host
	git   string // the git program ("" = git)
}

func (s *qualifySource) Host() qualify.Host { return s.host() }

// Tasks lists, for each prepared clone whose manifest parses, the tasks that name a reference host in their `qualify`. The manifest is the
// one in the clone's working tree, read the same bounded way the reference labels are; DefHash is the manifest's own definition hash of the
// task, so the coordinator can tell when a pair is done that the task is still the one that was run. A clone with no manifest, or one that
// does not parse, contributes nothing.
func (s *qualifySource) Tasks() []qualify.Task {
	var out []qualify.Task
	for _, repo := range s.repos.Names() {
		root, ok := s.repos.Path(repo)
		if !ok {
			continue
		}
		_, b, ok := readManifest(root)
		if !ok {
			continue
		}
		m, err := manifest.Parse(b)
		if err != nil {
			continue
		}
		var names []string
		for name, t := range m.Tasks {
			if t.Qualify != nil && t.Qualify.Reference != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			t := m.Tasks[name]
			args := t.Qualify.Args
			if args == nil {
				args = []string{}
			}
			out = append(out, qualify.Task{
				Repo: repo, Name: name, Reference: t.Qualify.Reference, Args: args,
				Needs: environmentNeeds(repo, name, t.Labels), DefHash: t.DefinitionHash(),
			})
		}
	}
	return out
}

// Prepare takes the snapshot of the task's clone and returns the task's submission as its manifest declares it, with the qualify args as its
// args: one call, so both halves of the pair share one snapshot (submit.Prepare validates the args against the task's kind, as a person's
// `pdx test` would be).
func (s *qualifySource) Prepare(ctx context.Context, t qualify.Task) (wire.JobSubmission, error) {
	root, ok := s.repos.Path(t.Repo)
	if !ok {
		return wire.JobSubmission{}, fmt.Errorf("%s has no prepared clone on this host", t.Repo)
	}
	p, err := submit.Prepare(ctx, submit.Request{
		Dir: root, Task: t.Name, Args: t.Args, Class: wire.ClassDev, Git: s.git,
		Submitter: wire.Submitter{Session: qualify.SessionName},
	})
	if err != nil {
		return wire.JobSubmission{}, err
	}
	return p.Body, nil
}
