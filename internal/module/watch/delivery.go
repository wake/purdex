package watchmod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// Delivery of events to the watcher (host-load-control spec §12.3, plan W2b-1). The resources module records an event and hands the
// record to this module (watch.EventSink); the outbox folds and queues it; a pass finds the watcher (the locator) and sends the oldest
// message: on this host straight to the watcher, on another through the peer route pathEvent, whose daemon notifies its own watcher
// from the watcher's own inbox (a daemon has no session of its own to send as). The sender removes a message only on a clear
// success; the receiver remembers the event ids it delivered, so a retry or a lost reply never gives the watcher an event twice.

// pathEvent is the peer route a host sends an event message to the host of the watcher on.
const pathEvent = "/api/peers/watch/event"

const (
	maxEventBody   = 32 << 10
	maxEventText   = 8 << 10
	maxEventIDs    = 300
	retryBase      = 30 * time.Second
	retryMax       = 5 * time.Minute
	idleWake       = 10 * time.Minute
	backlogHistory = 30 * 24 * time.Hour
)

var eventIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)

// eventMsg is one message on the wire (the outbox's Message without its retry state).
type eventMsg struct {
	ID       string   `json:"id"`
	Backlog  bool     `json:"backlog,omitempty"`
	Host     string   `json:"host,omitempty"`
	EventIDs []string `json:"event_ids"`
	Text     string   `json:"text"`
}

type eventBody struct {
	ToHostID string   `json:"to_host_id"`
	Message  eventMsg `json:"message"`
}

// deliveryState is the module's delivery part: the queue, the receiving side's remembered ids and the loop's wake-up.
type deliveryState struct {
	outbox   *watch.Outbox
	inbox    *watch.Inbox
	recvMu   sync.Mutex // one message at a time is checked, notified and marked
	kick     chan struct{}
	notifier WatcherNotifier // nil: the peers module's (made at Start)
	// handled says whether an event has been dealt with (the watcher's ack, W2b-2); nil: none is.
	handled func(id string) bool
	logDir  string
}

var errNoWatcherHere = errors.New("no watcher is awake on this host")

// errRefusedForGood is a refusal that sending the same message again cannot change (the receiver says it is malformed or too big).
var errRefusedForGood = errors.New("refused for good")

// initDelivery opens the outbox and the delivered ids beside holder.json; a file that cannot be read is set aside (and logged), and the
// daemon goes on with an empty one.
func (m *Module) initDelivery(dir string) {
	m.dlv.kick = make(chan struct{}, 1)
	outPath, inPath := "", ""
	if dir != "" {
		outPath, inPath = filepath.Join(dir, "outbox.json"), filepath.Join(dir, "delivered.json")
		m.dlv.logDir = dir
	}
	var err error
	defer func() { m.dlv.outbox.SetLogf(func(f string, a ...any) { m.logf(f, a...) }) }()
	if m.dlv.outbox, err = watch.OpenOutbox(outPath, func() time.Time { return m.now() }); err != nil {
		m.logf("[watch] %v", err)
	}
	if m.dlv.inbox, err = watch.OpenInbox(inPath); err != nil {
		m.logf("[watch] %v", err)
	}
}

// Enqueue is the EventSink: the record goes to the outbox and the delivery loop is woken.
func (m *Module) Enqueue(rec watch.Record) {
	m.dlv.outbox.Add(rec)
	m.wakeDelivery()
}

func (m *Module) wakeDelivery() {
	select {
	case m.dlv.kick <- struct{}{}:
	default:
	}
}

// deliveryLoop runs a pass when woken (an event, a change of the remembered watcher), when a folded window ends, and while a message
// waits for a retry.
func (m *Module) deliveryLoop(ctx context.Context) {
	for {
		m.deliverPass(ctx)
		wait := idleWake
		if m.dlv.outbox.Pending() > 0 {
			wait = retryBase
		}
		if due, ok := m.dlv.outbox.NextFold(); ok {
			if d := due.Sub(m.now()); d < wait {
				wait = max(d, time.Second)
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-m.dlv.kick:
		case <-t.C:
		}
		t.Stop()
	}
}

// retryDue says whether a message that failed before may be tried again (30 s, doubling, up to 5 min).
func (m *Module) retryDue(msg watch.Message) bool {
	if msg.Attempts == 0 {
		return true
	}
	d := retryBase << min(msg.Attempts-1, 4)
	return m.now().Sub(msg.LastTry) >= min(d, retryMax)
}

// deliverPass sends what is queued to the watcher, oldest first, and stops at the first failure.
func (m *Module) deliverPass(ctx context.Context) {
	m.dlv.outbox.FlushFolded()
	s, err := m.readSettings()
	if err != nil {
		return
	}
	var where watch.Where
	if m.dlv.outbox.Pending() > 0 {
		cctx, done, ok := m.enter(ctx)
		if !ok {
			return
		}
		defer done()
		if where, err = m.loc.Current(cctx, s.Targets, s.AutoWake); err != nil {
			return // nobody to send to now: the queue waits
		}
	} else {
		cur, _ := m.loc.Remembered() // no events waiting: look only where the watcher is known to be, never search for the backlog
		if cur == nil {
			return
		}
		where = *cur
	}
	if where.State != watch.HolderAwake {
		return
	}
	if where.SessionID != m.dlv.outbox.BacklogFor() {
		m.makeBacklog(where.SessionID)
	}
	for {
		msg, ok := m.dlv.outbox.Next()
		if !ok || !m.retryDue(msg) {
			return
		}
		cctx, done, ok := m.enter(ctx)
		if !ok {
			return
		}
		err := m.sendMessage(cctx, where, msg)
		done()
		if errors.Is(err, errRefusedForGood) {
			// a poison message must not hold the ones behind it: it is dropped and logged (it stays in the record and in the log)
			m.dlv.outbox.Drop(msg.ID)
			m.logf("[watch] event message %s dropped: %v", msg.ID, err)
			continue
		}
		if err != nil {
			m.dlv.outbox.Tried(msg.ID)
			m.loc.Failed(where) // the next pass looks for the watcher again (W2a)
			m.logf("[watch] event message %s to %s: %v", msg.ID, where.Alias, err)
			return
		}
		m.dlv.outbox.Delivered(msg.ID)
	}
}

// makeBacklog queues the summary of this host's events that no watcher has dealt with, for a watcher session not told yet.
func (m *Module) makeBacklog(session string) {
	recs, err := watch.ReadLog(m.dlv.logDir, m.now().Add(-backlogHistory), "")
	if err != nil {
		m.logf("[watch] backlog: %v", err)
		return // not marked: the next pass tries again
	}
	if m.dlv.handled != nil {
		kept := recs[:0]
		for _, r := range recs {
			if !m.dlv.handled(r.ID) {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	m.dlv.outbox.Backlog(session, recs)
}

// sendMessage hands msg to the watcher at where: on this host straight to its notifier, on another through the peer route.
func (m *Module) sendMessage(ctx context.Context, where watch.Where, msg watch.Message) error {
	em := eventMsg{ID: msg.ID, Backlog: msg.Backlog, Host: msg.Host, EventIDs: msg.EventIDs, Text: msg.Text}
	m.core.CfgMu.RLock()
	ownID := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	if where.HostID == ownID {
		return m.receiveEvent(ctx, em)
	}
	if m.caller == nil {
		return errors.New("no host caller")
	}
	res := m.caller.Call(ctx, where.HostID, pathEvent, eventBody{ToHostID: where.HostID, Message: em})
	switch res.Class {
	case peersmod.ClassDone:
		return nil
	case peersmod.ClassUnsupported:
		return errors.New("that host has no event delivery")
	case peersmod.ClassRefused:
		if res.Status == http.StatusBadRequest || res.Status == http.StatusRequestEntityTooLarge {
			return fmt.Errorf("%w: %d %s %s", errRefusedForGood, res.Status, res.Code, res.Detail)
		}
	}
	return fmt.Errorf("%s %d %s", res.Class, res.Status, res.Detail)
}

// receiveEvent delivers a message to this host's own awake watcher, once per set of event ids. A host whose holder says awake but
// whose watcher cannot be told reconciles (the watcher may be gone).
func (m *Module) receiveEvent(ctx context.Context, em eventMsg) error {
	m.dlv.recvMu.Lock()
	defer m.dlv.recvMu.Unlock()
	if m.dlv.inbox.Seen(em.EventIDs) {
		return nil
	}
	m.fresh(ctx)
	own := m.hs().Get()
	if own.State != watch.HolderAwake {
		return errNoWatcherHere
	}
	n := m.dlv.notifier
	if n == nil {
		n = peerNotifier{m}
	}
	if err := n.Notify(ctx, own, em.Text); err != nil {
		if _, _, rerr := m.Reconcile(ctx); rerr != nil {
			m.logf("[watch] reconcile after a failed delivery: %v", rerr)
		}
		return err
	}
	if err := m.dlv.inbox.Mark(em.EventIDs); err != nil {
		m.logf("[watch] remember delivered events: %v", err)
	}
	now := m.now()
	if _, err := m.hs().UpdateCommitted(func(h *watch.Holder) { h.LastDelivery = now }); err != nil {
		m.logf("[watch] holder last delivery: %v", err)
	}
	return nil
}

// validEventMsg checks a message from another host: its text is a [負載守門] text of bounded size and its ids are plain ids.
func validEventMsg(em *eventMsg) string {
	if len(em.Text) == 0 || len(em.Text) > maxEventText || !strings.HasPrefix(em.Text, "[負載守門]") {
		return "the text must be a [負載守門] message of at most 8 KiB"
	}
	if err := ipeers.ValidateText(em.Text); err != nil {
		return err.Error()
	}
	if len(em.EventIDs) == 0 || len(em.EventIDs) > maxEventIDs {
		return "event_ids must hold 1 to 300 ids"
	}
	for _, id := range em.EventIDs {
		if !eventIDPattern.MatchString(id) {
			return "an event id has characters it may not have"
		}
	}
	if len(em.ID) == 0 || len(em.ID) > 80 {
		return "the message needs an id"
	}
	return ""
}

// handlePeerEvent serves POST /api/peers/watch/event: a paired host sends an event message for the watcher on this host. The order
// is the other watch routes': the binding of the host principal, the rate limit and the body cap, then to_host_id.
func (m *Module) handlePeerEvent(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, "watch event", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] event refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req eventBody
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxEventBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 32 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	if why := validEventMsg(&req.Message); why != "" {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, why)
		return
	}
	err := m.receiveEvent(r.Context(), req.Message)
	switch {
	case err == nil:
		m.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host_id": ours}) // the caller checks the answer names the host it asked
	case errors.Is(err, errNoWatcherHere):
		m.writeErr(w, http.StatusConflict, "no_watcher", err.Error())
	default:
		m.writeErr(w, http.StatusBadGateway, "notify_failed", err.Error())
	}
}
