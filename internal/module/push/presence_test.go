package push

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// PU-3 Task 2: the Mac presence reports (spec §5.4), in memory, bounded, expiring.

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newClockPresence() (*Presence, *fakeClock) {
	c := &fakeClock{now: time.Unix(1_000_000, 0)}
	return NewPresence(c.Now), c
}

func put(client string, active bool, ttlMs int, sessions ...push.PresenceSession) push.PresenceRequest {
	return push.PresenceRequest{ClientID: client, Active: active, Sessions: sessions, TTLMs: ttlMs}
}

func sess(code, name string) push.PresenceSession {
	return push.PresenceSession{Code: code, Name: name}
}

func TestPresence_ShowsByCodeAndByName(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(put("mac-1", true, 45000, sess("c1", "dev"), sess("c2", "ops")))
	if !p.ShowsCode("c1") || !p.ShowsCode("c2") || !p.ShowsName("dev") || !p.ShowsName("ops") {
		t.Fatal("a present Mac does not show its sessions")
	}
	if p.ShowsCode("c3") || p.ShowsName("other") {
		t.Fatal("it shows a session it did not report")
	}
	if p.ShowsName("") || p.ShowsCode("") {
		t.Fatal("the empty name / code is never shown")
	}
}

// Mutation gate: drop the expiry check → red.
func TestPresence_ExpiresAfterItsTTL(t *testing.T) {
	p, c := newClockPresence()
	p.Put(put("mac-1", true, 2000, sess("c1", "dev")))
	c.advance(1999 * time.Millisecond)
	if !p.ShowsCode("c1") {
		t.Fatal("expired early")
	}
	c.advance(2 * time.Millisecond)
	if p.ShowsCode("c1") || p.ShowsName("dev") {
		t.Fatal("still shown after its ttl")
	}
}

// Mutation gate: ignore `active` → red.
func TestPresence_AnInactiveWindowDoesNotCount(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(put("mac-1", false, 45000, sess("c1", "dev")))
	if p.ShowsCode("c1") || p.ShowsName("dev") {
		t.Fatal("an inactive window counted as present")
	}
}

// One window's second report replaces its first; another window's report does not.
func TestPresence_ASecondPutReplacesTheFirstOfTheSameClient(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(put("mac-1", true, 45000, sess("c1", "dev")))
	p.Put(put("mac-2", true, 45000, sess("c9", "nine")))
	p.Put(put("mac-1", true, 45000, sess("c2", "ops")))
	if p.ShowsCode("c1") || !p.ShowsCode("c2") {
		t.Fatal("mac-1's second report did not replace its first")
	}
	if !p.ShowsCode("c9") {
		t.Fatal("mac-1's report removed mac-2's")
	}
	p.Put(put("mac-1", false, 45000)) // the window went idle: a report with active=false
	if p.ShowsCode("c2") {
		t.Fatal("an inactive report left the old sessions shown")
	}
}

// 64 live entries; the 65th client replaces the one that expires soonest. Mutation gate: no cap → red; replace the
// newest instead of the soonest-expiring → red.
func TestPresence_The65thClientReplacesTheSoonestExpiring(t *testing.T) {
	p, _ := newClockPresence()
	for i := 0; i < maxPresenceEntries; i++ {
		ttl := 30000 + i*100 // client 0 expires soonest
		p.Put(put(fmt.Sprintf("mac-%d", i), true, ttl, sess(fmt.Sprintf("code-%d", i), "n")))
	}
	if p.Len() != maxPresenceEntries {
		t.Fatalf("entries = %d", p.Len())
	}
	p.Put(put("mac-new", true, 60000, sess("code-new", "n")))
	if p.Len() != maxPresenceEntries {
		t.Fatalf("entries = %d after the 65th, want %d", p.Len(), maxPresenceEntries)
	}
	if p.ShowsCode("code-0") {
		t.Fatal("the soonest-expiring entry was kept")
	}
	if !p.ShowsCode("code-new") || !p.ShowsCode("code-1") || !p.ShowsCode("code-63") {
		t.Fatal("a later-expiring entry was dropped instead")
	}
	// A known client reporting again at the cap replaces itself and evicts nobody.
	p.Put(put("mac-5", true, 45000, sess("code-5b", "n")))
	if p.Len() != maxPresenceEntries || !p.ShowsCode("code-1") {
		t.Fatal("an update of a known client evicted another")
	}
}

// Expired entries are dropped on every PUT, so they do not hold the cap. Mutation gate: no sweep on PUT → red.
func TestPresence_ExpiredEntriesAreDroppedOnEveryPut(t *testing.T) {
	p, c := newClockPresence()
	for i := 0; i < maxPresenceEntries; i++ {
		p.Put(put(fmt.Sprintf("mac-%d", i), true, 1000, sess(fmt.Sprintf("code-%d", i), "n")))
	}
	c.advance(5 * time.Second)
	p.Put(put("fresh", true, 45000, sess("c-fresh", "n")))
	if p.Len() != 1 {
		t.Fatalf("entries = %d, want only the fresh one", p.Len())
	}
}

func presenceBody(mut func(*push.PresenceRequest)) string {
	r := put("mac-1", true, 45000, sess("c1", "dev"))
	if mut != nil {
		mut(&r)
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func TestPresenceRoute_AcceptsAValidReportWith204AndStoresIt(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("PUT", "/api/push/presence", presenceBody(nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("answered %d %s", rec.Code, rec.Body.String())
	}
	if !e.mod.pres.ShowsCode("c1") || !e.mod.pres.ShowsName("dev") {
		t.Fatal("the report was not stored")
	}
}

// Mutation gate: a limit above 16 KiB → red.
func TestPresenceRoute_BodyOver16KiBIs413(t *testing.T) {
	e := newEnv(t)
	big := presenceBody(func(r *push.PresenceRequest) { r.ClientID = strings.Repeat("a", 17<<10) })
	if rec := e.do("PUT", "/api/push/presence", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("answered %d", rec.Code)
	}
	if e.mod.pres.Len() != 0 {
		t.Fatal("an oversize body was stored")
	}
}

func TestPresenceRoute_Validation(t *testing.T) {
	many := make([]push.PresenceSession, 201)
	for i := range many {
		many[i] = sess(fmt.Sprintf("c%d", i), "n")
	}
	cases := map[string]func(*push.PresenceRequest){
		"empty client id":      func(r *push.PresenceRequest) { r.ClientID = "" },
		"client id too long":   func(r *push.PresenceRequest) { r.ClientID = strings.Repeat("a", 65) },
		"control in client id": func(r *push.PresenceRequest) { r.ClientID = "a\x00b" },
		"ttl too small":        func(r *push.PresenceRequest) { r.TTLMs = 999 },
		"ttl too large":        func(r *push.PresenceRequest) { r.TTLMs = 60001 },
		"201 sessions":         func(r *push.PresenceRequest) { r.Sessions = many },
		"empty session code":   func(r *push.PresenceRequest) { r.Sessions = []push.PresenceSession{sess("", "n")} },
		"long session code":    func(r *push.PresenceRequest) { r.Sessions = []push.PresenceSession{sess(strings.Repeat("c", 65), "n")} },
		"long session name":    func(r *push.PresenceRequest) { r.Sessions = []push.PresenceSession{sess("c", strings.Repeat("n", 65))} },
		"control in name":      func(r *push.PresenceRequest) { r.Sessions = []push.PresenceSession{sess("c", "a\nb")} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if rec := e.do("PUT", "/api/push/presence", presenceBody(mut)); rec.Code != http.StatusBadRequest {
				t.Fatalf("answered %d, want 400", rec.Code)
			}
			if e.mod.pres.Len() != 0 {
				t.Fatal("an invalid report was stored")
			}
		})
	}
	e := newEnv(t)
	if rec := e.do("PUT", "/api/push/presence", `{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json answered %d", rec.Code)
	}
	if rec := e.do("PUT", "/api/push/presence", strings.Replace(presenceBody(nil), "mac-1", "mac-\xff", 1)); rec.Code != http.StatusBadRequest { // json.Marshal would have repaired the byte
		t.Fatalf("invalid utf-8 answered %d", rec.Code)
	}
	// 200 sessions and the boundary ttls are fine.
	ok200 := make([]push.PresenceSession, 200)
	for i := range ok200 {
		ok200[i] = sess(fmt.Sprintf("c%d", i), "n")
	}
	for _, mut := range []func(*push.PresenceRequest){
		func(r *push.PresenceRequest) { r.Sessions = ok200 },
		func(r *push.PresenceRequest) { r.TTLMs = 1000 },
		func(r *push.PresenceRequest) { r.TTLMs = 60000 },
	} {
		if rec := e.do("PUT", "/api/push/presence", presenceBody(mut)); rec.Code != http.StatusNoContent {
			t.Fatalf("a boundary value answered %d %s", rec.Code, rec.Body.String())
		}
	}
}

func httptestDo(mux *http.ServeMux, method, path, body string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// A soft-failed module (no key) serves no presence route either, as with every push route.
func TestPresenceRoute_AbsentWhenPushIsOff(t *testing.T) {
	m := New() // never Init'ed: not ready
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptestDo(mux, "PUT", "/api/push/presence", presenceBody(nil))
	if rec != http.StatusNotFound && rec != http.StatusMethodNotAllowed {
		t.Fatalf("answered %d, want the route absent", rec)
	}
}
