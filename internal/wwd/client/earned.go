package client

import (
	"context"
	"errors"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// EarnedLabels is GET /v1/earned-labels (local socket only): this host's earned and quarantined task labels with the fingerprints they
// were earned under. known is false when the wwd answered 404 for the route: it predates earned labels (or has no store), which a reader
// takes as "none yet" — the list is then empty. A 404 that says "unknown host" is an error.
func (c *Client) EarnedLabels(ctx context.Context) (list wire.EarnedLabelList, known bool, err error) {
	list, err = get[wire.EarnedLabelList](ctx, c, "/v1/earned-labels", nil)
	if err == nil {
		return list, true, nil
	}
	var se *StatusError
	if errors.As(err, &se) && se.Code == 404 && !strings.Contains(se.Body, "unknown host") {
		return wire.EarnedLabelList{Labels: []wire.EarnedLabel{}}, false, nil
	}
	return wire.EarnedLabelList{}, false, err
}
