package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Person tokens (RT-1a T1). Mutation gates: drop the `consumed` check in consume → the second consume is true (red); drop the
// session or command comparison → the wrong-session / wrong-command cases are true (red); drop the expiry check → the edge case
// is true (red); drop the oldest-drop in mint → the fifth mint leaves five live (red).

func (f *fixture) mintPerson(sid, cmd string) team.PersonResponse {
	f.t.Helper()
	code, body := f.do(http.MethodPost, team.PersonRoute, team.PersonRequest{SessionID: sid, Command: cmd})
	if code != http.StatusOK {
		f.t.Fatalf("mint %s/%s: %d %s", sid, cmd, code, body)
	}
	var out team.PersonResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestPerson_AMintedTokenIsUsedOnce(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	if got := f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token); !got {
		t.Fatal("the first consume of a fresh token is false")
	}
	if got := f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token); got {
		t.Fatal("a spent token was consumed twice")
	}
}

func TestPerson_ATokenIsBoundToItsSessionAndCommand(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	if f.m.consumePerson("sid-2", team.PersonCmdRelay, tok.Token) {
		t.Fatal("another session consumed the token")
	}
	if f.m.consumePerson("sid-1", team.PersonCmdLead, tok.Token) {
		t.Fatal("another command consumed the token")
	}
	if f.m.consumePerson("sid-1", team.PersonCmdRelay, "") || f.m.consumePerson("sid-1", team.PersonCmdRelay, "0000") {
		t.Fatal("an empty or unknown token was consumed")
	}
	// the failed attempts spent nothing
	if !f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the right session and command lost the token to wrong attempts")
	}
}

func TestPerson_TheLifetimesAreSixtySecondsAndTenMinutes(t *testing.T) {
	for _, c := range []struct {
		cmd string
		ttl int64
	}{{team.PersonCmdRelay, 60}, {team.PersonCmdAdopt, 60}, {team.PersonCmdLead, 600}, {team.PersonCmdSpawn, 600}} {
		f := newFixture(t)
		tok := f.mintPerson("sid-1", c.cmd)
		if want := f.clock.Load() + c.ttl*1000; tok.ExpiresAt != want {
			t.Fatalf("%s expires at %d, want %d", c.cmd, tok.ExpiresAt, want)
		}
		f.clock.Store(tok.ExpiresAt - 1)
		if !f.m.consumePerson("sid-1", c.cmd, tok.Token) {
			t.Fatalf("%s: one ms before expiry the token is dead", c.cmd)
		}
		tok2 := f.mintPerson("sid-1", c.cmd)
		f.clock.Store(tok2.ExpiresAt)
		if f.m.consumePerson("sid-1", c.cmd, tok2.Token) {
			t.Fatalf("%s: at its expiry the token still works", c.cmd)
		}
	}
}

func TestPerson_ConcurrentConsumersHaveOneWinner(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners, want 1", wins.Load())
	}
}

func TestPerson_TheFifthMintDropsTheOldest(t *testing.T) {
	f := newFixture(t)
	var toks []team.PersonResponse
	for i := 0; i < team.PersonMaxLive+1; i++ {
		f.clock.Add(1)
		toks = append(toks, f.mintPerson("sid-1", team.PersonCmdRelay))
	}
	if f.m.consumePerson("sid-1", team.PersonCmdRelay, toks[0].Token) {
		t.Fatal("the oldest token survived the fifth mint")
	}
	for _, tk := range toks[1:] {
		if !f.m.consumePerson("sid-1", team.PersonCmdRelay, tk.Token) {
			t.Fatal("a newer token was dropped")
		}
	}
}

func TestPerson_MintRefusals(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do(http.MethodPost, team.PersonRoute, team.PersonRequest{SessionID: "sid-1", Command: "bogus"}); code != http.StatusBadRequest {
		t.Fatalf("unknown command: %d, want 400", code)
	}
	if code, _ := f.do(http.MethodPost, team.PersonRoute, team.PersonRequest{SessionID: "", Command: "relay"}); code != http.StatusBadRequest {
		t.Fatalf("no session: %d, want 400", code)
	}
	code, body := f.do(http.MethodPost, team.PersonRoute, team.PersonRequest{SessionID: "no-such", Command: "relay"})
	if e := decodeErr(t, body); code != http.StatusNotFound || e.Error != team.ErrUnknownSession {
		t.Fatalf("unknown session: %d %s, want 404 %s", code, body, team.ErrUnknownSession)
	}
}

func TestPerson_TheDaemonLogNeverHoldsAToken(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var log strings.Builder
	f.m.logf = func(format string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		log.WriteString(strings.TrimSpace(format))
		for _, x := range a {
			if s, ok := x.(string); ok {
				log.WriteString(" " + s)
			}
		}
	}
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token)
	f.m.consumePerson("sid-1", team.PersonCmdRelay, tok.Token)
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(log.String(), tok.Token) {
		t.Fatal("the token is in the daemon log")
	}
}
