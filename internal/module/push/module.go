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
	"unicode/utf8"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apnskey"
)

const maxBody = 64 << 10 // device registration body cap (spec §4.1)

// Module is the push module.
type Module struct {
	core  *core.Core
	store *Store
	key   apnskey.Key
	home  func() (string, error) // the daemon user's home; injectable for tests

	// mu orders every write: the store first, the cache after it succeeds, both under the lock, so the cache never
	// holds a row the store does not.
	mu      sync.Mutex
	devices map[string]push.Device // by device id
	ready   bool                   // the key loaded and the store opened; false = soft-failed (initErr says why)
	initErr string
}

func New() *Module { return &Module{home: os.UserHomeDir, devices: map[string]push.Device{}} }

func (m *Module) Name() string           { return "push" }
func (m *Module) Dependencies() []string { return nil }

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
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr}
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
}

func (m *Module) Start(context.Context) error {
	if !m.isReady() {
		return nil
	}
	log.Printf("[push] enabled (%v, %d device(s))", m.key, len(m.snapshot()))
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
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
	m.mu.Lock()
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

func (m *Module) handleList(w http.ResponseWriter, _ *http.Request) {
	devs := m.snapshot()
	views := make([]push.DeviceView, 0, len(devs))
	for _, d := range devs {
		views = append(views, d.View())
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": views})
}

func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	if !push.ValidDeviceID(id) { // not an id this module ever issued: nothing to remove, and nothing of it is logged
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.mu.Lock()
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
