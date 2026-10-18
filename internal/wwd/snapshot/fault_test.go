package snapshot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The delay hook runs before the request and its error ends the fetch with no request sent; unset, it is not there at all.
func TestClientDelayHook(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "t")
	defer c.Close()
	c.sleep = func(context.Context, time.Duration) error { return nil }
	boom := errors.New("delay ended")
	c.Delay = func(context.Context) error { return boom }
	if _, err := c.Tar(context.Background(), "j_01JC0000000000000000000000", "c_x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a request went out after the delay failed (%d)", hits.Load())
	}
	ran := false
	c.Delay = func(context.Context) error { ran = true; return nil }
	_, _ = c.Tar(context.Background(), "j_01JC0000000000000000000000", "c_x")
	if !ran || hits.Load() == 0 {
		t.Fatalf("delay ran %v, requests %d", ran, hits.Load())
	}
}
