package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
)

// Per-device upload slots (#2466). A "held" upload is a handler whose body is an io.Pipe: the first Write returns only
// once the handler has read it, so no sleep is needed to know it is past the slot check and parked on the body.

type heldUpload struct {
	pw   *io.PipeWriter
	done chan *httptest.ResponseRecorder
}

func holdUpload(t *testing.T, m *Module, ctx context.Context) *heldUpload {
	t.Helper()
	pr, pw := io.Pipe()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	req := httptest.NewRequest("POST", "/api/agent/upload", pr).WithContext(ctx)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h := &heldUpload{pw: pw, done: make(chan *httptest.ResponseRecorder, 1)}
	go func() {
		rec := httptest.NewRecorder()
		m.handleUpload(rec, req)
		h.done <- rec
	}()
	t.Cleanup(func() { pw.CloseWithError(errors.New("test over")) })
	wrote := make(chan error, 1)
	go func() { _, err := pw.Write([]byte("--" + mw.Boundary() + "\r\n")); wrote <- err }()
	select {
	case err := <-wrote:
		require.NoError(t, err)
	case rec := <-h.done: // the handler answered without reading the body: refused
		t.Fatalf("upload was refused before reading its body: %d", rec.Code)
	}
	return h
}

// drop ends the upload the way a vanished client does: the body errors out.
func (h *heldUpload) drop() *httptest.ResponseRecorder {
	h.pw.CloseWithError(errors.New("client gone"))
	return <-h.done
}

func deviceCtx(id string) context.Context {
	return devices.WithPrincipal(context.Background(), devices.Principal{ID: id})
}

func TestUploadLimit_ThirdConcurrentDeviceUploadIs429(t *testing.T) {
	m, _ := newUploadTestModule(t)
	ctx := deviceCtx("d_aaaaaaaaaaaa")
	h1, h2 := holdUpload(t, m, ctx), holdUpload(t, m, ctx)
	rec := postBig(t, m, ctx, 10)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "5", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), "too many uploads")
	h1.drop()
	h2.drop()
}

func TestUploadLimit_DevicesDoNotShareSlots(t *testing.T) {
	m, _ := newUploadTestModule(t)
	h1, h2 := holdUpload(t, m, deviceCtx("d_aaaaaaaaaaaa")), holdUpload(t, m, deviceCtx("d_aaaaaaaaaaaa"))
	assert.Equal(t, http.StatusOK, postBig(t, m, deviceCtx("d_bbbbbbbbbbbb"), 10).Code)
	h1.drop()
	h2.drop()
}

func TestUploadLimit_AdminIsUnlimited(t *testing.T) {
	m, _ := newUploadTestModule(t)
	var hs []*heldUpload
	for i := 0; i < 4; i++ {
		hs = append(hs, holdUpload(t, m, context.Background()))
	}
	assert.Equal(t, http.StatusOK, postBig(t, m, context.Background(), 10).Code)
	for _, h := range hs {
		h.drop()
	}
}

func TestUploadLimit_DisconnectFreesSlotAndMapEmpties(t *testing.T) {
	m, _ := newUploadTestModule(t)
	ctx := deviceCtx("d_aaaaaaaaaaaa")
	h1, h2 := holdUpload(t, m, ctx), holdUpload(t, m, ctx)
	require.Equal(t, http.StatusTooManyRequests, postBig(t, m, ctx, 10).Code)
	assert.Equal(t, http.StatusBadRequest, h1.drop().Code)
	assert.Equal(t, http.StatusOK, postBig(t, m, ctx, 10).Code, "a dropped upload must free its slot")
	h2.drop()
	m.uploadSlots.mu.Lock()
	defer m.uploadSlots.mu.Unlock()
	assert.Empty(t, m.uploadSlots.n, "zeroed entries are deleted")
}

// Success, 413 and 400 each finish more than twice in a row on one device: any path that leaked its slot would 429.
func TestUploadLimit_EveryExitPathReleases(t *testing.T) {
	shrinkUploadCaps(t)
	m, fake := newUploadTestModule(t)
	ctx := deviceCtx("d_aaaaaaaaaaaa")
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusRequestEntityTooLarge, postBig(t, m, ctx, 2000).Code)
		assert.Equal(t, http.StatusBadRequest, postNoSession(m, ctx).Code)
		assert.Equal(t, http.StatusOK, postBig(t, m, ctx, 10).Code)
		assert.Equal(t, http.StatusNotFound, postWithSession(m, ctx, "nonexistent").Code)
		fake.FailPasteText = true
		assert.Equal(t, http.StatusInternalServerError, postWithSession(m, ctx, "my-sess").Code)
		fake.FailPasteText = false
	}
}

func postWithSession(m *Module, ctx context.Context, session string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", session)
	fw, _ := w.CreateFormFile("file", "a.txt")
	fw.Write([]byte("x"))
	w.Close()
	req := httptest.NewRequest("POST", "/api/agent/upload", &buf).WithContext(ctx)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	m.handleUpload(rec, req)
	return rec
}

func postNoSession(m *Module, ctx context.Context) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "a.txt")
	fw.Write([]byte("x"))
	w.Close()
	req := httptest.NewRequest("POST", "/api/agent/upload", &buf).WithContext(ctx)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	m.handleUpload(rec, req)
	return rec
}
