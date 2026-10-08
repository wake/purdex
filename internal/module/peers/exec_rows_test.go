package peers

import (
	"context"
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/execpeers"
)

// Execution rows through the module (peer mailbox spec §4.1, plan P4 step 2).

const execSessionID = "eeeeeeee-0000-4000-8000-000000000001"

type fakeExecPeers struct {
	rows    []execpeers.Row
	err     error
	mailbox bool
}

func (f *fakeExecPeers) Rows(context.Context) ([]execpeers.Row, error) { return f.rows, f.err }
func (f *fakeExecPeers) MailboxEnabled() bool                          { return f.mailbox }
func (f *fakeExecPeers) Send(context.Context, string, execpeers.PeerSend) (execpeers.PeerSendResult, error) {
	return execpeers.PeerSendResult{}, errors.New("not used in P4a")
}

func (s *sendEnv) withExecutions(f *fakeExecPeers) {
	s.m.core.Registry.Register(execpeers.RegistryKey, f)
}

func (s *sendEnv) local() ipeers.Envelope {
	return s.m.localEnvelope(context.Background(), localHostID, localAlias)
}

func execRowIn(env ipeers.Envelope, id string) (ipeers.PeerRecord, bool) {
	for _, r := range env.Peers {
		if r.ExecutionID == id {
			return r, true
		}
	}
	return ipeers.PeerRecord{}, false
}

// No nex module: nothing changes. With one, each execution is a row named
// from its cwd's basename (it has never had a registry name), listed whether
// or not its mailbox is on.
func TestLocalEnvelope_ExecutionRows(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	before := s.local()
	if before.ExecutionsUnavailable || len(before.Peers) != 1 {
		t.Fatalf("without nex: flag %v, rows %+v", before.ExecutionsUnavailable, before.Peers)
	}

	f := &fakeExecPeers{mailbox: true, rows: []execpeers.Row{{ExecutionID: "E1", SessionID: execSessionID, Cwd: "/work/My Repo", State: "idle"}}}
	s.withExecutions(f)
	r, ok := execRowIn(s.local(), "E1")
	want := vname(t, "my-repo", execSessionID)
	if !ok || r.RowKind != ipeers.RowKindExecution || r.Name != want || r.Address != localAlias+"/"+want {
		t.Fatalf("execution row = %+v (found %v), want named %q", r, ok, want)
	}
	if r.Reason != ipeers.ReasonMailboxNotWired {
		t.Errorf("reason = %q", r.Reason)
	}

	f.mailbox = false
	if r, ok := execRowIn(s.local(), "E1"); !ok || r.Reason != ipeers.ReasonMailboxDisabled || r.Name != want {
		t.Errorf("mailbox off: row = %+v (found %v)", r, ok)
	}
}

// A listing failure lists no execution and says so; an address that then
// matches nothing is not-ready (503), never peer_not_found.
func TestSend_ExecutionsUnavailableMissIsNotReady(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.withExecutions(&fakeExecPeers{err: errors.New("repeated cursor")})
	env := s.local()
	if !env.ExecutionsUnavailable || env.Partial {
		t.Errorf("executions_unavailable/partial = %v/%v, want true/false", env.ExecutionsUnavailable, env.Partial)
	}
	req := s.localSendReq()
	req.To = localAlias + "/nobody-x1"
	if rr := s.send(adminCtx(), req); rr.Code != http.StatusServiceUnavailable || decodeAPIError(t, rr).Error != ipeers.ErrNotReady {
		t.Errorf("miss = %d %s, want 503 not_ready", rr.Code, rr.Body.String())
	}
}

// P4a resolves an execution but delivers nothing to it: not_deliverable,
// with the row's reason, and nothing reaches any socket.
func TestSend_ExecutionRowIsNotDeliverableYet(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.withExecutions(&fakeExecPeers{mailbox: true, rows: []execpeers.Row{{ExecutionID: "E1", SessionID: execSessionID, Cwd: "/work/app", State: "idle"}}})
	req := s.localSendReq()
	req.To = localAlias + "/" + vname(t, "app", execSessionID)
	rr := s.send(adminCtx(), req)
	if e := decodeAPIError(t, rr); rr.Code != http.StatusConflict || e.Error != ipeers.ErrNotDeliverable || e.Detail != ipeers.ReasonMailboxNotWired {
		t.Errorf("send = %d %+v, want 409 not_deliverable %s", rr.Code, e, ipeers.ReasonMailboxNotWired)
	}
}

// A remote's sleeping execution row keeps its name and address here too.
func TestNormalizeRemoteRows_ExecutionRowKeepsItsName(t *testing.T) {
	row := ipeers.PeerRecord{RowKind: ipeers.RowKindExecution, Ref: ipeers.RefID(execSessionID), Name: "app-xx",
		Agent: &ipeers.AgentInfo{Type: "cc", SessionID: execSessionID}}
	got := normalizeRemoteRows([]ipeers.PeerRecord{row}, remoteAlias, remoteHostID, ipeers.AddressVersionV5)[0]
	if got.Name != "app-xx" || got.Address != remoteAlias+"/app-xx" {
		t.Errorf("name/address = %q/%q", got.Name, got.Address)
	}
}

// A running execution's own process folds into its execution row, and that
// row is still the origin of the process's own `pdx msg send`.
func TestSend_RunningExecutionIsAnOrigin(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	s.withExecutions(&fakeExecPeers{mailbox: true, rows: []execpeers.Row{{ExecutionID: "E1", SessionID: targetSessionID, Cwd: "/w", State: "running", PID: targetPID}}})
	env := s.local()
	if r, ok := execRowIn(env, "E1"); !ok || r.Agent.Inbox != s.targetSock || len(env.Peers) != 2 {
		t.Fatalf("rows = %+v, want the origin folded into E1", env.Peers)
	}
	if resp := s.sendOK(s.localSendReq()); resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q", resp.Result)
	}
	p.recvLine()
}
