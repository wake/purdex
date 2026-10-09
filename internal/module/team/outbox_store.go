package teammod

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	peersmod "github.com/wake/purdex/internal/module/peers"
)

// L's commands outbox (cross-host team spec §3.1, §4.1; plan X3a). A command is written in the SAME transaction as the
// change that causes it (EnqueueCommand takes the caller's transaction), addressed to a destination HOST ID, and sent by
// the pump; its answer is marked done and applied in one transaction. This file is the table and its operations; the
// row state machine the answers drive is X3b-1's (commandOutcomes).

// Command kinds (§6.2). A kind M does not announce is never queued (rule 7).
const (
	CmdAdopt     = "adopt"
	CmdRelease   = "release"
	CmdKill      = "kill"
	CmdEnd       = "end"
	CmdLeadMoved = "lead_moved"
	CmdSpawn     = "spawn"
	CmdVoid      = "void"
)

// commandsPath is the one route every command goes to.
const commandsPath = "/api/peers/team/commands"

// commandExpiryMS is X-U8: a spawn or adopt not done within 10 minutes of its creation is void.
const commandExpiryMS = 10 * 60 * 1000

// Command states.
const (
	cmdPending = "pending"
	cmdDone    = "done"
	cmdVoid    = "void"
	cmdDropped = "dropped"
)

// Remote row states on L (§4.2) the local wrap-ups below act on (the state machine itself is X3b-1's).
const (
	rowJoining   = "joining"
	rowActive    = "active"
	rowReleasing = "releasing"
	rowKilling   = "killing"
	rowGone      = "gone"
	rowFailed    = "failed"
)

// expiring says whether a kind is subject to the 10 minute void (release / kill / end / lead_moved / void queue forever).
func expiring(kind string) bool { return kind == CmdAdopt || kind == CmdSpawn }

// Command is what a cause enqueues. Body is the request JSON as it will be sent: it must carry the same id, kind and
// to_host_id (the receiver refuses what is not addressed to it, rule 1).
type Command struct {
	ID, Kind, TeamID, MK, HostID string
	Body                         json.RawMessage
}

// commandRow is a stored command.
type commandRow struct {
	Command
	BodyHash                                 string
	State                                    string
	Outcome                                  json.RawMessage
	Attempts                                 int
	NextAt, First401At, CreatedAt, UpdatedAt int64
}

func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// EnqueueCommand inserts c into q — the caller's transaction, the same one that writes the cause. The same id with the
// same body is a replay (nothing changes); the same id with another body is an error. Not sent here: the caller kicks the
// pump after its commit.
func (s *Store) EnqueueCommand(q dbtx, c Command, now int64) error {
	if c.ID == "" || c.Kind == "" || c.HostID == "" || c.TeamID == "" {
		return fmt.Errorf("enqueue command: id, kind, team and host must be set")
	}
	var head struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		ToHost string `json:"to_host_id"`
		TeamID string `json:"team_id"`
		MK     string `json:"mk"`
	}
	// The stored routing columns (the expiry and the outcomes read them) and the bytes that are sent must say the same.
	if err := json.Unmarshal(c.Body, &head); err != nil || head.ID != c.ID || head.Kind != c.Kind || head.ToHost != c.HostID || head.TeamID != c.TeamID || head.MK != c.MK {
		return fmt.Errorf("enqueue command %s: the body must carry the same id, kind, to_host_id, team_id and mk", c.ID)
	}
	hash := hashBody(c.Body)
	res, err := q.Exec(`INSERT INTO team_commands (id, kind, team_id, mk, host_id, body_json, body_hash, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?) ON CONFLICT (id) DO NOTHING`,
		c.ID, c.Kind, c.TeamID, c.MK, c.HostID, string(c.Body), hash, now, now)
	if err != nil {
		return fmt.Errorf("enqueue command %s: %w", c.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	// A replay: the same id means the same command in every persisted field, not only the same bytes.
	var stored Command
	var storedHash string
	if err := q.QueryRow(`SELECT kind, team_id, mk, host_id, body_hash FROM team_commands WHERE id = ?`, c.ID).
		Scan(&stored.Kind, &stored.TeamID, &stored.MK, &stored.HostID, &storedHash); err != nil {
		return fmt.Errorf("enqueue command %s: %w", c.ID, err)
	}
	if storedHash != hash || stored.Kind != c.Kind || stored.TeamID != c.TeamID || stored.MK != c.MK || stored.HostID != c.HostID {
		return fmt.Errorf("enqueue command %s: the id is taken by another command", c.ID)
	}
	return nil
}

const commandCols = `id, kind, team_id, mk, host_id, body_json, body_hash, state, outcome_json, attempts, next_at, first_401_at, created_at, updated_at`

func scanCommand(sc interface{ Scan(...any) error }) (commandRow, error) {
	var c commandRow
	var body, outcome string
	err := sc.Scan(&c.ID, &c.Kind, &c.TeamID, &c.MK, &c.HostID, &body, &c.BodyHash, &c.State, &outcome, &c.Attempts, &c.NextAt, &c.First401At, &c.CreatedAt, &c.UpdatedAt)
	c.Body, c.Outcome = json.RawMessage(body), json.RawMessage(outcome)
	return c, err
}

// GetCommand reads one command.
func (s *Store) GetCommand(id string) (commandRow, bool, error) {
	c, err := scanCommand(s.db.QueryRow(`SELECT `+commandCols+` FROM team_commands WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return commandRow{}, false, nil
	}
	return c, err == nil, err
}

// commandOutcomes is X3b-1's seam (rule 4): the answer to a command is applied to the member row / spawn op in the
// SAME transaction that marks the command done. res.Class is ClassDone (res.Body is M's answer) or a permanent
// refusal (ClassRefused / ClassWrongHost, res.Code). X3a installs none: nothing is applied, only recorded.
type commandOutcomes interface {
	ApplyOutcome(tx *sql.Tx, c commandRow, res peersmod.CallResult) error
}

type noOutcomes struct{}

func (noOutcomes) ApplyOutcome(*sql.Tx, commandRow, peersmod.CallResult) error { return nil }

// SettleCommand marks the command done and applies its outcome in ONE transaction (rule 4): res is the receiver's answer
// (ClassDone, res.Body) or its permanent refusal (ClassRefused / ClassWrongHost, res.Code). The CAS makes a command that
// was voided or dropped meanwhile a no-op, so a late answer never resurrects it (rule 5). An outcome that cannot be applied
// rolls everything back: the command stays pending, is sent again and the stored outcome comes back (rule 3).
func (s *Store) SettleCommand(id string, res peersmod.CallResult, now int64, out commandOutcomes) (settled bool, err error) {
	outcome := []byte(res.Body)
	if res.Class != peersmod.ClassDone {
		outcome, _ = json.Marshal(map[string]string{"refused": res.Code, "detail": res.Detail})
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE team_commands SET state = 'done', outcome_json = ?, updated_at = ? WHERE id = ? AND state = 'pending'`, string(outcome), now, id)
	if err != nil {
		return false, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return false, nil
	}
	c, err := scanCommand(tx.QueryRow(`SELECT `+commandCols+` FROM team_commands WHERE id = ?`, id))
	if err != nil {
		return false, err
	}
	if err := out.ApplyOutcome(tx, c, res); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
