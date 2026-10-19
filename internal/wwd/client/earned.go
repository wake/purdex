package client

import (
	"context"

	"github.com/wake/purdex/internal/wwd/wire"
)

// EarnedLabels is GET /v1/earned-labels (local socket only): this host's earned and quarantined task labels with the fingerprints they
// were earned under.
func (c *Client) EarnedLabels(ctx context.Context) (wire.EarnedLabelList, error) {
	return get[wire.EarnedLabelList](ctx, c, "/v1/earned-labels", nil)
}
