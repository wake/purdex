package client

import (
	"context"
	"fmt"
	"net/url"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Qualification is GET /v1/qualification (WWD-7b2-0a): what a host holds of each task and what happens next, asked of the local wwd (host ""
// is the local host itself, an alias is forwarded to that peer). known is false when the host answered 404 for the route: its wwd predates
// it, which a reader takes as "not known" — the lists are then empty. The local wwd's own "unknown host" 404 is an error, since it means the
// alias is not in the pool.
func (c *Client) Qualification(ctx context.Context, host string) (v wire.HostQualification, known bool, err error) {
	var q url.Values
	timeout := c.timeout()
	if host != "" {
		q = url.Values{"host": {host}}
		if timeout < MutateTimeout { // the local wwd may forward it (its own limit is 10 s)
			timeout = MutateTimeout
		}
	}
	v, err = getLimit[wire.HostQualification](ctx, c, "/v1/qualification", q, timeout, maxBody)
	if err == nil {
		if v.Tasks == nil {
			v.Tasks = []wire.QualificationTask{}
		}
		if v.Recent == nil {
			v.Recent = []wire.QualifyRecord{}
		}
		if err := v.Validate(); err != nil { // Decode checks the keys, not what the answer means
			return wire.HostQualification{}, false, fmt.Errorf("%w: %v", ErrBadResponse, err)
		}
		if host != "" && v.Host != host { // the caller names the host on what it shows: another host's tasks must not wear this one's name
			return wire.HostQualification{}, false, fmt.Errorf("%w: qualification: asked for %q, answered for %q", ErrBadResponse, host, v.Host)
		}
		return v, true, nil
	}
	if RouteMissing(err) { // 404 only: no wwd ever had another method on this path, so a 405 (even the mux's own) is a refusal, not an old host
		return wire.HostQualification{Host: host, Tasks: []wire.QualificationTask{}, Recent: []wire.QualifyRecord{}}, false, nil
	}
	return wire.HostQualification{}, false, err
}
