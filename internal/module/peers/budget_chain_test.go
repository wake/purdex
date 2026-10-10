package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

// #1009: the constants the daemon actually runs on are the ones the budget chain is derived from.
func TestBudgetChain_TheRunningTimeoutsAreTheDerivedOnes(t *testing.T) {
	if got := newDeliverClient().Timeout; got != ipeers.InterDaemonTimeout {
		t.Errorf("deliver client timeout = %v, want InterDaemonTimeout %v", got, ipeers.InterDaemonTimeout)
	}
	if got := newRemoteClient().Timeout; got != ipeers.RemoteFetchTimeout {
		t.Errorf("remote fetch client timeout = %v, want %v", got, ipeers.RemoteFetchTimeout)
	}
	if remoteFetchTimeout != ipeers.RemoteFetchTimeout {
		t.Errorf("remoteFetchTimeout = %v, want %v", remoteFetchTimeout, ipeers.RemoteFetchTimeout)
	}
	if HelperReadyTimeout != ipeers.HelperReadyTimeout {
		t.Errorf("HelperReadyTimeout = %v, want %v", HelperReadyTimeout, ipeers.HelperReadyTimeout)
	}
	m := New(nil, nil)
	if m.budget != ipeers.InventoryBudget {
		t.Errorf("inventory budget = %v, want %v", m.budget, ipeers.InventoryBudget)
	}
	if m.sockWriteTimeout != ipeers.SocketWriteTimeout {
		t.Errorf("socket write timeout = %v, want %v", m.sockWriteTimeout, ipeers.SocketWriteTimeout)
	}
}

// A receiver that finishes at its worst case — inventory, helper start and socket write all used up — still gets its answer
// through: the sender's call (real client, real constants) waits longer. Slow by design (the worst case is 10 s): it runs in
// parallel with the rest and is skipped under -short. Mutation gate: InterDaemonTimeout back to the receiver's worst case -> red.
func TestPostDeliver_AReceiverAtItsWorstCaseStillAnswersInTime(t *testing.T) {
	if testing.Short() {
		t.Skip("takes the receiver's whole worst case")
	}
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(ipeers.ReceiverWorstCase)
		_ = json.NewEncoder(w).Encode(ipeers.DeliverResponse{MsgID: "m", Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), ipeers.InterDaemonTimeout) // what handleSend does
	defer cancel()
	resp, remote, err := postDeliver(ctx, newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
	if err != nil || remote != nil || resp.Result != ipeers.ResultDelivered {
		t.Fatalf("postDeliver = %+v remote %+v err %v, want the answer of a receiver that finished in its worst case", resp, remote, err)
	}
}

// A call that gave up AFTER the whole request was written may have been delivered: it says so, and the caller does not resend. A
// call that never got the request out is a plain failure.
func TestPostDeliver_TimeoutAfterTheRequestWentOutIsUncertain(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	client := newDeliverClient()
	client.Timeout = 100 * time.Millisecond
	_, remote, err := postDeliver(context.Background(), client, srv.URL, "tok", sampleDeliverRequest())
	var unc *DeliveryUncertainError
	if remote != nil || !errors.As(err, &unc) {
		t.Fatalf("postDeliver = remote %+v err %v (%T), want a DeliveryUncertainError", remote, err, err)
	}
}

func TestPostDeliver_ARequestThatNeverWentOutIsAPlainFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	ln.Close() // nothing listens: connection refused
	_, remote, perr := postDeliver(context.Background(), newDeliverClient(), url, "tok", sampleDeliverRequest())
	var unc *DeliveryUncertainError
	if remote != nil || perr == nil || errors.As(perr, &unc) {
		t.Fatalf("postDeliver = remote %+v err %v, want a plain failure", remote, perr)
	}
}

// The send handler turns it into the answer the contract promises — 200, result delivery_uncertain — and the audit row says
// so, instead of a remote_error that an agent would retry. Mutation gate: treat it as an ordinary error -> red.
func TestSend_ATimeoutAfterTheRequestWentOutIsDeliveryUncertain(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) { s.postErr = &DeliveryUncertainError{Err: errors.New("context deadline exceeded")} })
	resp := s.sendOK(s.sendReq())
	if resp.Result != ipeers.ResultDeliveryUncertain {
		t.Fatalf("result = %q, want delivery_uncertain", resp.Result)
	}
	if resp.MsgID == "" || resp.ToAddress == "" {
		t.Errorf("response = %+v", resp)
	}
	rows := s.rows()
	if len(rows) != 1 || rows[0].Result != ipeers.ResultDeliveryUncertain {
		t.Errorf("rows = %+v, want one audit row with result delivery_uncertain", rows)
	}
}
