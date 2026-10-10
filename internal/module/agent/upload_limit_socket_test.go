package agent

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
)

// A refused upload must reach a client that is still streaming a big body as a 429, not as a connection reset.
// Real TCP: a recorder cannot show what the server does with the unread body once the handler returns.

func multipartOf(t *testing.T, size int) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("session", "my-sess"))
	fw, err := w.CreateFormFile("file", "big.bin")
	require.NoError(t, err)
	_, err = fw.Write(make([]byte, size))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

func TestUploadLimit_RealSocketClientSeesTheRefusal(t *testing.T) {
	m, _ := newUploadTestModule(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(devices.WithPrincipal(r.Context(), devices.Principal{ID: "d_aaaaaaaaaaaa"}))
		m.handleUpload(w, r)
	}))
	t.Cleanup(srv.Close)
	require.True(t, m.uploadSlots.acquire("d_aaaaaaaaaaaa"))
	require.True(t, m.uploadSlots.acquire("d_aaaaaaaaaaaa"))

	ctype, body := multipartOf(t, 32<<20)
	got429, failed := 0, 0
	for i := 0; i < 20; i++ {
		req, err := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", ctype)
		resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			failed++
			t.Logf("run %d: %v", i, err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429++
		}
	}
	t.Logf("429: %d/20, transport errors: %d", got429, failed)
	assert.Equal(t, 20, got429, "every refused upload must be seen as a 429")
}
