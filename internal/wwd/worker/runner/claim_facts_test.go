package runner

import (
	"context"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// claimRecorder is the fake origin that also keeps the claim requests it was sent.
type claimRecorder struct {
	*fakeOrigin
	mu   sync.Mutex
	reqs []wire.ClaimRequest
}

func (o *claimRecorder) Claim(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error) {
	o.mu.Lock()
	o.reqs = append(o.reqs, req)
	o.mu.Unlock()
	return o.fakeOrigin.Claim(ctx, id, req)
}

// A claim says what the worker is when it claims, not what it was when it started: the labels and the fingerprint come from Config.Facts at
// the time of each claim. A worker that read them once at start (before the first facts record) claimed every job with a partial
// fingerprint and no tool labels, so no result of it could be filed for reuse (spec §4.5).
func TestAClaimCarriesTheLabelsAndFingerprintOfTheMomentOfTheClaim(t *testing.T) {
	labels := []string{"os:darwin"}
	fp := map[string]string{"os_version": "27.0.1"}
	c := New(Config{Worker: "air26", Locks: locks.New(), Exec: &fakeExec{}, Unit: unit, Facts: func() ([]string, map[string]string) { return labels, fp }})
	o := &claimRecorder{fakeOrigin: &fakeOrigin{}}
	fin := func(context.Context, Ended) {}
	c.Work(context.Background(), o, scored(testJob("j1")), fin)

	labels = []string{"os:darwin", "tool:go1.26", "task:purdex/go-changed"} // the facts came in, and a label was earned
	fp = map[string]string{"os_version": "27.0.1", "go": "1.26.0"}
	c.Work(context.Background(), o, scored(testJob("j2")), fin)

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.reqs) != 2 {
		t.Fatalf("%d claims", len(o.reqs))
	}
	if len(o.reqs[0].Labels) != 1 || len(o.reqs[0].Fingerprint) != 1 {
		t.Errorf("first claim: %v %v", o.reqs[0].Labels, o.reqs[0].Fingerprint)
	}
	if len(o.reqs[1].Labels) != 3 || o.reqs[1].Fingerprint["go"] != "1.26.0" {
		t.Errorf("second claim still carries the facts of the first: %v %v", o.reqs[1].Labels, o.reqs[1].Fingerprint)
	}
}

// Without a Facts function the fixed Labels and Fingerprint are sent, as before.
func TestAClaimWithoutFactsCarriesTheFixedValues(t *testing.T) {
	c := New(Config{Worker: "air26", Locks: locks.New(), Exec: &fakeExec{}, Unit: unit, Labels: []string{"os:linux"}, Fingerprint: map[string]string{"k": "v"}})
	o := &claimRecorder{fakeOrigin: &fakeOrigin{}}
	c.Work(context.Background(), o, scored(testJob("j1")), func(context.Context, Ended) {})
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.reqs) != 1 || o.reqs[0].Labels[0] != "os:linux" || o.reqs[0].Fingerprint["k"] != "v" {
		t.Fatalf("%+v", o.reqs)
	}
}
