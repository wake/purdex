package client

import (
	"context"
	"net/http"
	"testing"
)

// A wwd that is a later build answers with a field this build does not know (#3710): the reader skips it. What the contract refuses
// besides that is still refused (the *_RefusesWhatTheContractRefuses tests).
func TestAnswersWithAFieldOfALaterBuildAreRead(t *testing.T) {
	serve := func(path, body string) *Client {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		return New(fakeWWD(t, mux))
	}
	ctx := context.Background()
	if p, err := serve("/v1/pool", `{"peers": [], "extra": 1}`).Pool(ctx); err != nil || len(p.Peers) != 0 {
		t.Errorf("pool: %+v %v", p, err)
	}
	if _, _, err := serve("/v1/alerts", `{"host":"air26","alerts":[],"extra":1}`).Alerts(ctx, ""); err != nil {
		t.Errorf("alerts: %v", err)
	}
	if _, _, err := serve("/v1/exceptions", `[{"name":"x","until":"2026-10-19T09:00:00Z","pid":null,"holds":[],"zzz":1}]`).Exceptions(ctx, ""); err != nil {
		t.Errorf("exceptions: %v", err)
	}
	if _, err := serve("/v1/usage", `{"host":"air26","since":"2026-10-18T01:02:03Z","records":[],"truncated":false,"next_since":null,"next_after":null,"extra":1}`).
		Usage(ctx, UsageQuery{}); err != nil {
		t.Errorf("usage: %v", err)
	}
}
