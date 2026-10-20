package main

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/manifestref"
)

// maxManifestBytes bounds the manifest read on the facts path (every 5 s): a real manifest is a few KiB.
const maxManifestBytes = 256 << 10

// refTaskSource lists the tasks of this host's prepared clones that name a reference host (`qualify.reference`), for the daemon to
// hold the task label as the reference (spec §7). The manifest is read (bounded, and only if it is a regular file inside the clone)
// at every call and parsed again only when its content changed, so a call costs one small read per repo and a stale cache cannot keep
// a capability that was taken out of the file. A manifest that does not parse contributes nothing (the same as no manifest).
type refTaskSource struct {
	repos *clones.Source
	// mrefs, when set, answers for the repos that have a manifest_ref: their manifest is the one at that ref (#3658), never the working tree.
	mrefs *manifestref.Source
	// ttl is how long the last answer is kept: the daemon's labels and the drift watcher both ask at every facts record, and the answer
	// comes from files, so one read serves both (and a slow disk is met once, not once per asker). 0: every call reads. A manifest change
	// shows up within ttl.
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	seen  map[string]refCache // by manifest path; the tasks carry no repo name (the same clone may be configured under two names)
	at    time.Time
	last  []labels.RefTask
	asked bool
}

type refCache struct {
	sum   [sha256.Size]byte
	tasks []labels.RefTask // Repo is empty
}

func newRefTaskSource(repos *clones.Source) *refTaskSource {
	return &refTaskSource{repos: repos, seen: map[string]refCache{}}
}

func (s *refTaskSource) Tasks() []labels.RefTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	if s.asked && s.ttl > 0 && now().Sub(s.at) < s.ttl {
		return slices.Clone(s.last) // a copy: the callers are free to change what they get
	}
	out := s.read()
	s.last, s.at, s.asked = out, now(), true
	return slices.Clone(out)
}

// read looks at the clones now.
func (s *refTaskSource) read() []labels.RefTask {
	out := []labels.RefTask{}
	live := map[string]bool{}
	for _, name := range s.repos.Names() {
		root, ok := s.repos.Path(name)
		if !ok {
			continue
		}
		var file string
		var b []byte
		if s.mrefs != nil && s.mrefs.Configured(name) {
			snap, got := s.mrefs.Get(name)
			if !got {
				continue // not read at the ref yet: no manifest, not the stale working tree
			}
			file, b = "ref:"+name, snap.Manifest
		} else if file, b, ok = readManifest(root); !ok {
			continue
		}
		live[file] = true
		sum := sha256.Sum256(b)
		c, hit := s.seen[file]
		if !hit || c.sum != sum {
			c = refCache{sum: sum, tasks: referenceTasks(b)}
			s.seen[file] = c
		}
		for _, t := range c.tasks {
			t.Repo = name
			t.Needs = environmentNeeds(name, t.Task, t.Needs)
			out = append(out, t)
		}
	}
	for f := range s.seen { // a clone that went away takes its cache with it
		if !live[f] {
			delete(s.seen, f)
		}
	}
	return out
}

// readManifest reads <root>/.wwd/tasks.toml when it is a regular file that really lies inside the clone (no symlink out of it) and
// not larger than maxManifestBytes.
func readManifest(root string) (file string, b []byte, ok bool) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, false
	}
	file, err = filepath.EvalSymlinks(filepath.Join(root, ".wwd", "tasks.toml"))
	if err != nil || !strings.HasPrefix(file, realRoot+string(filepath.Separator)) {
		return "", nil, false
	}
	f, err := os.Open(file)
	if err != nil {
		return "", nil, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", nil, false
	}
	b, err = io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil || len(b) > maxManifestBytes {
		return "", nil, false
	}
	return file, b, true
}

// environmentNeeds is the labels of a task that are conditions of the host: a task's manifest labels also name its own repo
// (repo:<repo>, which labels.ReferenceLabels checks on its own) and, for a task that may run on other hosts, its own earned label
// (task:<repo>/<task>, what this host is about to hold), and neither is a need. What is left goes on as it is, so a label that is
// not an environment label (a foreign task:, host:, anything else) still keeps the task from being held.
func environmentNeeds(repo, task string, needs []string) []string {
	out := make([]string, 0, len(needs))
	for _, n := range needs {
		if n == "repo:"+repo || n == labels.TaskLabel(repo, task) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func referenceTasks(src []byte) []labels.RefTask {
	m, err := manifest.Parse(src)
	if err != nil {
		return nil
	}
	var out []labels.RefTask
	for name, t := range m.Tasks {
		if t.Qualify == nil || t.Qualify.Reference == "" {
			continue
		}
		out = append(out, labels.RefTask{Task: name, Reference: t.Qualify.Reference, Needs: t.Labels})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out
}
