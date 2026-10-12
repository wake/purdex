// internal/module/team/pause_verdict_test.go — PauseVerdict, the one primitive both delivery points ask (unattended quota
// pause spec §3.2, UP-1b).
package teammod

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

func bounceRows(t *testing.T, s *Store, sid string) []struct {
	From, Hash, FirstLine string
	At                    int64
} {
	t.Helper()
	rows, err := s.db.Query(`SELECT from_addr, text_hash, first_line, at FROM pause_bounces WHERE session_id = ? ORDER BY from_addr, text_hash`, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		From, Hash, FirstLine string
		At                    int64
	}
	for rows.Next() {
		var r struct {
			From, Hash, FirstLine string
			At                    int64
		}
		if err := rows.Scan(&r.From, &r.Hash, &r.FirstLine, &r.At); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// resume is what UP-3 will do, in the shape the race needs: the pause rows and the session's bounces go in one transaction.
func resume(t *testing.T, s *Store, sid string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM pause_bounces WHERE session_id = ?`, `DELETE FROM session_pauses WHERE session_id = ?`} {
		if _, err := tx.Exec(q, sid); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Not paused: ok, and nothing is written. Mutation gate: bounce without looking for the pause row → a row for a session
// that is not paused (red).
func TestPauseVerdict_NotPausedIsOKAndWritesNothing(t *testing.T) {
	s := openTestStore(t)
	paused, err := s.PauseVerdict("sid-1", "air/foo", "hello", 100)
	if err != nil || paused {
		t.Fatalf("not paused: paused=%v err=%v", paused, err)
	}
	if rows := bounceRows(t, s, "sid-1"); len(rows) != 0 {
		t.Fatalf("bounce rows for a session that is not paused: %+v", rows)
	}
}

func TestPauseVerdict_PausedBouncesAndMergesTheSameSenderAndText(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddPause(pauseRow("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	for i, at := range []int64{100, 200, 300} {
		paused, err := s.PauseVerdict("sid-1", "air/foo", "hello\nsecond line", at)
		if err != nil || !paused {
			t.Fatalf("call %d: paused=%v err=%v", i, paused, err)
		}
	}
	rows := bounceRows(t, s, "sid-1")
	if len(rows) != 1 || rows[0].From != "air/foo" || rows[0].FirstLine != "hello" || rows[0].At != 300 {
		t.Fatalf("one sender, one text, three tries = %+v, want one row (latest at, first line only)", rows)
	}
	// another text, and another sender, are other rows
	for _, c := range [][2]string{{"air/foo", "something else"}, {"air/bar", "hello\nsecond line"}} {
		if paused, err := s.PauseVerdict("sid-1", c[0], c[1], 400); err != nil || !paused {
			t.Fatal(paused, err)
		}
	}
	if rows := bounceRows(t, s, "sid-1"); len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3", rows)
	}
}

func TestPauseVerdict_FirstLineIsAtMost120Characters(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddPause(pauseRow("sid-1", team.PauseCauseLead, team.PauseRoleMember, 10)); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("字", 300)
	if paused, err := s.PauseVerdict("sid-1", "a", long, 1); err != nil || !paused {
		t.Fatal(paused, err)
	}
	rows := bounceRows(t, s, "sid-1")
	if len(rows) != 1 || []rune(rows[0].FirstLine)[0] != '字' || len([]rune(rows[0].FirstLine)) != 120 {
		t.Fatalf("first line = %d runes, want 120", len([]rune(rows[0].FirstLine)))
	}
	// a text that starts with blank lines keeps the first non-blank line
	if paused, err := s.PauseVerdict("sid-1", "b", "\n\n  real first\nmore", 2); err != nil || !paused {
		t.Fatal(paused, err)
	}
	for _, r := range bounceRows(t, s, "sid-1") {
		if r.From == "b" && r.FirstLine != "real first" {
			t.Fatalf("first line = %q", r.FirstLine)
		}
	}
}

// Delivery against the resume, in both orders: the outcome is bounced-and-listed (the bounce exists when the resume reads it)
// or delivered (the verdict found no pause), never a bounce nobody will list. Mutation gate: check the pause in one statement
// and write the bounce in another, outside a transaction → the first order leaves an orphan after the resume (red).
func TestPauseVerdict_AgainstTheResumeInBothOrders(t *testing.T) {
	s := openTestStore(t)
	// order 1: the verdict first → bounced, and the resume deletes the bounce with the pause
	if _, err := s.AddPause(pauseRow("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	if paused, err := s.PauseVerdict("sid-1", "a", "x", 1); err != nil || !paused {
		t.Fatal(paused, err)
	}
	if n := len(bounceRows(t, s, "sid-1")); n != 1 {
		t.Fatalf("bounces before the resume = %d", n)
	}
	resume(t, s, "sid-1")
	if n := len(bounceRows(t, s, "sid-1")); n != 0 {
		t.Fatalf("bounces after the resume = %d", n)
	}
	// order 2: the resume first → the verdict finds no pause and delivers
	if paused, err := s.PauseVerdict("sid-1", "a", "x", 2); err != nil || paused {
		t.Fatalf("after the resume: paused=%v err=%v", paused, err)
	}
	if n := len(bounceRows(t, s, "sid-1")); n != 0 {
		t.Fatalf("a delivery after the resume left %d bounce rows", n)
	}
}

// A flood of different texts cannot grow team.db without bound: a paused session keeps at most maxBounces rows, further new
// ones are refused all the same (the verdict stays paused) but not listed; an existing one still refreshes. Mutation gate: no cap
// → 300 rows (red); a capped insert answering "not paused" → the message would be delivered (red).
func TestPauseVerdict_AFloodOfDifferentTextsIsBounded(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddPause(pauseRow("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		paused, err := s.PauseVerdict("sid-1", "air/foo", fmt.Sprintf("flood %d", i), int64(i))
		if err != nil || !paused {
			t.Fatalf("message %d: paused=%v err=%v", i, paused, err)
		}
	}
	rows := bounceRows(t, s, "sid-1")
	if len(rows) != maxBounces {
		t.Fatalf("bounce rows = %d, want the cap %d", len(rows), maxBounces)
	}
	// one that is already listed still refreshes at the cap
	if paused, err := s.PauseVerdict("sid-1", "air/foo", "flood 0", 9999); err != nil || !paused {
		t.Fatal(paused, err)
	}
	var at int64
	if err := s.db.QueryRow(`SELECT at FROM pause_bounces WHERE session_id = 'sid-1' AND first_line = 'flood 0'`).Scan(&at); err != nil || at != 9999 {
		t.Fatalf("at = %d err=%v", at, err)
	}
	// a long sender address is cut, not stored whole
	long := strings.Repeat("x", 1000)
	if _, err := s.AddPause(pauseRow("sid-2", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	if paused, err := s.PauseVerdict("sid-2", long, "hi", 1); err != nil || !paused {
		t.Fatal(paused, err)
	}
	if rows := bounceRows(t, s, "sid-2"); len(rows) != 1 || len([]rune(rows[0].From)) != maxBounceFrom {
		t.Fatalf("from = %d runes, want %d", len([]rune(rows[0].From)), maxBounceFrom)
	}
}

// Verdicts and resumes racing: whatever the interleaving, no session ends with a bounce and no pause row.
func TestPauseVerdict_NeverLeavesAnOrphanBounce(t *testing.T) {
	s := openTestStore(t)
	const rounds = 40
	for r := 0; r < rounds; r++ {
		sid := fmt.Sprintf("sid-%d", r)
		if _, err := s.AddPause(pauseRow(sid, team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 5; j++ {
					if _, err := s.PauseVerdict(sid, fmt.Sprintf("from-%d", i), fmt.Sprintf("text-%d", j), int64(j)); err != nil {
						t.Error(err)
					}
				}
			}(i)
		}
		wg.Add(1)
		go func() { defer wg.Done(); resume(t, s, sid) }()
		wg.Wait()
		var orphans int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pause_bounces b WHERE NOT EXISTS (SELECT 1 FROM session_pauses p WHERE p.session_id = b.session_id)`).Scan(&orphans); err != nil {
			t.Fatal(err)
		}
		if orphans != 0 {
			t.Fatalf("round %d: %d bounce rows with no pause row", r, orphans)
		}
	}
}

// The module registers itself as peers' gate: what peers asks through the registry reaches the store.
func TestPauseGate_IsRegisteredForPeers(t *testing.T) {
	f := newFixture(t)
	svc, ok := f.core.Registry.Get(peersmod.PauseGateKey)
	if !ok {
		t.Fatal("the team module did not register the pause gate")
	}
	gate, ok := svc.(peersmod.PauseGate)
	if !ok {
		t.Fatalf("the registered service is %T, not a PauseGate", svc)
	}
	if paused, err := gate.PauseVerdict("sid-1", "air/foo", "hi", 1); err != nil || paused {
		t.Fatalf("not paused: %v %v", paused, err)
	}
	if _, err := f.m.store.AddPause(pauseRow("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, 10)); err != nil {
		t.Fatal(err)
	}
	if paused, err := gate.PauseVerdict("sid-1", "air/foo", "hi", 2); err != nil || !paused {
		t.Fatalf("paused: %v %v", paused, err)
	}
	if rows := bounceRows(t, f.m.store, "sid-1"); len(rows) != 1 {
		t.Fatalf("bounce rows = %+v", rows)
	}
}
