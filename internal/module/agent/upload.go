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
	"sync"
	"time"

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

// uploadDeviceSlots is how many uploads one paired device (one token) may have in flight; admin is unlimited.
// uploadRetryAfter is the Retry-After (seconds) a refused upload carries.
const (
	uploadDeviceSlots = 2
	uploadRetryAfter  = "5"
)

// uploadRefuseDrainTimeout is the absolute time a refused upload's body may be drained for. A var so tests can shrink it.
var uploadRefuseDrainTimeout = 10 * time.Second

// drainRefusedBody reads and discards (never to disk) up to what a device may legitimately send, so the 429 reaches a
// client that is still streaming: closing a connection with unread body makes the kernel RST, and the client then sees
// a reset instead of the 429 (measured: 1 MiB of draining still lost 60-75% of 32 MB refusals; the full cap lost none).
// A refused request holds no slot, so the drain has an absolute deadline (not a per-read stall one, which a 1-byte
// trickle defeats); when it expires the 429 goes out anyway and may meet an RST. Best-effort by design.
func drainRefusedBody(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(uploadRefuseDrainTimeout)); err == nil {
		defer rc.SetReadDeadline(time.Time{})
	}
	_, _ = io.CopyN(io.Discard, r.Body, uploadMaxFileBytesDevice+uploadFormOverhead)
}

// uploadLimiter counts in-flight uploads per device id. A full device is refused at once (no queueing), and an entry
// that drops to zero is deleted so the map does not grow with every device ever seen.
type uploadLimiter struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *uploadLimiter) acquire(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[id] >= uploadDeviceSlots {
		return false
	}
	if l.n == nil {
		l.n = map[string]int{}
	}
	l.n[id]++
	return true
}

func (l *uploadLimiter) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[id] <= 1 {
		delete(l.n, id)
		return
	}
	l.n[id]--
}

// handleUpload handles POST /api/agent/upload.
// It saves the uploaded file and injects the path into the tmux pane.
func (m *Module) handleUpload(w http.ResponseWriter, r *http.Request) {
	limit := uploadMaxFileBytes
	if p, isDevice := devices.PrincipalFrom(r.Context()); isDevice {
		limit = uploadMaxFileBytesDevice
		// Per-token slot, taken before the body is touched and released on every exit (success, error, disconnect).
		if !m.uploadSlots.acquire(p.ID) {
			w.Header().Set("Retry-After", uploadRetryAfter)
			// Connection: close only on HTTP/1: under h2 Go turns it into a GOAWAY for the whole connection, which would
			// take the same client's legitimate uploads down with it.
			if r.ProtoMajor == 1 {
				w.Header().Set("Connection", "close")
			}
			drainRefusedBody(w, r)
			http.Error(w, `{"error":"too many uploads"}`, http.StatusTooManyRequests)
			return
		}
		defer m.uploadSlots.release(p.ID)
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
