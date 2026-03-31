// internal/core/ticket.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const ticketTTL = 30 * time.Second

type ticketEntry struct {
	createdAt time.Time
}

// TicketStore manages one-time WS authentication tickets.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]ticketEntry
}

func NewTicketStore() *TicketStore {
	return &TicketStore{tickets: make(map[string]ticketEntry)}
}

// Generate creates a one-time ticket valid for 30 seconds.
func (ts *TicketStore) Generate() (string, error) {
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
	ts.tickets[ticket] = ticketEntry{createdAt: now}
	return ticket, nil
}

// Validate checks and consumes a ticket. Returns true if valid.
func (ts *TicketStore) Validate(ticket string) bool {
	if ticket == "" {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry, ok := ts.tickets[ticket]
	if !ok {
		return false
	}
	delete(ts.tickets, ticket) // one-time use
	return time.Since(entry.createdAt) <= ticketTTL
}

// handleWsTicket issues a one-time WS authentication ticket.
func (c *Core) handleWsTicket(w http.ResponseWriter, r *http.Request) {
	ticket, err := c.Tickets.Generate()
	if err != nil {
		http.Error(w, "ticket generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}
