package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"
)

// SearchRequest is the daemon-internal shape (after handler resolves
// capabilities). The HTTP handler in search_handler.go is responsible for
// mapping client capability roots to absolute paths; the engine itself never
// validates the trust of the supplied paths — that is a layer-2 concern.
type SearchRequest struct {
	Mode    string
	Query   SearchQuery
	Roots   []SearchRoot
	Limits  SearchLimits
	Filters SearchFilters
}

type SearchQuery struct {
	Basename string
}

// SearchRoot is an already-resolved absolute path on the local filesystem.
// `Kind` is retained for telemetry / logging so we can tell whether the path
// originally came from `session-cwd`, `workspace-projectPath`, or a test-only
// raw path.
type SearchRoot struct {
	Kind     string
	Absolute string
}

type SearchLimits struct {
	MaxResults int
	MaxDepth   int
}

// SearchFilters carry caller-supplied filters. Mandatory excludes are union'd
// on top of these and CANNOT be disabled by sending empty arrays.
//
// RespectGitignore is a *bool so that an omitted JSON field maps to nil rather
// than Go's `false` zero value — `nil` is treated as "default true". This
// closes the fail-open hole called out in attacker review #10.
type SearchFilters struct {
	ExcludeDirs          []string
	ExcludeBasenameGlobs []string
	RespectGitignore     *bool
}

type SearchMatch struct {
	Path      string    `json:"path"`
	ModTime   time.Time `json:"modTime"`
	SizeBytes int64     `json:"sizeBytes"`
	Root      string    `json:"root"`
}

type SearchResponse struct {
	Matches  []SearchMatch `json:"matches"`
	Partial  bool          `json:"partial"`
	Warnings []string      `json:"warnings,omitempty"`
}

const (
	defaultSearchMaxResults = 50
	defaultSearchMaxDepth   = 8
	hardCapMaxResults       = 200

	// rawAbsoluteTestOnly is a SearchRoot.Kind used by unit tests that want
	// to hand the engine a raw absolute path. Production callers (the HTTP
	// handler) never produce roots with this kind — they always go through
	// capability resolution.
	rawAbsoluteTestOnly = "raw-absolute-test-only"
)

// mandatoryExcludeDirs are dir basenames the engine ALWAYS prunes, regardless
// of caller input. Union'd with SearchFilters.ExcludeDirs. (attacker review
// #10 — sending [] cannot defeat them.)
var mandatoryExcludeDirs = []string{
	"node_modules",
	".git",
	".cache",
	"dist",
	".pnpm-store",
	".next",
	".turbo",
}

// mandatoryExcludeBasenameGlobs are file basename globs the engine ALWAYS
// rejects. Union'd with SearchFilters.ExcludeBasenameGlobs.
var mandatoryExcludeBasenameGlobs = []string{
	"*.lock",
	"*.log",
}

// ErrGitignoreParse signals that a .gitignore in one of the search roots could
// not be parsed; the HTTP handler maps this to 4xx so we never silently
// fail-open. (attacker review #11.)
var ErrGitignoreParse = errors.New("gitignore parse failure")

// Search executes a synchronous filesystem search across the resolved roots.
// `ctx` is honoured for cancellation/timeout — partial=true is set when the
// walk is cut short by either the deadline, a result-count cap, or context
// cancellation.
//
// The engine is a pure function: it does not consult any state outside `req`,
// `os`, and `ctx`, and it does not validate trust on `req.Roots` (that is the
// HTTP handler's job — see search_handler.go).
func Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if req.Mode == "" {
		req.Mode = "basename"
	}
	if req.Mode != "basename" {
		return SearchResponse{}, errors.New("unsupported search mode: " + req.Mode)
	}
	if req.Query.Basename == "" {
		return SearchResponse{}, errors.New("query.basename required")
	}
	if req.Limits.MaxResults <= 0 {
		req.Limits.MaxResults = defaultSearchMaxResults
	}
	if req.Limits.MaxResults > hardCapMaxResults {
		req.Limits.MaxResults = hardCapMaxResults
	}
	if req.Limits.MaxDepth <= 0 {
		req.Limits.MaxDepth = defaultSearchMaxDepth
	}

	excludeDirs := make(map[string]struct{}, len(mandatoryExcludeDirs)+len(req.Filters.ExcludeDirs))
	for _, d := range mandatoryExcludeDirs {
		excludeDirs[d] = struct{}{}
	}
	for _, d := range req.Filters.ExcludeDirs {
		if d != "" {
			excludeDirs[d] = struct{}{}
		}
	}
	excludeGlobs := make([]string, 0, len(mandatoryExcludeBasenameGlobs)+len(req.Filters.ExcludeBasenameGlobs))
	excludeGlobs = append(excludeGlobs, mandatoryExcludeBasenameGlobs...)
	excludeGlobs = append(excludeGlobs, req.Filters.ExcludeBasenameGlobs...)

	respectGI := true
	if req.Filters.RespectGitignore != nil {
		respectGI = *req.Filters.RespectGitignore
	}

	matches := make([]SearchMatch, 0, req.Limits.MaxResults)
	partial := false

	for _, root := range req.Roots {
		var gi *gitignore.GitIgnore
		if respectGI {
			gp := filepath.Join(root.Absolute, ".gitignore")
			if data, err := os.ReadFile(gp); err == nil {
				if perr := preflightGitignore(string(data)); perr != nil {
					return SearchResponse{}, perr
				}
				gi = gitignore.CompileIgnoreLines(strings.Split(string(data), "\n")...)
			}
			// Missing .gitignore is fine — nothing to filter.
		}

		walkErr := filepath.WalkDir(root.Absolute, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Skip unreadable entries rather than aborting the whole walk.
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			rel, relErr := filepath.Rel(root.Absolute, path)
			if relErr != nil {
				return nil
			}
			depth := 0
			if rel != "." {
				depth = strings.Count(rel, string(os.PathSeparator))
			}

			if d.IsDir() {
				// Mandatory + caller dir excludes by basename.
				if path != root.Absolute {
					if _, skip := excludeDirs[d.Name()]; skip {
						return filepath.SkipDir
					}
				}
				// Gitignore prune — note the trailing slash so dir-only patterns match.
				if gi != nil && rel != "." && gi.MatchesPath(rel+"/") {
					return filepath.SkipDir
				}
				if depth > req.Limits.MaxDepth {
					return filepath.SkipDir
				}
				return nil
			}

			// File: depth check first.
			if depth > req.Limits.MaxDepth {
				return nil
			}

			// Mandatory + caller basename glob excludes.
			for _, glob := range excludeGlobs {
				if ok, _ := filepath.Match(glob, d.Name()); ok {
					return nil
				}
			}
			if gi != nil && gi.MatchesPath(rel) {
				return nil
			}
			if d.Name() != req.Query.Basename {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}
			matches = append(matches, SearchMatch{
				Path:      path,
				ModTime:   info.ModTime(),
				SizeBytes: info.Size(),
				Root:      root.Absolute,
			})
			if len(matches) >= req.Limits.MaxResults {
				partial = true
				return filepath.SkipAll
			}
			return nil
		})
		if errors.Is(walkErr, context.DeadlineExceeded) || errors.Is(walkErr, context.Canceled) {
			partial = true
			break
		}
		if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
			// Any other walk failure surfaces; the engine doesn't know which
			// classification applies.
			return SearchResponse{Matches: matches, Partial: partial}, walkErr
		}
		if partial {
			break
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].ModTime.After(matches[j].ModTime) })
	return SearchResponse{Matches: matches, Partial: partial}, nil
}

// preflightGitignore performs a minimal sanity check on .gitignore contents
// because go-gitignore silently swallows lines whose generated regex fails to
// compile. This pre-pass walks each non-empty / non-comment pattern and
// rejects unbalanced character classes — the failure mode the daemon must NOT
// silently fall back from. See attacker review #11.
//
// The check is intentionally conservative: it is permitted to false-positive
// on the simplest unbalanced-bracket case and skip everything else. Any line
// the gitignore library would also have silently dropped is something we want
// to surface as ErrGitignoreParse.
func preflightGitignore(content string) error {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading negate so we examine the actual pattern.
		if strings.HasPrefix(line, "!") {
			line = line[1:]
		}
		if !bracketsBalanced(line) {
			return ErrGitignoreParse
		}
	}
	return nil
}

// bracketsBalanced returns false if the line has an unbalanced `[`/`]`
// character class. It honours backslash escapes so that `foo\[bar` still
// counts as balanced.
func bracketsBalanced(s string) bool {
	open := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++ // skip the escaped char
			}
		case '[':
			open++
		case ']':
			if open > 0 {
				open--
			}
			// stray closing brackets are tolerated by go-gitignore, ignore.
		}
	}
	return open == 0
}
