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

// createDedupFile atomically creates a file in dir using O_CREATE|O_EXCL to
// avoid TOCTOU races. If "photo.png" already exists it tries "photo-1.png",
// "photo-2.png", etc. Returns the open file and the chosen filename.
func createDedupFile(dir, name string) (*os.File, string, error) {
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		return f, name, nil
	}
	if !os.IsExist(err) {
		return nil, "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		path = filepath.Join(dir, candidate)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			return f, candidate, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
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
	dir := filepath.Join(m.getUploadDir(), sessionCode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[agent] mkdir upload dir: %v", err)
		http.Error(w, `{"error":"cannot create upload directory"}`, http.StatusInternalServerError)
		return
	}

	// Save file with atomic dedup. Strip directory components to prevent path traversal.
	dst, filename, err := createDedupFile(dir, filepath.Base(header.Filename))
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

// resolveSessionName maps a pdx session code to the tmux session name.
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
