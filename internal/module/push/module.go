// Package push is the "push" daemon module: phone push notifications through APNs (spec
// docs/specs/2026-10-09-push-spec.md). It is mounted only when config.toml has a [push] section (cmd/pdx/main.go), so
// its routes, and the `push.v1` capability, exist only then.
//
// Push is not a required feature, so a key that cannot be loaded does not stop the daemon: Init records why in init_error
// and returns nil, the module serves no route, announces nothing (/api/info reports push.ready=false and the error) and
// does nothing at Start. Nothing it says carries key material.
//
// This step (PU-1) holds the device registry: the key is loaded and validated at Init, devices are registered through
// POST/GET /api/push/devices and DELETE /api/push/devices/{device_id}, stored in push.db and mirrored in memory. The
// full token never leaves the module: responses carry the masked token and the device id, log lines the masked token.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/module/agent"
	devicesmod "github.com/wake/purdex/internal/module/devices"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/push/apnskey"
	"github.com/wake/purdex/internal/reads"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/workbooklines"
)

const maxBody = 64 << 10 // device registration body cap (spec §4.1)

// Module is the push module.
type Module struct {
	followed atomic.Bool // the revoke feed is subscribed (once per module)
	core     *core.Core
	store    *Store
	key      apnskey.Key
	home     func() (string, error) // the daemon user's home; injectable for tests

	// mu orders every write: the store first, the cache after it succeeds, both under the lock, so the cache never
	// holds a row the store does not.
	mu      sync.Mutex
	devices map[string]push.Device // by device id
	ready   bool                   // the key loaded and the store opened; false = soft-failed (initErr says why)
	initErr string

	// The trigger side (started in Start, only when ready). events / newAPNs / presence are seams: nil = the real ones.
	events   team.ApprovalEvents
	notify   agent.NotifyFeed // the agent module's live hook frames; nil = from the registry
	newAPNs  func() apnsClient
	presence presenceChecker        // what the gates ask; nil = pres
	pres     *Presence              // what PUT /api/push/presence feeds
	sender   atomic.Pointer[sender] // read by the approval callback, which Stop does not wait for
	unsub    func()

	// The agent-event side (agent_trigger.go).
	gate           *Gate
	asks           *openAsks
	reader         team.OpenApprovalsReader                                         // fresh read of the open set at send time (purdex.open_approvals); nil = unknown
	sessions       func(ctx context.Context) (map[string]session.SessionRef, error) // ONE read of every tmux session (name -> code, creation time); nil = from the session module
	sessBudget     time.Duration                                                    // how long a snapshot waits for that read; 0 = sessionBudget
	sessBusy       atomic.Bool                                                      // a session read is still running: never more than one outstanding
	sessStalled    atomic.Bool                                                      // the "still busy" line was already logged for this stall
	membership     func(sessionID string) (bool, error)                             // a test seam: team.MembershipReader; nil = from the registry
	memBudget      time.Duration                                                    // how long an event waits for the membership read; 0 = membershipBudget
	memBusy        atomic.Bool                                                      // a membership read is still running: never more than one outstanding
	rs             *readSync                                                        // read sync: the pending reads per registration and their flush timers (readsync.go)
	regSeq         map[string]uint64                                                // the order registrations were made in (this run), to tell a rotated token from the one it replaced
	regNext        uint64
	readsFeed      reads.Feed // a test seam: the reads module's feed; nil = from the registry
	unsubReads     func()
	closes         *closeCoalescer // closed badge cards waiting for their 2 s window (badge_close.go); fires into the sender's queue
	holds          holdSet
	ap             approvalPush               // the state of each approval push (approval_push.go)
	apClock        func() time.Time           // a test seam: nil = time.Now
	apAfterRead    func(id string)            // a test seam: runs between the late check's read of the card and its push path
	apAfterReserve func(id string)            // a test seam: runs after a push path reserved the card, before it enqueues
	wbHolds        wbHoldSet                  // Stop pushes held for the session workbook's line (agent_trigger.go)
	wbWait         func() time.Duration       // a test seam: the push_wait_s setting; nil = from the registry
	wbLines        func() workbooklines.Lines // a test seam: the workbook's waiter; nil = from the registry
	holdFor        time.Duration              // how long a waiting event waits for its hook_ask (spec §5.2 rule 8); a test seam
	unsubNotify    func()
}

// presenceChecker answers "does a present Mac show this session" (push spec R6, §5.4): by tmux session name for an
// approval, by session code for an agent event.
type presenceChecker interface {
	ShowsName(tmuxSession string) bool
	ShowsCode(code string) bool
	AtComputer() bool // PA-1: some Mac says the person is at the machine now (v2 reports)
}

func New() *Module {
	m := &Module{home: os.UserHomeDir, devices: map[string]push.Device{}, pres: NewPresence(time.Now),
		gate: NewGate(time.Now), asks: newOpenAsks(time.Now), holdFor: waitingHold}
	m.closes = newCloseCoalescer(m.fireBadge)
	m.regSeq = map[string]uint64{}
	m.rs = newReadSync(time.Now, time.Duration(config.DefaultReadFlushMinutes)*time.Minute, m.fireReadsFlush)
	return m
}

func (m *Module) Name() string           { return "push" }
func (m *Module) Dependencies() []string { return []string{"team", "agent"} }

// Init loads the APNs key, opens push.db and reads every device into the cache. Any failure is recorded (without key
// material) and leaves the module off; Init itself returns nil so the daemon starts (push spec §3).
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.CfgMu.RLock()
	cfg := *c.Cfg
	c.CfgMu.RUnlock()
	m.initReadSync(cfg)
	if err := m.load(c); err != nil {
		m.mu.Lock()
		m.ready, m.initErr = false, err.Error()
		m.mu.Unlock()
		log.Printf("[push] disabled: %v", err)
		return nil
	}
	m.mu.Lock()
	m.ready, m.initErr = true, ""
	m.mu.Unlock()
	return nil
}

func (m *Module) load(c *core.Core) error {
	c.CfgMu.RLock()
	dir, dataDir := c.Cfg.PushAPNsDir(), c.Cfg.DataDir
	c.CfgMu.RUnlock()
	if dir == "" {
		return errors.New("push: no apns_dir configured")
	}
	dir, err := m.expand(dir)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	key, err := apnskey.Load(dir)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	store, err := OpenStore(filepath.Join(dataDir, "push.db"))
	if err != nil {
		return errors.New("push: the device store cannot be opened")
	}
	list, err := store.List()
	if err != nil {
		store.Close()
		return errors.New("push: the device store cannot be read")
	}
	m.key, m.store = key, store
	m.devices = make(map[string]push.Device, len(list))
	for _, d := range list {
		m.devices[d.DeviceID] = d
	}
	return nil
}

// Status is what /api/info reports under "push" (alongside the core's "configured").
func (m *Module) Status() map[string]any {
	entries, active := m.pres.Counts() // its own lock; taken before m.mu, never inside it
	v2, atComputer := m.pres.V2Counts()
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr, "presence_entries": entries, "presence_active": active,
		"presence_v2_entries": v2, "presence_at_computer": atComputer}
}

func (m *Module) isReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

func (m *Module) expand(dir string) (string, error) {
	if dir != "~" && !strings.HasPrefix(dir, "~/") {
		return dir, nil
	}
	home, err := m.home()
	if err != nil || home == "" {
		return "", errors.New("cannot expand ~ in apns_dir")
	}
	return filepath.Join(home, strings.TrimPrefix(dir, "~")), nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if !m.isReady() {
		return // soft-failed: the routes do not exist (404), as if push were not configured
	}
	mux.HandleFunc("POST /api/push/devices", m.handlePost)
	mux.HandleFunc("GET /api/push/devices", m.handleList)
	mux.HandleFunc("DELETE /api/push/devices/{device_id}", m.handleDelete)
	mux.HandleFunc("PUT /api/push/presence", m.handlePresence)
}

// Start arms the approval trigger: the sender goroutine, then the subscription to the team module's approval feed. A
// module that is off (soft-failed) does nothing.
func (m *Module) Start(ctx context.Context) error {
	if !m.isReady() {
		return nil
	}
	m.followRevokes()
	if m.events == nil {
		svc, ok := m.core.Registry.Get(team.ApprovalEventsKey)
		if ev, isEv := svc.(team.ApprovalEvents); ok && isEv {
			m.events = ev
		} else {
			log.Printf("[push] the team module's approval feed is not available: approvals will not be pushed")
		}
	}
	if m.reader == nil && m.events != nil { // the team module is one object that is both the feed and the reader
		m.reader, _ = m.events.(team.OpenApprovalsReader)
	}
	if m.reader == nil {
		log.Printf("[push] no open-approvals reader: open_approvals is left out of pushes")
	}
	if m.sessions == nil {
		svc, _ := m.core.Registry.Get(session.RegistryKey)
		if l, ok := svc.(interface {
			SessionsByName(context.Context) (map[string]session.SessionRef, error)
		}); ok {
			m.sessions = l.SessionsByName
		} else {
			log.Printf("[push] no session lookup: approval pushes carry no session_code and badge keys fall back to approval ids")
		}
	}
	if m.newAPNs == nil {
		signer := apns.NewSigner(m.key, nil)
		m.newAPNs = func() apnsClient { return &apns.Client{HTTP: &http.Client{}, Signer: signer} }
	}
	if m.presence == nil {
		m.presence = m.pres
	}
	m.core.CfgMu.RLock()
	hostID := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	snd := newSender(m, m.newAPNs(), hostID, push.BundleID)
	snd.Start(ctx)
	snd.openState = m.openState
	m.sender.Store(snd)
	m.core.Registry.Register(team.PauseNotifyKey, m.pauseNotifier()) // the team module's pause outbox asks for a phone push through it
	m.holds.reset()
	m.ap.start()
	m.wbHolds.reset()
	m.closes.reset()
	snd.reads = m.rs
	m.followReads()
	m.asks.Clear() // a restart starts with no asks: the feed's open snapshot proves nothing was pushed (spec §4.4), so it is not loaded
	if m.events != nil {
		_, m.unsub = m.events.SubscribeApprovals(m.onApproval)
	}
	if m.notify == nil {
		svc, ok := m.core.Registry.Get(agent.NotifyFeedKey)
		if feed, isFeed := svc.(agent.NotifyFeed); ok && isFeed {
			m.notify = feed
		} else {
			log.Printf("[push] the agent module's hook feed is not available: agent events will not be pushed")
		}
	}
	if m.notify != nil {
		m.unsubNotify = m.notify.SubscribeNotify(m.onNotify)
	}
	log.Printf("[push] enabled (%v, %d device(s))", m.key, len(m.snapshot()))
	return nil
}

// followRevokes ties a registration's life to the paired phone that made it: a phone revoked now loses its registrations at
// once, and one revoked while push was down (or never existed) loses them here, at Start. Without the devices module there
// are no paired phones to follow and the registrations of phones are dropped as unverifiable.
func (m *Module) followRevokes() {
	svc, _ := m.core.Registry.Get(devicesmod.RevokeFeedKey)
	if feed, ok := svc.(devices.RevokeFeed); ok && m.followed.CompareAndSwap(false, true) { // once, however often Start runs
		feed.SubscribeRevoked(m.dropOwned)
	}
	live, _ := m.core.Registry.Get(devicesmod.RegistryKey)
	ref, _ := live.(devices.Refresher)
	owners := map[string]bool{}
	for _, d := range m.snapshot() {
		if d.OwnerDeviceID != "" {
			owners[d.OwnerDeviceID] = true
		}
	}
	var dead []string
	for o := range owners {
		if ref == nil {
			dead = append(dead, o)
		} else if _, ok := ref.RefreshPrincipal(o); !ok {
			dead = append(dead, o)
		}
	}
	m.dropOwned(dead)
}

// phoneLive: the devices module still knows the phone (not revoked). With no devices module, no phone is live.
func (m *Module) phoneLive(id string) bool {
	svc, _ := m.core.Registry.Get(devicesmod.RegistryKey)
	ref, ok := svc.(devices.Refresher)
	if !ok {
		return false
	}
	_, live := ref.RefreshPrincipal(id)
	return live
}

// dropOwned removes the registrations of revoked paired phones from the store and the cache; a push already queued for one
// finds no device when it is sent.
func (m *Module) dropOwned(owners []string) {
	if len(owners) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	gone, err := m.store.DeleteByOwners(owners)
	for _, id := range gone {
		delete(m.devices, id)
		m.rs.Forget(id)
	}
	// Fail closed: whatever the database said, nothing owned by a revoked phone stays in the send cache (a failed delete is
	// retried by the reconcile at the next Start).
	revoked := make(map[string]bool, len(owners))
	for _, o := range owners {
		revoked[o] = true
	}
	for id, d := range m.devices {
		if d.OwnerDeviceID != "" && revoked[d.OwnerDeviceID] {
			delete(m.devices, id)
			m.rs.Forget(id)
		}
	}
	if err != nil {
		log.Printf("[push] drop registrations of revoked phones: %v", err)
	}
	if len(gone) > 0 {
		log.Printf("[push] dropped %d registration(s) of %d revoked phone(s)", len(gone), len(owners))
	}
}

// Stop ends the subscription and the sender (cancelling a request in flight), then closes the store.
func (m *Module) Stop(context.Context) error {
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	if m.unsubNotify != nil {
		m.unsubNotify()
		m.unsubNotify = nil
	}
	m.ap.stop()      // no new approval push path; the ones under way finish before the sender goes
	m.wbHolds.stop() // before the sender: a hold that is mid-send enqueues onto a live sender, then nothing more
	m.holds.stopAll()
	m.closes.stop() // a window that has not ended sends nothing
	m.stopReads()   // the reads feed and every flush timer
	if snd := m.sender.Swap(nil); snd != nil {
		snd.Stop() // a callback that already holds snd only enqueues onto a stopped sender: harmless
	}
	return nil // the store is released by Close, after the HTTP server drained (#1033)
}

// onApproval runs on the approval feed's own goroutine (never under the team module's lock). Only an `opened` approval of
// the three pushed kinds becomes a notification, to every device (the Mac raises approvals whatever tabs are open); not
// when a present Mac shows the requesting session (R6, by the tmux session name of the origin; an origin with no tmux is
// never suppressed).
func (m *Module) onApproval(op string, a team.Approval) {
	if op == "closed" {
		m.asks.Closed(a.ID) // an ask that was never recorded (not pushed) has nothing to close
		if push.CountsForBadge(string(a.Kind)) {
			m.closes.add(a.ID) // a card the badge counts went away: tell the phones that asked, after the 2 s window
		}
	}
	if op == "closed" {
		if _, pushed := push.ApprovalContent(toPushApproval(a), "", "en"); pushed {
			m.closedApproval(a) // the tombstone: a late callback or a push path that has not enqueued yet stops
		}
	}
	snd := m.sender.Load() // one read: Stop may clear it at any moment
	if op != "opened" || snd == nil {
		return
	}
	pa := toPushApproval(a)
	if _, pushed := push.ApprovalContent(pa, "", "en"); !pushed {
		logApproval(a, "not pushed: kind")
		return
	}
	m.openedApproval(a, pa)
}

func toPushApproval(a team.Approval) push.Approval {
	return push.Approval{ID: a.ID, Kind: string(a.Kind), Payload: a.Payload, SessionID: a.Origin.SessionID,
		Origin: push.ApprovalOrigin{Title: a.Origin.Title, Name: a.Origin.Name, Ref: a.Origin.Ref}}
}

// tmuxSessionOf is the session name of an origin.tmux ("<session>:@<win>.%<pane>"): tmux session names cannot hold ':'.
func tmuxSessionOf(tmux string) string {
	name, _, _ := strings.Cut(tmux, ":")
	return name
}

// The deviceBook the sender works through: the store first, the cache after, under the module's mutex.

func (m *Module) Get(deviceID string) (push.Device, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[deviceID]
	return d, ok
}

func (m *Module) Remove(deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.store.DeleteByID(deviceID); err != nil {
		log.Printf("[push] remove %s: %v", deviceID, err)
		return
	}
	delete(m.devices, deviceID)
	m.rs.Forget(deviceID)
}

func (m *Module) MarkSent(deviceID string, at int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.MarkSent(deviceID, at); err != nil {
		log.Printf("[push] mark sent %s: %v", deviceID, err)
		return
	}
	if d, ok := m.devices[deviceID]; ok {
		d.LastSentAt, d.LastError = at, ""
		m.devices[deviceID] = d
	}
}

func (m *Module) MarkError(deviceID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.MarkError(deviceID, reason); err != nil {
		log.Printf("[push] mark error %s: %v", deviceID, err)
		return
	}
	if d, ok := m.devices[deviceID]; ok {
		d.LastError = reason
		m.devices[deviceID] = d
	}
}

// snapshot is the devices in registration order (a copy; the caller may keep it).
func (m *Module) snapshot() []push.Device {
	m.mu.Lock()
	out := make([]push.Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

func (m *Module) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_body")
		return
	}
	if !utf8.Valid(body) { // encoding/json would rewrite a bad byte to U+FFFD and let it through
		writeError(w, http.StatusBadRequest, "invalid_utf8")
		return
	}
	var req push.DeviceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d := push.Device{
		DeviceID: push.DeviceID(req.Token), Token: req.Token, BundleID: req.BundleID, Env: req.Env, Platform: req.Platform,
		DeviceName: req.DeviceName, HostLabel: req.HostLabel, Locale: req.Locale, Prefs: req.Prefs,
	}
	p, isDevice := devices.PrincipalFrom(r.Context())
	if isDevice {
		d.OwnerDeviceID = p.ID // a paired phone registers as itself; the same APNs token again moves to whoever sends it
	}
	m.mu.Lock()
	if isDevice && !m.phoneLive(p.ID) {
		// Revoked after this request passed authentication. The revoke's drop runs under this same mutex, so checking here
		// (not earlier) means either the phone is still live and the drop follows, or it is refused.
		m.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	stored, err := m.store.Upsert(d)
	if err == nil {
		m.devices[stored.DeviceID] = stored
		m.regNext++
		m.regSeq[stored.DeviceID] = m.regNext
		m.registered(stored)
	}
	m.mu.Unlock()
	if err != nil {
		log.Printf("[push] register %s: %v", push.MaskToken(d.Token), err)
		writeError(w, http.StatusInternalServerError, "store_failed")
		return
	}
	log.Printf("[push] registered %s %s (%s, %d tab(s))", stored.DeviceID, push.MaskToken(stored.Token), stored.Env, len(stored.Prefs.Tabs))
	writeJSON(w, http.StatusOK, stored.View())
}

func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	p, isDevice := devices.PrincipalFrom(r.Context())
	devs := m.snapshot()
	views := make([]push.DeviceView, 0, len(devs))
	for _, d := range devs {
		if isDevice && d.OwnerDeviceID != p.ID {
			continue // a paired phone sees only its own registrations
		}
		views = append(views, d.View())
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": views})
}

func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	p, isDevice := devices.PrincipalFrom(r.Context())
	if !push.ValidDeviceID(id) { // not an id this module ever issued: nothing to remove, and nothing of it is logged
		if isDevice { // to a paired phone every id that is not its own is the same 404, well-formed or not
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.mu.Lock()
	if isDevice { // someone else's registration (or none) is the same 404 to a paired phone
		if d, ok := m.devices[id]; !ok || d.OwnerDeviceID != p.ID {
			m.mu.Unlock()
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	gone, err := m.store.DeleteByID(id)
	if err == nil {
		delete(m.devices, id)
		m.rs.Forget(id)
	}
	m.mu.Unlock()
	if err != nil {
		log.Printf("[push] delete %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "store_failed")
		return
	}
	if gone {
		log.Printf("[push] removed device %s", id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// Close releases the store. It runs from CloseModules, after the HTTP server has drained (spec §4.5), which is why Stop
// leaves the store open: a request in flight during shutdown must not meet a closed database (#1033). Idempotent
// (sql.DB.Close is).
func (m *Module) Close() error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
