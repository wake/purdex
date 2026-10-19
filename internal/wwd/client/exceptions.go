package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Exceptions is GET /v1/exceptions (#3479 G4): host's exceptions in force with their holds, asked of the local wwd (host "" is the local
// host itself, an alias is forwarded to that peer). known is false when the host answered 404 for the route: its wwd predates it, which
// a reader takes as "not known" — the list is then empty. The local wwd's own "unknown host" 404 is an error, since it means the alias
// is not in the pool.
func (c *Client) Exceptions(ctx context.Context, host string) (list []wire.Exception, known bool, err error) {
	var q url.Values
	if host != "" {
		q = url.Values{"host": {host}}
	}
	list, err = get[[]wire.Exception](ctx, c, "/v1/exceptions", q)
	if err == nil {
		if list == nil {
			list = []wire.Exception{}
		}
		for _, e := range list { // Decode checks the shape of a slice's elements, not what each one means
			if err := e.Validate(); err != nil {
				return nil, false, fmt.Errorf("%w: %v", ErrBadResponse, err)
			}
		}
		return list, true, nil
	}
	var se *StatusError
	if errors.As(err, &se) && se.Code == 404 && !strings.Contains(se.Body, "unknown host") {
		return []wire.Exception{}, false, nil
	}
	return nil, false, err
}
