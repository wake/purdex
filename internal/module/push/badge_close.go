package push

import (
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/push"
)

// The background badge push (badge spec §3.1, BG-1b): when a card the badge counts closes — whichever way, the team module
// broadcasts one `closed` for all of them — the phones that asked (prefs.badge_push) are told silently, so their App-icon
// badge drops while the App is in the background. Closes are coalesced before the sender queue.

// closeWindow is how long the first close of a burst waits for the others. It is fixed, not sliding: the latency of a push
// is bounded by it however long the burst lasts.
const closeWindow = 2 * time.Second

// closeCoalescer collects the ids of closed cards and delivers them as one batch when the window that the first one opened
// ends. A close inside a window only adds its id; the deadline never moves.
type closeCoalescer struct {
	window   time.Duration
	schedule func(d time.Duration, fn func()) (cancel func()) // time.AfterFunc; a test seam
	fire     func(ids []string)

	mu      sync.Mutex
	gen     uint64 // the window that is open; a timer callback may only end the window it was made for
	pending bool
	ids     []string
	cancel  func()
	stopped bool
}

func newCloseCoalescer(fire func(ids []string)) *closeCoalescer {
	return &closeCoalescer{window: closeWindow, fire: fire, schedule: func(d time.Duration, fn func()) func() {
		t := time.AfterFunc(d, fn)
		return func() { t.Stop() }
	}}
}

// add records a closed card. After stop (until reset) it records nothing.
func (c *closeCoalescer) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if !c.pending {
		c.pending = true
		c.ids = nil
		c.gen++
		gen := c.gen
		c.cancel = c.schedule(c.window, func() { c.flush(gen) })
	}
	for _, have := range c.ids {
		if have == id {
			return
		}
	}
	c.ids = append(c.ids, id)
}

// flush ends window gen: the batch goes to fire. A callback of any other window (one that was already running when stop
// cancelled its timer, which Timer.Stop cannot recall) finds a different generation and does nothing. fire runs under the
// lock on purpose: it only reads the device list and enqueues without waiting, and holding the lock makes stop wait for a
// fire in flight, so once stop returns nothing of the old run can still be enqueued.
func (c *closeCoalescer) flush(gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || !c.pending || gen != c.gen {
		return
	}
	ids := c.ids
	c.pending, c.ids, c.cancel = false, nil, nil
	c.fire(ids)
}

// stop cancels a window that has not ended: nothing is delivered, and nothing is collected until reset.
func (c *closeCoalescer) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.gen++ // whatever callback is still on its way is of an older window now
	if c.cancel != nil {
		c.cancel()
	}
	c.pending, c.ids, c.cancel = false, nil, nil
}

// reset opens the coalescer for a new run (Start).
func (c *closeCoalescer) reset() {
	c.mu.Lock()
	c.stopped = false
	c.mu.Unlock()
}

// fireBadge turns one window's batch into a Job for the devices that asked. The sender reads the open set when it processes
// the job (the freshest value) and sends nothing if that read fails. A full queue drops the job: best effort, the next push
// or the next foreground corrects the badge.
func (m *Module) fireBadge(ids []string) {
	snd := m.sender.Load()
	if snd == nil {
		return
	}
	var devs []string
	for _, d := range m.snapshot() {
		if d.Prefs.BadgePush {
			devs = append(devs, d.DeviceID)
		}
	}
	if len(devs) == 0 {
		return
	}
	if !snd.Enqueue(Job{DeviceIDs: devs, RequireOpen: true, Make: func(d push.Device) (push.Content, bool) {
		if !d.Prefs.BadgePush { // it may have changed since the job was queued
			return push.Content{}, false
		}
		return push.Content{Kind: "badge", Background: true, ClosedApprovalIDs: ids}, true
	}}) {
		log.Printf("[push] badge job dropped: the send queue is full (%d closed card(s); the next push or foreground corrects the badge)", len(ids))
	}
}
