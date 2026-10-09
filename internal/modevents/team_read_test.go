package modevents

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TI-5a: GET /mod/v1/team?session_id= on the mod socket.

type fakeTeam struct {
	reads map[string]TeamRead
	err   error
	asked []string
}

func (f *fakeTeam) read(sid string) (TeamRead, error) {
	f.asked = append(f.asked, sid)
	if f.err != nil {
		return TeamRead{}, f.err
	}
	if r, ok := f.reads[sid]; ok {
		return r, nil
	}
	return TeamRead{Role: "none"}, nil
}

func teamHandler(f *fakeTeam) http.Handler {
	return NewHandler(NewRegistry(time.Now), WithTeamReader(f.read))
}

func TestTeamRead_ShapeForLeadMemberAndNone(t *testing.T) {
	f := &fakeTeam{reads: map[string]TeamRead{
		"lead-sid":   {Role: "lead", Members: 3, TeamLabel: "資源線"},
		"member-sid": {Role: "member"},
	}}
	h := teamHandler(f)
	for target, want := range map[string]string{
		"/mod/v1/team?session_id=lead-sid":   `{"role":"lead","members":3,"team_label":"資源線"}`,
		"/mod/v1/team?session_id=member-sid": `{"role":"member","members":0,"team_label":""}`,
		"/mod/v1/team?session_id=unknown":    `{"role":"none","members":0,"team_label":""}`,
	} {
		rec := post(t, h, http.MethodGet, target, "")
		if rec.Code != 200 || rec.Body.String() != want || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %s (%s), want %s", target, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), want)
		}
	}
}

func TestTeamRead_BadRequestsAndMethods(t *testing.T) {
	f := &fakeTeam{}
	h := teamHandler(f)
	for _, target := range []string{"/mod/v1/team", "/mod/v1/team?session_id=", "/mod/v1/team?other=x",
		"/mod/v1/team?session_id=" + strings.Repeat("a", 129), "/mod/v1/team?session_id=a&session_id=b"} {
		rec := post(t, h, http.MethodGet, target, "")
		if rec.Code != 400 || rec.Body.String() != `{"error":"bad_request"}` {
			t.Errorf("%s: %d %s, want 400 bad_request", target, rec.Code, rec.Body.String())
		}
	}
	if len(f.asked) != 0 {
		t.Fatalf("a bad request reached the team module: %v", f.asked)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := post(t, h, method, "/mod/v1/team?session_id=x", "")
		if rec.Code != 405 || rec.Header().Get("Allow") != http.MethodGet {
			t.Errorf("%s: %d allow=%q, want 405 Allow: GET", method, rec.Code, rec.Header().Get("Allow"))
		}
	}
	// the events route is untouched: GET is still 405 there, POST still works
	if rec := post(t, h, http.MethodGet, "/mod/v1/events", ""); rec.Code != 405 || rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET events: %d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestTeamRead_NoReaderOrAFailingOneIsNotAnEmptyAnswer(t *testing.T) {
	// no team module: 503, never role none (the mod keeps its last good value)
	h := NewHandler(NewRegistry(time.Now))
	if rec := post(t, h, http.MethodGet, "/mod/v1/team?session_id=x", ""); rec.Code != 503 || rec.Body.String() != `{"error":"unavailable"}` {
		t.Errorf("no reader: %d %s", rec.Code, rec.Body.String())
	}
	h = teamHandler(&fakeTeam{err: errors.New("db down")})
	rec := post(t, h, http.MethodGet, "/mod/v1/team?session_id=x", "")
	if rec.Code != 500 || rec.Body.String() != `{"error":"internal"}` || strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("failing reader: %d %s (the error text must not leak)", rec.Code, rec.Body.String())
	}
}

// The same socket rules as the events route: over the real peer-uid listener it works; a foreign uid is closed before a
// byte is read. Mutation gate: serve the route outside the guarded server → n/a here, covered by the module test on TCP.
func TestTeamRead_OverTheSocketWithThePeerUIDGate(t *testing.T) {
	f := &fakeTeam{reads: map[string]TeamRead{"s1": {Role: "lead", Members: 2, TeamLabel: "L"}}}
	p := sockPath(t)
	serve(t, mustListen(t, p), teamHandler(f))
	c := unixClient(t, p)
	res, err := c.Get("http://pdx/mod/v1/team?session_id=s1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var got TeamRead
	if res.StatusCode != 200 || json.Unmarshal(b, &got) != nil || got.Role != "lead" || got.Members != 2 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}

	orig := peerUID
	peerUID = func(net.Conn) (uint32, error) { return 4242, nil } // another user
	t.Cleanup(func() { peerUID = orig })
	p2 := sockPath(t)
	serve(t, mustListen(t, p2), teamHandler(f))
	if res, err := unixClient(t, p2).Get("http://pdx/mod/v1/team?session_id=s1"); err == nil {
		res.Body.Close()
		t.Fatalf("a foreign uid was served: %d", res.StatusCode)
	}
}
