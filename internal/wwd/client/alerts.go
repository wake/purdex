package client

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Alerts is GET /v1/alerts (spec §13.3): host's active alerts, asked of the local wwd (host "" is the local host itself, an alias is
// forwarded to that peer). known is false when the host answered 404 for the route: its wwd predates proto 2 or has no alerts store,
// which a reader takes as "no alerts yet" (§13.1) — the list is then empty and carries the asked host. The local wwd's own
// "unknown host" 404 is an error, since it means the alias is not in the pool.
func (c *Client) Alerts(ctx context.Context, host string) (list wire.AlertList, known bool, err error) {
	var q url.Values
	if host != "" {
		q = url.Values{"host": {host}}
	}
	list, err = get[wire.AlertList](ctx, c, "/v1/alerts", q)
	if err == nil {
		return list, true, nil
	}
	var se *StatusError
	if errors.As(err, &se) && se.Code == 404 && !strings.Contains(se.Body, "unknown host") {
		return wire.AlertList{Host: host, Alerts: []wire.Alert{}}, false, nil
	}
	return wire.AlertList{}, false, err
}
