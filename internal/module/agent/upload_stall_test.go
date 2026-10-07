package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Upload stall detection (#1523): a body that keeps trickling in is fine
// however long it takes; one that goes silent for longer than the stall
// timeout is cut. Real TCP, millisecond-scale timeout.

func startUploadServer(t *testing.T) *httptest.Server {
	t.Helper()
	m, _ := newUploadTestModule(t)
	old := uploadStallTimeout
	uploadStallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { uploadStallTimeout = old })
	srv := httptest.NewServer(http.HandlerFunc(m.handleUpload))
	t.Cleanup(srv.Close)
	return srv
}

func uploadBody(t *testing.T) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("session", "my-sess"))
	fw, err := w.CreateFormFile("file", "slow.bin")
	require.NoError(t, err)
	_, err = fw.Write(bytes.Repeat([]byte("a"), 600))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

func dialUpload(t *testing.T, srv *httptest.Server, ctype string, length int) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = fmt.Fprintf(conn, "POST /api/agent/upload HTTP/1.1\r\nHost: x\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", ctype, length)
	require.NoError(t, err)
	return conn
}

func TestHandleUpload_StalledBodyIsCut(t *testing.T) {
	srv := startUploadServer(t)
	ctype, body := uploadBody(t)
	conn := dialUpload(t, srv, ctype, len(body))
	_, err := conn.Write(body[:20])
	require.NoError(t, err)
	// Go silent. The server must give up and answer on its own.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "server should respond after the stall instead of hanging")
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleUpload_SlowButSteadyBodySucceeds(t *testing.T) {
	srv := startUploadServer(t)
	ctype, body := uploadBody(t)
	conn := dialUpload(t, srv, ctype, len(body))

	start := time.Now()
	const slices = 10
	step := len(body)/slices + 1
	for off := 0; off < len(body); off += step {
		end := off + step
		if end > len(body) {
			end = len(body)
		}
		_, err := conn.Write(body[off:end])
		require.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, time.Since(start), uploadStallTimeout, "test bug: not longer than the stall timeout")

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
}
