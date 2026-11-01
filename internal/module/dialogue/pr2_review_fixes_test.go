package dialogue

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// A recap written while later summaries completed keeps their count: since_recap is the done summaries past the recap's to_no.
func TestInsertRecap_KeepsTheUncoveredSummaryCount(t *testing.T) {
	s := storeWithSession(t)
	finish := func(no int) {
		id := mustInsertSummary(t, s, pendingRow("s1", fmt.Sprintf("t%d", no), no, int64(1000+no)))
		if ok, err := s.SetSummaryDone(id, "標題", "推播", "內文。", false, usage{}); err != nil || !ok {
			t.Fatalf("done %d: %v %v", no, ok, err)
		}
	}
	for no := 1; no <= 30; no++ {
		finish(no)
	}
	finish(31) // done while the recap is still running
	if n, _ := s.SinceRecap("s1"); n != 31 {
		t.Fatalf("since_recap = %d, want 31", n)
	}
	if _, ok, err := s.InsertRecap(RecapRow{JobID: "j1", SessionID: "s1", FromNo: 1, ToNo: 30, Trigger: "every30", Headline: "h", BodyJSON: "{}"}); err != nil || !ok {
		t.Fatalf("InsertRecap: %v %v", ok, err)
	}
	if n, _ := s.SinceRecap("s1"); n != 1 {
		t.Fatalf("since_recap after the recap = %d, want 1", n)
	}
	for no := 32; no <= 59; no++ {
		finish(no)
	}
	if n, _ := s.SinceRecap("s1"); n != 29 {
		t.Fatalf("since_recap at 59 = %d, want 29", n)
	}
	finish(60)
	if n, _ := s.SinceRecap("s1"); n != recapEvery {
		t.Fatalf("since_recap at 60 = %d, want %d", n, recapEvery)
	}
}

// A rebuild that finishes between SetSummaryDone and after has put the blocks in already: after's append adds nothing.
func TestAppendMarker_SkipsABlockTheRebuildPlaced(t *testing.T) {
	const sum = "<turn-summary 1 · 09:05 | 俳句>\n寫了兩首俳句。\n"
	const rec = "<recap 1-1 · 09:06 | 回顧>\n內容\n"
	e := newEnv(t, Hooks{Placed: func(sid, turnID string) []string {
		if turnID == "t1" {
			return []string{sum, rec}
		}
		return nil
	}})
	turns := markerTurns()
	e.write(sidS, fromRes(turns[:1], ""), "", false)
	if err := os.Remove(e.seg(sidS, 1)); err != nil { // the next Write rebuilds, and Placed puts the blocks back
		t.Fatal(err)
	}
	e.write(sidS, fromRes(turns, ""), "", false)
	if err := e.w.AppendMarker(sidS, 1, sum); err != nil {
		t.Fatal(err)
	}
	if err := e.w.AppendMarker(sidS, 1, rec); err != nil {
		t.Fatal(err)
	}
	got := e.read(sidS, 1)
	for _, b := range []string{sum, rec} {
		if n := strings.Count(got, strings.SplitN(b, "\n", 2)[0]); n != 1 {
			t.Fatalf("%q is in the file %d times: %q", b, n, got)
		}
	}
	st, _, _ := e.store.Session(sidS)
	if fi, _ := os.Stat(e.seg(sidS, 1)); fi.Size() != st.SegBytes {
		t.Fatalf("size %d, recorded %d", fi.Size(), st.SegBytes)
	}
}

func TestAPIRecapBody(t *testing.T) {
	a := newAPIEnv(t)
	for _, c := range []struct {
		name, body string
		want       int
	}{
		{"no body", "", 200},
		{"manual", `{"trigger":"manual"}`, 200},
		{"bad trigger", `{"trigger":"every30"}`, 400},
		{"empty object", `{}`, 400},
		{"not json", `nope`, 400},
	} {
		code, out := a.do("POST", "/api/dialogue/"+sidS+"/recap", c.body)
		if code != c.want {
			t.Fatalf("%s: %d %v, want %d", c.name, code, out, c.want)
		}
		if c.name == "bad trigger" && out["error"] != "bad_trigger" {
			t.Fatalf("%s: %v", c.name, out)
		}
	}
}

func TestRepairSummary_EllipsisStaysWithin150(t *testing.T) {
	in := strings.Repeat("abcdefghi ", 30) // 300 runes, no sentence end
	got := repairSummary(summaryOut{Summary: in}).Summary
	if utf8.RuneCountInString(got) > maxSummary || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d: %q", utf8.RuneCountInString(got), got)
	}
	in = strings.Repeat("x", 300) // one long run
	got = repairSummary(summaryOut{Summary: in}).Summary
	if utf8.RuneCountInString(got) > maxSummary || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d: %q", utf8.RuneCountInString(got), got)
	}
}
