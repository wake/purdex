// internal/core/ticket.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/devices"
)

const ticketTTL = 30 * time.Second

type ticketEntry struct {
	createdAt time.Time
	caller    devices.Caller // who asked for it (a device principal is held by value)
}

// TicketStore manages one-time WS authentication tickets.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]ticketEntry
}

func NewTicketStore() *TicketStore {
	return &TicketStore{tickets: make(map[string]ticketEntry)}
}

// Generate creates a one-time ticket valid for 30 seconds, for nobody in particular (what tickets were before principals:
// valid, carrying no identity).
func (ts *TicketStore) Generate() (string, error) { return ts.GenerateFor(devices.Caller{}) }

// GenerateFor creates a one-time ticket valid for 30 seconds that speaks for caller: whoever redeems it is that caller (the
// admin, or the one device), so a WebSocket opened with it is attributed, scoped and closed on revoke like one opened with
// the bearer itself.
func (ts *TicketStore) GenerateFor(caller devices.Caller) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(raw)

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Clean expired tickets opportunistically
	now := time.Now()
	for k, v := range ts.tickets {
		if now.Sub(v.createdAt) > ticketTTL {
			delete(ts.tickets, k)
		}
	}
	if caller.Device != nil {
		p := *caller.Device
		caller.Device = &p // the ticket keeps its own copy
	}
	ts.tickets[ticket] = ticketEntry{createdAt: now, caller: caller}
	return ticket, nil
}

// Validate checks and consumes a ticket. Returns true if valid.
func (ts *TicketStore) Validate(ticket string) bool {
	_, ok := ts.ValidateCaller(ticket)
	return ok
}

// ValidateCaller checks and consumes a ticket in one step and returns who it speaks for.
func (ts *TicketStore) ValidateCaller(ticket string) (devices.Caller, bool) {
	if ticket == "" {
		return devices.Caller{}, false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry, ok := ts.tickets[ticket]
	if !ok {
		return devices.Caller{}, false
	}
	delete(ts.tickets, ticket) // one-time use
	if time.Since(entry.createdAt) > ticketTTL {
		return devices.Caller{}, false
	}
	return entry.caller, true
}

// handleWsTicket issues a one-time WS authentication ticket.
func (c *Core) handleWsTicket(w http.ResponseWriter, r *http.Request) {
	ticket, err := c.Tickets.GenerateFor(devices.CallerFrom(r.Context()))
	if err != nil {
		http.Error(w, "ticket generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}
