package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/fsutil"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/session"
)

// uploadStallTimeout is how long the upload body may stall. A var so tests
// can shrink it.
// Body caps: admin / Mac, a paired phone (a stolen token must not be able to fill the disk), the multipart framing
// allowance, and the in-memory threshold above which parts spill to temp files. Vars so tests can shrink them.
var (
	uploadMaxFileBytes       int64 = 256 << 20
	uploadMaxFileBytesDevice int64 = 64 << 20
	uploadFormOverhead       int64 = 1 << 20
	uploadMemBytes           int64 = 32 << 20
)

var uploadStallTimeout = middleware.UploadStallTimeout

// handleUpload handles POST /api/agent/upload.
// It saves the uploaded file and injects the path into the tmux pane.
func (m *Module) handleUpload(w http.ResponseWriter, r *http.Request) {
	limit := uploadMaxFileBytes
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice {
		limit = uploadMaxFileBytesDevice
	}
	// Hard body cap (ParseMultipartForm's argument is only the in-memory threshold); the stall wrapper stays inside it.
	r.Body = http.MaxBytesReader(w, middleware.StallTimeoutBody(w, r, uploadStallTimeout), limit+uploadFormOverhead)
	if err := r.ParseMultipartForm(uploadMemBytes); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, `{"error":"too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	sessionCode := r.FormValue("session")
	if sessionCode == "" {
		http.Error(w, `{"error":"missing session"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"missing file"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Resolve session code to tmux session name.
	tmuxName := m.resolveSessionName(r.Context(), sessionCode)
	if tmuxName == "" {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Ensure upload directory exists.
	dir := filepath.Join(m.getUploadDir(), sessionCode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[agent] mkdir upload dir: %v", err)
		http.Error(w, `{"error":"cannot create upload directory"}`, http.StatusInternalServerError)
		return
	}

	// Save file with atomic dedup. Strip directory components to prevent path traversal.
	dst, filename, err := fsutil.CreateDedupFile(dir, safeUploadName(header.Filename))
	if err != nil {
		log.Printf("[agent] create file: %v", err)
		http.Error(w, `{"error":"cannot save file"}`, http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	destPath := filepath.Join(dir, filename)

	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(destPath) // Clean up atomically-created but partially-written file
		log.Printf("[agent] write file: %v", err)
		http.Error(w, `{"error":"write failed"}`, http.StatusInternalServerError)
		return
	}

	// Inject path into tmux pane via paste-buffer with bracketed paste markers.
	// Using paste (not send-keys) so Claude Code's TUI recognises the event
	// as a paste and auto-detects image file paths → [Image #N] chip.
	// inject=0|false (the iOS deck / chat, which sends the path in its own message) only saves.
	inject := uploadInjectWanted(r.FormValue("inject"))
	if inject {
		if err := m.core.Tmux.PasteText(tmuxName, destPath); err != nil {
			os.Remove(destPath) // Clean up orphaned file
			log.Printf("[agent] paste-text: %v", err)
			http.Error(w, `{"error":"inject failed"}`, http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"filename": filename,
		"path":     destPath,
		"injected": inject,
	})
}

// uploadInjectWanted: the optional "inject" form field; absent or anything but 0/false means paste (the Mac behaviour).
func uploadInjectWanted(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false":
		return false
	}
	return true
}

// safeUploadName reduces a client-supplied filename to a single plain path element: directory parts are dropped
// (both separators), and names that would resolve to a directory ("", ".", "..") or hold a NUL become "upload".
func safeUploadName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	base := filepath.Base(name)
	if base == "" || base == "." || base == ".." || base == "/" || strings.ContainsRune(base, 0) {
		return "upload"
	}
	return base
}

// resolveSessionName maps a pdx session code to the tmux session name.
func (m *Module) resolveSessionName(ctx context.Context, code string) string {
	if m.sessions == nil {
		return ""
	}
	info, err := session.GetSessionWithin(ctx, m.sessions, code)
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}
