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

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/module/agent"
	devicesmod "github.com/wake/purdex/internal/module/devices"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/push/apnskey"
	"github.com/wake/purdex/internal/team"
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
	gate        *Gate
	asks        *openAsks
	approvals   *openApprovals // open approvals of the pushed kinds: purdex.open_approvals on every push
	holds       holdSet
	holdFor     time.Duration // how long a waiting event waits for its hook_ask (spec §5.2 rule 8); a test seam
	unsubNotify func()
}

// presenceChecker answers "does a present Mac show this session" (push spec R6, §5.4): by tmux session name for an
// approval, by session code for an agent event.
type presenceChecker interface {
	ShowsName(tmuxSession string) bool
	ShowsCode(code string) bool
}

func New() *Module {
	return &Module{home: os.UserHomeDir, devices: map[string]push.Device{}, pres: NewPresence(time.Now),
		gate: NewGate(time.Now), asks: newOpenAsks(time.Now), approvals: newOpenApprovals(), holdFor: waitingHold}
}

func (m *Module) Name() string           { return "push" }
func (m *Module) Dependencies() []string { return []string{"team", "agent"} }

// Init loads the APNs key, opens push.db and reads every device into the cache. Any failure is recorded (without key
// material) and leaves the module off; Init itself returns nil so the daemon starts (push spec §3).
func (m *Module) Init(c *core.Core) error {
	m.core = c
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr, "presence_entries": entries, "presence_active": active}
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
	snd.openCount = m.approvals.Count
	m.sender.Store(snd)
	m.holds.reset()
	m.asks.Clear() // a restart begins from the feed's snapshot, not from asks an earlier run saw open
	m.approvals.Clear()
	if m.events != nil {
		// The feed arms the callback in the same step that returns the snapshot, and may run an event before this goroutine
		// has loaded the snapshot. Events wait for it: a `closed` that outruns the load would otherwise be lost, and the
		// snapshot would then reopen an ask that is closed.
		ready := make(chan struct{})
		var open []team.Approval
		open, m.unsub = m.events.SubscribeApprovals(func(op string, a team.Approval) {
			<-ready
			m.onApproval(op, a)
		})
		m.asks.Load(open)
		m.approvals.Load(open)
		close(ready)
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
	m.holds.stopAll()
	if snd := m.sender.Swap(nil); snd != nil {
		snd.Stop() // a callback that already holds snd only enqueues onto a stopped sender: harmless
	}
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

// onApproval runs on the approval feed's own goroutine (never under the team module's lock). Only an `opened` approval of
// the three pushed kinds becomes a notification, to every device (the Mac raises approvals whatever tabs are open); not
// when a present Mac shows the requesting session (R6, by the tmux session name of the origin; an origin with no tmux is
// never suppressed).
func (m *Module) onApproval(op string, a team.Approval) {
	switch op { // the open hook_ask set is kept whether or not this approval is pushed (rule 8 reads it)
	case "opened":
		m.asks.Opened(a)
		m.approvals.Opened(a)
	case "closed":
		m.asks.Closed(a.ID)
		m.approvals.Closed(a.ID)
	}
	snd := m.sender.Load() // one read: Stop may clear it at any moment
	if op != "opened" || snd == nil {
		return
	}
	pa := toPushApproval(a)
	if _, pushed := push.ApprovalContent(pa, "", "en"); !pushed {
		return
	}
	if name := tmuxSessionOf(a.Origin.Tmux); name != "" && m.presence.ShowsName(name) {
		return
	}
	devs := m.snapshot()
	if len(devs) == 0 {
		return
	}
	ids := make([]string, len(devs))
	for i, d := range devs {
		ids[i] = d.DeviceID
	}
	snd.Enqueue(Job{DeviceIDs: ids, Make: func(d push.Device) (push.Content, bool) {
		return push.ApprovalContent(pa, d.HostLabel, d.Locale)
	}})
}

func toPushApproval(a team.Approval) push.Approval {
	return push.Approval{ID: a.ID, Kind: string(a.Kind), Payload: a.Payload,
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
