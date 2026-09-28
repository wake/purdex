package nex

// POST /api/nex/executions/{id}/uploads (worker-pane theme spec §9.1): save
// one multipart file (field "file") for a worker and return its absolute
// path, which the SPA then references in the next message as a
// `[file: <path>]` line. No tmux is involved.
//
// The file lands inside the execution's cwd, at
// <cwd>/.purdex-uploads/<execution id>/, because under Nexen's `standard`
// and `readonly` profiles `claude -p` denies Read outside its cwd (measured
// 2026-09-28) — a file saved anywhere else would be unreadable to the
// agent it was uploaded for. .purdex-uploads/.gitignore holds `*` so the
// directory never shows up in the project's git status.

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/fsutil"
)

// uploadMaxBytes caps one uploaded file (spec §9.1: 50 MiB). A var so tests
// can shrink it.
var uploadMaxBytes int64 = 50 << 20

// uploadBodyOverhead is the multipart framing (boundaries, part headers,
// other small fields) allowed on top of the file cap for the whole body.
const uploadBodyOverhead = 1 << 20

// uploadsDirName is the directory under the execution's cwd.
const uploadsDirName = ".purdex-uploads"

// handleExecutionUpload: guard, principal, id, row, row preflights (ended,
// cwd), then stream the file part into a dedup'd file under the cwd.
func (m *Module) handleExecutionUpload(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("id")

	if m.sys.service == nil || m.sys.store == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	if _, err := m.principal(r); err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}

	// The id becomes a path component: one plain segment only.
	if !validUploadExecID(execID) {
		writeHandoffError(w, http.StatusBadRequest, "invalid_execution_id", "invalid execution id", nil)
		return
	}

	exec, err := m.getExecution(r.Context(), execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
			return
		}
		writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
		return
	}
	if executionEnded(exec) {
		writeHandoffError(w, http.StatusConflict, "execution_ended", "execution has ended; it cannot take new messages",
			map[string]any{"state": string(exec.State), "archived": exec.ArchivedAt != 0})
		return
	}
	if exec.Cwd == "" || !filepath.IsAbs(exec.Cwd) {
		writeHandoffError(w, http.StatusConflict, "cwd_unavailable", "execution has no absolute cwd", nil)
		return
	}

	cwd := filepath.Clean(exec.Cwd)
	root := filepath.Join(cwd, uploadsDirName)
	dir := filepath.Join(root, execID)
	if rel, err := filepath.Rel(cwd, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		writeHandoffError(w, http.StatusBadRequest, "invalid_execution_id", "upload directory escapes the execution's cwd", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBytes+uploadBodyOverhead)
	part, err := findFilePart(r)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeUploadTooLarge(w)
			return
		}
		writeHandoffError(w, http.StatusBadRequest, "missing_file", "multipart field \"file\" is required", nil)
		return
	}
	defer part.Close()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.logf("nex: upload mkdir %s: %v", dir, err)
		writeHandoffError(w, http.StatusInternalServerError, "upload_dir_failed", "cannot create upload directory", nil)
		return
	}
	ensureUploadsGitignore(m, root)

	dst, name, err := fsutil.CreateDedupFile(dir, uploadFileName(part.FileName()))
	if err != nil {
		m.logf("nex: upload create in %s: %v", dir, err)
		writeHandoffError(w, http.StatusInternalServerError, "write_failed", "cannot save file", nil)
		return
	}
	path := filepath.Join(dir, name)

	n, copyErr := io.Copy(dst, io.LimitReader(part, uploadMaxBytes+1))
	closeErr := dst.Close()
	if copyErr == nil && n > uploadMaxBytes {
		os.Remove(path)
		writeUploadTooLarge(w)
		return
	}
	if copyErr != nil {
		os.Remove(path)
		var tooBig *http.MaxBytesError
		if errors.As(copyErr, &tooBig) {
			writeUploadTooLarge(w)
			return
		}
		m.logf("nex: upload write %s: %v", path, copyErr)
		writeHandoffError(w, http.StatusInternalServerError, "write_failed", "writing file failed", nil)
		return
	}
	if closeErr != nil {
		os.Remove(path)
		m.logf("nex: upload close %s: %v", path, closeErr)
		writeHandoffError(w, http.StatusInternalServerError, "write_failed", "writing file failed", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"path": path, "name": name, "size": n})
}

// executionEnded: a terminal state (rejected, failed, terminated — no
// outgoing transitions in Nexen's store) or archived. Nothing more will be
// said to such an execution, so a file for it has no reader.
func executionEnded(e store.Execution) bool {
	if e.ArchivedAt != 0 {
		return true
	}
	switch e.State {
	case store.StateTerminated, store.StateFailed, store.StateRejected:
		return true
	}
	return false
}

// validUploadExecID: non-empty, one path segment, not a dot entry.
func validUploadExecID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`) && !strings.ContainsRune(id, 0)
}

// findFilePart streams the multipart body to the first part named "file"
// that carries a filename. Parts before it are skipped (small fields).
func findFilePart(r *http.Request) (*multipart.Part, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			if err == io.EOF {
				return nil, errors.New("no file part")
			}
			return nil, err
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return part, nil
		}
		part.Close()
	}
}

// uploadFileName strips directory components; a name with nothing left
// (".", "..", "/") becomes "upload".
func uploadFileName(raw string) string {
	name := filepath.Base(raw)
	switch name {
	case "", ".", "..", string(filepath.Separator):
		return "upload"
	}
	return name
}

// ensureUploadsGitignore writes <root>/.gitignore = "*" when absent. O_EXCL
// so an existing one — the user's or a concurrent request's — is kept.
// Failure is logged, not fatal: the upload itself is still usable.
func ensureUploadsGitignore(m *Module, root string) {
	f, err := os.OpenFile(filepath.Join(root, ".gitignore"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			m.logf("nex: upload .gitignore in %s: %v", root, err)
		}
		return
	}
	if _, err := f.WriteString("*\n"); err != nil {
		m.logf("nex: upload .gitignore write in %s: %v", root, err)
	}
	f.Close()
}

func writeUploadTooLarge(w http.ResponseWriter) {
	writeHandoffError(w, http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds the upload limit",
		map[string]any{"max_bytes": uploadMaxBytes})
}
