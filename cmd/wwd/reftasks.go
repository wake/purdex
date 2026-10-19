package main

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/manifest"
)

// refTaskSource lists the tasks of this host's prepared clones that name a reference host (`qualify.reference`), for the daemon to
// hold the task label as the reference (spec §7). The manifest of a clone is read again only when its file changed, so a call per facts
// record costs a stat per repo. A manifest that does not parse contributes nothing (the same as no manifest).
type refTaskSource struct {
	repos *clones.Source
	mu    sync.Mutex
	seen  map[string]refCache
}

type refCache struct {
	mod   time.Time
	size  int64
	tasks []labels.RefTask
}

func newRefTaskSource(repos *clones.Source) *refTaskSource {
	return &refTaskSource{repos: repos, seen: map[string]refCache{}}
}

func (s *refTaskSource) Tasks() []labels.RefTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []labels.RefTask{}
	live := map[string]bool{}
	for _, name := range s.repos.Names() {
		root, ok := s.repos.Path(name)
		if !ok {
			continue
		}
		file := filepath.Join(root, ".wwd", "tasks.toml")
		fi, err := os.Stat(file)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		live[file] = true
		c, hit := s.seen[file]
		if !hit || !c.mod.Equal(fi.ModTime()) || c.size != fi.Size() {
			c = refCache{mod: fi.ModTime(), size: fi.Size(), tasks: referenceTasks(name, file)}
			s.seen[file] = c
		}
		out = append(out, c.tasks...)
	}
	for f := range s.seen { // a clone that went away takes its cache with it
		if !live[f] {
			delete(s.seen, f)
		}
	}
	return out
}

func referenceTasks(repo, file string) []labels.RefTask {
	m, err := manifest.Load(file)
	if err != nil {
		return nil
	}
	var out []labels.RefTask
	for name, t := range m.Tasks {
		if t.Qualify == nil || t.Qualify.Reference == "" {
			continue
		}
		out = append(out, labels.RefTask{Repo: repo, Task: name, Reference: t.Qualify.Reference, Needs: t.Labels})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out
}
