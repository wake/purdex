package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func tmpSPA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ROOT</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("APP_JS"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doReq(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler_ServesAsset(t *testing.T) {
	h, err := Handler(tmpSPA(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, h, "GET", "/assets/app.js")
	if rec.Code != 200 || rec.Body.String() != "APP_JS" {
		t.Fatalf("asset: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_ServesIndexAtRoot(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "GET", "/")
	if rec.Code != 200 || rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("root: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_SPAFallbackForUnknownPath(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "GET", "/some/client/route")
	if rec.Code != 200 || rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("fallback: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_HeadRequests(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	// HEAD on an unknown client route must still succeed (fallback), body empty.
	rec := doReq(t, h, "HEAD", "/some/client/route")
	if rec.Code != 200 {
		t.Fatalf("HEAD fallback: got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD fallback: body must be empty, got %d bytes", rec.Body.Len())
	}
	rec2 := doReq(t, h, "HEAD", "/assets/app.js")
	if rec2.Code != 200 {
		t.Fatalf("HEAD asset: got %d", rec2.Code)
	}
}

func TestHandler_RejectsNonGetHead(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "POST", "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got %d, want 405", rec.Code)
	}
}

func TestHandler_NoPathTraversal(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	// A traversal attempt must not escape the SPA root; it must fall back to
	// index.html exactly (200 + index body), never leak a file outside the
	// root and never regress to a 404/500.
	rec := doReq(t, h, "GET", "/../../etc/passwd")
	if rec.Code != 200 || rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("traversal: got %d %q, want 200 %q", rec.Code, rec.Body.String(), "<html>ROOT</html>")
	}
}

func TestHandler_FailFastMissingIndex(t *testing.T) {
	// A dir without index.html must fail at construction, not at first request.
	if _, err := Handler(t.TempDir()); err == nil {
		t.Fatal("expected error for missing index.html, got nil")
	}
}

func TestHandler_EmbeddedRoot(t *testing.T) {
	// Handler("") uses the embedded FS (fs.Sub(embedded, "dist")). The
	// tracked placeholder internal/webui/dist/index.html must exist, so this
	// must succeed even without a real SPA build.
	h, err := Handler("")
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, h, "GET", "/")
	if rec.Code != 200 || rec.Body.Len() == 0 {
		t.Fatalf("embedded root: got %d, body len %d", rec.Code, rec.Body.Len())
	}
	rec2 := doReq(t, h, "GET", "/some/unknown/route")
	if rec2.Code != 200 {
		t.Fatalf("embedded fallback: got %d", rec2.Code)
	}
}
