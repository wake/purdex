package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Alerts is GET /v1/alerts (spec §13.3): host's active alerts, asked of the local wwd (host "" is the local host itself, an alias is
// forwarded to that peer). known is false when the host answered 404 for the route: its wwd predates proto 2 or has no alerts store,
// which a reader takes as "no alerts yet" (§13.1) — the list is then empty and carries the asked host. The local wwd's own
// "unknown host" 404 is an error, since it means the alias is not in the pool.
//
// The list is read one alert at a time (S3-3a): an alert whose kind or level this build does not know is a rule of a later release, so it
// is left out and said once in the log (Client.Logf), and the rest of the list is what the reader gets. Anything else that is wrong with the
// list, or with an alert, still refuses the whole of it. This is the only reader that is lenient this way; every other answer is checked whole.
func (c *Client) Alerts(ctx context.Context, host string) (list wire.AlertList, known bool, err error) {
	var q url.Values
	if host != "" {
		q = url.Values{"host": {host}}
	}
	var raw json.RawMessage
	raw, err = get[json.RawMessage](ctx, c, "/v1/alerts", q)
	if err == nil {
		if list, err = c.readAlertList(raw); err == nil {
			return list, true, nil
		}
		err = fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	var se *StatusError
	if errors.As(err, &se) && se.Code == 404 && !strings.Contains(se.Body, "unknown host") {
		return wire.AlertList{Host: host, Alerts: []wire.Alert{}}, false, nil
	}
	return wire.AlertList{}, false, err
}

// readAlertList checks the envelope as wire.AlertList does (the host is named, the alerts are an array) and each alert on its own.
func (c *Client) readAlertList(raw json.RawMessage) (wire.AlertList, error) {
	var env struct {
		Host   string            `json:"host"`
		Alerts []json.RawMessage `json:"alerts"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return wire.AlertList{}, err
	}
	if env.Host == "" {
		return wire.AlertList{}, errors.New("alerts: missing host")
	}
	out := wire.AlertList{Host: env.Host, Alerts: make([]wire.Alert, 0, len(env.Alerts))}
	for i, item := range env.Alerts {
		a, err := wire.ParseAlert(item)
		switch {
		case err == nil:
			out.Alerts = append(out.Alerts, a)
		case wire.IsUnknownAlert(err):
			if what, first := c.alertSkips.First(a, err); first {
				c.logf("wwd client: %s has an alert (key %q) of %s that this build does not know: left out (said once)", env.Host, a.Key, what)
			}
		default:
			return wire.AlertList{}, fmt.Errorf("alerts[%d]: %w", i, err)
		}
	}
	return out, nil
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}
