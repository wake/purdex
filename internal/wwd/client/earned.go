package client

import (
	"context"
	"errors"
	"net/http"
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

// QuarantineLabel is POST /v1/earned-labels/quarantine (local socket only): this host stops advertising the label it earned, until the
// task is qualified again. A StatusError with Code 404 says the host has no such earned label.
func (c *Client) QuarantineLabel(ctx context.Context, label string) error {
	return c.changeLabel(ctx, "quarantine", label)
}

// ReleaseLabel is POST /v1/earned-labels/release (local socket only): this host lets go of the label (the row is deleted; the host holds
// it again by qualifying for it).
func (c *Client) ReleaseLabel(ctx context.Context, label string) error {
	return c.changeLabel(ctx, "release", label)
}

func (c *Client) changeLabel(ctx context.Context, action, label string) error {
	if !wire.ValidTaskLabel(label) {
		return errors.New("not a task label (task:<repo>/<task>): " + label)
	}
	_, err := c.send(ctx, http.MethodPost, "/v1/earned-labels/"+action, nil, map[string]string{"label": label}, maxBody)
	return err
}
