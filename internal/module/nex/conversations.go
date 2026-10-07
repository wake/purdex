package nex

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/conversations"
	"github.com/wake/purdex/internal/module/agent"
	pstore "github.com/wake/purdex/internal/store"
	"lab.protype.tw/wake/nexen/store"
)

// Ended and gone conversations (spec §13.2, §13.3, §13.5): which Claude Code
// conversations on this host have no owner, and whether their transcript
// still exists. buildConversations is a pure join of materialized inputs;
// its only I/O is the two stat seams.

// conversationState names the two lists (the endpoint's ?state=).
type conversationState string

const (
	conversationEnded conversationState = "ended" // no owner, transcript present
	conversationGone  conversationState = "gone"  // no owner, transcript absent
)

// title_source values (R-4-10, in fallback order).
const (
	titleFromCustom    = "custom"
	titleFromAI        = "ai"
	titleFromNexen     = "nexen"
	titleFromRegistry  = "registry"
	titleFromPrompt    = "prompt"
	titleFromSessionID = "session_id"
)

// last_in values (R-4-6).
const (
	lastInTerminal = "terminal"
	lastInWorker   = "worker"
)

// conversationTitleMaxRunes bounds a title taken from the first prompt.
const conversationTitleMaxRunes = 120

// conversationRow is one ended or gone conversation. Its JSON tags are the
// wire format of GET /api/nex/conversations.
type conversationRow struct {
	SessionID         string `json:"session_id"`
	Title             string `json:"title"`
	TitleSource       string `json:"title_source"` // "custom" | "ai" | "nexen" | "registry" | "prompt" | "session_id"
	FirstPrompt       string `json:"first_prompt,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	CwdExists         bool   `json:"cwd_exists"`
	LastActivityAt    int64  `json:"last_activity_at"` // Unix ms
	LastIn            string `json:"last_in"`          // "terminal" | "worker"
	TranscriptPath    string `json:"transcript_path,omitempty"`
	LatestExecutionID string `json:"latest_execution_id,omitempty"`
	EffectiveProfile  string `json:"effective_profile,omitempty"`
}

// conversationInputs are materialized rows and results, never services.
// Both seams are required.
type conversationInputs struct {
	IndexRows []pstore.ConversationIndexRow
	Scan      conversations.ScanResult
	Execs     []store.Execution       // Nexen: every execution, archived included
	Names     map[string]string       // lowercase session id -> registry name (title fallback); may be nil
	Terminals []agent.TerminalSession // LiveSessions(ctx, "cc"), in any order
	IsRegular func(path string) bool  // Lstat(path).Mode().IsRegular()
	DirExists func(path string) bool  // Stat(path).IsDir()
}

type conversationsResult struct {
	Ended, Gone  []conversationRow // never nil; each sorted LastActivityAt desc, then SessionID asc; uncapped
	UnknownOwner int               // S skipped because only unverified frames hold them (R-4-9)
}

// buildConversations decides, for every in-scope S without an owner, whether
// it is ended or gone, and fills its row. Session ids from the index, the
// listing, the frames and Nexen are compared lowercase.
//
//   - Scope (R-4-14): S's indexed first entrypoint is non-empty and not
//     sdk-*, or S has a stint (an execution keyed to S under D9:
//     SessionID, else ResumeSessionID).
//   - Running (R-4-9, D1): a verified frame holds S, or a live execution is
//     for S through either id (executionIsFor, as checkOwners). Not listed.
//   - Unknown owner (R-4-9): only unverified frames hold S. Listed nowhere,
//     counted.
//   - Ended when the transcript is present, else gone. Present: the listing
//     failed (R-4-1: nothing is gone from a failed listing); S is listed; the
//     latest stint's TranscriptPath is a regular file (R-4-8); or S's indexed
//     or latest-stint path lies directly in an unreadable slug dir (R-4-7).
func buildConversations(in conversationInputs) conversationsResult {
	// Stints: the latest one per S represents it (D9). running collects every
	// S a live execution is for.
	latest := make(map[string]store.Execution)
	running := make(map[string]bool)
	for _, e := range in.Execs {
		if isLiveExecution(e) {
			for _, id := range [...]string{e.SessionID, e.ResumeSessionID} {
				if id != "" {
					running[strings.ToLower(id)] = true
				}
			}
		}
		s := e.SessionID
		if s == "" {
			s = e.ResumeSessionID
		}
		if s == "" {
			continue // names no conversation
		}
		s = strings.ToLower(s)
		if cur, ok := latest[s]; !ok || newerStint(e, cur) {
			latest[s] = e
		}
	}

	index := make(map[string]pstore.ConversationIndexRow, len(in.IndexRows))
	for _, r := range in.IndexRows {
		if s := strings.ToLower(r.SessionID); s != "" {
			index[s] = r
		}
	}

	// Terminal owners. A verified frame anywhere in the list wins over an
	// unverified one for the same S, whatever the order.
	unverified := make(map[string]bool)
	for _, t := range in.Terminals {
		s := strings.ToLower(t.SessionID)
		switch {
		case s == "":
		case t.Verified:
			running[s] = true
		default:
			unverified[s] = true
		}
	}

	candidates := make(map[string]bool, len(index)+len(latest))
	for s, r := range index {
		if bornInteractively(r.FirstEntrypoint) {
			candidates[s] = true
		}
	}
	for s := range latest {
		candidates[s] = true
	}

	unreadable := make(map[string]bool, len(in.Scan.UnreadableDirs))
	for _, d := range in.Scan.UnreadableDirs {
		unreadable[filepath.Clean(d)] = true
	}
	inUnreadableDir := func(path string) bool {
		return path != "" && unreadable[filepath.Dir(path)]
	}

	// Non-nil, so an empty list serializes as [] (never null).
	res := conversationsResult{Ended: []conversationRow{}, Gone: []conversationRow{}}
	for s := range candidates {
		if running[s] {
			continue
		}
		if unverified[s] {
			res.UnknownOwner++
			continue
		}
		row, hasRow := index[s]
		stint, hasStint := latest[s]
		entry, listed := in.Scan.Present[s]

		present := in.Scan.RootErr != nil || listed ||
			(hasStint && stint.TranscriptPath != "" && in.IsRegular(stint.TranscriptPath)) ||
			(hasRow && inUnreadableDir(row.TranscriptPath)) ||
			(hasStint && inUnreadableDir(stint.TranscriptPath))

		r := conversationRow{SessionID: s, FirstPrompt: row.FirstPrompt}
		r.Title, r.TitleSource = conversationTitle(s, row, stint.TitleText, in.Names[s])
		r.Cwd = firstNonEmpty(row.Cwd, stint.Cwd)
		r.CwdExists = r.Cwd != "" && in.DirExists(r.Cwd)
		// The listing, else the last the index saw (§13.3), else the stint.
		switch {
		case listed:
			r.LastActivityAt, r.TranscriptPath = entry.MtimeMs, entry.Path
		case hasRow:
			r.LastActivityAt, r.TranscriptPath = row.MtimeMs, firstNonEmpty(row.TranscriptPath, stint.TranscriptPath)
		default:
			r.LastActivityAt, r.TranscriptPath = stint.UpdatedAt, stint.TranscriptPath
		}
		r.LastIn = conversationLastIn(row.FirstEntrypoint, row.LastEntrypoint, hasStint)
		r.LatestExecutionID, r.EffectiveProfile = stint.ID, stint.EffectiveProfile

		if present {
			res.Ended = append(res.Ended, r)
		} else {
			res.Gone = append(res.Gone, r)
		}
	}
	sortConversationRows(res.Ended)
	sortConversationRows(res.Gone)
	return res
}

// newerStint: a is later than b under D9 (CreatedAt, then ID).
func newerStint(a, b store.Execution) bool {
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	return a.ID > b.ID
}

// bornInteractively (R-4-14): a first entrypoint that is non-empty and not
// sdk-* (cli, an IDE extension, …).
func bornInteractively(firstEntrypoint string) bool {
	return firstEntrypoint != "" && !strings.HasPrefix(firstEntrypoint, "sdk-")
}

// conversationLastIn is 上次在 (R-4-6): the last entrypoint, else the first;
// sdk-* means worker and any other value terminal; with neither, worker when
// S has a stint, terminal otherwise.
func conversationLastIn(first, last string, hasStint bool) string {
	ep := firstNonEmpty(last, first)
	if ep == "" && hasStint || strings.HasPrefix(ep, "sdk-") {
		return lastInWorker
	}
	return lastInTerminal
}

// conversationTitle (R-4-10): custom-title, ai-title, the latest stint's
// Nexen title, the registry name Claude Code gave the session while it was
// alive, the first prompt's first line, then S[:8].
func conversationTitle(s string, row pstore.ConversationIndexRow, nexenTitle, registryName string) (title, source string) {
	if t := strings.TrimSpace(row.CustomTitle); t != "" {
		return t, titleFromCustom
	}
	if t := strings.TrimSpace(row.AITitle); t != "" {
		return t, titleFromAI
	}
	if t := strings.TrimSpace(nexenTitle); t != "" {
		return t, titleFromNexen
	}
	if t := strings.TrimSpace(registryName); t != "" {
		return t, titleFromRegistry
	}
	if t := promptFirstLine(row.FirstPrompt); t != "" {
		return t, titleFromPrompt
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return s, titleFromSessionID
}

// promptFirstLine is the prompt up to its first newline, trimmed, cut to
// conversationTitleMaxRunes runes.
func promptFirstLine(prompt string) string {
	line, _, _ := strings.Cut(prompt, "\n")
	line = strings.TrimSpace(line)
	n := 0
	for i := range line {
		if n == conversationTitleMaxRunes {
			return strings.TrimSpace(line[:i])
		}
		n++
	}
	return line
}

func sortConversationRows(rows []conversationRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastActivityAt != rows[j].LastActivityAt {
			return rows[i].LastActivityAt > rows[j].LastActivityAt
		}
		return rows[i].SessionID < rows[j].SessionID
	})
}
