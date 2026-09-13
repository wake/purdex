package dev

import (
	"bytes"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDownloadServer(t *testing.T, repoRoot, head string) *httptest.Server {
	t.Helper()
	m := &DevModule{repoRoot: repoRoot, versionFile: filepath.Join(repoRoot, "VERSION"), gitHeadFn: func() string { return head }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dev/daemon/download", m.handleDaemonDownload)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDownload_RejectsBadTarget(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "abc1234")
	for _, q := range []string{"", "goos=darwin", "goarch=arm64", "goos=plan9&goarch=arm64", "goos=darwin&goarch=mips"} {
		resp, err := http.Get(srv.URL + "/api/dev/daemon/download?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestDownload_NoGitHashIs500(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "git hash") {
		t.Fatalf("error = %q", body["error"])
	}
}

// fetchOK downloads one target and returns the body + response.
func fetchOK(t *testing.T, srv *httptest.Server, goos, goarch string) ([]byte, *http.Response) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=" + goos + "&goarch=" + goarch)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s/%s: status %d: %s", goos, goarch, resp.StatusCode, body)
	}
	return body, resp
}

func TestDownload_CacheMiss_BuildsEveryDarwinAndLinuxTarget(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("7.7.7\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := newDownloadServer(t, dir, "abc1234")

	cases := []struct {
		goos, goarch string
		check        func(t *testing.T, body []byte)
	}{
		{"darwin", "arm64", func(t *testing.T, b []byte) { assertMachO(t, b, macho.CpuArm64) }},
		{"darwin", "amd64", func(t *testing.T, b []byte) { assertMachO(t, b, macho.CpuAmd64) }},
		{"linux", "amd64", func(t *testing.T, b []byte) {
			if string(b[:4]) != "\x7fELF" || b[18] != 0x3e || b[19] != 0x00 {
				t.Fatalf("not a linux/amd64 ELF")
			}
		}},
		{"linux", "arm64", func(t *testing.T, b []byte) {
			if string(b[:4]) != "\x7fELF" || b[18] != 0xb7 || b[19] != 0x00 {
				t.Fatalf("not a linux/arm64 ELF")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.goos+"/"+c.goarch, func(t *testing.T) {
			body, resp := fetchOK(t, srv, c.goos, c.goarch)
			c.check(t, body)
			if resp.Header.Get("X-Pdx-Hash") != "abc1234" || resp.Header.Get("X-Pdx-Version") != "7.7.7" {
				t.Fatalf("identity headers: %v", resp.Header)
			}
			sum := sha256.Sum256(body)
			if resp.Header.Get("X-Pdx-Sha256") != hex.EncodeToString(sum[:]) {
				t.Fatalf("sha256 header mismatch")
			}
			if resp.ContentLength != int64(len(body)) {
				t.Fatalf("Content-Length %d, body %d", resp.ContentLength, len(body))
			}
			if resp.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatalf("content-type %q", resp.Header.Get("Content-Type"))
			}
			artifact := filepath.Join(dir, "bin", "dist", "pdx-"+c.goos+"-"+c.goarch+"-abc1234")
			if _, err := os.Stat(artifact); err != nil {
				t.Fatalf("artifact missing: %v", err)
			}
			if _, err := os.Stat(artifact + ".tmp"); !os.IsNotExist(err) {
				t.Fatal(".tmp left behind")
			}
		})
	}
}

func assertMachO(t *testing.T, body []byte, cpu macho.Cpu) {
	t.Helper()
	f, err := macho.NewFile(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not a Mach-O: %v", err)
	}
	if f.Cpu != cpu {
		t.Fatalf("Mach-O cpu = %v, want %v", f.Cpu, cpu)
	}
}

// A pre-existing artifact is served without any build: the source module
// does not even compile, and the artifact's mtime is pinned in the past.
func TestDownload_CacheHit_ServesWithoutBuilding(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("7.7.7\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(dir, "bin", "dist")
	if err := os.MkdirAll(dist, 0755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dist, "pdx-linux-amd64-abc1234")
	want := []byte("not really a binary but exactly these bytes")
	if err := os.WriteFile(artifact, want, 0755); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(artifact, past, past); err != nil {
		t.Fatal(err)
	}
	srv := newDownloadServer(t, dir, "abc1234")

	body, resp := fetchOK(t, srv, "linux", "amd64")
	if !bytes.Equal(body, want) {
		t.Fatalf("served %q, want the cached bytes", body)
	}
	sum := sha256.Sum256(want)
	if resp.Header.Get("X-Pdx-Sha256") != hex.EncodeToString(sum[:]) || resp.Header.Get("X-Pdx-Hash") != "abc1234" || resp.Header.Get("X-Pdx-Version") != "7.7.7" {
		t.Fatalf("headers: %v", resp.Header)
	}
	if resp.ContentLength != int64(len(want)) {
		t.Fatalf("Content-Length %d, want %d", resp.ContentLength, len(want))
	}
	st, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(past) {
		t.Fatal("cache hit rebuilt the artifact")
	}
}

// The checksum always describes the whole artifact; a Range request gets a
// 206 with a partial body and the same X-Pdx-Sha256. Clients verify only a
// full 200 response (Plan B does).
func TestDownload_RangeIsPartialButChecksumIsWhole(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	dist := filepath.Join(dir, "bin", "dist")
	if err := os.MkdirAll(dist, 0755); err != nil {
		t.Fatal(err)
	}
	want := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(dist, "pdx-linux-amd64-abc1234"), want, 0755); err != nil {
		t.Fatal(err)
	}
	srv := newDownloadServer(t, dir, "abc1234")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/dev/daemon/download?goos=linux&goarch=amd64", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "0123" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	sum := sha256.Sum256(want)
	if resp.Header.Get("X-Pdx-Sha256") != hex.EncodeToString(sum[:]) {
		t.Fatal("sha256 header must describe the whole artifact")
	}
}

func TestDownload_PrunesStaleArtifactsForSameTargetOnly(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	dist := filepath.Join(dir, "bin", "dist")
	os.MkdirAll(dist, 0755)
	stale := filepath.Join(dist, "pdx-linux-amd64-old0000")
	other := filepath.Join(dist, "pdx-darwin-arm64-old0000")
	tmp := filepath.Join(dist, "pdx-linux-amd64-zzz.tmp")
	for _, p := range []string{stale, other, tmp} {
		os.WriteFile(p, []byte("x"), 0755)
	}
	srv := newDownloadServer(t, dir, "abc1234")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale same-target artifact not pruned")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("other-target artifact must be kept")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Error("*.tmp must be kept")
	}
}

func TestDownload_BuildFailureIs500WithDetail(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	srv := newDownloadServer(t, dir, "abc1234")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "build failed" || !strings.Contains(body["detail"], "undefined") {
		t.Fatalf("body = %v", body)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "bin", "dist")); len(entries) != 0 {
		t.Fatalf("dist not clean after failure: %v", entries)
	}
}

func TestDownload_409WhileRebuildMutexHeld(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "abc1234")
	daemonRebuildMu.Lock()
	defer daemonRebuildMu.Unlock()
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
}

func TestRegisterRoutes_DownloadIsGated(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "0")
	m := &DevModule{}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/dev/daemon/download?goos=darwin&goarch=arm64", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}
