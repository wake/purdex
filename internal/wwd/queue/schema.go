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
const schemaVersion = 9

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
		if have < 5 {
			if err := migrateV4ToV5(ctx, c); err != nil {
				return err
			}
		}
		if have < 6 {
			if err := migrateV5ToV6(ctx, c); err != nil {
				return err
			}
		}
		if have < 7 {
			if err := migrateV6ToV7(ctx, c); err != nil {
				return err
			}
		}
		if have < 8 {
			if err := migrateV7ToV8(ctx, c); err != nil {
				return err
			}
		}
		if have < 9 {
			if err := migrateV8ToV9(ctx, c); err != nil {
				return err
			}
		}
		_, err := c.ExecContext(ctx, `UPDATE queue_schema SET version = ?`, schemaVersion)
		return err
	})
}

// migrateV8ToV9 adds jobs.started_at (unix milliseconds on the origin's clock, null for a job that has not started): the time Start moved the
// job to running. A job the database already holds has none, which is also what a queued or claimed job shows. It can run again: the column is
// looked for first. The column is only added: an older wwd refuses the database (it checks the version), so going back means restoring the
// backup made before the upgrade (docs/wwd-deploy.md, Roll back). No trigger changes: started_at is written in the same statement that
// changes state, and the cursor already moves with state.
func migrateV8ToV9(ctx context.Context, c *sql.Conn) error {
	var n int
	if err := c.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'started_at'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := c.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN started_at INTEGER`)
	return err
}

// outcomeStatements (version 8) are the origin's record of how declared tasks ended on the hosts that vouch for them, and of the pairs of
// different outcomes for the same work (spec §7 Disagreement). task_outcomes has one row per ended job that is compared; work_key is
// the hash of what the work was (snapshot tree, task, arguments, definition), so two rows with one work_key are the same work. A
// disagreement row is written when the later outcome of a pair is recorded, one row for each host whose label is to be quarantined.
// Both go with the jobs they describe, after the same retention. The tables are only added: a database brought back to version 7 by an
// older wwd is refused by that wwd (it checks the version), so going back means restoring the backup made before the upgrade.
var outcomeStatements = []string{
	`CREATE TABLE IF NOT EXISTS task_outcomes (
		seq       INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id    TEXT NOT NULL UNIQUE,
		work_key  TEXT NOT NULL,
		host      TEXT NOT NULL,
		label     TEXT NOT NULL,
		tree      TEXT NOT NULL,
		task      TEXT NOT NULL,
		ok        INTEGER NOT NULL,
		standing  TEXT NOT NULL,
		at        INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS task_outcomes_work_host ON task_outcomes (work_key, host, seq)`,
	`CREATE INDEX IF NOT EXISTS task_outcomes_at ON task_outcomes (at)`,
	`CREATE TABLE IF NOT EXISTS outcome_disagreements (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		host       TEXT NOT NULL,
		label      TEXT NOT NULL,
		other_host TEXT NOT NULL,
		tree       TEXT NOT NULL,
		task       TEXT NOT NULL,
		job_old    TEXT NOT NULL,
		job_new    TEXT NOT NULL,
		at         INTEGER NOT NULL,
		UNIQUE (host, job_old, job_new)
	)`,
	`CREATE INDEX IF NOT EXISTS outcome_disagreements_host ON outcome_disagreements (host, seq)`,
	`CREATE INDEX IF NOT EXISTS outcome_disagreements_at ON outcome_disagreements (at)`,
}

// migrateV7ToV8 adds the outcome tables. It can run again: every statement is IF NOT EXISTS.
func migrateV7ToV8(ctx context.Context, c *sql.Conn) error {
	for _, stmt := range outcomeStatements {
		if _, err := c.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// resultReuseColumns (version 7) are what a stored result is filed under (spec §4.5), kept on the job: the definition hash of the task as
// submitted (def_hash), and what the worker that holds the claim said about itself when it claimed (claim_labels, claim_fingerprint; JSON,
// empty for a job nobody has claimed or a worker that said nothing). A claim overwrites them, so at the end they are those of the claim that
// ended the job. A job from before the version has none, and its result is never stored for reuse.
var resultReuseColumns = []struct{ name, def string }{
	{"def_hash", `TEXT NOT NULL DEFAULT ''`},
	{"claim_labels", `TEXT NOT NULL DEFAULT ''`},
	{"claim_fingerprint", `TEXT NOT NULL DEFAULT ''`},
}

// migrateV6ToV7 adds the columns result reuse needs. It can run again: each column is looked for first. Going back to an older wwd needs the
// wwd.db backup from before this step (the older code refuses a newer schema).
func migrateV6ToV7(ctx context.Context, c *sql.Conn) error {
	for _, col := range resultReuseColumns {
		var n int
		if err := c.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('jobs') WHERE name = ?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := c.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN `+col.name+` `+col.def); err != nil {
				return err
			}
		}
	}
	return nil
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
	revUpdTrigger(revColumns),
}

// revColumns are the columns whose change moves jobs.rev; revColumnsV4 are the ones before version 6 added the manual place (a database
// brought forward from version 3 gets the trigger with the full list at step 4, which SQLite accepts before the columns exist, and
// step 6 replaces it all the same).
const (
	revColumnsV4 = `state, result, cancel_requested, superseded_by, log_size, claim_id`
	revColumns   = revColumnsV4 + `, manual, manual_seq`
)

func revUpdTrigger(cols string) string {
	return `CREATE TRIGGER IF NOT EXISTS jobs_rev_upd AFTER UPDATE OF ` + cols + ` ON jobs BEGIN
		UPDATE job_rev SET n = n + 1;
		UPDATE jobs SET rev = (SELECT n FROM job_rev) WHERE seq = NEW.seq;
	END`
}

// migrateV5ToV6 adds the manual place (jobs.manual, jobs.manual_seq) and makes the rev trigger watch them; CREATE TRIGGER IF NOT EXISTS
// does not replace an existing trigger, so the old one is dropped first. It can run again: each column is looked for first.
func migrateV5ToV6(ctx context.Context, c *sql.Conn) error {
	for _, col := range []struct{ name, def string }{
		{"manual", `TEXT NOT NULL DEFAULT ''`},
		{"manual_seq", `INTEGER NOT NULL DEFAULT 0`},
	} {
		var n int
		if err := c.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('jobs') WHERE name = ?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := c.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN `+col.name+` `+col.def); err != nil {
				return err
			}
		}
	}
	if _, err := c.ExecContext(ctx, `DROP TRIGGER IF EXISTS jobs_rev_upd`); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, revUpdTrigger(revColumns))
	return err
}

// epochStatements (version 5) give the database a name that is made once, with its tables, and never again: one row of 32 hex digits. A
// client that keeps a cursor keeps this with it, and a database that was removed and made again has another, which tells the client its
// cursor is of no use here. The insert only happens when the table is empty, so running it again (a migration repeated) keeps the first.
//
// The epoch is the history of the database FILE. Putting back an older copy of wwd.db puts back its epoch, and a client whose cursor is
// ahead of that copy's head is only told so (409) until the head catches up to it; after that the cursor is taken and the changes below
// it are missed. So a restore is not complete until its epoch is replaced:
//
//	UPDATE db_epoch SET id = lower(hex(randomblob(16)));
//
// (an operator's step in the restore procedure, with wwd stopped). Nothing here can tell a restore from a restart.
var epochStatements = []string{
	`CREATE TABLE IF NOT EXISTS db_epoch (id TEXT NOT NULL)`,
	`INSERT INTO db_epoch (id) SELECT lower(hex(randomblob(16))) WHERE NOT EXISTS (SELECT 1 FROM db_epoch)`,
}

// migrateV4ToV5 gives an existing database its epoch.
func migrateV4ToV5(ctx context.Context, c *sql.Conn) error {
	for _, stmt := range epochStatements {
		if _, err := c.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
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

// createSchema is the tables of a new database, as of the current version (git_pins, version 2, jobs.lapses, version 3, the cursor,
// version 4, the epoch, version 5, and the manual place, version 6, are in it). Times are unix milliseconds on the origin's clock.
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
			rev              INTEGER NOT NULL DEFAULT 0,
			manual           TEXT NOT NULL DEFAULT '',
			manual_seq       INTEGER NOT NULL DEFAULT 0,
			def_hash          TEXT NOT NULL DEFAULT '',
			claim_labels      TEXT NOT NULL DEFAULT '',
			claim_fingerprint TEXT NOT NULL DEFAULT '',
			started_at        INTEGER
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
	for _, stmt := range append(append(append([]string{}, revStatements...), epochStatements...), outcomeStatements...) {
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
