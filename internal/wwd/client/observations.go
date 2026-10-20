package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Observations is GET /v1/observations (spec 8.4): host's observations since the given time, asked of the local wwd (host "" is the local
// host itself, an alias is forwarded to that peer). since and until are passed on as given and left out when empty. The answer is checked
// against the contract and returned as wwd wrote it, byte for byte: next_until is a cursor that has to go back to wwd exactly as it came.
// known is false (and raw nil) when the host answered 404 for the route: its wwd predates it. The local wwd's own "unknown host" 404 is an
// error, since it means the alias is not in the pool.
func (c *Client) Observations(ctx context.Context, host, since, until string) (raw []byte, known bool, err error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if until != "" {
		q.Set("until", until)
	}
	return c.observed(ctx, "/v1/observations", host, q, func(b []byte) error {
		_, err := wire.DecodeLenient[wire.ObservationsResponse](b)
		return err
	})
}

// Undeclared is GET /v1/observations/undeclared: the same records grouped by command signature. Everything else is as for Observations.
func (c *Client) Undeclared(ctx context.Context, host, since string) (raw []byte, known bool, err error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	return c.observed(ctx, "/v1/observations/undeclared", host, q, func(b []byte) error {
		_, err := wire.DecodeLenient[wire.UndeclaredResponse](b)
		return err
	})
}

func (c *Client) observed(ctx context.Context, path, host string, q url.Values, check func([]byte) error) ([]byte, bool, error) {
	timeout := c.timeout()
	if host != "" {
		q.Set("host", host)
		if timeout < MutateTimeout { // the local wwd may forward it (its own limit is 10 s)
			timeout = MutateTimeout
		}
	}
	raw, err := c.sendRaw(ctx, http.MethodGet, path, q, nil, "", timeout, maxBody)
	if err != nil {
		if RouteMissing(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := check(raw); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	return raw, true, nil
}
