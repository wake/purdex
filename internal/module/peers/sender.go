package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

// SenderKey is the service-registry key of the in-process sender (adopt plan PL-1d1, plan v3 P6-1):
// daemon modules send a peer message through the one send path — resolution, delivery, audit — without
// an HTTP hop of their own.
const SenderKey = "peers.sender"

// Sender sends one peer message as the admin principal. A refusal (any non-2xx answer of
// POST /api/peers/send) is a *SendError; anything else that goes wrong is a plain error.
type Sender interface {
	Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error)
}

// SendError is a refused send: the HTTP status and the wire error handleSend answered.
type SendError struct {
	Status int
	API    ipeers.APIError
}

func (e *SendError) Error() string {
	if e.API.Detail != "" {
		return fmt.Sprintf("peers send refused (%d %s): %s", e.Status, e.API.Error, e.API.Detail)
	}
	return fmt.Sprintf("peers send refused (%d %s)", e.Status, e.API.Error)
}

var (
	_ Sender           = moduleSender{}
	_ Sender           = sendOnly{}
	_ FlowSender       = moduleSender{}
	_ SelfNoticeSender = moduleSender{}
)

// registerSenders publishes the two in-process senders: the shared one (Send only, what every module holds) and the pause
// outbox's flow sender (UP-2a), under separate keys.
func (m *Module) registerSenders(reg *core.ServiceRegistry) {
	reg.Register(SenderKey, sendOnly{moduleSender{m: m}})
	reg.Register(FlowSenderKey, moduleSender{m: m})
	reg.Register(SelfNoticeSenderKey, moduleSender{m: m})
}

// sendOnly is the Sender the shared registry hands out: moduleSender with only Send visible, so a module that type-asserts the
// shared service to FlowSender gets nothing.
type sendOnly struct{ s moduleSender }

func (o sendOnly) Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	return o.s.Send(ctx, req)
}

// moduleSender runs handleSend in process under the admin principal.
type moduleSender struct{ m *Module }

// bufferedResponse is the ResponseWriter handleSend writes into.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	return b.body.Write(p)
}

func (s moduleSender) Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	return s.send(ctx, req)
}

// SendFlow is Send for the pause's own flow messages (resume notices, bounce lists, reask controls): it alone skips the pause
// check. It is an in-process Go call that nothing over HTTP can reach, so no message text can claim the exemption; and it is
// on FlowSender, registered under FlowSenderKey, not on the Sender the other modules share.
func (s moduleSender) SendFlow(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	return s.send(context.WithValue(ctx, flowKey{}, true), req)
}

// SendSelfNotice is Send for a daemon notice to a team lead about a member of another host, sent from the lead's own inbox (see
// SelfNoticeSenderKey): it alone skips the self_target refusal. The pause check still applies. An in-process Go call, on its own key.
func (s moduleSender) SendSelfNotice(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	return s.send(context.WithValue(ctx, selfNoticeKey{}, true), req)
}

func (s moduleSender) send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return ipeers.SendResponse{}, fmt.Errorf("encode send request: %w", err)
	}
	ctx = middleware.WithPrincipal(ctx, middleware.Principal{Kind: middleware.PrincipalAdmin})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/peers/send", bytes.NewReader(raw))
	if err != nil {
		return ipeers.SendResponse{}, fmt.Errorf("build send request: %w", err)
	}
	w := &bufferedResponse{header: http.Header{}}
	s.m.handleSend(w, r)
	if w.status >= 200 && w.status < 300 {
		var out ipeers.SendResponse
		if err := json.Unmarshal(w.body.Bytes(), &out); err != nil {
			return ipeers.SendResponse{}, fmt.Errorf("decode send response: %w", err)
		}
		return out, nil
	}
	se := &SendError{Status: w.status}
	if err := json.Unmarshal(w.body.Bytes(), &se.API); err != nil {
		se.API = ipeers.APIError{Error: "bad_answer", Detail: w.body.String()}
	}
	return ipeers.SendResponse{}, se
}
