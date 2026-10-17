package watchmod

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// Registration, announcement and "two awake at once" (host-load-control spec §12.3–§12.4, plan W3a step 2): a host that registers
// a new watcher frame tells the other hosts, and when two watchers are awake the daemon of the one that has been awake the
// shorter time tells its own watcher to stand down.

// pathAnnounce is the peer route a host announces a newly registered watcher on (an older daemon answers 404, which is ignored).
const pathAnnounce = "/api/peers/watch/announce"

// announceBody is the request of pathAnnounce: the announcing host's holder, as its holder answer says it.
type announceBody struct {
	ToHostID string     `json:"to_host_id"`
	Holder   HolderView `json:"holder"`
}

// maxAnnounceBody bounds the announce request.
const maxAnnounceBody = 8 << 10

// standDownBudget bounds the settle that follows a registration (the other hosts' answers, then the notice).
const standDownBudget = 15 * time.Second

// StandDowner delivers the stand-down notice to this host's own watcher (a [負載守門] message through peers.Sender): the holder
// is the watcher to tell, winner the answer of the watcher that stays.
type StandDowner interface {
	StandDown(ctx context.Context, own watch.Holder, winner watch.HolderAnswer) error
}

// twoAwake is the module's state for the rule: the frames this host already told to stand down (once per frame).
type twoAwake struct {
	mu     sync.Mutex
	frames map[string]*standState // by frame id
}

// standState is where the notice for one frame is: being sent (inflight is open), or delivered.
type standState struct {
	delivered bool
	inflight  chan struct{}
}

// onRegistered is the registration hook (reconcile finds a new watcher frame): announce it to the other hosts, then apply the
// two-awake rule against what they answer. It runs after the reconcile lock is released.
func (m *Module) onRegistered(h watch.Holder) {
	ctx, done, ok := m.enterFor(context.Background(), standDownBudget)
	if !ok {
		return
	}
	defer done()
	// The hooks run in the order the frames were committed, and an earlier one may have waited behind a slow peer: a frame that is
	// no longer this host's watcher is not announced (the current one has its own turn).
	if cur := m.hs().Get(); cur.State != watch.HolderAwake || cur.FrameID != h.FrameID {
		return
	}
	m.announce(ctx, h)
	m.settle(ctx)
}

// otherTargets are the hosts in targets other than this one (nil when the setting cannot be read).
func (m *Module) otherTargets() []string {
	s, err := m.readSettings()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range s.Targets {
		if !m.isSelf(a) {
			out = append(out, a)
		}
	}
	return out
}

// announce tells every other target about h, side by side; a host that does not know the route (404) or cannot be reached is
// only logged, because the rule needs no message: that host asks again, and an announce that arrives late reaches the same answer.
func (m *Module) announce(ctx context.Context, h watch.Holder) {
	if m.caller == nil {
		return
	}
	view := m.viewOf(h)
	var wg sync.WaitGroup
	for _, alias := range m.otherTargets() {
		entry, _, ok := m.peerEntry(alias)
		if !ok || entry.HostID == "" {
			continue
		}
		wg.Add(1)
		go func(alias, hostID string) {
			defer wg.Done()
			res := m.caller.Call(ctx, hostID, pathAnnounce, announceBody{ToHostID: hostID, Holder: view})
			switch res.Class {
			case peers.ClassDone, peers.ClassUnsupported:
			default:
				m.logf("[watch] announce to %s: %s %s", alias, res.Class, res.Detail)
			}
		}(alias, entry.HostID)
	}
	wg.Wait()
}

// settle applies the two-awake rule for this host's awake watcher against the other targets' holder answers: when one of them
// wins, this host's own watcher is told to stand down.
func (m *Module) settle(ctx context.Context) {
	own := m.hs().Get()
	if own.State != watch.HolderAwake {
		return
	}
	mine := answerOf(m.viewOf(own))
	var winner *watch.HolderAnswer
	for _, alias := range m.otherTargets() {
		a, err := (asker{m}).Holder(ctx, alias)
		if err != nil || a.State != watch.HolderAwake || a.SessionID == "" {
			continue
		}
		if watch.Earlier(a, mine) && (winner == nil || watch.Earlier(a, *winner)) {
			a := a
			winner = &a
		}
	}
	if winner != nil {
		m.standDown(ctx, own, *winner)
	}
}

// standDown tells this host's watcher (the frame own) to stand down, once per frame: the announce and the settle can both lead
// here for the same pair, and the watcher must get one notice. A trigger that finds the notice being sent waits for its outcome:
// if it was delivered there is nothing to do, if it failed this trigger sends it itself (a failed send is not lost to the trigger
// that arrived while it was in flight).
func (m *Module) standDown(ctx context.Context, own watch.Holder, winner watch.HolderAnswer) {
	if own.FrameID == "" {
		return
	}
	for {
		// The watcher to tell is the one that is awake NOW: the answers of the other hosts took time, and a watcher that restarted in
		// between is another frame.
		if cur := m.hs().Get(); cur.State != watch.HolderAwake || cur.FrameID != own.FrameID {
			return
		}
		m.two.mu.Lock()
		if m.two.frames == nil {
			m.two.frames = map[string]*standState{}
		}
		st := m.two.frames[own.FrameID]
		switch {
		case st != nil && st.delivered:
			m.two.mu.Unlock()
			return
		case st != nil && st.inflight != nil:
			wait := st.inflight
			m.two.mu.Unlock()
			select {
			case <-wait:
				continue // delivered, or failed and removed: look again
			case <-ctx.Done():
				return
			}
		}
		st = &standState{inflight: make(chan struct{})}
		m.two.frames[own.FrameID] = st
		m.two.mu.Unlock()

		err := errors.New("no stand-down sender")
		if m.standDowner != nil {
			err = m.standDowner.StandDown(ctx, own, winner)
		}
		m.two.mu.Lock()
		if err == nil {
			st.delivered = true
		} else {
			delete(m.two.frames, own.FrameID) // not told: a later trigger tries again
		}
		close(st.inflight)
		st.inflight = nil
		m.two.mu.Unlock()
		if err != nil {
			m.logf("[watch] stand-down of %s (the watcher of %s stays): %v", own.SessionID, winner.HostID, err)
			return
		}
		m.logf("[watch] this host's watcher %s was told to stand down: the one on %s has been awake longer", own.SessionID, winner.HostID)
		return
	}
}

// handlePeerAnnounce serves POST /api/peers/watch/announce: a paired host registered a watcher. The order is the holder route's
// (the binding of the host principal, the rate limit and the body cap before the body is decoded, then to_host_id). The announced
// watcher makes the locator look again if it beats the remembered one, and if this host's own watcher is awake and loses to it,
// that watcher is told to stand down.
func (m *Module) handlePeerAnnounce(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peers.BindHostPrincipal(r, "watch announce", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] announce refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req announceBody
	switch st := peers.AdmitDecode(w, r, m.limit, entry.HostID, maxAnnounceBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 8 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	// The announcer speaks for itself only: its holder names its own host id, and a watcher that is awake with a session.
	h := req.Holder
	if h.HostID != entry.HostID || h.State != string(watch.HolderAwake) || h.SessionID == "" || strings.TrimSpace(h.SessionID) != h.SessionID {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "the holder must be this host's own awake watcher")
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host_id": ours}) // the caller checks the answer names the host it asked
	// The announce is a hint to look, not the truth: what it says (a session, a since) is the announcer's claim and may be late or
	// wrong. After the answer, this host asks the announcer for its holder as it is now and decides on that: it switches the
	// locator to it if it beats the remembered watcher, and tells its own watcher to stand down if that one loses.
	alias := entry.Alias
	go func() {
		ctx, done, ok := m.enterFor(context.Background(), standDownBudget)
		if !ok {
			return
		}
		defer done()
		ans, err := (asker{m}).Holder(ctx, alias)
		if err != nil || ans.State != watch.HolderAwake || ans.SessionID == "" {
			return // nothing awake there now
		}
		m.loc.Prefer(ans)
		own := m.hs().Get()
		if own.State == watch.HolderAwake && watch.Earlier(ans, answerOf(m.viewOf(own))) {
			m.standDown(ctx, own, ans)
		}
	}()
}

// standDownText is what the notice says; fixed text but for the winning host's id.
func standDownText(winner watch.HolderAnswer) string {
	return "[負載守門] 另一台主機（" + winner.HostID + "）的守門員比你醒得久，之後事件都送給那一位。請把你的筆記寫好並推送，然後結束這個 session（/exit）。"
}

// peerStandDowner delivers the notice through the peers module: from the watcher's own inbox (a notice about itself, so the
// self-notice sender, which alone skips the self_target refusal), to its own address by ref. A watcher that restarted since has
// another session and another ref, so the notice for the old one cannot reach the new one.
type peerStandDowner struct{ m *Module }

func (s peerStandDowner) StandDown(ctx context.Context, own watch.Holder, winner watch.HolderAnswer) error {
	return peerNotifier(s).Notify(ctx, own, standDownText(winner))
}

// WatcherNotifier delivers a [負載守門] text to this host's own watcher: the stand-down notice and the events.
type WatcherNotifier interface {
	Notify(ctx context.Context, own watch.Holder, text string) error
}

// peerNotifier is the WatcherNotifier through the peers module (see peerStandDowner for the origin and the address).
type peerNotifier struct{ m *Module }

func (s peerNotifier) Notify(ctx context.Context, own watch.Holder, text string) error {
	return peers.NotifySelf(ctx, s.m.core, s.m.logf, own.SessionID, text)
}
