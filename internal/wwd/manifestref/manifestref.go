// Package manifestref reads a prepared clone's manifest at a git ref instead of in its working tree (#3658). Nobody updates a prepared clone's
// working tree, so a manifest read there goes stale; a clone with a `manifest_ref` (config [manifest_refs], for example origin/main) is
// fetched in the background and its manifest, and the commit the qualification snapshot is taken at, both come from the commit the ref names.
// One read gives both: Get returns the commit and the manifest bytes of the same moment.
package manifestref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/gitenv"
	"github.com/wake/purdex/internal/wwd/clones"
)

// MaxBytes bounds the manifest, as it does for a manifest read from a working tree: a real one is a few KiB.
const MaxBytes = 256 << 10

// ManifestPath is where the manifest lives in a repo.
const ManifestPath = ".wwd/tasks.toml"

// FetchTimeout bounds one `git fetch`.
const FetchTimeout = 60 * time.Second

// Snapshot is what a ref named at one moment: the commit and the manifest of that commit.
type Snapshot struct {
	Commit   string
	Manifest []byte
}

// Source holds the last good Snapshot of each clone that has a manifest_ref.
type Source struct {
	repos *clones.Source
	refs  map[string]string // repo → ref
	git   string

	mu  sync.Mutex
	cur map[string]Snapshot
}

// New builds a Source. refs maps a repo name to its manifest_ref; a repo not in it is not this package's. git is the git program ("" = git).
func New(repos *clones.Source, refs map[string]string, git string) *Source {
	if git == "" {
		git = "git"
	}
	return &Source{repos: repos, refs: refs, git: git, cur: map[string]Snapshot{}}
}

// Configured reports whether repo has a manifest_ref.
func (s *Source) Configured(repo string) bool {
	_, ok := s.refs[repo]
	return ok
}

// Get is the last good Snapshot of repo; false when none was read yet (or the repo has no manifest_ref). The bytes are a copy.
func (s *Source) Get(repo string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.cur[repo]
	if !ok {
		return Snapshot{}, false
	}
	snap.Manifest = bytes.Clone(snap.Manifest)
	return snap, true
}

func (s *Source) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := execstat.CommandContext(ctx, "wwd.manifestref.git", s.git, append([]string{"-C", dir}, args...)...)
	cmd.Env = gitenv.Clean(os.Environ())
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Refresh resolves the ref of repo now and reads the manifest of that commit; on success it becomes the answer of Get, on any failure the
// last answer stays and the error says why. Nothing is fetched.
func (s *Source) Refresh(ctx context.Context, repo string) error {
	ref, ok := s.refs[repo]
	if !ok {
		return fmt.Errorf("%s has no manifest_ref", repo)
	}
	root, ok := s.repos.Path(repo)
	if !ok {
		return fmt.Errorf("%s has no prepared clone on this host", repo)
	}
	out, err := s.run(ctx, root, "rev-parse", "--verify", "-q", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("%s: manifest_ref %q does not resolve to a commit: %w", repo, ref, err)
	}
	commit := strings.TrimSpace(string(out))
	// only a plain file: a symlink in the committed tree could name anything on this host
	out, err = s.run(ctx, root, "ls-tree", "-l", commit, "--", ManifestPath)
	if err != nil {
		return err
	}
	meta, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	fields := strings.Fields(meta) // mode type oid size
	if len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return fmt.Errorf("%s: %s at %s is missing or not a regular file", repo, ManifestPath, commit)
	}
	// the size comes from the listing, so a huge blob is refused before any of it is read into memory
	if size, err := strconv.Atoi(fields[3]); err != nil || size > MaxBytes {
		return fmt.Errorf("%s: %s at %s is larger than %d bytes (or its size is unreadable: %q)", repo, ManifestPath, commit, MaxBytes, fields[3])
	}
	b, err := s.run(ctx, root, "cat-file", "blob", fields[2])
	if err != nil {
		return err
	}
	if len(b) > MaxBytes {
		return fmt.Errorf("%s: %s at %s is larger than %d bytes", repo, ManifestPath, commit, MaxBytes)
	}
	s.mu.Lock()
	s.cur[repo] = Snapshot{Commit: commit, Manifest: b}
	s.mu.Unlock()
	return nil
}

// Fetch brings up to date the remote the ref lives under (origin for origin/main, upstream for upstream/main or refs/remotes/upstream/main),
// at most FetchTimeout. A ref under no remote (a local branch, a tag, a commit) has nothing to fetch. A failure changes nothing.
func (s *Source) Fetch(ctx context.Context, repo string) error {
	ref, ok := s.refs[repo]
	if !ok {
		return fmt.Errorf("%s has no manifest_ref", repo)
	}
	root, ok := s.repos.Path(repo)
	if !ok {
		return fmt.Errorf("%s has no prepared clone on this host", repo)
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	first, _, under := strings.Cut(strings.TrimPrefix(ref, "refs/remotes/"), "/")
	if !under {
		return nil
	}
	out, err := s.run(ctx, root, "remote")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(out)), first) {
		return nil
	}
	_, err = s.run(ctx, root, "fetch", "--quiet", "--no-tags", first)
	return err
}

// Run keeps every configured repo current until ctx ends: it reads what the refs name at once (so the labels do not wait for the network),
// then every interval fetches and reads again. A failure is logged and the last answer stays.
func (s *Source) Run(ctx context.Context, interval time.Duration) {
	s.each(ctx, false)
	t := time.NewTicker(interval)
	defer t.Stop()
	s.each(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.each(ctx, true)
		}
	}
}

func (s *Source) each(ctx context.Context, fetch bool) {
	for repo := range s.refs {
		if ctx.Err() != nil {
			return
		}
		if fetch {
			if err := s.Fetch(ctx, repo); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("wwd: manifest_ref %s: fetch failed (the last manifest stays): %v", repo, err)
			}
		}
		if err := s.Refresh(ctx, repo); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("wwd: manifest_ref %s: %v (the last manifest stays)", repo, err)
		}
	}
}
