// Package push is the "push" daemon module: phone push notifications through APNs (spec
// docs/specs/2026-10-09-push-spec.md). It is mounted only when config.toml has a [push] section (cmd/pdx/main.go), so
// its routes, and the `push.v1` capability, exist only then.
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
}

func New() *Module { return &Module{home: os.UserHomeDir, devices: map[string]push.Device{}} }

func (m *Module) Name() string           { return "push" }
func (m *Module) Dependencies() []string { return nil }

// Init loads the APNs key (a failure refuses the module, logged without key material), opens push.db and reads every
// device into the cache.
func (m *Module) Init(c *core.Core) error {
	m.core = c
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
	m.key, err = apnskey.Load(dir)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	m.store, err = OpenStore(filepath.Join(dataDir, "push.db"))
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	list, err := m.store.List()
	if err != nil {
		m.store.Close()
		return fmt.Errorf("push: %w", err)
	}
	m.devices = make(map[string]push.Device, len(list))
	for _, d := range list {
		m.devices[d.DeviceID] = d
	}
	return nil
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
	mux.HandleFunc("POST /api/push/devices", m.handlePost)
	mux.HandleFunc("GET /api/push/devices", m.handleList)
	mux.HandleFunc("DELETE /api/push/devices/{device_id}", m.handleDelete)
}

func (m *Module) Start(context.Context) error {
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
	log.Printf("[push] registered %s (%s, %s, %d tab(s))", push.MaskToken(stored.Token), stored.Env, stored.DeviceName, len(stored.Prefs.Tabs))
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
