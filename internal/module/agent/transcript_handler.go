package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wake/purdex/internal/transcripttail"
)

const (
	transcriptDefaultTail = 800
	transcriptMaxTail     = 5000
	transcriptMaxBytes    = 2 << 20
)

type transcriptResponse struct {
	TranscriptID string   `json:"transcript_id"`
	Size         int64    `json:"size"`
	Mtime        int64    `json:"mtime"`
	StartOffset  int64    `json:"start_offset"`
	EndOffset    int64    `json:"end_offset"`
	More         bool     `json:"more"`
	Reset        bool     `json:"reset"`
	Lines        []string `json:"lines"`
}

func writeTranscriptJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func transcriptError(w http.ResponseWriter, status int, code string) {
	writeTranscriptJSON(w, status, map[string]string{"error": code})
}

// resolveOwner is the test seam over resolveSessionOwnerErr.
func (m *Module) resolveOwnerForTranscript(ctx context.Context, code string) (PaneOwner, bool, error) {
	if m.ownerResolver != nil {
		return m.ownerResolver(ctx, code)
	}
	return m.resolveSessionOwnerErr(ctx, code)
}

// handleSessionTranscript serves GET /api/sessions/{code}/transcript: complete
// jsonl lines of the Claude Code transcript behind a tmux session. The client
// never names a path; it is derived from the session owner and validated.
func (m *Module) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tailN := transcriptDefaultTail
	_, hasTail := q["tail"]
	_, hasAfter := q["after"]
	if hasTail && hasAfter {
		transcriptError(w, http.StatusBadRequest, "tail_and_after")
		return
	}
	if hasTail {
		n, err := strconv.Atoi(q.Get("tail"))
		if err != nil || n <= 0 {
			transcriptError(w, http.StatusBadRequest, "bad_tail")
			return
		}
		tailN = min(n, transcriptMaxTail)
	}
	var afterOff int64
	if hasAfter {
		v, err := strconv.ParseInt(q.Get("after"), 10, 64)
		if err != nil || v < 0 {
			transcriptError(w, http.StatusBadRequest, "bad_after")
			return
		}
		afterOff = v
	}

	owner, found, err := m.resolveOwnerForTranscript(r.Context(), r.PathValue("code"))
	if err != nil {
		transcriptError(w, http.StatusServiceUnavailable, "lookup_failed")
		return
	}
	if !found {
		transcriptError(w, http.StatusNotFound, "no_agent")
		return
	}
	if owner.AgentType != "cc" {
		writeTranscriptJSON(w, http.StatusNotFound, map[string]string{"error": "unsupported", "agent_type": owner.AgentType})
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		transcriptError(w, http.StatusNotFound, "no_transcript")
		return
	}
	path, err := resolveTranscriptPath(owner, home)
	if err != nil {
		transcriptError(w, http.StatusNotFound, err.Error())
		return
	}
	f, err := openTranscript(path)
	if err != nil {
		transcriptError(w, http.StatusNotFound, err.Error())
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		transcriptError(w, http.StatusNotFound, "file_missing")
		return
	}
	size := fi.Size()
	resp := transcriptResponse{
		TranscriptID: filepath.Base(path),
		Size:         size,
		Mtime:        fi.ModTime().UnixMilli(),
		Lines:        []string{},
	}

	clientID := q.Get("transcript_id")
	if (clientID != "" && clientID != resp.TranscriptID) || (hasAfter && afterOff > size) {
		// Rotated or truncated: tell the client to re-tail from the end.
		end, err := transcripttail.Tail(f, size, 0, transcriptMaxBytes)
		if err != nil {
			transcriptError(w, http.StatusInternalServerError, "read_failed")
			return
		}
		resp.Reset, resp.StartOffset, resp.EndOffset = true, end.End, end.End
		writeTranscriptJSON(w, http.StatusOK, resp)
		return
	}

	var res transcripttail.Result
	if hasAfter {
		res, err = transcripttail.After(f, size, afterOff, transcriptMaxBytes)
	} else {
		res, err = transcripttail.Tail(f, size, tailN, transcriptMaxBytes)
	}
	if errors.Is(err, transcripttail.ErrLineTooLarge) {
		transcriptError(w, http.StatusRequestEntityTooLarge, "line_too_large")
		return
	}
	if err != nil {
		transcriptError(w, http.StatusInternalServerError, "read_failed")
		return
	}
	resp.Lines, resp.StartOffset, resp.EndOffset, resp.More = res.Lines, res.Start, res.End, res.More
	writeTranscriptJSON(w, http.StatusOK, resp)
}
