package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/store"
)

// ---------------------------------------------------------------------------
// Scaffolding: the receiving daemon (deliver_test.go's env) with the
// origin's helper already acquired. The env's "target" cc session — the
// registry entry bound to a live Unix listener — is the REPLIER here: it
// writes a native reply into the helper's socket, and the daemon forwards
// it to the origin host "air" through the return route.
// ---------------------------------------------------------------------------

const nativeMsgID = "7c1a9d2e-0f3b-4c5d-8e6f-a1b2c3d4e5f6"

type replyEnv struct {
	*deliverEnv
	h *helper // the origin's helper (air/foo), acquired from the manager

	mu      sync.Mutex
	posts   []postCall
	started chan postCall // every post call, as it begins
	resp    ipeers.DeliverResponse
	rem     *ipeers.RemoteError
	err     error
	hold    func(ctx context.Context) // nil ⇒ answer at once; else runs before answering
	ctxErrs []error                   // ctx.Err() as each post returned
}

func newReplyEnv(t *testing.T, o envOpts) *replyEnv {
	t.Helper()
	r := &replyEnv{
		deliverEnv: newDeliverEnv(t, o),
		started:    make(chan postCall, 32),
		resp:       ipeers.DeliverResponse{Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting},
	}
	r.m.post = func(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
		call := postCall{baseURL, bearer, req}
		r.mu.Lock()
		r.posts = append(r.posts, call)
		hold, resp, rem, err := r.hold, r.resp, r.rem, r.err
		r.mu.Unlock()
		r.started <- call
		if hold != nil {
			hold(ctx)
		}
		r.mu.Lock()
		r.ctxErrs = append(r.ctxErrs, ctx.Err())
		r.mu.Unlock()
		if client != r.m.deliverClient {
			t.Errorf("post used client %p, want the module's deliverClient %p", client, r.m.deliverClient)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > ipeers.InterDaemonTimeout {
			t.Errorf("post ctx deadline = %v/%v, want within %v", deadline, ok, ipeers.InterDaemonTimeout)
		}
		if resp.MsgID == "" {
			resp.MsgID = req.MsgID
		}
		return resp, rem, err
	}
	r.h = r.acquireOriginHelper()
	return r
}

// acquireOriginHelper acquires the helper for the air sender's origin
// tuple, exactly as a delivery from air would have.
func (r *replyEnv) acquireOriginHelper() *helper {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := r.m.helpers.Acquire(ctx, r.request().From.Key(), remoteAlias+"/"+senderSession)
	if err != nil {
		r.t.Fatalf("Acquire origin helper: %v", err)
	}
	return h
}

func (r *replyEnv) set(fn func(r *replyEnv)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r)
}

func (r *replyEnv) postCalls() []postCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]postCall(nil), r.posts...)
}

func (r *replyEnv) ctxErrsSeen() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.ctxErrs...)
}

// reply hands line to the daemon as the helper's pump would.
func (r *replyEnv) reply(line string) { r.m.handleReplyFrame(r.h, line) }

// join waits for every reply worker started so far.
func (r *replyEnv) join() {
	r.t.Helper()
	done := make(chan struct{})
	go func() { r.m.workers.Wait(); close(done) }()
	waitClosed(r.t, done, 5*time.Second, "reply workers")
}

// awaitPost waits (bounded) for the next post call to begin.
func (r *replyEnv) awaitPost() postCall {
	r.t.Helper()
	select {
	case c := <-r.started:
		return c
	case <-time.After(2 * time.Second):
		r.t.Fatal("no post call within 2 s")
		return postCall{}
	}
}

// frameLine is one NDJSON line as Claude Code writes it into a socket.
func frameLine(t *testing.T, msgID, typ, from, content string) string {
	t.Helper()
	f := map[string]any{
		"msgV": 1, "msg_id": msgID, "type": typ, "priority": "next",
		"message": map[string]any{"role": "user", "content": content},
	}
	if from != "" {
		f["from"] = from
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// replierFrom is the reply address the env's target cc session presents.
func (r *replyEnv) replierFrom() string { return "uds:" + r.targetSock }

// wrapped is a native reply from the target with a full wrapper.
func (r *replyEnv) wrapped(mode, hop, text string) string {
	return frameLine(r.t, nativeMsgID, "user", r.replierFrom(), ccuds.Wrapper{
		From: r.replierFrom(), FromName: targetPeerName, FromMode: mode, HopChain: hop, Text: text,
	}.Format())
}

// onlyReplyRow asserts exactly one audit row exists, that it is a reply
// row bound to the helper's origin with the native id kept, and returns it.
func (r *replyEnv) onlyReplyRow() store.PeerMessage {
	r.t.Helper()
	row := r.onlyRow()
	if row.Direction != store.DirReply {
		r.t.Errorf("row direction = %q, want reply", row.Direction)
	}
	if row.NativeMsgID != nativeMsgID {
		r.t.Errorf("row native_msg_id = %q, want %q", row.NativeMsgID, nativeMsgID)
	}
	if !ipeers.IsUUID(row.MsgID) || row.MsgID == nativeMsgID {
		r.t.Errorf("row msg_id = %q, want a fresh UUID (never the native id)", row.MsgID)
	}
	if row.ToHostID != remoteHostID || row.ToSessionID != senderSessionID {
		r.t.Errorf("row to = %s/%s, want the helper's origin %s/%s", row.ToHostID, row.ToSessionID, remoteHostID, senderSessionID)
	}
	if row.FromHostID != localHostID {
		r.t.Errorf("row from_host_id = %q, want %q", row.FromHostID, localHostID)
	}
	return row
}

// assertDropped asserts the reply was audited as a drop with code, that
// nothing was posted, and returns the row.
func (r *replyEnv) assertDropped(code, fromSessionID string) store.PeerMessage {
	r.t.Helper()
	r.join()
	if n := len(r.postCalls()); n != 0 {
		r.t.Errorf("post calls = %d, want none", n)
	}
	row := r.onlyReplyRow()
	if row.Result != code {
		r.t.Errorf("row result = %q, want %q (error %q)", row.Result, code, row.Error)
	}
	if row.FromSessionID != fromSessionID {
		r.t.Errorf("row from_session_id = %q, want %q", row.FromSessionID, fromSessionID)
	}
	return row
}

func (r *replyEnv) helperMapLen() int {
	r.m.helpers.mu.Lock()
	defer r.m.helpers.mu.Unlock()
	return len(r.m.helpers.helpers)
}

// ---------------------------------------------------------------------------
// Happy path and the wire contract
// ---------------------------------------------------------------------------

func TestReply_HappyPath(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.set(func(r *replyEnv) {
		r.resp = ipeers.DeliverResponse{Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModeBypass}
	})

	r.reply(r.wrapped(ipeers.ModeBypass, "hc-1", "PONG"))
	call := r.awaitPost()
	r.join()

	if call.baseURL != remoteEntryURL || call.bearer != remoteToken {
		t.Errorf("post to %q with %q, want the entry's %q / %q", call.baseURL, call.bearer, remoteEntryURL, remoteToken)
	}
	req := call.req
	wantTo := ipeers.WireTo{AgentSessionID: senderSessionID, PID: senderPID, ProcStart: senderProcStart}
	if req.To != wantTo {
		t.Errorf("to = %+v, want the helper's origin %+v", req.To, wantTo)
	}
	wantFrom := ipeers.WireFrom{
		HostID: localHostID, AgentSessionID: targetSessionID, PID: targetPID, ProcStart: targetProcStart,
		PeerName: targetPeerName, SessionName: "cc:" + targetPeerName, DeclaredMode: ipeers.ModeBypass,
	}
	if req.From != wantFrom {
		t.Errorf("from = %+v, want the replier tuple %+v", req.From, wantFrom)
	}
	if req.HopChain != "hc-1" {
		t.Errorf("hop_chain = %q, want hc-1", req.HopChain)
	}
	if req.Text != "PONG" {
		t.Errorf("text = %q, want the unwrapped PONG", req.Text)
	}
	if !ipeers.IsUUID(req.MsgID) || req.MsgID == nativeMsgID {
		t.Errorf("msg_id = %q, want a fresh UUID (never the native id)", req.MsgID)
	}
	if err := req.Validate(); err != nil {
		t.Errorf("outbound request does not validate: %v", err)
	}

	row := r.onlyReplyRow()
	if row.MsgID != req.MsgID {
		t.Errorf("row msg_id = %q, want the posted %q", row.MsgID, req.MsgID)
	}
	if row.FromSessionID != targetSessionID {
		t.Errorf("row from_session_id = %q, want %q", row.FromSessionID, targetSessionID)
	}
	if row.DeclaredMode != ipeers.ModeBypass || row.EffectiveMode != ipeers.ModeBypass {
		t.Errorf("row modes = %q/%q, want bypass/bypass (effective from the origin's answer)", row.DeclaredMode, row.EffectiveMode)
	}
	if row.Result != ipeers.ResultDelivered || row.Error != "" {
		t.Errorf("row result/error = %q/%q, want delivered/\"\"", row.Result, row.Error)
	}
	if row.Bytes != len("PONG") {
		t.Errorf("row bytes = %d, want %d", row.Bytes, len("PONG"))
	}
	if r.helperMapLen() != 1 {
		t.Errorf("helper map = %d, want the helper kept", r.helperMapLen())
	}
}

func TestReply_OneWayRecordedAsNoReturnRoute(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.set(func(r *replyEnv) { r.resp.OneWay = true })
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.awaitPost()
	r.join()
	row := r.onlyReplyRow()
	if row.Result != ipeers.ResultDelivered || row.Error != ipeers.ErrNoReturnRoute {
		t.Errorf("row result/error = %q/%q, want delivered/no_return_route", row.Result, row.Error)
	}
}

func TestReply_PlainContent(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.reply(frameLine(t, nativeMsgID, "user", r.replierFrom(), "just text, no wrapper"))
	call := r.awaitPost()
	r.join()
	if call.req.Text != "just text, no wrapper" || call.req.HopChain != "" || call.req.From.DeclaredMode != ipeers.ModePrompting {
		t.Errorf("req = text %q hop %q mode %q, want the whole content, no hop chain, prompting", call.req.Text, call.req.HopChain, call.req.From.DeclaredMode)
	}
	if row := r.onlyReplyRow(); row.DeclaredMode != ipeers.ModePrompting {
		t.Errorf("row declared_mode = %q, want prompting", row.DeclaredMode)
	}
}

func TestReply_WrapperWithBadModeIsPrompting(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.reply(r.wrapped("yolo", "", "PONG"))
	call := r.awaitPost()
	r.join()
	if call.req.From.DeclaredMode != ipeers.ModePrompting {
		t.Errorf("declared_mode = %q, want prompting for an unparsable from-mode (D3)", call.req.From.DeclaredMode)
	}
}

func TestReply_EachReplyMintsItsOwnMsgID(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.reply(r.wrapped(ipeers.ModePrompting, "", "one"))
	first := r.awaitPost()
	r.reply(r.wrapped(ipeers.ModePrompting, "", "two"))
	second := r.awaitPost()
	r.join()
	if first.req.MsgID == second.req.MsgID {
		t.Errorf("both replies (same native id) posted msg_id %q", first.req.MsgID)
	}
	if rows := r.rows(); len(rows) != 2 || rows[0].NativeMsgID != nativeMsgID || rows[1].NativeMsgID != nativeMsgID {
		t.Errorf("rows = %+v, want two reply rows sharing the native id", rows)
	}
}

// ---------------------------------------------------------------------------
// Drops, in step order
// ---------------------------------------------------------------------------

func TestReply_NonUserOrUnparsableFrameDroppedWithoutAudit(t *testing.T) {
	cases := map[string]string{
		"non-user type": frameLine(t, nativeMsgID, "message", "uds:/x.sock", "hi"),
		"not JSON":      "this is not a frame",
		"empty line":    "",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			r := newReplyEnv(t, envOpts{})
			r.reply(line)
			r.join()
			if len(r.postCalls()) != 0 || len(r.rows()) != 0 {
				t.Errorf("posts/rows = %d/%d, want none", len(r.postCalls()), len(r.rows()))
			}
			var logged bool
			for _, l := range r.f.logs.all() {
				if strings.Contains(l, "reply") && strings.Contains(l, "drop") {
					logged = true
				}
			}
			if !logged {
				t.Errorf("no log line about the dropped frame in %q", r.f.logs.all())
			}
		})
	}
}

func TestReply_FromWithoutUDS(t *testing.T) {
	for name, from := range map[string]string{"absent": "", "other scheme": "tcp:127.0.0.1:1", "bare prefix": "uds:"} {
		t.Run(name, func(t *testing.T) {
			r := newReplyEnv(t, envOpts{})
			r.reply(frameLine(t, nativeMsgID, "user", from, "PONG"))
			r.assertDropped(ipeers.ErrReplierUnknown, "")
		})
	}
}

func TestReply_FromOwnHelperSockIsProxyToProxy(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.reply(frameLine(t, nativeMsgID, "user", "uds:"+r.h.sock, "PONG"))
	r.assertDropped(ipeers.ErrProxyToProxy, "")
	if r.helperMapLen() != 1 {
		t.Errorf("helper map = %d, want the helper kept", r.helperMapLen())
	}
}

func TestReply_FromForeignProxyRowIsProxyToProxy(t *testing.T) {
	r := newReplyEnv(t, envOpts{proxyInfo: map[int]bool{555: true}})
	foreignSock := filepath.Join(r.root, "foreign.sock")
	ln, err := net.Listen("unix", foreignSock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	writeRegistryFixture(t, r.regDir, "555.json", `{"pid":555,"sessionId":"55555555-5555-4555-8555-555555555555","cwd":"/w","procStart":"`+targetProcStart+`","messagingSocketPath":"`+foreignSock+`","name":"air/foo"}`)
	r.reply(frameLine(t, nativeMsgID, "user", "uds:"+foreignSock, "PONG"))
	r.assertDropped(ipeers.ErrProxyToProxy, "")
}

func TestReply_UnknownSockIsReplierUnknown(t *testing.T) {
	t.Run("no such socket", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.reply(frameLine(t, nativeMsgID, "user", "uds:"+filepath.Join(r.root, "nope.sock"), "PONG"))
		r.assertDropped(ipeers.ErrReplierUnknown, "")
	})
	t.Run("inventory unavailable", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.m.sessions = &fakeSessions{err: errFakeProvider}
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
		r.assertDropped(ipeers.ErrReplierUnknown, "")
	})
}

// TestReply_PartialInventoryIsNotReady pins spec §4.2's partial semantics
// on the replier lookup (R2-A): a reply address that no row carries while
// the inventory is partial (the replier's tmux session's owner lookup
// failed ⇒ agent:null) is audited not_ready with the fixed error
// "inventory partial" — never replier_unknown, which claims no session
// listens there — and the origin's helper is kept, ready for the retry.
func TestReply_PartialInventoryIsNotReady(t *testing.T) {
	r := newReplyEnv(t, envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}})
	writeRegistryFixture(t, r.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(r.targetSock, "foo:@1.%1"))
	r.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}

	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	row := r.assertDropped(ipeers.ErrNotReady, "")
	if row.Error != "inventory partial" {
		t.Errorf("row error = %q, want %q", row.Error, "inventory partial")
	}
	if _, ok := r.m.helpers.FindBySock(r.h.sock); !ok || r.helperMapLen() != 1 || r.f.fake.Stops() != 0 {
		t.Errorf("helper kept = %v (map %d, stops %d), want the origin's helper kept", ok, r.helperMapLen(), r.f.fake.Stops())
	}

	// Once the lookup completes the same frame is forwarded.
	r.m.owners = &fakeOwners{owners: map[string]agent.PaneOwner{"s1": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%1"}}}
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.awaitPost()
	r.join()
	rows := r.rows()
	if len(rows) != 2 || rows[1].Result != ipeers.ResultDelivered || rows[1].FromSessionID != targetSessionID {
		t.Errorf("rows = %+v, want a second, delivered reply row from the replier", rows)
	}
}

func TestReply_TextValidation(t *testing.T) {
	t.Run("too large", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.reply(r.wrapped(ipeers.ModePrompting, "", strings.Repeat("x", ipeers.MaxTextBytes+1)))
		row := r.assertDropped(ipeers.ErrTextTooLarge, targetSessionID)
		if row.Bytes != ipeers.MaxTextBytes+1 {
			t.Errorf("row bytes = %d, want %d", row.Bytes, ipeers.MaxTextBytes+1)
		}
	})
	t.Run("empty", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.reply(r.wrapped(ipeers.ModePrompting, "", ""))
		r.assertDropped(ipeers.ErrBadRequest, targetSessionID)
	})
}

func TestReply_NoReturnRoute(t *testing.T) {
	cases := map[string][]config.PeerHost{
		"entry without token": {airHost("", false)},
		"no entry for origin": {{Alias: "other", URL: "http://other.invalid", HostID: "other:1", Token: "t", InboundToken: "i"}},
		"no hosts at all":     {},
	}
	for name, hosts := range cases {
		t.Run(name, func(t *testing.T) {
			r := newReplyEnv(t, envOpts{hosts: hosts})
			r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
			row := r.assertDropped(ipeers.ErrNoReturnRoute, targetSessionID)
			if row.DeclaredMode != ipeers.ModePrompting || row.Bytes != len("PONG") {
				t.Errorf("row = %+v, want declared prompting and bytes %d", row, len("PONG"))
			}
		})
	}
}

func TestReply_AuditInsertFailureNoPost(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.f.audit.fail(errors.New("disk full"), nil)
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.join()
	if len(r.postCalls()) != 0 {
		t.Errorf("post calls = %d, want none after an audit insert failure", len(r.postCalls()))
	}
	var logged bool
	for _, l := range r.f.logs.all() {
		if strings.Contains(l, "audit insert") && strings.Contains(l, "disk full") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no log line about the insert failure in %q", r.f.logs.all())
	}
}

func TestReply_NilAuditNoPost(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.m.audit = nil
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.join()
	if len(r.postCalls()) != 0 {
		t.Errorf("post calls = %d, want none without an audit store", len(r.postCalls()))
	}
}

// ---------------------------------------------------------------------------
// The origin's answer
// ---------------------------------------------------------------------------

func TestReply_RemoteTargetGoneReleasesHelper(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.set(func(r *replyEnv) {
		r.rem = &ipeers.RemoteError{Status: http.StatusConflict, Error: ipeers.ErrTargetGone, Detail: "pid does not match the live session"}
	})
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.awaitPost()
	r.join()

	row := r.onlyReplyRow()
	if row.Result != ipeers.ErrTargetGone || row.Error != "pid does not match the live session" || row.EffectiveMode != "" {
		t.Errorf("row = %+v, want result target_gone with the remote detail and no effective mode", row)
	}
	if _, ok := r.m.helpers.FindBySock(r.h.sock); ok {
		t.Error("helper still registered after the origin was found gone")
	}
	if n := r.helperMapLen(); n != 0 {
		t.Errorf("helper map = %d, want empty", n)
	}
	if _, err := os.Lstat(r.h.sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("helper socket %s: %v, want gone", r.h.sock, err)
	}
	if r.f.fake.Stops() != 1 {
		t.Errorf("helper stops = %d, want 1", r.f.fake.Stops())
	}
}

// TestReply_RemoteNotReadyKeepsHelper pins the other side of the
// target_gone contract: an origin that could not build its inventory
// answers 503 not_ready, which says nothing about the origin session, so
// the helper is kept and the reply row records not_ready.
func TestReply_RemoteNotReadyKeepsHelper(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.set(func(r *replyEnv) {
		r.rem = &ipeers.RemoteError{Status: http.StatusServiceUnavailable, Error: ipeers.ErrNotReady, Detail: "inventory unavailable"}
	})
	r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	r.awaitPost()
	r.join()

	row := r.onlyReplyRow()
	if row.Result != ipeers.ErrNotReady || row.Error != "inventory unavailable" || row.EffectiveMode != "" {
		t.Errorf("row = %+v, want result not_ready with the remote detail and no effective mode", row)
	}
	if _, ok := r.m.helpers.FindBySock(r.h.sock); !ok {
		t.Error("helper released on not_ready; only target_gone may release it")
	}
	if r.helperMapLen() != 1 || r.f.fake.Stops() != 0 {
		t.Errorf("helper map/stops = %d/%d, want the helper kept", r.helperMapLen(), r.f.fake.Stops())
	}
}

func TestReply_RemoteOtherErrorKeepsHelper(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.set(func(r *replyEnv) {
			r.rem = &ipeers.RemoteError{Status: http.StatusTooManyRequests, Error: ipeers.ErrRateLimited, Detail: "pair rate limit exceeded"}
		})
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
		r.awaitPost()
		r.join()
		row := r.onlyReplyRow()
		if row.Result != ipeers.ErrRateLimited || row.Error != "pair rate limit exceeded" {
			t.Errorf("row result/error = %q/%q, want rate_limited with the remote detail", row.Result, row.Error)
		}
		if r.helperMapLen() != 1 || r.f.fake.Stops() != 0 {
			t.Errorf("helper map/stops = %d/%d, want the helper kept", r.helperMapLen(), r.f.fake.Stops())
		}
	})
	t.Run("transport", func(t *testing.T) {
		r := newReplyEnv(t, envOpts{})
		r.set(func(r *replyEnv) { r.err = errors.New("dial tcp: " + strings.Repeat("z", 400)) })
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
		r.awaitPost()
		r.join()
		row := r.onlyReplyRow()
		if row.Result != "" || !strings.HasPrefix(row.Error, "dial tcp: ") || len(row.Error) > maxRemoteTextBytes+len("…") {
			t.Errorf("row result/error = %q/%d bytes, want \"\" with the bounded transport error", row.Result, len(row.Error))
		}
		if r.helperMapLen() != 1 {
			t.Errorf("helper map = %d, want the helper kept", r.helperMapLen())
		}
	})
}

// ---------------------------------------------------------------------------
// Workers: the semaphore, back-pressure and Stop
// ---------------------------------------------------------------------------

// TestReply_StopJoinsBlockedWorkers pins the worker contract: eight
// replies parked inside a slow post (one per semaphore slot) are joined
// by Stop — it returns once they do, within 3× the fake's delay, every
// post ran under stopCtx (it was cancelled by the time they returned) and
// no goroutine is left behind.
func TestReply_StopJoinsBlockedWorkers(t *testing.T) {
	const delay = 200 * time.Millisecond
	r := newReplyEnv(t, envOpts{})
	// The baseline includes the env's own long-lived goroutines (the
	// target's listener, database/sql's opener, the helper and its pump):
	// a worker left behind by Stop would push the count above it.
	before := runtime.NumGoroutine()
	r.set(func(r *replyEnv) {
		r.hold = func(context.Context) { time.Sleep(delay) } // deliberately ignores ctx
	})
	for i := 0; i < replyWorkerCap; i++ {
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	}
	for i := 0; i < replyWorkerCap; i++ {
		r.awaitPost()
	}

	start := time.Now()
	done := make(chan struct{})
	go func() { r.m.Stop(context.Background()); close(done) }()
	waitClosed(t, done, 3*delay+time.Second, "Module.Stop")
	if took := time.Since(start); took > 3*delay {
		t.Errorf("Stop took %v, want within %v", took, 3*delay)
	}
	errs := r.ctxErrsSeen()
	if len(errs) != replyWorkerCap {
		t.Fatalf("posts returned = %d, want %d", len(errs), replyWorkerCap)
	}
	for i, err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("post %d ctx.Err() = %v, want Canceled (posts run under stopCtx)", i, err)
		}
	}
	if rows := r.rows(); len(rows) != replyWorkerCap {
		t.Errorf("rows = %d, want one per reply", len(rows))
	}
	assertNoGoroutineGrowth(t, before)
}

// TestReply_NinthFrameBlocksPumpUntilSlotFrees pins the back-pressure:
// with every slot held, handleReplyFrame blocks the (calling) pump, and
// resumes as soon as one worker finishes.
func TestReply_NinthFrameBlocksPumpUntilSlotFrees(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	release := make(chan struct{})
	r.set(func(r *replyEnv) {
		r.hold = func(ctx context.Context) {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	for i := 0; i < replyWorkerCap; i++ {
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	}
	for i := 0; i < replyWorkerCap; i++ {
		r.awaitPost()
	}

	ninth := make(chan struct{})
	go func() {
		r.reply(r.wrapped(ipeers.ModePrompting, "", "ninth"))
		close(ninth)
	}()
	stillBlocked(t, ninth, 100*time.Millisecond, "the ninth handleReplyFrame")
	if len(r.postCalls()) != replyWorkerCap {
		t.Fatalf("post calls = %d, want exactly %d while every slot is held", len(r.postCalls()), replyWorkerCap)
	}

	release <- struct{}{} // one worker finishes ⇒ one slot frees
	waitClosed(t, ninth, 2*time.Second, "the ninth handleReplyFrame")
	if c := r.awaitPost(); c.req.Text != "ninth" {
		t.Errorf("ninth post text = %q, want ninth", c.req.Text)
	}
	close(release)
	r.join()
	if rows := r.rows(); len(rows) != replyWorkerCap+1 {
		t.Errorf("rows = %d, want %d", len(rows), replyWorkerCap+1)
	}
}

// TestReply_SemaphoreWaitGivesUpOnStop pins the other half of R2-B1: a
// pump parked on a full semaphore returns as soon as stopCtx is
// cancelled, without starting a worker.
func TestReply_SemaphoreWaitGivesUpOnStop(t *testing.T) {
	r := newReplyEnv(t, envOpts{})
	r.set(func(r *replyEnv) { r.hold = func(ctx context.Context) { <-ctx.Done() } })
	for i := 0; i < replyWorkerCap; i++ {
		r.reply(r.wrapped(ipeers.ModePrompting, "", "PONG"))
	}
	for i := 0; i < replyWorkerCap; i++ {
		r.awaitPost()
	}
	ninth := make(chan struct{})
	go func() {
		r.reply(r.wrapped(ipeers.ModePrompting, "", "ninth"))
		close(ninth)
	}()
	stillBlocked(t, ninth, 50*time.Millisecond, "the ninth handleReplyFrame")

	r.m.stopCancel()
	waitClosed(t, ninth, 2*time.Second, "the ninth handleReplyFrame after stopCancel")
	r.join()
	if n := len(r.postCalls()); n != replyWorkerCap {
		t.Errorf("post calls = %d, want %d (the ninth never became a worker)", n, replyWorkerCap)
	}
}

// ---------------------------------------------------------------------------
// GET /api/peers/log
// ---------------------------------------------------------------------------

func getLog(t *testing.T, m *Module, ctx context.Context, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/peers/log"+query, nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeLog(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if body.Messages == nil {
		t.Fatalf("messages is null, want an array; body=%s", rr.Body.String())
	}
	return body.Messages
}

func insertLogRow(t *testing.T, e *deliverEnv, i int) {
	t.Helper()
	id, err := e.f.audit.Insert(store.PeerMessage{
		MsgID:         "00000000-0000-4000-8000-" + strings.Repeat("0", 11) + string(rune('a'+i)),
		NativeMsgID:   "native-" + string(rune('a'+i)),
		Direction:     store.DirReply,
		TS:            time.Date(2026, 9, 14, 10, 0, i, 123_000_000, time.UTC),
		FromHostID:    localHostID,
		FromSessionID: targetSessionID,
		ToHostID:      remoteHostID,
		ToSessionID:   senderSessionID,
		DeclaredMode:  ipeers.ModeBypass,
		EffectiveMode: ipeers.ModePrompting,
		Bytes:         4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.f.audit.SetResult(id, "", ipeers.ResultDelivered, "e"); err != nil {
		t.Fatal(err)
	}
}

func TestPeersLog_ShapeAndOrdering(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for i := 0; i < 3; i++ {
		insertLogRow(t, e, i)
	}
	msgs := decodeLog(t, getLog(t, e.m, adminCtx(), "?tail=2"))
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the 2 newest", len(msgs))
	}
	// Oldest first: rows b then c.
	if msgs[0]["native_msg_id"] != "native-b" || msgs[1]["native_msg_id"] != "native-c" {
		t.Errorf("order = %v, %v; want native-b then native-c", msgs[0]["native_msg_id"], msgs[1]["native_msg_id"])
	}
	want := map[string]any{
		"id":              float64(2),
		"msg_id":          "00000000-0000-4000-8000-00000000000b",
		"native_msg_id":   "native-b",
		"direction":       "reply",
		"ts":              "2026-09-14T10:00:01.123Z",
		"from_host_id":    localHostID,
		"from_session_id": targetSessionID,
		"to_host_id":      remoteHostID,
		"to_session_id":   senderSessionID,
		"declared_mode":   ipeers.ModeBypass,
		"effective_mode":  ipeers.ModePrompting,
		"bytes":           float64(4),
		"result":          ipeers.ResultDelivered,
		"error":           "e",
	}
	got := msgs[0]
	if len(got) != len(want) {
		t.Errorf("keys = %d, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
	tsPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	for _, m := range msgs {
		if ts, _ := m["ts"].(string); !tsPattern.MatchString(ts) {
			t.Errorf("ts = %q, want RFC 3339 with milliseconds in UTC", ts)
		}
	}
}

func TestPeersLog_DefaultTail(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for i := 0; i < 26; i++ {
		insertLogRow(t, e, i)
	}
	if msgs := decodeLog(t, getLog(t, e.m, adminCtx(), "")); len(msgs) != 26 {
		t.Errorf("messages = %d, want all 26 under the default tail of 50", len(msgs))
	}
	if msgs := decodeLog(t, getLog(t, e.m, adminCtx(), "?tail=0")); len(msgs) != 0 {
		t.Errorf("messages = %d, want none for tail=0", len(msgs))
	}
}

func TestPeersLog_EmptyIsArray(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	rr := getLog(t, e.m, adminCtx(), "")
	if strings.TrimSpace(rr.Body.String()) != `{"messages":[]}` {
		t.Errorf("body = %q, want {\"messages\":[]}", rr.Body.String())
	}
}

func TestParseTail(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"", defaultLogTail, true},
		{"7", 7, true},
		{"0", 0, true},
		{"1000", maxLogTail, true},
		{"5000", maxLogTail, true},
		{"abc", 0, false},
		{"-1", 0, false},
		{"1.5", 0, false},
		{" 3", 0, false},
	}
	for _, c := range cases {
		got, err := parseTail(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("parseTail(%q) = %d, %v; want %d, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestPeersLog_BadTail(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for _, q := range []string{"?tail=abc", "?tail=-1", "?tail=1.5"} {
		rr := getLog(t, e.m, adminCtx(), q)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rr.Code)
		}
		if ae := decodeAPIError(t, rr); ae.Error != ipeers.ErrBadRequest {
			t.Errorf("%s: error = %q, want bad_request", q, ae.Error)
		}
	}
}

func TestPeersLog_Forbidden(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	insertLogRow(t, e, 0)
	for name, ctx := range map[string]context.Context{"host principal": e.hostCtx(), "no principal": context.Background()} {
		rr := getLog(t, e.m, ctx, "")
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rr.Code)
		}
		if ae := decodeAPIError(t, rr); ae.Error != ipeers.ErrForbidden {
			t.Errorf("%s: error = %q, want forbidden", name, ae.Error)
		}
		if strings.Contains(rr.Body.String(), "native-a") {
			t.Errorf("%s: body leaks rows: %s", name, rr.Body.String())
		}
	}
}

func TestPeersLog_AuditUnavailable(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.m.audit = nil
	rr := getLog(t, e.m, adminCtx(), "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if ae := decodeAPIError(t, rr); ae.Error != ipeers.ErrAuditUnavailable {
		t.Errorf("error = %q, want audit_unavailable", ae.Error)
	}
}

// The middleware-level policy refuses host principals on the log route
// before the handler's own check ever runs.
func TestHostRoutePolicy_RefusesLog(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/peers/log?tail=5", nil)
	if HostRoutePolicy(req) {
		t.Error("HostRoutePolicy allows a host principal on GET /api/peers/log")
	}
}
