package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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

var _ Sender = moduleSender{}

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
