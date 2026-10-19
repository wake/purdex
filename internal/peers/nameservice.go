package peers

import (
	"context"
	"errors"
	"time"
)

// NameServiceKey is the service-registry key of the NameService the peers module registers (name pool v2 plan §1.3). The team
// module resolves it at use time, so the start order of the modules does not matter, and works without it (a build without the
// peers module has no lead names and no pair history).
const NameServiceKey = "peers.name-service"

var (
	// ErrNameTaken: the persona code is held here (a live session, another host's snapshot), or, for an undo, the name to go
	// back to now belongs to another session.
	ErrNameTaken = errors.New("peers: the name is held by another session")
	// ErrNameUnknown: the code is not an active persona code, or no name can be built from it.
	ErrNameUnknown = errors.New("peers: not a pickable persona code")
	// ErrNameLost: the session has no stored name, or its name changed under the rename (a compare-and-set that lost).
	ErrNameLost = errors.New("peers: the name of the session changed or is not stored")
)

// LeadCandidate is one name a lead's name card offers. Keep marks the session's current name, which comes first.
type LeadCandidate struct {
	Code string `json:"code"`
	Keep bool   `json:"keep,omitempty"`
}

// NameService is the team module's door to the names (name pool v2 spec §5.3, §6): the lead name card, the rename at approval
// with its undo, and the history the picking rules read. It is implemented by the peers module. The reservations of a
// member's name (spec §5.2) join it in NP-4.
type NameService interface {
	// LeadCandidates is rule 1 for the session: its current name first (when it has a pool name), then free former leads,
	// free lead:true personas and the other free names.
	LeadCandidates(ctx context.Context, sessionID string) ([]LeadCandidate, error)

	// RenameForLead renames the session to <base>-<code> and keeps the old name as an alias, in one meta transaction (the
	// first step of spec §5.3). It returns the old name. The code being the session's own code changes nothing. Errors:
	// ErrNameUnknown, ErrNameTaken, ErrNameLost.
	RenameForLead(ctx context.Context, sessionID, code string) (oldName string, err error)
	// UndoRenameForLead reverses the last RenameForLead of the session when it is still the current name (a compare-and-set,
	// so a later rename wins) and deletes the alias that rename added. It is for a team row that failed or whose close was
	// lost. Nothing to undo (a daemon restarted since, a later rename) is not an error. ErrNameTaken when oldName now belongs
	// to another session: the undo is refused and nothing changes.
	UndoRenameForLead(ctx context.Context, sessionID, oldName string) error
	// RecordLead writes the lead-history row of the session when its name is a persona code; otherwise it does nothing.
	RecordLead(ctx context.Context, sessionID string) error

	// RecordPairCodes writes one lead/member pair by code; it does nothing unless both are persona codes.
	RecordPairCodes(ctx context.Context, leadCode, memberCode string) error
	// CodeOf is the persona code a session's name ends in.
	CodeOf(ctx context.Context, sessionID string) (code string, ok bool)

	// ReserveMember reserves a free name for the member a spawn op is about to start, by rule 2 for the lead's team (spec
	// §5.2). at is when the op was created, tmuxName the op's tmux session name (the first naming pass finds the reservation
	// by it). The reservation is held in memory, with the live names, until ReleaseReservation or ten minutes after at. "" and
	// no error: nothing is free. The same opID again answers the name it already holds.
	ReserveMember(ctx context.Context, leadSessionID, opID, tmuxName string, at time.Time) (code string, err error)
	// ReserveExact is ReserveMember for a name the lead's host chose (a remote spawn). ErrNameTaken when the name is held here:
	// the member host then names the session by its own rules and the spawn goes on. ErrNameUnknown for a code that is not an
	// active persona code. The same opID with the same code is a success.
	ReserveExact(ctx context.Context, code, opID, tmuxName string, at time.Time) error
	// ReleaseReservation ends a reservation (the op registered, failed, was voided or timed out, or its remote command
	// expired). It is idempotent and ignores an unknown opID.
	ReleaseReservation(opID string)
}

// Reservation is one open spawn op's reserved name, as the team module still has it.
type Reservation struct {
	OpID, Code, TmuxName string
	CreatedAt            time.Time
}

// SpawnReservationReader is what the team module registers under team.SpawnReservationsKey: the open spawn ops that carry a
// pool name, read once by the name service after a boot to rebuild its in-memory reservations. The peers module never opens
// team.db. The name service works without it (it then starts with no reservations).
type SpawnReservationReader interface {
	OpenReservations(ctx context.Context) ([]Reservation, error)
}
