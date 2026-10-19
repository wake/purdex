package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The owner of a label quarantines it (spec §7): every origin records how declared tasks came out and offers, on a read-only GET, the
// disagreements that concern a host (GET /v1/outcomes/disagreements?subject=<alias>); this host reads that of its own queue and of each
// reachable peer, and quarantines its own earned labels. No host writes to another: the only writer of a host's labels is the host.

// LabelKeeper is this host's earned-label table, as far as the quarantine needs it (store.Store).
type LabelKeeper interface {
	EarnedLabel(label string) (wire.EarnedLabel, bool, error)
	QuarantineEarnedLabel(label string, at time.Time) (changed bool, err error)
	SetEarnedLabelState(label, state string) (ok bool, err error)
	DeleteEarnedLabel(label string) error
}

// CacheDropper drops the reuse entries this host made under a label (store.Store).
type CacheDropper interface {
	DeleteCacheByLabelAndHost(label, host string) (int, error)
}

// QuarantinePeriod is how often the quarantine pass runs: not with every facts record (a disagreement is rare and a label stays
// advertised for at most this long after the origin records it).
const QuarantinePeriod = 60 * time.Second

// maxPagesPerPass bounds how much one pass reads from one origin: more waits for the next pass.
const maxPagesPerPass = 10

// ErrNoSuchLabel: the label is not in this host's earned-label table.
var ErrNoSuchLabel = errors.New("daemon: no such earned label")

// ApplyDisagreement quarantines the label a disagreement names, if it is this host's, earned now, and was granted before the
// disagreement (a disagreement read again, or from before the label was earned anew, takes nothing away). It then drops the reuse
// entries this host made under the label, and tells the daemon so the label stops being advertised at once. changed says whether the
// label was quarantined by this call.
func (d *Daemon) ApplyDisagreement(x wire.Disagreement) (changed bool, err error) {
	if d.opts.Labels == nil || x.Host != d.opts.Alias {
		return false, nil
	}
	changed, err = d.opts.Labels.QuarantineEarnedLabel(x.Label, x.At)
	if err != nil || !changed {
		return false, err
	}
	d.afterQuarantine(x.Label)
	// the exception: the label was taken from the host, so it says so, with the evidence
	log.Printf("wwd: quarantine: %s quarantined: %s and %s disagree on tree %s (jobs %s and %s)", x.Label, x.Host, x.OtherHost, x.Tree, x.Jobs[0], x.Jobs[1])
	return true, nil
}

// afterQuarantine drops the entries the host made under the label (the lookup would not answer from them without the label, so a failure
// is logged, not retried) and announces the labels.
func (d *Daemon) afterQuarantine(label string) {
	if d.opts.CacheDrop != nil {
		if n, err := d.opts.CacheDrop.DeleteCacheByLabelAndHost(label, d.opts.Alias); err != nil {
			log.Printf("wwd: quarantine: dropping the reuse entries of %s: %v", label, err)
		} else if n > 0 {
			log.Printf("wwd: quarantine: %d reuse entries of %s dropped", n, label)
		}
	}
	d.RefreshLabels()
}

// Quarantine quarantines a label by hand (pdx wwd quarantine). ErrNoSuchLabel when this host has no such earned label.
func (d *Daemon) Quarantine(label string) error {
	if d.opts.Labels == nil {
		return ErrNoSuchLabel
	}
	ok, err := d.opts.Labels.SetEarnedLabelState(label, wire.EarnedLabelQuarantined)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoSuchLabel
	}
	d.afterQuarantine(label)
	log.Printf("wwd: quarantine: %s quarantined by hand", label)
	return nil
}

// Release lets go of a label by hand (pdx wwd release): the row is deleted, so the host does not hold the label until it qualifies for
// it again (a release does not restore the label: whatever made the quarantine is not known to be gone). ErrNoSuchLabel when there is none.
func (d *Daemon) Release(label string) error {
	if d.opts.Labels == nil {
		return ErrNoSuchLabel
	}
	if _, ok, err := d.opts.Labels.EarnedLabel(label); err != nil {
		return err
	} else if !ok {
		return ErrNoSuchLabel
	}
	if err := d.opts.Labels.DeleteEarnedLabel(label); err != nil {
		return err
	}
	d.RefreshLabels()
	log.Printf("wwd: quarantine: %s released by hand", label)
	return nil
}

// quarantineState is what the pass remembers between runs, in memory only: where it has read to in each origin, and what it last said
// about an origin it could not read (said once, not once a minute).
type quarantineState struct {
	mu     sync.Mutex
	cursor map[string]int64  // origin alias → the highest disagreement id handled
	failed map[string]string // origin alias → the error last logged
}

// QuarantinePass reads the disagreements of this host's own queue and of each peer once and applies them. A peer that does not answer
// is left for the next pass; one that answers 404 is an older build with nothing to offer. A page that is not valid is refused whole.
// The cursor of an origin moves past a disagreement only once it is handled, so a failure to write is met again on the next pass.
func (d *Daemon) QuarantinePass(ctx context.Context) {
	if d.opts.Labels == nil || d.opts.Alias == "" {
		return
	}
	d.quar.mu.Lock()
	defer d.quar.mu.Unlock()
	if d.quar.cursor == nil {
		d.quar.cursor, d.quar.failed = map[string]int64{}, map[string]string{}
	}
	if d.opts.Queue != nil {
		d.readOrigin(ctx, d.opts.Alias, d.opts.Queue.Disagreements)
	}
	for _, p := range d.opts.Peers {
		p := p
		d.readOrigin(ctx, p.Alias, func(ctx context.Context, host string, after int64, limit int) ([]wire.Disagreement, bool, error) {
			return d.peerDisagreements(ctx, p, host, after, limit)
		})
	}
}

// peerDisagreements is GET /v1/outcomes/disagreements of a peer; a 404 is an older build (nothing to offer).
func (d *Daemon) peerDisagreements(ctx context.Context, p PeerAddr, host string, after int64, limit int) ([]wire.Disagreement, bool, error) {
	q := url.Values{"subject": {host}, "after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	list, err := get[wire.DisagreementList](ctx, d.pool.client, p, d.opts.Token, "/v1/outcomes/disagreements?"+q.Encode())
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := list.Validate(); err != nil {
		return nil, false, err
	}
	if list.Host != host || list.Origin != p.Alias {
		return nil, false, fmt.Errorf("disagreements: answer for %q from %q", list.Host, list.Origin)
	}
	return list.Items, list.More, nil
}

// readOrigin reads pages from one origin from its cursor on and applies them. The caller holds quar.mu.
func (d *Daemon) readOrigin(ctx context.Context, origin string, read func(ctx context.Context, host string, after int64, limit int) ([]wire.Disagreement, bool, error)) {
	fail := func(err error) {
		msg := err.Error()
		if d.quar.failed[origin] != msg {
			d.quar.failed[origin] = msg
			log.Printf("wwd: quarantine: reading the disagreements of %s: %v", origin, err)
		}
	}
	for page := 0; page < maxPagesPerPass; page++ {
		items, more, err := read(ctx, d.opts.Alias, d.quar.cursor[origin], wire.MaxDisagreements)
		if err != nil {
			fail(err)
			return
		}
		delete(d.quar.failed, origin)
		for _, x := range items {
			if err := x.Validate(); err != nil {
				fail(err)
				return
			}
			if x.ID <= d.quar.cursor[origin] {
				fail(fmt.Errorf("disagreement %d is not after the cursor %d", x.ID, d.quar.cursor[origin]))
				return
			}
			if _, err := d.ApplyDisagreement(x); err != nil {
				fail(err)
				return
			}
			d.quar.cursor[origin] = x.ID
		}
		if !more || len(items) == 0 {
			return
		}
	}
}

// RunQuarantine runs QuarantinePass now and then every period until ctx ends.
func (d *Daemon) RunQuarantine(ctx context.Context, every time.Duration) {
	d.QuarantinePass(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.QuarantinePass(ctx)
		}
	}
}

// labelRequest is the body of POST /v1/earned-labels/quarantine and /release.
type labelRequest struct {
	Label string `json:"label"`
}

// quarantineRoutes registers POST /v1/earned-labels/quarantine and /release on the local socket only: a host's labels are changed by
// that host (pdx wwd quarantine|release), never over TCP and never forwarded with ?host=. Without a store they are not registered.
func (d *Daemon) quarantineRoutes(mux *http.ServeMux) {
	if d.opts.Labels == nil {
		return
	}
	mux.HandleFunc("POST /v1/earned-labels/quarantine", d.localOnly(d.handleLabelChange(d.Quarantine)))
	mux.HandleFunc("POST /v1/earned-labels/release", d.localOnly(d.handleLabelChange(d.Release)))
}

func (d *Daemon) handleLabelChange(change func(label string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req labelRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || !wire.ValidTaskLabel(req.Label) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"label\": \"task:<repo>/<task>\"}"})
			return
		}
		switch err := change(req.Label); {
		case errors.Is(err, ErrNoSuchLabel):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, map[string]string{"label": req.Label})
		}
	}
}
