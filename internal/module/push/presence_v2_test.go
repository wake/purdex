package push

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// PA-1 (push-away-presence spec §3.1, §4.1): the v2 presence report and the "at the computer" rule.

func i64(v int64) *int64 { return &v }
func bptr(v bool) *bool  { return &v }
func iptr(v int) *int    { return &v }

// v2 is a v2 report for client; idle/awayAfter in ms.
func v2(client string, idleMs, awayAfterMs int64) push.PresenceRequest {
	r := put(client, false, 60000) // the longest ttl, so the extrapolation tests can go past 45 s
	r.V, r.IdleMs, r.Locked, r.LockedMs, r.AwayAfterMs = iptr(2), i64(idleMs), bptr(false), i64(0), i64(awayAfterMs)
	return r
}

func v2locked(client string, lockedMs int64) push.PresenceRequest {
	r := v2(client, 0, 300000)
	r.Locked, r.LockedMs = bptr(true), i64(lockedMs)
	return r
}

// The idle boundary: N-1 s at the computer, N s away. Mutation gate: `<` → `<=` → red.
func TestAtComputer_IdleBoundary(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(v2("mac-1", 299999, 300000))
	if !p.AtComputer() {
		t.Fatal("idle 299999 ms of 300000 is still at the computer")
	}
	p.Put(v2("mac-1", 300000, 300000))
	if p.AtComputer() {
		t.Fatal("idle == away_after is away")
	}
}

// The report is as of its receipt: the time since then counts as more idle. Mutation gate: ignore elapsed → red.
func TestAtComputer_ExtrapolatesAcrossTheGap(t *testing.T) {
	p, c := newClockPresence()
	p.Put(v2("mac-1", 12000, 60000))
	c.advance(47 * time.Second)
	if !p.AtComputer() {
		t.Fatal("12 s + 47 s < 60 s")
	}
	c.advance(1 * time.Second)
	if p.AtComputer() {
		t.Fatal("12 s + 48 s = 60 s is away")
	}
}

// A newer report from the same Mac replaces the older one: the user came back.
func TestAtComputer_ANewerReportWithSmallerIdleWins(t *testing.T) {
	p, c := newClockPresence()
	p.Put(v2("mac-1", 290000, 300000))
	c.advance(20 * time.Second)
	if p.AtComputer() {
		t.Fatal("290 s + 20 s is away")
	}
	p.Put(v2("mac-1", 1000, 300000))
	if !p.AtComputer() {
		t.Fatal("the user came back (idle 1 s)")
	}
}

// Locked: 59 s at the computer, 60 s away; the threshold is not the idle one. Mutation gate: `<` → `<=` → red; use idle
// while locked → red.
func TestAtComputer_LockBoundary(t *testing.T) {
	p, c := newClockPresence()
	p.Put(v2locked("mac-1", 59999))
	if !p.AtComputer() {
		t.Fatal("locked 59.999 s")
	}
	p.Put(v2locked("mac-1", 60000))
	if p.AtComputer() {
		t.Fatal("locked 60 s is away")
	}
	p.Put(v2locked("mac-1", 0))
	c.advance(59 * time.Second)
	if !p.AtComputer() {
		t.Fatal("locked 0 + 59 s")
	}
	c.advance(time.Second)
	if p.AtComputer() {
		t.Fatal("locked 0 + 60 s")
	}
}

// While locked the idle time is not consulted: a long-idle, just-locked Mac is still "at" for the first minute, and a
// fresh-idle locked Mac (a lock during activity) is away after the minute.
func TestAtComputer_LockedIgnoresIdle(t *testing.T) {
	p, _ := newClockPresence()
	r := v2locked("mac-1", 100)
	r.IdleMs = i64(10_000_000) // way past away_after
	p.Put(r)
	if !p.AtComputer() {
		t.Fatal("locked 100 ms is at the computer whatever the idle")
	}
	r = v2locked("mac-1", 90000)
	r.IdleMs = i64(0)
	p.Put(r)
	if p.AtComputer() {
		t.Fatal("locked 90 s is away whatever the idle")
	}
}

// Unlocked: locked_ms is ignored (validated but meaningless).
func TestAtComputer_UnlockedIgnoresLockedMs(t *testing.T) {
	p, _ := newClockPresence()
	r := v2("mac-1", 1000, 300000)
	r.LockedMs = i64(9_999_999)
	p.Put(r)
	if !p.AtComputer() {
		t.Fatal("an unlocked Mac with a stale locked_ms")
	}
}

// Mutation gate: drop the expiry → red.
func TestAtComputer_AnExpiredEntryIsAway(t *testing.T) {
	p, c := newClockPresence()
	r := v2("mac-1", 0, 3600000)
	r.TTLMs = 2000
	p.Put(r)
	if !p.AtComputer() {
		t.Fatal("fresh")
	}
	c.advance(2 * time.Second)
	if p.AtComputer() {
		t.Fatal("no report within the TTL means away")
	}
}

// Several Macs: any one at the computer is enough, each with its own N.
func TestAtComputer_AnyOneMacIsEnough(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(v2("mac-1", 400000, 300000)) // away by its own N
	p.Put(v2("mac-2", 400000, 600000)) // at the computer by its (longer) N
	if !p.AtComputer() {
		t.Fatal("mac-2 is at the computer")
	}
	p.Put(v2("mac-2", 700000, 600000))
	if p.AtComputer() {
		t.Fatal("both away")
	}
}

// A v1 report never says "at the computer", and a v2 report never feeds the legacy match.
func TestPresenceV2_AndLegacyDoNotCrossOver(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(put("mac-1", true, 45000, sess("c1", "dev")))
	if p.AtComputer() {
		t.Fatal("a v1 entry is not at the computer")
	}
	if !p.ShowsCode("c1") || !p.ShowsName("dev") {
		t.Fatal("the legacy match works for v1")
	}
	r := v2("mac-2", 0, 300000)
	r.Active, r.Sessions = true, []push.PresenceSession{sess("c2", "ops")}
	p.Put(r)
	if p.ShowsCode("c2") || p.ShowsName("ops") {
		t.Fatal("a v2 entry fed the legacy match") // mutation gate: drop the v1-only filter → red
	}
	if !p.AtComputer() {
		t.Fatal("the v2 entry is at the computer")
	}
}

// A client switching from v2 back to v1 (older App window) replaces its own entry.
func TestPresenceV2_TheSameClientReplacedByV1IsNotAtTheComputer(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(v2("mac-1", 0, 300000))
	p.Put(put("mac-1", true, 45000, sess("c1", "dev")))
	if p.AtComputer() {
		t.Fatal("the v1 report replaced the v2 one")
	}
}

func TestStatus_ReportsAtComputerAndTheV2Count(t *testing.T) {
	e := newEnv(t)
	st := e.mod.Status()
	if st["presence_at_computer"] != 0 || st["presence_v2_entries"] != 0 {
		t.Fatalf("status = %v", st)
	}
	b, _ := json.Marshal(v2("mac-1", 1000, 300000))
	if rec := e.do("PUT", "/api/push/presence", string(b)); rec.Code != http.StatusNoContent {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	st = e.mod.Status()
	if st["presence_at_computer"] != 1 || st["presence_v2_entries"] != 1 {
		t.Fatalf("status = %v", st)
	}
	out, _ := json.Marshal(st)
	if strings.Contains(string(out), "mac-1") {
		t.Fatalf("the status names a client: %s", out)
	}
}

// The wire (spec §3.1). Every case goes through the handler so the error contract is the real one.
func TestPresenceV2_Validation(t *testing.T) {
	base := func(extra string) string {
		return `{"client_id":"mac-1","active":true,"sessions":[],"ttl_ms":45000` + extra + `}`
	}
	full := `,"v":2,"idle_ms":12000,"locked":false,"locked_ms":0,"away_after_ms":300000`
	without := func(field string) string {
		parts := map[string]string{"idle_ms": `"idle_ms":12000`, "locked": `"locked":false`, "locked_ms": `"locked_ms":0`, "away_after_ms": `"away_after_ms":300000`}
		var b strings.Builder
		b.WriteString(`,"v":2`)
		for _, k := range []string{"idle_ms", "locked", "locked_ms", "away_after_ms"} {
			if k != field {
				b.WriteString("," + parts[k])
			}
		}
		return b.String()
	}
	// set replaces a field's value: build the string from the pieces instead of patching.
	set := func(field, val string) string {
		vals := map[string]string{"idle_ms": "12000", "locked": "false", "locked_ms": "0", "away_after_ms": "300000"}
		vals[field] = val
		return fmt.Sprintf(`,"v":2,"idle_ms":%s,"locked":%s,"locked_ms":%s,"away_after_ms":%s`, vals["idle_ms"], vals["locked"], vals["locked_ms"], vals["away_after_ms"])
	}

	type tc struct {
		name, body string
		code       int
		err        string
	}
	cases := []tc{
		{"v1 (no v)", base(""), 204, ""},
		{"v null is v1, other v2 fields ignored", base(`,"v":null,"idle_ms":"junk"`), 400, "invalid_json"}, // junk is still a type error
		{"v null with garbage-free extras", base(`,"v":null,"idle_ms":5`), 204, ""},
		{"v 2 full", base(full), 204, ""},
		{"v 1", base(`,"v":1`), 400, "v: want 2"},
		{"v 3", base(`,"v":3`), 400, "v: want 2"},
		{"v 0", base(`,"v":0`), 400, "v: want 2"},
		{"v string", base(`,"v":"2"`), 400, "invalid_json"},
		{"v fraction", base(`,"v":2.5`), 400, "invalid_json"},
		{"v bool", base(`,"v":true`), 400, "invalid_json"},
	}
	for _, f := range []string{"idle_ms", "locked", "locked_ms", "away_after_ms"} {
		cases = append(cases,
			tc{f + " omitted", base(without(f)), 400, f + ": required"},
			tc{f + " null", base(set(f, "null")), 400, f + ": required"},
			tc{f + " string", base(set(f, `"1"`)), 400, "invalid_json"},
		)
	}
	for _, f := range []string{"idle_ms", "locked_ms", "away_after_ms"} {
		cases = append(cases,
			tc{f + " fraction", base(set(f, "1.5")), 400, "invalid_json"},
			tc{f + " beyond int64", base(set(f, "9223372036854775808")), 400, "invalid_json"},
			tc{f + " negative", base(set(f, "-1")), 400, f + ": want"},
		)
	}
	cases = append(cases,
		tc{"locked number", base(set("locked", "1")), 400, "invalid_json"},
		tc{"idle_ms 0", base(set("idle_ms", "0")), 204, ""},
		tc{"idle_ms max", base(set("idle_ms", "2592000000")), 204, ""},
		tc{"idle_ms max+1", base(set("idle_ms", "2592000001")), 400, "idle_ms: want 0-2592000000"},
		tc{"locked_ms 0", base(set("locked_ms", "0")), 204, ""},
		tc{"locked_ms max", base(set("locked_ms", "2592000000")), 204, ""},
		tc{"locked_ms max+1 though unlocked", base(set("locked_ms", "2592000001")), 400, "locked_ms: want 0-2592000000"},
		tc{"away_after 59999", base(set("away_after_ms", "59999")), 400, "away_after_ms: want 60000-3600000"},
		tc{"away_after 60000", base(set("away_after_ms", "60000")), 204, ""},
		tc{"away_after 3600000", base(set("away_after_ms", "3600000")), 204, ""},
		tc{"away_after 3600001", base(set("away_after_ms", "3600001")), 400, "away_after_ms: want 60000-3600000"},
		tc{"away_after 0", base(set("away_after_ms", "0")), 400, "away_after_ms: want 60000-3600000"},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			rec := e.do("PUT", "/api/push/presence", c.body)
			if rec.Code != c.code {
				t.Fatalf("answered %d %s, want %d", rec.Code, rec.Body, c.code)
			}
			if c.err != "" && !strings.Contains(rec.Body.String(), c.err) {
				t.Fatalf("body %s lacks %q", rec.Body, c.err)
			}
			if c.code == 400 && e.mod.pres.Len() != 0 {
				t.Fatal("an invalid report was stored")
			}
		})
	}
}

// A v1 body with no v2 fields stays v1 even when a v2-looking extra field is null.
func TestPresenceV2_NullVIsAV1Report(t *testing.T) {
	e := newEnv(t)
	body := `{"client_id":"mac-1","active":true,"sessions":[{"code":"c1","name":"dev"}],"ttl_ms":45000,"v":null}`
	if rec := e.do("PUT", "/api/push/presence", body); rec.Code != 204 {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if e.mod.pres.AtComputer() || !e.mod.pres.ShowsCode("c1") {
		t.Fatal("a v null report is a v1 report")
	}
}
