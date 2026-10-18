package client

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// UsageQuery is the query of GET /v1/usage: the jobs that ended at or after Since (zero: wwd's default, the last day), at most Limit
// of them (0: wwd's default). With After set (a job id) the page starts after that job, whose end time is Since: the cursor a
// truncated answer gives (next_since, next_after).
type UsageQuery struct {
	Since time.Time
	After string
	Limit int
}

// Usage is GET /v1/usage: the usage of this host's finished jobs. The local socket only; a peer does not serve it.
func (c *Client) Usage(ctx context.Context, q UsageQuery) (wire.UsageResponse, error) {
	v := url.Values{}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339Nano))
	}
	if q.After != "" {
		v.Set("after", q.After)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return get[wire.UsageResponse](ctx, c, "/v1/usage", v)
}
