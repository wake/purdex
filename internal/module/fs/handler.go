package fs

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxBodySize  = 1 << 20  // 1 MB — general request body limit
	maxWriteSize = 20 << 20 // 20 MB — write endpoint body limit (base64 ≈ 15 MB decoded)
	maxReadSize  = 10 << 20 // 10 MB — read file size limit
)

// limitBody applies a MaxBytesReader to the request body.
func limitBody(w http.ResponseWriter, r *http.Request, n int64) {
	r.Body = http.MaxBytesReader(w, r.Body, n)
}

// ── shared types ────────────────────────────────────────────────────────────

type pathRequest struct {
	Path string `json:"path"`
}

type fileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// ── shared helpers ───────────────────────────────────────────────────────────

func decodePath(r *http.Request) (string, error) {
	var req pathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", err
	}
	return req.Path, nil
}

func validatePath(path string) bool {
	return filepath.IsAbs(filepath.Clean(path))
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// ── handlers ─────────────────────────────────────────────────────────────────

// handleList reads a directory and returns its entries (hidden files skipped,
// directories sorted first then alphabetically).
func (m *FsModule) handleList(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	path = filepath.Clean(path)
	if !validatePath(path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	result := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, fileEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})

	jsonOK(w, map[string]any{
		"path":    path,
		"entries": result,
	})
}

// handleRead returns the raw bytes of a file with Content-Type: application/octet-stream.
func (m *FsModule) handleRead(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	path = filepath.Clean(path)
	if !validatePath(path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		jsonError(w, "path is a directory", http.StatusBadRequest)
		return
	}
	if info.Size() > maxReadSize {
		jsonError(w, "file too large (max 10 MB)", http.StatusRequestEntityTooLarge)
		return
	}

	// Read via the open fd to avoid TOCTOU between stat and read.
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, io.LimitReader(f, maxReadSize+1))
}

// handleWrite decodes base64 content and writes it to a file,
// auto-creating parent directories as needed. Returns 204.
func (m *FsModule) handleWrite(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxWriteSize)
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)
	if !validatePath(req.Path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		jsonError(w, "content must be base64 encoded", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(req.Path, data, 0o644); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleStat returns metadata about a file or directory.
func (m *FsModule) handleStat(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	path = filepath.Clean(path)
	if !validatePath(path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	jsonOK(w, map[string]any{
		"size":        info.Size(),
		"mtime":       info.ModTime().UnixMilli(),
		"isDirectory": info.IsDir(),
		"isFile":      info.Mode().IsRegular(),
	})
}

// handleMkdir creates a directory, optionally creating parents. Returns 204.
func (m *FsModule) handleMkdir(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)
	if !validatePath(req.Path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	var err error
	if req.Recursive {
		err = os.MkdirAll(req.Path, 0o755)
	} else {
		err = os.Mkdir(req.Path, 0o755)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDelete removes a file or directory, optionally recursive. Returns 204.
func (m *FsModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Path = filepath.Clean(req.Path)
	if !validatePath(req.Path) {
		jsonError(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	// Reject recursive delete on shallow paths (depth < 3) to prevent catastrophic mistakes.
	// e.g. /, /Users, /Users/wake are rejected; /Users/wake/projects is allowed.
	if req.Recursive && strings.Count(req.Path, string(filepath.Separator)) < 3 {
		jsonError(w, "recursive delete rejected: path too shallow", http.StatusForbidden)
		return
	}

	var err error
	if req.Recursive {
		err = os.RemoveAll(req.Path)
	} else {
		err = os.Remove(req.Path)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleRename renames/moves a file or directory from → to. Returns 204.
func (m *FsModule) handleRename(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.From = filepath.Clean(req.From)
	req.To = filepath.Clean(req.To)
	if !validatePath(req.From) || !validatePath(req.To) {
		jsonError(w, "paths must be absolute", http.StatusBadRequest)
		return
	}

	if err := os.Rename(req.From, req.To); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
