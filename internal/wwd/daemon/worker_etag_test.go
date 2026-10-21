package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func getView(t *testing.T, h http.Handler, path, auth string) wire.WorkerView {
	t.Helper()
	w := send(h, "GET", path, auth, "")
	v, err := wire.Decode[wire.WorkerView](w.Body.Bytes())
	if w.Code != 200 || err != nil || v.Etag == "" {
		t.Fatalf("GET %s: %d %s (%v)", path, w.Code, w.Body, err)
	}
	return v
}

func putBody(enabled bool, heavy int, rule, expect string) string {
	exp := ""
	if expect != "" {
		exp = `,"expect_etag":"` + expect + `"`
	}
	return fmt.Sprintf(`{"enabled":%t,"slots":{"heavy":%d,"light":1},"owner_rule":%q%s}`, enabled, heavy, rule, exp)
}

func TestWorkerPutWithExpectOnlyChangesTheSettingItNames(t *testing.T) {
	level := 1
	d, _ := stateDaemon(t, "mlab", &level)
	h := d.TCPHandler()
	auth := "Bearer " + testToken

	v := getView(t, h, "/v1/worker", auth)
	if v.Etag != v.WorkerSettings.Etag() {
		t.Fatalf("the etag is not the settings' own: %+v", v)
	}
	w := send(h, "PUT", "/v1/worker", auth, putBody(true, 3, "", v.Etag))
	if w.Code != 200 {
		t.Fatalf("matching expect: %d %s", w.Code, w.Body)
	}
	after, _ := wire.Decode[wire.WorkerView](w.Body.Bytes())
	if after.Slots.Heavy != 3 || after.Etag == v.Etag || after.Etag != getView(t, h, "/v1/worker", auth).Etag {
		t.Fatalf("the answer is the setting now in force: %+v", after)
	}

	// somebody else changes the owner rule after the etag was read: the stale write changes nothing at all
	if w := send(h, "PUT", "/v1/worker", auth, putBody(true, 3, "hid<5m", "")); w.Code != 200 {
		t.Fatal(w.Body)
	}
	cur := getView(t, h, "/v1/worker", auth)
	w = send(h, "PUT", "/v1/worker", auth, putBody(false, 0, "", after.Etag))
	if w.Code != http.StatusConflict {
		t.Fatalf("stale expect: %d %s", w.Code, w.Body)
	}
	mm, err := wire.Decode[wire.WorkerEtagMismatch](w.Body.Bytes())
	if err != nil || mm.Reason != wire.WorkerEtagMismatchReason || mm.Etag != cur.Etag || mm.Worker.Etag != cur.Etag || mm.Worker.OwnerRule != "hid<5m" {
		t.Fatalf("409 body %s (%v), setting in force %+v", w.Body, err, cur)
	}
	if now := getView(t, h, "/v1/worker", auth); now.Etag != cur.Etag || now.OwnerRule != "hid<5m" || now.Slots.Heavy != 3 {
		t.Fatalf("a refused write changed the setting: %+v -> %+v", cur, now)
	}

	// without expect_etag the PUT is the one it always was
	if w := send(h, "PUT", "/v1/worker", auth, putBody(false, 0, "", "")); w.Code != 200 || getView(t, h, "/v1/worker", auth).Enabled {
		t.Fatalf("no expect: %d %s", w.Code, w.Body)
	}
	// an invalid setting is still a 400, expect or not
	if w := send(h, "PUT", "/v1/worker", auth, putBody(true, -1, "", getView(t, h, "/v1/worker", auth).Etag)); w.Code != http.StatusBadRequest {
		t.Errorf("invalid with a good etag: %d", w.Code)
	}
}

func TestOfTwoWritersWithTheSameEtagOnlyOneWins(t *testing.T) {
	level := 1
	d, _ := stateDaemon(t, "mlab", &level)
	h := d.TCPHandler()
	auth := "Bearer " + testToken
	v := getView(t, h, "/v1/worker", auth)

	const n = 16
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = send(h, "PUT", "/v1/worker", auth, putBody(true, 2+i, "", v.Etag)).Code
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("codes %v: want one 200 and %d 409", codes, n-1)
	}
}

func TestWorkerEtagThroughTheSocketToAPeer(t *testing.T) {
	level := 1
	remote, _ := stateDaemon(t, "air26", &level)
	srv := httptest.NewServer(remote.TCPHandler())
	defer srv.Close()
	local, _ := stateDaemon(t, "mlab", &level, PeerAddr{"air26", addr(srv)})
	sock := local.SocketHandler()

	v := getView(t, sock, fwdPath("/v1/worker", "air26"), "")
	if v.Etag != remote.opts.State.Worker().Etag() {
		t.Fatalf("the etag is the peer's: %+v", v)
	}
	if w := send(sock, "PUT", fwdPath("/v1/worker", "air26"), "", putBody(true, 4, "", v.Etag)); w.Code != 200 || remote.opts.State.Worker().Slots.Heavy != 4 {
		t.Fatalf("forwarded write: %d %s", w.Code, w.Body)
	}
	// the stale etag: the peer's 409 comes back as it is, and the peer is unchanged
	w := send(sock, "PUT", fwdPath("/v1/worker", "air26"), "", putBody(false, 0, "", v.Etag))
	mm, err := wire.Decode[wire.WorkerEtagMismatch](w.Body.Bytes())
	if w.Code != http.StatusConflict || err != nil || mm.Etag != remote.opts.State.Worker().Etag() || remote.opts.State.Worker().Slots.Heavy != 4 {
		t.Fatalf("forwarded stale write: %d %s (%v)", w.Code, w.Body, err)
	}
	if local.opts.State.Worker().Slots.Heavy == 4 {
		t.Fatal("the write landed on this host")
	}
}
