package client

import (
	"context"
	"net/http"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Qualify is POST /v1/qualify (local socket only): this host starts a qualification pair for the task <repo>/<task> of one of its prepared
// clones. A refusal is a StatusError whose Body is {"error": "..."}: 404 for a task the host has no qualify for, 409 for one it may not be
// asked about now (it is the reference, its worker is off, it holds the label already, a pair is running, it lacks a tool).
func (c *Client) Qualify(ctx context.Context, task string) (wire.QualifyStarted, error) {
	req := wire.QualifyRequest{Task: task}
	if err := req.Validate(); err != nil {
		return wire.QualifyStarted{}, err
	}
	b, err := c.send(ctx, http.MethodPost, "/v1/qualify", nil, req, maxBody)
	if err != nil {
		return wire.QualifyStarted{}, err
	}
	return wire.DecodeLenient[wire.QualifyStarted](b)
}

// QualifyStatus is GET /v1/qualify (local socket only): the pairs in flight and the last ones that ended.
func (c *Client) QualifyStatus(ctx context.Context) (wire.QualifyStatus, error) {
	st, err := get[wire.QualifyStatus](ctx, c, "/v1/qualify", nil)
	if err != nil {
		return wire.QualifyStatus{}, err
	}
	return st, st.Validate()
}
