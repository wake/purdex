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
const schemaVersion = 4

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
		if have < 2 {
			if err := migrateV1ToV2(ctx, c); err != nil {
				return err
			}
		}
		if have < 3 {
			if err := migrateV2ToV3(ctx, c); err != nil {
				return err
			}
		}
		if have < 4 {
			if err := migrateV3ToV4(ctx, c); err != nil {
				return err
			}
		}
		_, err := c.ExecContext(ctx, `UPDATE queue_schema SET version = ?`, schemaVersion)
		return err
	})
}

// gitPinsTable (version 2) is the queue's own record of the snapshots it pinned: one row per pin, written BEFORE the pin is made, so that
// a pin left behind (the row failed after the pin, or the process died between the two) is still known and is released by the sweep. The
// sweep only ever releases ids in this table, so it cannot touch a ref some other wwd, or a person, keeps under refs/wwd/ in a shared
// repository. A row goes when its pin is released.
const gitPinsTable = `CREATE TABLE IF NOT EXISTS git_pins (
	id      TEXT PRIMARY KEY,
	git_dir TEXT NOT NULL,
	at      INTEGER NOT NULL
)`

// migrateV1ToV2 adds git_pins and fills it from the jobs that hold a pin, so their pins are still released when they end.
func migrateV1ToV2(ctx context.Context, c *sql.Conn) error {
	if _, err := c.ExecContext(ctx, gitPinsTable); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO git_pins (id, git_dir, at)
		SELECT id, git_dir, created_at FROM jobs WHERE pinned = 1 AND git_dir != ''`)
	return err
}

// migrateV2ToV3 adds jobs.lapses, the number of times a job's lease ran out. It is counted apart from attempts (which also counts
// infrastructure releases) because only lapses decide when a job is failed as lost. A database created by version 3 code has the column
// already, so the step looks first.
func migrateV2ToV3(ctx context.Context, c *sql.Conn) error {
	var n int
	if err := c.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'lapses'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := c.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN lapses INTEGER NOT NULL DEFAULT 0`)
	return err
}

// revStatements (version 4) are the origin's cursor: a counter that only goes up (job_rev, one row), jobs.rev holding the counter's value
// at the job's last change, and the triggers that move it. A job gets the next number when it is inserted and when a column a client can
// see changes (state, result, cancellation, supersession, log, claim); a lease renewal touches none of them. The counter is a table of its
// own, not max(rev), so that deleting the newest job (retention) never lets its number be given out again. Writers are serialized by
// BEGIN IMMEDIATE, so numbers are handed out, and become visible, in the order of the commits: a reader that has seen number n will never
// see a lower one appear later. The triggers write only jobs.rev, which they do not list, so they do not fire themselves.
var revStatements = []string{
	`CREATE TABLE IF NOT EXISTS job_rev (n INTEGER NOT NULL)`,
	`INSERT INTO job_rev (n) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM job_rev)`,
	`CREATE INDEX IF NOT EXISTS jobs_rev ON jobs (rev)`,
	`CREATE TRIGGER IF NOT EXISTS jobs_rev_ins AFTER INSERT ON jobs BEGIN
		UPDATE job_rev SET n = n + 1;
		UPDATE jobs SET rev = (SELECT n FROM job_rev) WHERE seq = NEW.seq;
	END`,
	`CREATE TRIGGER IF NOT EXISTS jobs_rev_upd AFTER UPDATE OF state, result, cancel_requested, superseded_by, log_size, claim_id ON jobs BEGIN
		UPDATE job_rev SET n = n + 1;
		UPDATE jobs SET rev = (SELECT n FROM job_rev) WHERE seq = NEW.seq;
	END`,
}

// migrateV3ToV4 gives an existing database the cursor: the column, a number for every job in the order it was made (its seq, which only
// goes up), the counter above the highest, then the triggers. It can run again: each step looks first.
func migrateV3ToV4(ctx context.Context, c *sql.Conn) error {
	var n int
	if err := c.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'rev'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := c.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN rev INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// the tables first, the backfill, and only then the triggers: a trigger that existed would number each backfilled job again
	if _, err := c.ExecContext(ctx, revStatements[0]); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, revStatements[1]); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `UPDATE jobs SET rev = seq WHERE rev = 0`); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `UPDATE job_rev SET n = max(n, (SELECT coalesce(max(rev), 0) FROM jobs))`); err != nil {
		return err
	}
	for _, stmt := range revStatements[2:] {
		if _, err := c.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// createSchema is the tables of a new database, as of the current version (git_pins, version 2, jobs.lapses, version 3, and the cursor,
// version 4, are in it). Times are unix milliseconds on the origin's clock.
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
			lapses           INTEGER NOT NULL DEFAULT 0,
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
			updated_at       INTEGER NOT NULL,
			rev              INTEGER NOT NULL DEFAULT 0
		)`,
		gitPinsTable,
		`CREATE INDEX IF NOT EXISTS jobs_state_seq ON jobs (state, seq)`,
		`CREATE INDEX IF NOT EXISTS jobs_supersede ON jobs (supersede_key) WHERE supersede_key != ''`,
		`CREATE INDEX IF NOT EXISTS jobs_session ON jobs (sub_session, seq)`,
	} {
		if _, err := c.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, stmt := range revStatements {
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
