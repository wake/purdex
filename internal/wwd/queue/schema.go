package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// schemaVersion is the version of the queue's tables inside wwd.db. They have their own version table (queue_schema) beside the ones
// the host-state tables keep, so neither side migrates the other's. Once wwd is deployed the tables exist, so a change from here on
// is a migration (a case in migrate), never an edit of createSchema.
const schemaVersion = 1

// Migrate creates the queue's tables in db, or checks them. It is idempotent (wwd calls it on every start), and refuses a database a
// newer wwd wrote: the older code would misread it.
func Migrate(ctx context.Context, db *sql.DB) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("queue: migrate: %w", err)
	}
	defer c.Close()
	return inImmediateTx(ctx, c, func() error {
		if _, err := c.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS queue_schema (version INTEGER NOT NULL)`); err != nil {
			return err
		}
		var have int
		switch err := c.QueryRowContext(ctx, `SELECT version FROM queue_schema`).Scan(&have); {
		case errors.Is(err, sql.ErrNoRows):
			if err := createSchema(ctx, c); err != nil {
				return err
			}
			_, err := c.ExecContext(ctx, `INSERT INTO queue_schema (version) VALUES (?)`, schemaVersion)
			return err
		case err != nil:
			return err
		}
		if have > schemaVersion {
			return fmt.Errorf("queue: wwd.db has queue schema %d, this wwd knows %d: a newer wwd wrote it", have, schemaVersion)
		}
		// have < schemaVersion: the cases of the migrations go here, one per version step
		return nil
	})
}

// createSchema is version 1. Every column of the whole state machine is here (claim, lease, result, log), so the claim and
// result transitions need no migration when they land. Times are unix milliseconds on the origin's clock.
func createSchema(ctx context.Context, c *sql.Conn) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			seq              INTEGER PRIMARY KEY AUTOINCREMENT,
			id               TEXT NOT NULL UNIQUE,
			origin           TEXT NOT NULL,
			repo_name        TEXT NOT NULL,
			repo_root        TEXT NOT NULL,
			repo_remote      TEXT NOT NULL,
			snap_commit      TEXT NOT NULL,
			snap_tree        TEXT NOT NULL,
			snap_base        TEXT NOT NULL,
			snap_rel_cwd     TEXT NOT NULL,
			task             TEXT NOT NULL,
			args             TEXT NOT NULL,
			class            TEXT NOT NULL,
			labels           TEXT NOT NULL,
			locks            TEXT NOT NULL,
			cacheable        INTEGER NOT NULL,
			timeout_s        INTEGER NOT NULL,
			supersede_key    TEXT NOT NULL,
			interruptible    INTEGER NOT NULL,
			queue_timeout_s  INTEGER NOT NULL,
			adhoc            INTEGER NOT NULL,
			sub_session      TEXT NOT NULL,
			sub_ref          TEXT NOT NULL,
			sub_team         TEXT NOT NULL,
			sub_host         TEXT NOT NULL,
			created_at       INTEGER NOT NULL,
			state            TEXT NOT NULL,
			attempts         INTEGER NOT NULL DEFAULT 0,
			claim_worker     TEXT,
			claim_id         TEXT,
			claim_since      INTEGER,
			lease_deadline   INTEGER,
			lease_s          INTEGER,
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			superseded_by    TEXT,
			result           TEXT,
			result_claim_id  TEXT,
			finished_at      INTEGER,
			log_size         INTEGER,
			log_at           INTEGER,
			local            INTEGER NOT NULL DEFAULT 0,
			git_dir          TEXT NOT NULL DEFAULT '',
			pinned           INTEGER NOT NULL DEFAULT 0,
			updated_at       INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS jobs_state_seq ON jobs (state, seq)`,
		`CREATE INDEX IF NOT EXISTS jobs_supersede ON jobs (supersede_key) WHERE supersede_key != ''`,
		`CREATE INDEX IF NOT EXISTS jobs_session ON jobs (sub_session, seq)`,
	} {
		if _, err := c.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// inImmediateTx runs fn between BEGIN IMMEDIATE and COMMIT on c: the write lock is taken at once, so two writers queue on
// busy_timeout instead of deadlocking on a read-to-write upgrade. fn's error rolls back.
func inImmediateTx(ctx context.Context, c *sql.Conn, fn func() error) (err error) {
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, _ = c.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		}
	}()
	if err = fn(); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, `COMMIT`)
	return err
}
