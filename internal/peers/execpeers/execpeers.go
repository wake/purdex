// Package execpeers is the seam between the nex module, which knows the
// executions, and the peers module, which addresses conversations (peer
// mailbox spec §4.1, plan C7). It holds only plain types, so neither module
// imports the other: the nex module registers an ExecPeers under RegistryKey
// in Init, and the peers module looks it up per request, treating its absence
// as "no executions" rather than an error.
package execpeers

import "context"

// RegistryKey is the service-registry key the nex module publishes its
// ExecPeers under. Registered only when the engine assembled: a daemon whose
// nex is off or soft-failed has no execution rows, and its peers inventory is
// exactly what it was before executions were listed.
const RegistryKey = "nex.peers"

// Row is one execution a peer can be addressed through: not terminal, not
// archived, and carrying a Claude Code session id.
type Row struct {
	ExecutionID string
	// SessionID is the execution's session id (Nexen's session_id, else its
	// resume_session_id when the first turn has not reported one yet),
	// lowercase.
	SessionID string
	Cwd       string
	State     string // idle | running | queued
	Title     string // Nexen's effective title, "" when none
	// PID is the running turn's process, 0 when no turn is live (idle,
	// queued, or a turn whose process has not reported yet).
	PID int
}

// PeerSend is one peer message for an execution's mailbox (spec §4.2's
// field table).
type PeerSend struct {
	FromName, ReplyTo, FromMode, MsgID, Text string
}

// PeerSendResult is what the mailbox answered: the turn the message opened
// and whether it ran at once (delivered) or waits (queued).
type PeerSendResult struct {
	TurnID   string
	Delivery string // delivered | queued
}

// ExecPeers is what the peers module may do with executions. It has no
// method that can interrupt a turn, on purpose: the peer path never
// interrupts (spec §4.2), and an interface without the verb makes that true
// by construction rather than by review.
type ExecPeers interface {
	// Rows lists every addressable execution, or fails whole: a listing
	// that could not reach every execution returns an error, never the rows
	// it did see, so a missing row is never mistaken for a gone one. Two
	// rows sharing a session id fail it too: one address cannot name two.
	Rows(ctx context.Context) ([]Row, error)
	// MailboxEnabled reports whether the assembled engine accepts peer
	// messages ([nex.peer].enabled).
	MailboxEnabled() bool
	// Send hands m to the execution's peer mailbox.
	Send(ctx context.Context, executionID string, m PeerSend) (PeerSendResult, error)
}
