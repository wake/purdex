package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// deduplicateFilename returns a filename that does not conflict with
// existing files in dir. If "photo.png" exists it tries "photo-1.png",
// "photo-2.png", etc.
func deduplicateFilename(dir, name string) string {
	if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// handleUpload handles POST /api/agent/upload.
// It saves the uploaded file and injects the path into the tmux pane.
func (m *Module) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}

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
	tmuxName := m.resolveSessionName(sessionCode)
	if tmuxName == "" {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Ensure upload directory exists.
	dir := filepath.Join(m.uploadDir, sessionCode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[agent] mkdir upload dir: %v", err)
		http.Error(w, `{"error":"cannot create upload directory"}`, http.StatusInternalServerError)
		return
	}

	// Save file with dedup. Strip directory components to prevent path traversal.
	filename := deduplicateFilename(dir, filepath.Base(header.Filename))
	destPath := filepath.Join(dir, filename)
	dst, err := os.Create(destPath)
	if err != nil {
		log.Printf("[agent] create file: %v", err)
		http.Error(w, `{"error":"cannot save file"}`, http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("[agent] write file: %v", err)
		http.Error(w, `{"error":"write failed"}`, http.StatusInternalServerError)
		return
	}

	// Inject path into tmux pane via send-keys (space prefix, quoted, literal mode).
	// Quoting handles filenames with spaces so CC receives the full path as one token.
	if err := m.core.Tmux.SendKeysRaw(tmuxName, "-l", ` "`+destPath+`"`); err != nil {
		os.Remove(destPath) // Clean up orphaned file
		log.Printf("[agent] send-keys: %v", err)
		http.Error(w, `{"error":"inject failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"filename": filename,
		"injected": true,
	})
}

// resolveSessionName maps a tbox session code to the tmux session name.
func (m *Module) resolveSessionName(code string) string {
	if m.sessions == nil {
		return ""
	}
	info, err := m.sessions.GetSession(code)
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}
