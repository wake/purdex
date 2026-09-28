package nex

// POST /api/nex/executions/{id}/uploads (worker-pane theme spec §9.1): a
// file is saved inside the execution's cwd at .purdex-uploads/<id>/ and its
// absolute path is returned for the SPA to reference in the next message.
// Reuses the take-back fixtures for the module, store and auth.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"
)

// newUploadEnv: the execution is idle and its cwd is a fresh temp dir.
func newUploadEnv(t *testing.T) (*takebackEnv, string) {
	t.Helper()
	env := newTakebackEnv(t)
	cwd := t.TempDir()
	env.store.results = []getResult{{exec: uploadExec(store.StateIdle, cwd)}}
	return env, cwd
}

func uploadExec(state store.State, cwd string) store.Execution {
	return store.Execution{ID: tbExecID, State: state, Provider: "claude", Cwd: cwd, SessionID: tbSessionID}
}

// postUpload sends a multipart body; field "" sends a form with no file.
func postUpload(t *testing.T, env *takebackEnv, id, field, filename string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("note", "x"))
	if field != "" {
		fw, err := mw.CreateFormFile(field, filename)
		require.NoError(t, err)
		_, err = fw.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	resp, err := http.Post(env.srv.URL+"/api/nex/executions/"+id+"/uploads", mw.FormDataContentType(), &buf)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response is JSON")
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	return resp.StatusCode, out
}

// realDir resolves symlinks (macOS temp dirs live under /var → /private/var).
func realDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

func TestUploadSavesIntoExecutionCwd(t *testing.T) {
	env, cwd := newUploadEnv(t)
	status, body := postUpload(t, env, tbExecID, "file", "notes.txt", []byte("hello"))
	require.Equal(t, http.StatusOK, status, body)

	want := filepath.Join(cwd, ".purdex-uploads", tbExecID, "notes.txt")
	path, _ := body["path"].(string)
	assert.True(t, filepath.IsAbs(path), "path is absolute: %q", path)
	assert.Equal(t, want, path)
	assert.Equal(t, "notes.txt", body["name"])
	assert.Equal(t, float64(5), body["size"])
	assert.True(t, strings.HasPrefix(realDir(t, filepath.Dir(path)), realDir(t, cwd)+string(filepath.Separator)))

	got, err := os.ReadFile(want)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))

	ignore, err := os.ReadFile(filepath.Join(cwd, ".purdex-uploads", ".gitignore"))
	require.NoError(t, err, ".gitignore created")
	assert.Equal(t, "*\n", string(ignore))
}

func TestUploadKeepsExistingGitignore(t *testing.T) {
	env, cwd := newUploadEnv(t)
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".purdex-uploads"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".purdex-uploads", ".gitignore"), []byte("custom\n"), 0o644))
	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	require.Equal(t, http.StatusOK, status, body)
	ignore, err := os.ReadFile(filepath.Join(cwd, ".purdex-uploads", ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "custom\n", string(ignore))
}

func TestUploadDedupsSameName(t *testing.T) {
	env, cwd := newUploadEnv(t)
	status, body := postUpload(t, env, tbExecID, "file", "photo.png", []byte("one"))
	require.Equal(t, http.StatusOK, status, body)
	status, body = postUpload(t, env, tbExecID, "file", "photo.png", []byte("two"))
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "photo-1.png", body["name"])
	assert.Equal(t, filepath.Join(cwd, ".purdex-uploads", tbExecID, "photo-1.png"), body["path"])
	got, err := os.ReadFile(filepath.Join(cwd, ".purdex-uploads", tbExecID, "photo-1.png"))
	require.NoError(t, err)
	assert.Equal(t, "two", string(got))
}

func TestUploadStripsTraversalFromFilename(t *testing.T) {
	env, cwd := newUploadEnv(t)
	status, body := postUpload(t, env, tbExecID, "file", "../../x", []byte("t"))
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "x", body["name"])
	assert.Equal(t, filepath.Join(cwd, ".purdex-uploads", tbExecID, "x"), body["path"])
	_, err := os.Stat(filepath.Join(cwd, ".purdex-uploads", tbExecID, "x"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(cwd, "x"))
	assert.True(t, os.IsNotExist(err), "nothing written above the upload dir")
}

func TestUploadRejectsIDThatEscapesCwd(t *testing.T) {
	env, cwd := newUploadEnv(t)
	// %2F survives the mux as one segment and PathValue unescapes it.
	status, body := postUpload(t, env, "..%2F..%2Fescape", "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusBadRequest, status, body)
	assert.Equal(t, "invalid_execution_id", body["code"])
	_, err := os.Stat(filepath.Join(cwd, ".purdex-uploads"))
	assert.True(t, os.IsNotExist(err), "nothing created")
}

func TestUpload404UnknownExecution(t *testing.T) {
	env, _ := newUploadEnv(t)
	env.store.results = []getResult{{err: store.ErrNotFound}}
	status, body := postUpload(t, env, "exec-nope", "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "execution_not_found", body["code"])
}

func TestUpload409EndedExecution(t *testing.T) {
	cases := map[string]func(cwd string) store.Execution{
		"terminated": func(cwd string) store.Execution { return uploadExec(store.StateTerminated, cwd) },
		"failed":     func(cwd string) store.Execution { return uploadExec(store.StateFailed, cwd) },
		"rejected":   func(cwd string) store.Execution { return uploadExec(store.StateRejected, cwd) },
		"archived": func(cwd string) store.Execution {
			e := uploadExec(store.StateIdle, cwd)
			e.ArchivedAt = 1700000000
			return e
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			env, cwd := newUploadEnv(t)
			env.store.results = []getResult{{exec: mk(cwd)}}
			status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
			assert.Equal(t, http.StatusConflict, status)
			assert.Equal(t, "execution_ended", body["code"])
			_, err := os.Stat(filepath.Join(cwd, ".purdex-uploads"))
			assert.True(t, os.IsNotExist(err), "nothing created")
		})
	}
}

func TestUploadRunningExecutionAccepted(t *testing.T) {
	env, cwd := newUploadEnv(t)
	env.store.results = []getResult{{exec: uploadExec(store.StateRunning, cwd)}}
	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusOK, status, body)
}

func TestUpload409NoCwd(t *testing.T) {
	env, _ := newUploadEnv(t)
	env.store.results = []getResult{{exec: uploadExec(store.StateIdle, "")}}
	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "cwd_unavailable", body["code"])
}

// A cwd that no longer exists (removed, or an unmounted volume) must not be
// silently recreated by MkdirAll further down the handler.
func TestUpload409MissingCwdDir(t *testing.T) {
	env, cwd := newUploadEnv(t)
	require.NoError(t, os.RemoveAll(cwd))
	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "cwd_unavailable", body["code"])
	_, err := os.Stat(cwd)
	assert.True(t, os.IsNotExist(err), "cwd still does not exist")
}

// .purdex-uploads pre-created as a symlink pointing outside the cwd: the
// lexical containment check above can't see this (it never touches the
// filesystem), so the post-MkdirAll symlink-resolved check must catch it.
func TestUploadRejectsSymlinkedUploadsDir(t *testing.T) {
	env, cwd := newUploadEnv(t)
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(cwd, ".purdex-uploads")))

	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusBadRequest, status, body)
	assert.Equal(t, "upload_dir_outside_cwd", body["code"])

	entries, err := os.ReadDir(filepath.Join(outside, tbExecID))
	require.NoError(t, err, "mkdir followed the symlink before the check ran")
	assert.Empty(t, entries, "nothing written in the target")
}

func TestUpload413OverCap(t *testing.T) {
	old := uploadMaxBytes
	uploadMaxBytes = 16
	t.Cleanup(func() { uploadMaxBytes = old })

	env, cwd := newUploadEnv(t)
	status, body := postUpload(t, env, tbExecID, "file", "big.bin", bytes.Repeat([]byte("z"), 100))
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.Equal(t, "file_too_large", body["code"])
	entries, err := os.ReadDir(filepath.Join(cwd, ".purdex-uploads", tbExecID))
	require.NoError(t, err, "upload dir exists (created before the copy that overflowed)")
	assert.Empty(t, entries, "partial file removed")

	// Exactly at the cap is fine.
	status, body = postUpload(t, env, tbExecID, "file", "ok.bin", bytes.Repeat([]byte("z"), 16))
	assert.Equal(t, http.StatusOK, status, body)
}

// The cap can also be tripped by http.MaxBytesReader while findFilePart is
// still skipping past an earlier, oversized field — before the "file" part
// is ever reached, and before any directory is created.
func TestUpload413CapTrippedByPaddingField(t *testing.T) {
	old := uploadMaxBytes
	uploadMaxBytes = 16
	t.Cleanup(func() { uploadMaxBytes = old })

	env, cwd := newUploadEnv(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("pad", strings.Repeat("p", int(uploadMaxBytes+uploadBodyOverhead)+1024)))
	fw, err := mw.CreateFormFile("file", "a.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("a"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	resp, err := http.Post(env.srv.URL+"/api/nex/executions/"+tbExecID+"/uploads", mw.FormDataContentType(), &buf)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Equal(t, "file_too_large", out["code"])
	_, err = os.Stat(filepath.Join(cwd, ".purdex-uploads"))
	assert.True(t, os.IsNotExist(err), "nothing created")
}

func TestUploadCapDefaultIs50MiB(t *testing.T) {
	assert.Equal(t, int64(50<<20), uploadMaxBytes)
}

func TestUpload400MissingFile(t *testing.T) {
	env, cwd := newUploadEnv(t)
	status, body := postUpload(t, env, tbExecID, "", "", nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "missing_file", body["code"])
	_, err := os.Stat(filepath.Join(cwd, ".purdex-uploads"))
	assert.True(t, os.IsNotExist(err), "nothing created")

	status, body = postUpload(t, env, tbExecID, "other", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "missing_file", body["code"])
}

func TestUpload400NotMultipart(t *testing.T) {
	env, _ := newUploadEnv(t)
	resp, err := http.Post(env.srv.URL+"/api/nex/executions/"+tbExecID+"/uploads", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "missing_file", out["code"])
}

func TestUpload503WhenEngineUnavailable(t *testing.T) {
	env, _ := newUploadEnv(t)
	env.m.sys.store = nil
	status, body := postUpload(t, env, tbExecID, "file", "a.txt", []byte("a"))
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "nex_unavailable", body["code"])
}

// ensureUploadsGitignore's write/close-error cleanup (remove the file it
// started, so a later request's O_EXCL doesn't find a stale/short file and
// skip forever) has no test here: triggering a write or close failure on an
// fd this function just opened itself, without adding a production-only
// seam (an injected writer or a package-var os.OpenFile), isn't practical
// with the real filesystem this test suite uses everywhere else.

// Part.FileName already applies filepath.Base, so the handler tests cannot
// see uploadFileName's own stripping; pin it directly.
func TestUploadFileName(t *testing.T) {
	cases := map[string]string{
		"notes.txt":       "notes.txt",
		"../../x":         "x",
		"/etc/passwd":     "passwd",
		"a/b/c.png":       "c.png",
		"..":              "upload",
		".":               "upload",
		"":                "upload",
		"/":               "upload",
		".hidden":         ".hidden",
		"name with sp.md": "name with sp.md",
		"a\nb.txt":        "a_b.txt",
		"a\r\nb.txt":      "a__b.txt",
		"a\tb.txt":        "a_b.txt",
		"[x].txt":         "_x_.txt",
		"   ":             "upload",
	}
	for in, want := range cases {
		assert.Equal(t, want, uploadFileName(in), in)
	}
}

// The handler also refuses a directory outside the cwd, which backstops this
// check; pin the id rule itself.
func TestValidUploadExecID(t *testing.T) {
	for _, ok := range []string{"exec-1", "01J9ZK", "a.b"} {
		assert.True(t, validUploadExecID(ok), ok)
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, "a\x00b"} {
		assert.False(t, validUploadExecID(bad), bad)
	}
}
