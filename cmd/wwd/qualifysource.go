package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/manifestref"
	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// qualifySource is what the qualification coordinator reads of this host (qualify.Source): the host as it is now, the tasks of the prepared
// clones that have a `qualify`, and the snapshot a pair runs against. The clones are the host's own (config [repos]); nothing here reaches
// another host.
type qualifySource struct {
	repos *clones.Source
	// mrefs, when set, answers for the repos that have a manifest_ref: the manifest and the snapshot are the commit that ref names (#3658).
	mrefs *manifestref.Source
	host  func() qualify.Host
	git   string // the git program ("" = git)
}

func (s *qualifySource) Host() qualify.Host { return s.host() }

// Tasks lists, for each prepared clone whose manifest parses, the tasks that name a reference host in their `qualify`. The manifest is the
// one in the clone's working tree, read the same bounded way the reference labels are; DefHash is the manifest's own definition hash of the
// task, so the coordinator can tell when a pair is done that the task is still the one that was run. A clone with no manifest contributes nothing; one that does
// not parse contributes nothing and is named in the error.
func (s *qualifySource) Tasks() ([]qualify.Task, error) {
	var out []qualify.Task
	var errs []error
	for _, repo := range s.repos.Names() {
		root, ok := s.repos.Path(repo)
		if !ok {
			continue
		}
		var path, commit string
		var b []byte
		if s.mrefs != nil && s.mrefs.Configured(repo) {
			snap, got := s.mrefs.Get(repo)
			if !got {
				errs = append(errs, fmt.Errorf("%s: its manifest_ref has not been read yet", repo))
				continue
			}
			path, b, ok, commit = "the manifest_ref of "+repo+" ("+snap.Commit+")", snap.Manifest, true, snap.Commit
		} else {
			path, b, ok = readManifest(root)
		}
		if !ok {
			if file := filepath.Join(root, ".wwd", "tasks.toml"); fileExists(file) { // no manifest at all is not a fault; one that is there and cannot be read is
				errs = append(errs, fmt.Errorf("%s: %s cannot be read (not a regular file inside the clone, too large, or unreadable)", repo, file))
			}
			continue
		}
		m, err := manifest.Parse(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s does not parse: %w", repo, path, err))
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
				Needs: environmentNeeds(repo, name, t.Labels), DefHash: t.DefinitionHash(), Commit: commit,
			})
		}
	}
	return out, errors.Join(errs...)
}

// Prepare takes the snapshot of the task's clone and returns the task's submission as its manifest declares it, with the qualify args as its
// args: one call, so both halves of the pair share one snapshot (submit.Prepare validates the args against the task's kind, as a person's
// `pdx test` would be).
func (s *qualifySource) Prepare(ctx context.Context, t qualify.Task) (wire.JobSubmission, error) {
	root, ok := s.repos.Path(t.Repo)
	if !ok {
		return wire.JobSubmission{}, fmt.Errorf("%s has no prepared clone on this host", t.Repo)
	}
	// "": the working tree, as before. A repo with a manifest_ref is snapshotted at the commit Tasks listed the task at (t.Commit), not at what the
	// ref names now: the ref may have moved on in between, and the definition hash below does not cover everything a commit can change.
	if s.mrefs != nil && s.mrefs.Configured(t.Repo) && t.Commit == "" {
		return wire.JobSubmission{}, fmt.Errorf("%s/%s was not listed at a manifest_ref commit: ask again", t.Repo, t.Name)
	}
	p, err := submit.Prepare(ctx, submit.Request{
		Dir: root, Task: t.Name, Args: t.Args, Git: s.git, Commit: t.Commit,
		Submitter: wire.Submitter{Session: qualify.SessionName},
	})
	if err != nil {
		return wire.JobSubmission{}, err
	}
	if p.Body.DefHash != t.DefHash { // the manifest moved between listing the task and the snapshot: the pair would run a definition nobody asked for
		return wire.JobSubmission{}, fmt.Errorf("%s/%s changed its definition while the snapshot was taken: ask again", t.Repo, t.Name)
	}
	return p.Body, nil
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }
