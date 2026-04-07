package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type uploadStatsResponse struct {
	Path      string `json:"path"`
	TotalSize int64  `json:"total_size"`
	FileCount int    `json:"file_count"`
}

type uploadFileInfo struct {
	Session  string    `json:"session"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
}

// handleUploadStats returns aggregate stats for the upload directory.
func (m *Module) handleUploadStats(w http.ResponseWriter, r *http.Request) {
	stats := uploadStatsResponse{Path: m.uploadDir}

	_ = filepath.Walk(m.uploadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if !info.IsDir() {
			stats.TotalSize += info.Size()
			stats.FileCount++
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleUploadFiles lists all uploaded files grouped by session.
func (m *Module) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	var files []uploadFileInfo

	entries, err := os.ReadDir(m.uploadDir)
	if err != nil {
		// Directory doesn't exist or is unreadable — return empty array.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]uploadFileInfo{})
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := entry.Name()
		sessionPath := filepath.Join(m.uploadDir, sessionDir)
		fileEntries, err := os.ReadDir(sessionPath)
		if err != nil {
			continue
		}
		for _, fe := range fileEntries {
			if fe.IsDir() {
				continue
			}
			info, err := fe.Info()
			if err != nil {
				continue
			}
			files = append(files, uploadFileInfo{
				Session:  sessionDir,
				Filename: fe.Name(),
				Size:     info.Size(),
				ModTime:  info.ModTime(),
			})
		}
	}

	if files == nil {
		files = []uploadFileInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// handleDeleteUploadSession removes an entire session upload directory.
func (m *Module) handleDeleteUploadSession(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	target := filepath.Clean(filepath.Join(m.uploadDir, session))

	if !strings.HasPrefix(target, filepath.Clean(m.uploadDir)+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteUploadFile removes a single uploaded file.
func (m *Module) handleDeleteUploadFile(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	filename := r.PathValue("filename")
	target := filepath.Clean(filepath.Join(m.uploadDir, session, filename))

	if !strings.HasPrefix(target, filepath.Clean(m.uploadDir)+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteAllUploads removes all session subdirectories in the upload dir.
func (m *Module) handleDeleteAllUploads(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(m.uploadDir)
	if err != nil {
		// Nothing to delete.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	for _, entry := range entries {
		path := filepath.Join(m.uploadDir, entry.Name())
		_ = os.RemoveAll(path)
	}
	w.WriteHeader(http.StatusNoContent)
}
