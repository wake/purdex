package modeventsmod

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/team"
)

type fakeModReader struct {
	read team.ModRead
	err  error
	got  []string
}

func (f *fakeModReader) ModTeamRead(sid string) (team.ModRead, error) {
	f.got = append(f.got, sid)
	return f.read, f.err
}

func socketGet(t *testing.T, path, target string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	defer tr.CloseIdleConnections()
	res, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("http://pdx" + target)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// The team module's reader is found at request time (it need not be up when this module starts) and answers on the socket.
func TestTeamRead_ServedOnTheSocketFromTheTeamModule(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	// no team module yet: 503, not an empty answer
	if code, body := socketGet(t, m.path, "/mod/v1/team?session_id=s1"); code != 503 {
		t.Fatalf("before the team module: %d %s", code, body)
	}
	f := &fakeModReader{read: team.ModRead{Role: "lead", Members: 3, TeamLabel: "資源線"}}
	c.Registry.Register(team.ModReadKey, team.ModReader(f))
	code, body := socketGet(t, m.path, "/mod/v1/team?session_id=s1")
	if code != 200 || body != `{"role":"lead","members":3,"team_label":"資源線"}` || len(f.got) != 1 || f.got[0] != "s1" {
		t.Fatalf("%d %s asked=%v", code, body, f.got)
	}
	f.err = errors.New("db down")
	if code, _ := socketGet(t, m.path, "/mod/v1/team?session_id=s1"); code != 500 {
		t.Fatalf("failing reader: %d", code)
	}
}

// The read exists on the mod socket only: the daemon's TCP mux (token-guarded, remote) does not serve it.
// Mutation gate: add the route to RegisterRoutes → red.
func TestTeamRead_NotServedOnTheTCPMux(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	c.Registry.Register(team.ModReadKey, team.ModReader(&fakeModReader{read: team.ModRead{Role: "lead", Members: 1}}))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, target := range []string{"/mod/v1/team?session_id=s1", "/api/mod/team?session_id=s1"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", target, rec.Code)
		}
	}
	var _ = modevents.TeamPath
}
