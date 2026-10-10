package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	devicesmod "github.com/wake/purdex/internal/module/devices"
)

// #2493: an upload in flight stops when its token is revoked, a device upload has an absolute deadline, and a 413 reaches
// a client that is still streaming. Real TCP throughout: the behaviour is about the socket.

func deviceServer(t *testing.T, m *Module, id string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id != "" {
			r = r.WithContext(devices.WithPrincipal(r.Context(), devices.Principal{ID: id}))
		}
		m.handleUpload(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func inFlight(m *Module, id string) int {
	m.uploadSlots.mu.Lock()
	defer m.uploadSlots.mu.Unlock()
	return m.uploadSlots.n[id]
}

func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startTrickle opens an upload that declares `declared` bytes and then sends one byte every `every`, until the test ends.
func startTrickle(t *testing.T, srv *httptest.Server, declared int, every time.Duration) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Type: multipart/form-data; boundary=b\r\nContent-Length: %d\r\n\r\n", declared)
	require.NoError(t, err)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(every):
				if _, err := conn.Write([]byte("x")); err != nil {
					return
				}
			}
		}
	}()
	return conn
}

func readStatus(t *testing.T, conn net.Conn, within time.Duration) int {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(within)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "the server must answer within %v", within)
	return resp.StatusCode
}

func TestUploadResidue_RevokeAbortsOnlyThatDevicesUploads(t *testing.T) {
	m, _ := newUploadTestModule(t)
	srvX := deviceServer(t, m, "d_xxxxxxxxxxxx")
	srvY := deviceServer(t, m, "d_yyyyyyyyyyyy")
	connX := startTrickle(t, srvX, 1_000_000, 20*time.Millisecond)
	connY := startTrickle(t, srvY, 1_000_000, 20*time.Millisecond)
	waitForCond(t, "both uploads in flight", func() bool { return inFlight(m, "d_xxxxxxxxxxxx") == 1 && inFlight(m, "d_yyyyyyyyyyyy") == 1 })

	m.uploadSlots.abortDevices([]string{"d_xxxxxxxxxxxx"})

	assert.Equal(t, http.StatusUnauthorized, readStatus(t, connX, 3*time.Second))
	waitForCond(t, "the revoked device's slot freed", func() bool { return inFlight(m, "d_xxxxxxxxxxxx") == 0 })
	assert.Equal(t, 1, inFlight(m, "d_yyyyyyyyyyyy"), "another device's upload is untouched")

	m.uploadSlots.abortDevices([]string{"d_yyyyyyyyyyyy"})
	assert.Equal(t, http.StatusUnauthorized, readStatus(t, connY, 3*time.Second))
}

func TestUploadResidue_AbortTableIsCleanedUp(t *testing.T) {
	var l uploadLimiter
	called := 0
	untrack := l.track("d_a", func() { called++ })
	l.abortDevices([]string{"d_a"})
	assert.Equal(t, 1, called)
	untrack()
	l.abortDevices([]string{"d_a"})
	assert.Equal(t, 1, called, "an ended upload is not aborted again")
	assert.Empty(t, l.aborts, "no entry is left for a device with nothing in flight")
}

type fakeFeed struct {
	mu   sync.Mutex
	subs []func([]string)
}

func (f *fakeFeed) SubscribeRevoked(fn func([]string)) {
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
}

func TestUploadResidue_ModuleFollowsTheRevokeFeedOnce(t *testing.T) {
	m, _ := newUploadTestModule(t)
	feed := &fakeFeed{}
	m.core = &core.Core{Registry: core.NewServiceRegistry()}
	m.core.Registry.Register(devicesmod.RevokeFeedKey, devices.RevokeFeed(feed))

	m.followRevokes()
	m.followRevokes()
	require.Len(t, feed.subs, 1, "once, however often Start runs")

	aborted := false
	m.uploadSlots.track("d_a", func() { aborted = true })
	feed.subs[0]([]string{"d_a"})
	assert.True(t, aborted, "a revoke reaches the uploads in flight")
}

func TestUploadResidue_NoFeedIsNotAnError(t *testing.T) {
	m, _ := newUploadTestModule(t)
	m.core = &core.Core{Registry: core.NewServiceRegistry()}
	assert.NotPanics(t, m.followRevokes)
}

func TestUploadResidue_DeviceUploadHasAnAbsoluteDeadline(t *testing.T) {
	oldBase, oldRate := uploadDeviceBaseTime, uploadDeviceMinRate
	uploadDeviceBaseTime, uploadDeviceMinRate = 300*time.Millisecond, 1<<30
	t.Cleanup(func() { uploadDeviceBaseTime, uploadDeviceMinRate = oldBase, oldRate })

	m, _ := newUploadTestModule(t)
	srv := deviceServer(t, m, "d_aaaaaaaaaaaa")
	// one byte per 20 ms never trips the 30 s stall timeout
	conn := startTrickle(t, srv, 1_000_000, 20*time.Millisecond)
	waitForCond(t, "in flight", func() bool { return inFlight(m, "d_aaaaaaaaaaaa") == 1 })

	start := time.Now()
	assert.Equal(t, http.StatusBadRequest, readStatus(t, conn, 5*time.Second))
	assert.Less(t, time.Since(start), 2*time.Second, "cut by the deadline, not by the stall timeout")
	waitForCond(t, "the slot freed", func() bool { return inFlight(m, "d_aaaaaaaaaaaa") == 0 })
}

func TestUploadResidue_AdminHasNoAbsoluteDeadline(t *testing.T) {
	oldBase, oldRate := uploadDeviceBaseTime, uploadDeviceMinRate
	uploadDeviceBaseTime, uploadDeviceMinRate = 100*time.Millisecond, 1<<30
	t.Cleanup(func() { uploadDeviceBaseTime, uploadDeviceMinRate = oldBase, oldRate })

	m, _ := newUploadTestModule(t)
	srv := deviceServer(t, m, "") // no principal: admin
	ctype, body := multipartOf(t, 100)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", ctype, len(body))
	require.NoError(t, err)
	half := len(body) / 2
	_, err = conn.Write(body[:half])
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond) // five times a device's allowance
	_, err = conn.Write(body[half:])
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, readStatus(t, conn, 5*time.Second))
}

func TestUploadResidue_DeadlineFollowsTheDeclaredSize(t *testing.T) {
	const capBytes = 65 << 20
	assert.Equal(t, 30*time.Second+time.Duration(capBytes>>17)*time.Second, uploadTotalDeadline(capBytes, capBytes), "the cap: 30 s + 520 s")
	assert.Equal(t, uploadTotalDeadline(capBytes, capBytes), uploadTotalDeadline(-1, capBytes), "unknown length assumes the cap")
	assert.Equal(t, uploadTotalDeadline(capBytes, capBytes), uploadTotalDeadline(1<<40, capBytes), "a declared size beyond the cap is capped")
	assert.Equal(t, 31*time.Second, uploadTotalDeadline(128<<10, capBytes), "128 KiB: the base plus one second")
}

// A 413 must reach a client that is still streaming the oversize body (#2493 asked for a drain; measured, none is needed:
// without one the client sees the 413 in 20 of 20 runs, so this pins the outcome and not a mechanism).
func TestUploadResidue_RealSocket413ReachesTheClient(t *testing.T) {
	oldMax, oldOver := uploadMaxFileBytesDevice, uploadFormOverhead
	uploadMaxFileBytesDevice, uploadFormOverhead = 1<<20, 1<<20
	t.Cleanup(func() { uploadMaxFileBytesDevice, uploadFormOverhead = oldMax, oldOver })

	m, _ := newUploadTestModule(t)
	srv := deviceServer(t, m, "d_aaaaaaaaaaaa")
	ctype, body := multipartOf(t, 48<<20)
	got413 := 0
	for i := 0; i < 20; i++ {
		req, err := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", ctype)
		resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			t.Logf("run %d: %v", i, err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusRequestEntityTooLarge {
			got413++
		}
	}
	assert.Equal(t, 20, got413, "every oversize upload must be seen as a 413")
	waitForCond(t, "the slot freed", func() bool { return inFlight(m, "d_aaaaaaaaaaaa") == 0 })
}
