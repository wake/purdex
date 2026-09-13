// internal/module/dev/download.go
package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadBudget bounds build + transfer for one download request, measured
// from handler entry. The server has no global WriteTimeout, so the transfer
// needs its own deadline or a stalled reader would hold daemonRebuildMu
// indefinitely. The build alone gets buildBudget (spec §2.5 step 3).
const (
	downloadBudget = 6 * time.Minute
	buildBudget    = 5 * time.Minute
)

var allowedTargets = map[string]map[string]bool{
	"darwin": {"arm64": true, "amd64": true},
	"linux":  {"arm64": true, "amd64": true},
}

// handleDaemonDownload serves a pdx binary cross-compiled for ?goos=&goarch=.
// Artifacts are cached under bin/dist/pdx-<goos>-<goarch>-<hash>; the whole
// request runs under daemonRebuildMu so a /rebuild cannot exec the server
// mid-transfer and two downloads cannot race in bin/dist.
func (m *DevModule) handleDaemonDownload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	goos, goarch := q.Get("goos"), q.Get("goarch")
	if !allowedTargets[goos][goarch] {
		writeJSONError(w, http.StatusBadRequest, "unsupported target", "")
		return
	}

	if !daemonRebuildMu.TryLock() {
		writeJSONError(w, http.StatusConflict, "build in progress", "")
		return
	}
	defer daemonRebuildMu.Unlock()

	// Identity is captured exactly once and reused for the cache key, the
	// ldflags and the headers.
	hash := m.gitHeadFn()
	if hash == "" {
		writeJSONError(w, http.StatusInternalServerError, "git hash unavailable", "")
		return
	}
	version := m.readVersionFile()

	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	// Request budget runs from entry; the build is a child with its own cap.
	ctx, cancel := context.WithDeadline(parent, start.Add(downloadBudget))
	defer cancel()
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	_ = http.NewResponseController(w).SetWriteDeadline(start.Add(downloadBudget))

	distDir := filepath.Join(m.repoRoot, "bin", "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "mkdir", err.Error())
		return
	}
	name := fmt.Sprintf("pdx-%s-%s-%s", goos, goarch, hash)
	artifact := filepath.Join(distDir, name)

	if _, err := os.Stat(artifact); err != nil {
		tmp := artifact + ".tmp"
		ring := newRingBuffer(4 * 1024)
		buildCtx, buildCancel := context.WithTimeout(ctx, buildBudget)
		err := m.buildBinary(buildCtx, buildTarget{GOOS: goos, GOARCH: goarch}, hash, version, tmp, ring.WriteLine)
		buildCancel()
		if err != nil {
			os.Remove(tmp)
			writeJSONError(w, http.StatusInternalServerError, "build failed", ring.String())
			return
		}
		if err := os.Rename(tmp, artifact); err != nil {
			os.Remove(tmp)
			writeJSONError(w, http.StatusInternalServerError, "publish failed", err.Error())
			return
		}
	}

	pruneStaleArtifacts(distDir, goos, goarch, name)

	f, err := os.Open(artifact)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "open artifact", err.Error())
		return
	}
	defer f.Close()
	sum, err := fileSHA256(f)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "hash artifact", err.Error())
		return
	}
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="pdx"`)
	w.Header().Set("X-Pdx-Hash", hash)
	w.Header().Set("X-Pdx-Version", version)
	w.Header().Set("X-Pdx-Sha256", sum)
	http.ServeContent(w, r, "pdx", st.ModTime(), f)
}

// pruneStaleArtifacts removes every pdx-<goos>-<goarch>-* in dir except keep
// and any *.tmp, so the directory holds one artifact per target.
func pruneStaleArtifacts(dir, goos, goarch, keep string) {
	prefix := fmt.Sprintf("pdx-%s-%s-", goos, goarch)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if n == keep || !strings.HasPrefix(n, prefix) || strings.HasSuffix(n, ".tmp") {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}

// fileSHA256 hashes f and rewinds it.
func fileSHA256(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSONError(w http.ResponseWriter, status int, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]string{"error": msg}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ringBuffer keeps the last n bytes of build output for error details.
type ringBuffer struct {
	max int
	buf []byte
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

func (r *ringBuffer) WriteLine(line string) {
	r.buf = append(r.buf, line...)
	r.buf = append(r.buf, '\n')
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
}

func (r *ringBuffer) String() string { return string(r.buf) }
