package dialogue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apiEnv is a running module (real store, real channel) behind its own mux.
type apiEnv struct {
	t   *testing.T
	m   *Module
	mux *http.ServeMux
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	m, _, _, _ := newModuleEnv(t)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return &apiEnv{t: t, m: m, mux: mux}
}

func (a *apiEnv) do(method, path, body string) (int, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		a.t.Fatalf("%s %s: body %q is not JSON: %v", method, path, rec.Body.String(), err)
	}
	return rec.Code, out
}

func (a *apiEnv) setCapable(v bool) { a.m.jobs.d.capable = func(string) bool { return v } }

func (a *apiEnv) seedSummaries(n int) {
	a.t.Helper()
	for i := 1; i <= n; i++ {
		id, _, err := a.m.store.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: fmt.Sprintf("t%d", i), TurnNo: i, TurnAt: int64(1_800_000_000_000 + i*60_000)})
		if err != nil {
			a.t.Fatal(err)
		}
		if _, err := a.m.store.SetSummaryDone(id, fmt.Sprintf("題%d", i), fmt.Sprintf("推%d", i), fmt.Sprintf("內容%d。", i), false, usage{}); err != nil {
			a.t.Fatal(err)
		}
	}
}

func (a *apiEnv) seedRecap(job string, from, to int, body string) {
	a.t.Helper()
	if _, _, err := a.m.store.InsertRecap(RecapRow{JobID: job, SessionID: sidS, ConvKey: sidS, FromNo: from, ToNo: to, Trigger: "open", Headline: "H" + job, BodyJSON: body}); err != nil {
		a.t.Fatal(err)
	}
}

const fullBody = `{"headline":"H","done":["a","b"],"now":"現在","decided":["d"],"waiting":["w"],"next":"下一步"}`
const emptyListsBody = `{"headline":"H","done":[],"now":"","decided":[],"waiting":[],"next":""}`

func list(v any) []any { l, _ := v.([]any); return l }

func TestAPIRecapsEmpty(t *testing.T) {
	a := newAPIEnv(t)
	code, out := a.do("GET", "/api/dialogue/"+sidS+"/recaps", "")
	if code != 200 {
		t.Fatalf("code %d %v", code, out)
	}
	if l, ok := out["recaps"].([]any); !ok || len(l) != 0 {
		t.Fatalf("recaps = %#v, want []", out["recaps"])
	}
	if out["can_generate"] != false || out["newest_turn_no"] != float64(0) || out["last_recap_to_no"] != float64(0) {
		t.Fatalf("extras = %v", out)
	}
}

func TestAPIRecapsNormalAndExtras(t *testing.T) {
	a := newAPIEnv(t)
	a.seedSummaries(5)
	a.seedRecap("j1", 1, 3, fullBody)
	a.seedRecap("j2", 1, 4, emptyListsBody)
	a.setCapable(true)
	code, out := a.do("GET", "/api/dialogue/"+sidS+"/recaps", "")
	if code != 200 {
		t.Fatalf("code %d %v", code, out)
	}
	rs := list(out["recaps"])
	if len(rs) != 2 {
		t.Fatalf("recaps = %v", rs)
	}
	first := rs[0].(map[string]any) // newest first
	if first["headline"] != "Hj2" || first["to_no"] != float64(4) || first["trigger"] != "open" {
		t.Fatalf("first = %v", first)
	}
	for _, k := range []string{"done", "decided", "waiting"} {
		if l, ok := first[k].([]any); !ok || len(l) != 0 {
			t.Fatalf("%s = %#v, want []", k, first[k])
		}
	}
	second := rs[1].(map[string]any)
	if len(list(second["done"])) != 2 || second["now"] != "現在" || second["next"] != "下一步" || second["id"] == nil || second["at"] == nil || second["from_no"] != float64(1) {
		t.Fatalf("second = %v", second)
	}
	if out["can_generate"] != true || out["newest_turn_no"] != float64(5) || out["last_recap_to_no"] != float64(4) {
		t.Fatalf("extras = %v", out)
	}
	a.setCapable(false)
	_, out = a.do("GET", "/api/dialogue/"+sidS+"/recaps", "")
	if out["can_generate"] != false {
		t.Fatalf("can_generate = %v", out["can_generate"])
	}
}

func TestAPIRecapsLimit(t *testing.T) {
	a := newAPIEnv(t)
	for i := 1; i <= 120; i++ {
		a.seedRecap(fmt.Sprintf("j%d", i), 1, i, emptyListsBody)
	}
	_, out := a.do("GET", "/api/dialogue/"+sidS+"/recaps", "")
	if n := len(list(out["recaps"])); n != 20 {
		t.Fatalf("default limit gave %d", n)
	}
	_, out = a.do("GET", "/api/dialogue/"+sidS+"/recaps?limit=5", "")
	if n := len(list(out["recaps"])); n != 5 {
		t.Fatalf("limit=5 gave %d", n)
	}
	_, out = a.do("GET", "/api/dialogue/"+sidS+"/recaps?limit=5000", "")
	if n := len(list(out["recaps"])); n != 100 {
		t.Fatalf("over the cap gave %d, want 100", n)
	}
	for _, bad := range []string{"abc", "0", "-3"} {
		code, out := a.do("GET", "/api/dialogue/"+sidS+"/recaps?limit="+bad, "")
		if code != 400 || out["error"] != "bad_limit" {
			t.Fatalf("limit=%s: %d %v", bad, code, out)
		}
	}
}

func tsNos(out map[string]any) []int {
	var nos []int
	for _, x := range list(out["turn_summaries"]) {
		nos = append(nos, int(x.(map[string]any)["turn_no"].(float64)))
	}
	return nos
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAPITurnSummariesEmpty(t *testing.T) {
	a := newAPIEnv(t)
	code, out := a.do("GET", "/api/dialogue/"+sidS+"/turn-summaries", "")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if l, ok := out["turn_summaries"].([]any); !ok || len(l) != 0 {
		t.Fatalf("turn_summaries = %#v, want []", out["turn_summaries"])
	}
}

func TestAPITurnSummariesSelection(t *testing.T) {
	a := newAPIEnv(t)
	a.seedSummaries(10)
	p := "/api/dialogue/" + sidS + "/turn-summaries"
	cases := []struct {
		q    string
		want []int
	}{
		{"?from=8", []int{8, 9, 10}},
		{"?to=3", []int{1, 2, 3}},
		{"?from=3&to=5", []int{3, 4, 5}},
		{"?limit=2", []int{9, 10}},
		{"", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	}
	for _, c := range cases {
		code, out := a.do("GET", p+c.q, "")
		if code != 200 || !eqInts(tsNos(out), c.want) {
			t.Fatalf("%q: %d %v want %v", c.q, code, tsNos(out), c.want)
		}
	}
	_, out := a.do("GET", p+"?limit=1", "")
	x := list(out["turn_summaries"])[0].(map[string]any)
	if x["title"] != "題10" || x["push"] != "推10" || x["summary"] != "內容10。" || x["at"] == nil || x["turn_no"] != float64(10) {
		t.Fatalf("shape = %v", x)
	}
}

func TestAPITurnSummariesCapAndErrors(t *testing.T) {
	a := newAPIEnv(t)
	a.seedSummaries(130)
	p := "/api/dialogue/" + sidS + "/turn-summaries"
	_, out := a.do("GET", p+"?limit=9999", "")
	if n := len(list(out["turn_summaries"])); n != 100 {
		t.Fatalf("over the cap gave %d", n)
	}
	_, out = a.do("GET", p+"?from=1", "")
	if n := len(list(out["turn_summaries"])); n != 100 {
		t.Fatalf("from-only gave %d, want 100 (the newest of the range)", n)
	}
	for _, q := range []string{"?from=x", "?to=x", "?limit=x", "?limit=0", "?from=5&to=2", "?from=-1"} {
		code, out := a.do("GET", p+q, "")
		if code != 400 {
			t.Fatalf("%q: %d %v", q, code, out)
		}
	}
}

func TestAPIBadSessionAndUnavailable(t *testing.T) {
	a := newAPIEnv(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/dialogue/not-a-uuid/recaps"},
		{"GET", "/api/dialogue/not-a-uuid/turn-summaries"},
		{"POST", "/api/dialogue/not-a-uuid/recap"},
		{"GET", "/api/dialogue/..%2F..%2Fx/recaps"},
	} {
		code, out := a.do(c.method, c.path, `{"trigger":"open"}`)
		if code != 400 || out["error"] != "bad_session" {
			t.Fatalf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}
	// stopped module: store gone
	a.m.Stop(context.Background())
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/dialogue/" + sidS + "/recaps"},
		{"GET", "/api/dialogue/" + sidS + "/turn-summaries"},
		{"POST", "/api/dialogue/" + sidS + "/recap"},
	} {
		code, out := a.do(c.method, c.path, `{"trigger":"open"}`)
		if code != 503 || out["error"] != "unavailable" {
			t.Fatalf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}
}

func TestAPIPostRecapTriggerAndStates(t *testing.T) {
	a := newAPIEnv(t)
	p := "/api/dialogue/" + sidS + "/recap"
	for _, body := range []string{``, `{}`, `{"trigger":"every30"}`, `{"trigger":"x"}`, `not json`} {
		code, out := a.do("POST", p, body)
		if code != 400 || out["error"] != "bad_trigger" {
			t.Fatalf("body %q: %d %v", body, code, out)
		}
	}
	// nothing summarised yet: up_to_date
	a.setCapable(true)
	code, out := a.do("POST", p, `{"trigger":"open"}`)
	if code != 200 || out["state"] != "up_to_date" {
		t.Fatalf("empty: %d %v", code, out)
	}
	a.seedSummaries(3)
	// not capable: unavailable
	a.setCapable(false)
	code, out = a.do("POST", p, `{"trigger":"manual"}`)
	if code != 200 || out["state"] != "unavailable" {
		t.Fatalf("incapable: %d %v", code, out)
	}
	// capable: queued (and a second ask still says queued)
	a.setCapable(true)
	code, out = a.do("POST", p, `{"trigger":"manual"}`)
	if code != 200 || out["state"] != "queued" {
		t.Fatalf("capable: %d %v", code, out)
	}
	code, out = a.do("POST", p, `{"trigger":"open"}`)
	if code != 200 || out["state"] != "queued" {
		t.Fatalf("again: %d %v", code, out)
	}
}
