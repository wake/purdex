package nex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The projector's list walk (spec 2026-10-08 §3.6, §3.7, §8 R3-2): every
// execution the list shows, page by page, through the engine's own handler.
// It is what the projector knows the clients hold. An epoch start seeds
// lastPushed from it (projector_epoch.go), and the safety reconcile (§3.7)
// compares lastPushed with it.
//
// Each page is its own read inside the slot, with its own ver, exactly like
// a page the list wrapper serves a client (§3.4): the slot is released
// between pages, so the flush worker waits at most one page (§3.8), and a
// delta flushed between two pages is ordered against both. Nothing needs a
// snapshot of the whole list: what a page says about an execution is true
// as of that page's ver, and the consumers compare vers per page.
//
// The walk reads the default list — archived executions left out — because
// that is the list the clients hold: an execution absent from it is one a
// client holds no row for.

const (
	// walkPageLimit is a walk's page size: §3.8's bound for walks in delta
	// mode, the page whose slot hold was measured (about 10 ms steady at
	// 100k events, 40 ms at 500k).
	walkPageLimit = 100

	// walkMaxPages caps one walk: 20,000 executions at walkPageLimit, two
	// orders of magnitude past any real host. Reaching it means a list that
	// does not end (an engine bug), not a big one; the walk stops there and
	// its caller logs it.
	walkMaxPages = 200
)

// errWalkPageCap is a walk's error once it read walkMaxPages pages and the
// list still had more.
var errWalkPageCap = errors.New("nex list walk reached its page cap")

// walkRow is one execution as a walk saw it.
type walkRow struct {
	id     string
	digest rowDigest
}

// walkPage is one page of a walk: its rows, the ver its read was stamped
// with, and upTo, the id it ended at — its last id, or "" for the final
// page. A page answers for the ids after the previous page's upTo up to and
// including its own (to the end, for the final page): an execution in that
// range and not in the page was not in the list as of ver.
type walkPage struct {
	ver  uint64
	upTo string
	rows []walkRow
}

// covers reports whether id is in the range the page answers for, the
// previous page's upTo being prevUpTo ("" before the first page).
func (pg walkPage) covers(prevUpTo, id string) bool {
	return id > prevUpTo && (pg.upTo == "" || id <= pg.upTo)
}

// walk reads the list page by page, each page inside the slot (who names
// the walk in the slot's log, maxWait bounds each page's wait as in
// readSlot.acquire), and hands each page to visit after the slot was
// released. It stops at the final page, at the first error — a page that
// failed (no ver consumed for it), the slot not taken, ctx ending — or once
// visit returns one; and after walkPages pages with errWalkPageCap. Pages
// visited before an error stand: each is true as of its own ver.
func (p *projector) walk(ctx context.Context, who string, maxWait time.Duration, visit func(walkPage) error) error {
	cursor := ""
	for pages := 0; ; pages++ {
		if pages == p.timing.walkPages {
			return fmt.Errorf("%w: stopped after %d pages, next cursor %q", errWalkPageCap, pages, cursor)
		}
		var rows []walkRow
		var next string
		st, err := p.slot.read(ctx, who, maxWait, func(ctx context.Context) error {
			var err error
			rows, next, err = p.rows.page(ctx, cursor, p.timing.walkLimit)
			return err
		})
		if err != nil {
			return err
		}
		if err := visit(walkPage{ver: st.Ver, upTo: next, rows: rows}); err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// page reads one list page after cursor through the engine's handler,
// built as the row read is (rowReader): method GET, path "/v1/executions",
// a query of limit and cursor only, no headers (the bare "pdx:<hostID>"
// principal), no body, the caller's context, a panic contained. It answers
// the page's rows and its next_cursor ("" on the final page).
//
// Anything but a 200 page whose rows come in increasing id order after
// cursor, and whose next_cursor (when there is one) is its last id, is an
// error — Nexen's list is ordered by id and its next cursor is the last id
// returned (store.List), and a walk relies on both: the range a page
// answers for, and a cursor that always moves forward, so a walk ends.
func (rr rowReader) page(ctx context.Context, cursor string, limit int) ([]walkRow, string, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/executions?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("nex list page after %q: %w", cursor, err)
	}
	res, panicked := rr.serve(req, listBodyLimit)
	switch {
	case panicked:
		return nil, "", fmt.Errorf("nex list page after %q: engine handler panicked", cursor)
	case res.overflow:
		return nil, "", fmt.Errorf("nex list page after %q: response exceeds %d bytes", cursor, listBodyLimit)
	case res.code() != http.StatusOK:
		return nil, "", fmt.Errorf("nex list page after %q: status %d: %s", cursor, res.code(), snippet(res.body.Bytes()))
	}
	// A JSON null unmarshals into a struct without error and would read as an
	// empty last page, which the seed takes as "nothing is listed any more".
	if bytes.Equal(bytes.TrimSpace(res.body.Bytes()), []byte("null")) {
		return nil, "", fmt.Errorf("nex list page after %q: body is null, not a page", cursor)
	}
	var body struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(res.body.Bytes(), &body); err != nil {
		return nil, "", fmt.Errorf("nex list page after %q: %w", cursor, err)
	}
	rows := make([]walkRow, 0, len(body.Items))
	last := cursor
	for _, item := range body.Items {
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &r); err != nil || r.ID <= last {
			return nil, "", fmt.Errorf("nex list page after %q: row %q is not after %q", cursor, r.ID, last)
		}
		d, err := digestOf(item)
		if err != nil {
			return nil, "", fmt.Errorf("nex list page after %q: row %s: %w", cursor, r.ID, err)
		}
		rows = append(rows, walkRow{id: r.ID, digest: d})
		last = r.ID
	}
	if body.NextCursor != "" && (len(rows) == 0 || body.NextCursor != last) {
		return nil, "", fmt.Errorf("nex list page after %q: next_cursor %q is not its last row %q", cursor, body.NextCursor, last)
	}
	return rows, body.NextCursor, nil
}
