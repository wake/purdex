package core

import (
	"testing"
	"time"
)

func TestTicketGenerate(t *testing.T) {
	ts := NewTicketStore()
	ticket, err := ts.Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if ticket == "" {
		t.Fatal("Generate() returned empty string")
	}
	if len(ticket) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("ticket length = %d, want 32", len(ticket))
	}
}

func TestTicketValidateConsumes(t *testing.T) {
	ts := NewTicketStore()
	ticket, err := ts.Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// First validation should succeed
	if !ts.Validate(ticket) {
		t.Error("first Validate() = false, want true")
	}

	// Second validation should fail (one-time use)
	if ts.Validate(ticket) {
		t.Error("second Validate() = true, want false (ticket already consumed)")
	}
}

func TestTicketValidateExpired(t *testing.T) {
	ts := NewTicketStore()

	// Manually insert an already-expired ticket
	ts.mu.Lock()
	ts.tickets["expired-ticket"] = ticketEntry{
		createdAt: time.Now().Add(-(ticketTTL + time.Second)),
	}
	ts.mu.Unlock()

	if ts.Validate("expired-ticket") {
		t.Error("Validate() = true for expired ticket, want false")
	}
}

func TestTicketValidateEmpty(t *testing.T) {
	ts := NewTicketStore()
	if ts.Validate("") {
		t.Error("Validate(\"\") = true, want false")
	}
}

func TestTicketValidateUnknown(t *testing.T) {
	ts := NewTicketStore()
	if ts.Validate("nonexistent-ticket") {
		t.Error("Validate() = true for unknown ticket, want false")
	}
}

func TestTicketGenerateUniqueness(t *testing.T) {
	ts := NewTicketStore()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		ticket, err := ts.Generate()
		if err != nil {
			t.Fatalf("Generate() error on iteration %d: %v", i, err)
		}
		if seen[ticket] {
			t.Fatalf("duplicate ticket on iteration %d: %s", i, ticket)
		}
		seen[ticket] = true
	}
}

func TestTicketCleanupExpired(t *testing.T) {
	ts := NewTicketStore()

	// Insert an expired ticket manually
	ts.mu.Lock()
	ts.tickets["old"] = ticketEntry{
		createdAt: time.Now().Add(-(ticketTTL + time.Second)),
	}
	ts.mu.Unlock()

	// Generate a new ticket — this triggers cleanup
	_, err := ts.Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	ts.mu.Lock()
	_, exists := ts.tickets["old"]
	ts.mu.Unlock()

	if exists {
		t.Error("expired ticket was not cleaned up during Generate()")
	}
}
