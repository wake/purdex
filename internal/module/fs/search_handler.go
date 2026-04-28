package fs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// httpSearchRequest is the wire-format body accepted by POST /api/fs/search.
// The handler converts this into the daemon-internal SearchRequest after
// resolving capability roots.
type httpSearchRequest struct {
	Mode  string `json:"mode"`
	Query struct {
		Basename string `json:"basename"`
	} `json:"query"`
	Roots  []httpSearchRoot   `json:"roots"`
	Limits *httpSearchLimits  `json:"limits,omitempty"`
	Filters *httpSearchFilters `json:"filters,omitempty"`
}

type httpSearchRoot struct {
	Kind        string `json:"kind"`
	SessionCode string `json:"sessionCode,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

type httpSearchLimits struct {
	MaxResults int `json:"maxResults,omitempty"`
	MaxDepth   int `json:"maxDepth,omitempty"`
	TimeoutMs  int `json:"timeoutMs,omitempty"`
}

type httpSearchFilters struct {
	ExcludeDirs          []string `json:"excludeDirs,omitempty"`
	ExcludeBasenameGlobs []string `json:"excludeBasenameGlobs,omitempty"`
	RespectGitignore     *bool    `json:"respectGitignore,omitempty"`
}

// handleSearch is the canonical method handler for POST /api/fs/search.
//
// It accepts only capability-based roots. Absolute path roots, unknown kinds,
// missing sessions, and resolved system paths (`/`, `/etc`, `/sys`, `/Users`,
// `$HOME` direct) are all 4xx. The `workspace-projectPath` kind is currently
// 501 — daemon lacks a workspace registry; this is layer-3 follow-up.
func (m *FsModule) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limitBody(w, r, maxBodySize)

	var body httpSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Mode == "" {
		body.Mode = "basename"
	}
	if body.Mode != "basename" {
		jsonError(w, "unsupported mode: "+body.Mode, http.StatusBadRequest)
		return
	}
	if body.Query.Basename == "" {
		jsonError(w, "query.basename required", http.StatusBadRequest)
		return
	}
	if len(body.Roots) == 0 {
		jsonError(w, "roots required", http.StatusBadRequest)
		return
	}

	roots, status, err := m.resolveCapabilityRoots(body.Roots)
	if err != nil {
		jsonError(w, err.Error(), status)
		return
	}

	req := SearchRequest{
		Mode:  body.Mode,
		Query: SearchQuery{Basename: body.Query.Basename},
		Roots: roots,
	}
	if body.Limits != nil {
		req.Limits = SearchLimits{MaxResults: body.Limits.MaxResults, MaxDepth: body.Limits.MaxDepth}
	}
	if body.Filters != nil {
		req.Filters = SearchFilters{
			ExcludeDirs:          body.Filters.ExcludeDirs,
			ExcludeBasenameGlobs: body.Filters.ExcludeBasenameGlobs,
			RespectGitignore:     body.Filters.RespectGitignore,
		}
	}

	ctx := r.Context()
	if body.Limits != nil && body.Limits.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(body.Limits.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	resp, err := Search(ctx, req)
	if errors.Is(err, ErrGitignoreParse) {
		jsonError(w, "gitignore parse failure", http.StatusBadRequest)
		return
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, resp)
}

// resolveCapabilityRoots maps client capability roots to absolute paths and
// validates them against the system-path blocklist.
//
// Returns (roots, statusCode, error). statusCode is meaningful only on error.
//
// Rejects:
//   - Unknown kinds (including `absolute`) → 400
//   - Missing/empty session cwd → 400
//   - System paths (`/`, `/etc`, `/sys`, `/Users` direct, `$HOME` direct) → 400
//
// Defers:
//   - `workspace-projectPath` → 501 (layer-3 follow-up; daemon has no
//     workspace registry yet).
func (m *FsModule) resolveCapabilityRoots(roots []httpSearchRoot) ([]SearchRoot, int, error) {
	out := make([]SearchRoot, 0, len(roots))
	for _, r := range roots {
		switch r.Kind {
		case "session-cwd":
			if r.SessionCode == "" {
				return nil, http.StatusBadRequest, errors.New("sessionCode required for session-cwd root")
			}
			info, err := m.sessions.GetSession(r.SessionCode)
			if err != nil {
				return nil, http.StatusBadRequest, errors.New("session lookup failed: " + err.Error())
			}
			if info == nil || info.Cwd == "" {
				return nil, http.StatusBadRequest, errors.New("session cwd not found for " + r.SessionCode)
			}
			// R2-H1: Resolve symlinks before walking so the allowlist runs
			// against the *real* tree. We validate BOTH forms — cleaned (catches
			// lexical attempts like `/etc`, even though macOS resolves it to
			// `/private/etc`) and resolved (catches sneaky symlinks that point
			// from a benign-looking `/tmp/...` into `$HOME` or other system
			// roots). The walker walks the resolved path, so it's the ground
			// truth for trust.
			cleaned := filepath.Clean(info.Cwd)
			if err := validateNotSystemPath(cleaned); err != nil {
				return nil, http.StatusBadRequest, err
			}
			abs, err := filepath.EvalSymlinks(cleaned)
			if err != nil {
				return nil, http.StatusBadRequest, errors.New("session cwd unresolvable: " + err.Error())
			}
			if err := validateNotSystemPath(abs); err != nil {
				return nil, http.StatusBadRequest, err
			}
			out = append(out, SearchRoot{Kind: r.Kind, Absolute: abs})

		case "workspace-projectPath":
			// v6 降級：daemon 暫無 workspace registry — layer-3 follow-up.
			return nil, http.StatusNotImplemented, errors.New("workspace-projectPath roots not yet implemented")

		default:
			return nil, http.StatusBadRequest, errors.New("unsupported root kind: " + r.Kind)
		}
	}
	return out, http.StatusOK, nil
}

// validateNotSystemPath returns an error when `p` is a sensitive system path
// the daemon refuses to walk. Direct children of `/Users` (e.g. `/Users/wake`)
// are rejected too — they're whole-home roots that would expose every project.
// Anything deeper (e.g. `/Users/wake/Workspace`) is allowed.
func validateNotSystemPath(p string) error {
	switch p {
	case "/", "/etc", "/sys", "/Users", "/Volumes":
		return errors.New("system path rejected: " + p)
	}

	// Reject `/Users/<name>` — direct user home roots — but allow deeper
	// paths. Count separators after the prefix.
	if strings.HasPrefix(p, "/Users/") {
		rest := p[len("/Users/"):]
		if rest == "" || !strings.Contains(rest, "/") {
			return errors.New("user-home root level rejected: " + p)
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == filepath.Clean(home) {
			return errors.New("$HOME root rejected")
		}
	}
	return nil
}
