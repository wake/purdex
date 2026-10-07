package nex

// Upload stall detection (#1523) on the nex upload route: a body that keeps
// trickling in succeeds however long it takes; one that goes silent longer
// than uploadStallTimeout is cut. Real TCP, millisecond-scale timeout.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stallUploadSetup(t *testing.T) (net.Conn, []byte) {
	t.Helper()
	env, _ := newUploadEnv(t)
	old := uploadStallTimeout
	uploadStallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { uploadStallTimeout = old })

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "slow.bin")
	require.NoError(t, err)
	_, err = fw.Write(bytes.Repeat([]byte("a"), 600))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	conn, err := net.Dial("tcp", env.srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = fmt.Fprintf(conn, "POST /api/nex/executions/%s/uploads HTTP/1.1\r\nHost: x\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
		tbExecID, mw.FormDataContentType(), buf.Len())
	require.NoError(t, err)
	return conn, buf.Bytes()
}

func TestUpload_StalledBodyIsCut(t *testing.T) {
	conn, body := stallUploadSetup(t)
	_, err := conn.Write(body[:20])
	require.NoError(t, err)
	// Go silent; the server must give up and answer on its own.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "server should respond after the stall instead of hanging")
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestUpload_SlowButSteadyBodySucceeds(t *testing.T) {
	conn, body := stallUploadSetup(t)

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
