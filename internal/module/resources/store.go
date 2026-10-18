package resourcesmod

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// dbFileMode: the rows name sessions, pids and tool use ids, and the data dir
// itself is 0755, so the database and its WAL sidecars are owner-only.
const dbFileMode = 0o600

// restrictDBFiles chmods the database and whichever of its WAL siblings exist.
func restrictDBFiles(path string) error {
	if err := os.Chmod(path, dbFileMode); err != nil {
		return fmt.Errorf("chmod resources db: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, dbFileMode); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("chmod resources db%s: %w", suffix, err)
		}
	}
	return nil
}

// resourcesStore is resources.db: the host_minutes timeline, and the retired lease tables (resource_leases,
// resource_positions), which are still created and migrated so an existing database is left as it is; WWD-9 drops them.
type resourcesStore struct {
	db *sql.DB
}

// openResourcesStore opens (or creates) resources.db at path. ":memory:" is for
// tests.
func openResourcesStore(path string) (*resourcesStore, error) {
	dsn := path
	if path != ":memory:" {
		// Created private before SQLite sees it: it gives its WAL sidecars the
		// mode of the main file, so they are private from their first byte.
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, dbFileMode)
		if err != nil {
			return nil, fmt.Errorf("create resources db: %w", err)
		}
		_ = f.Close()
		// The mode above only applies to a new file: one an earlier run left
		// loose, and its sidecars, are tightened before SQLite opens them.
		if err := restrictDBFiles(path); err != nil {
			return nil, err
		}
		// busy_timeout: a concurrent writer waits instead of failing with SQLITE_BUSY.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open resources db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS resource_leases (
			id            TEXT PRIMARY KEY,
			client_id     TEXT    NOT NULL UNIQUE,
			state         TEXT    NOT NULL CHECK (state IN ('waiting','held','ended')),
			kind          TEXT    NOT NULL DEFAULT '',
			weight        INTEGER NOT NULL,
			session_id    TEXT    NOT NULL DEFAULT '',
			holder_pid    INTEGER NOT NULL,
			holder_start  TEXT    NOT NULL DEFAULT '',
			scope         TEXT    NOT NULL CHECK (scope IN ('process','session-new')),
			tool_use_id   TEXT    NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			deadline_at   INTEGER NOT NULL,
			lease_until   INTEGER NOT NULL,
			granted_at    INTEGER,
			ended_at      INTEGER,
			overrun       INTEGER NOT NULL DEFAULT 0,
			would_wait    INTEGER NOT NULL DEFAULT 0,
			end_reason    TEXT,
			waited_ms     INTEGER,
			peak_use      REAL,
			mean_use      REAL,
			ewma          REAL,
			samples       INTEGER NOT NULL DEFAULT 0,
			empty_samples INTEGER NOT NULL DEFAULT 0,
			baseline      TEXT
		);
		CREATE INDEX IF NOT EXISTS resource_leases_state ON resource_leases (state);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate resources db: %w", err)
	}
	if err := migrateDecisionColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		// A file an earlier run left loose, and any sidecar it left, are
		// tightened now that the migration has made SQLite create them.
		if err := restrictDBFiles(path); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &resourcesStore{db: db}, nil
}

// Close closes the database.
func (s *resourcesStore) Close() error { return s.db.Close() }

// positionsSchema is the amendment A table. One live position per session is
// enforced by the unique partial index (V15), not by code.
const positionsSchema = `
	CREATE TABLE IF NOT EXISTS resource_positions (
		id               TEXT PRIMARY KEY,
		session_id       TEXT    NOT NULL,
		agent_pid        INTEGER NOT NULL DEFAULT 0,
		agent_start      TEXT    NOT NULL DEFAULT '',
		state            TEXT    NOT NULL CHECK (state IN ('queued','offered','running','idle','ended')),
		created_at       INTEGER NOT NULL,
		requeued_from    TEXT    NOT NULL DEFAULT '',
		offered_at       INTEGER NOT NULL DEFAULT 0,
		delivered_at     INTEGER NOT NULL DEFAULT 0,
		passed_at        INTEGER NOT NULL DEFAULT 0,
		offer_passes     INTEGER NOT NULL DEFAULT 0,
		first_started_at INTEGER NOT NULL DEFAULT 0,
		window_until     INTEGER NOT NULL DEFAULT 0,
		commands_started INTEGER NOT NULL DEFAULT 0,
		commands_ended   INTEGER NOT NULL DEFAULT 0,
		warned           INTEGER NOT NULL DEFAULT 0,
		extensions       INTEGER NOT NULL DEFAULT 0,
		done_requested   INTEGER NOT NULL DEFAULT 0,
		notice_kind      TEXT    NOT NULL DEFAULT '',
		notice_due_at    INTEGER NOT NULL DEFAULT 0,
		notice_sent_at   INTEGER NOT NULL DEFAULT 0,
		notice_tries     INTEGER NOT NULL DEFAULT 0,
		notice_result    TEXT    NOT NULL DEFAULT '',
		offer_wait_ms    INTEGER NOT NULL DEFAULT 0,
		idle_wait_ms     INTEGER NOT NULL DEFAULT 0,
		offer_zone       TEXT    NOT NULL DEFAULT '',
		offer_trend      TEXT    NOT NULL DEFAULT '',
		wait_reasons     TEXT    NOT NULL DEFAULT '',
		end_reason       TEXT    NOT NULL DEFAULT '',
		ended_at         INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX IF NOT EXISTS resource_positions_live ON resource_positions (session_id) WHERE state != 'ended';
	CREATE INDEX IF NOT EXISTS resource_positions_state ON resource_positions (state);`

// decisionColumns are the D-8.1 columns of resource_leases, added to a
// database that alpha.610 already created (resources.db is live: every schema
// change is a migration). Only dec_recorded tells a real decision from a row
// that predates the columns: a row stays 0 until the grant that writes the
// snapshot sets it to 1, and the report counts only those. The defaults of
// the other columns carry no meaning.
var decisionColumns = []struct{ name, def string }{
	{"dec_recorded", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_load1", "REAL NOT NULL DEFAULT 0"},
	{"dec_ncpu", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_mem", "REAL NOT NULL DEFAULT 0"},
	{"dec_measured", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_full", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_sum_charge", "REAL NOT NULL DEFAULT 0"},
	{"dec_unleased", "REAL NOT NULL DEFAULT 0"},
	{"dec_weight", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_path", "TEXT NOT NULL DEFAULT ''"},
	{"would_wait_r2", "INTEGER NOT NULL DEFAULT 0"},
	// Lease v2 (spec D-7): the zone, the trend, whether a lease was running
	// and the gap left at the decision; how often the row was parked (P2a);
	// and the JSON of milliseconds waited by reason.
	{"dec_zone", "TEXT NOT NULL DEFAULT ''"},
	{"dec_trend", "TEXT NOT NULL DEFAULT ''"},
	{"dec_running", "INTEGER NOT NULL DEFAULT 0"},
	{"dec_gap_ms", "INTEGER NOT NULL DEFAULT 0"},
	{"parks", "INTEGER NOT NULL DEFAULT 0"},
	{"parked_until", "INTEGER NOT NULL DEFAULT 0"},
	{"parkable", "INTEGER NOT NULL DEFAULT 0"},
	{"wait_reasons", "TEXT NOT NULL DEFAULT ''"},
	// The advise shadow queue (P1c): how long v2 would have made the request
	// wait (-1: not recorded), and whether that is only a lower bound because
	// the row ended first.
	{"v2_wait_ms", "INTEGER NOT NULL DEFAULT -1"},
	{"v2_wait_open", "INTEGER NOT NULL DEFAULT 0"},
	// Amendment A: the position a row belongs to, how its caller wants a busy
	// host answered, what the queue shows, and the offer / confirm / notice
	// bookkeeping of the row.
	{"position_id", "TEXT NOT NULL DEFAULT ''"},
	{"mode", "TEXT NOT NULL DEFAULT ''"},
	{"command", "TEXT NOT NULL DEFAULT ''"},
	{"cwd", "TEXT NOT NULL DEFAULT ''"},
	{"queued_reason", "TEXT NOT NULL DEFAULT ''"},
	{"offered_at", "INTEGER NOT NULL DEFAULT 0"},
	{"confirmed_at", "INTEGER NOT NULL DEFAULT 0"},
	{"notice", "TEXT NOT NULL DEFAULT ''"},
	// P2f: how many nested answers (a run below this row's holder) it covered.
	{"nested_runs", "INTEGER NOT NULL DEFAULT 0"},
}

// hostMinutesSchema is the D-8.2 timeline: one row per minute, written by the
// sampler goroutine (at is the minute's start, unix ms). The figures are the
// minute's peaks, except ncpu (the last tick's), full (any tick) and the
// three full_* counters (see minute.go).
const hostMinutesSchema = `
	CREATE TABLE IF NOT EXISTS host_minutes (
		at             INTEGER PRIMARY KEY,
		load1          REAL    NOT NULL,
		ncpu           INTEGER NOT NULL,
		mem            REAL    NOT NULL,
		measured       INTEGER NOT NULL,
		full           INTEGER NOT NULL,
		full_ticks     INTEGER NOT NULL,
		full_starts    INTEGER NOT NULL,
		full_longest_s INTEGER NOT NULL,
		held           INTEGER NOT NULL,
		heavy_held     INTEGER NOT NULL,
		sum_charge     REAL    NOT NULL,
		waiting        INTEGER NOT NULL,
		unleased       REAL    NOT NULL
	)`

// minuteColumns are the lease v2 columns of host_minutes (spec D-7): the seconds
// sampled in each zone (zone shares are x_s / sampled_s; a row from before has
// sampled_s = 0 and is left out of them), and the minute's worst pressure and
// zone (0 normal, 1 warning, 2 danger). The waiting peak is the existing
// `waiting` column.
var minuteColumns = []struct{ name, def string }{
	{"sampled_s", "INTEGER NOT NULL DEFAULT 0"},
	{"normal_s", "INTEGER NOT NULL DEFAULT 0"},
	{"warning_s", "INTEGER NOT NULL DEFAULT 0"},
	{"danger_s", "INTEGER NOT NULL DEFAULT 0"},
	{"pressure_max", "INTEGER NOT NULL DEFAULT 0"},
	{"zone_max", "INTEGER NOT NULL DEFAULT 0"},
	// Host load control (L2): the minute's peaks of the gate's slot records, which replace the lease columns above
	// (held, heavy_held, sum_charge, waiting, unleased, written 0 from L2 on; a row from before keeps them).
	{"heavy_running", "INTEGER NOT NULL DEFAULT 0"},
	{"light_running", "INTEGER NOT NULL DEFAULT 0"},
	{"gate_waiting", "INTEGER NOT NULL DEFAULT 0"},
}

// migrateDecisionColumns adds what the D-8 monitoring record needs, and is a
// no-op on a database that has it.
func migrateDecisionColumns(db *sql.DB) error {
	have, err := columnSet(db, "resource_leases")
	if err != nil {
		return fmt.Errorf("migrate resources db: %w", err)
	}
	for _, c := range decisionColumns {
		if have[c.name] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE resource_leases ADD COLUMN " + c.name + " " + c.def); err != nil {
			return fmt.Errorf("migrate resources db: add %s: %w", c.name, err)
		}
	}
	if _, err := db.Exec(positionsSchema); err != nil {
		return fmt.Errorf("migrate resources db: positions: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS resource_leases_position ON resource_leases (position_id) WHERE position_id != ''`); err != nil {
		return fmt.Errorf("migrate resources db: position index: %w", err)
	}
	if _, err := db.Exec(hostMinutesSchema); err != nil {
		return fmt.Errorf("migrate resources db: host_minutes: %w", err)
	}
	haveMin, err := columnSet(db, "host_minutes")
	if err != nil {
		return fmt.Errorf("migrate resources db: %w", err)
	}
	for _, c := range minuteColumns {
		if haveMin[c.name] {
			continue
		}
		if err := addColumn(db, "host_minutes", c.name, c.def); err != nil {
			return fmt.Errorf("migrate resources db: add host_minutes.%s: %w", c.name, err)
		}
	}
	return nil
}

// addColumn adds a column. A column that is there already — another opener of the same database added it between our look at
// the columns and this statement — is what was wanted, not an error.
func addColumn(db *sql.DB, table, name, def string) error {
	_, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + def)
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

// columnSet lists a table's column names.
func columnSet(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info('" + table + "')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// minuteRow is one host_minutes row.
type minuteRow struct {
	At              int64
	Load1           float64
	NCPU            int
	Mem             float64
	Measured        int
	Full            bool
	FullTicks       int
	FullStarts      int
	FullLongestS    int
	Held, HeavyHeld int
	SumCharge       float64
	Waiting         int
	Unleased        float64
	// Lease v2: seconds sampled (in all, and per zone), the minute's worst
	// pressure level and zone.
	SampledS, NormalS, WarningS, DangerS int
	PressureMax, ZoneMax                 int
	// Host load control: the minute's peaks of the heavy and light commands holding a slot and of the commands waiting for one.
	HeavyRunning, LightRunning, GateWaiting int
}

// InsertMinute stores one row. A minute that is already stored is merged, not
// replaced: peaks take the larger, the full counters add up, ncpu is the
// newer. So a sampler that went off and on inside a minute, a restart in the
// minute, or a clock that stepped back can only add to what was written.
func (s *resourcesStore) InsertMinute(r minuteRow) error {
	full := 0
	if r.Full {
		full = 1
	}
	if _, err := s.db.Exec(`INSERT INTO host_minutes
		(at, load1, ncpu, mem, measured, full, full_ticks, full_starts, full_longest_s, held, heavy_held, sum_charge, waiting, unleased,
			sampled_s, normal_s, warning_s, danger_s, pressure_max, zone_max, heavy_running, light_running, gate_waiting)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(at) DO UPDATE SET
			load1 = max(load1, excluded.load1), ncpu = excluded.ncpu, mem = max(mem, excluded.mem),
			measured = max(measured, excluded.measured), full = max(full, excluded.full),
			full_ticks = full_ticks + excluded.full_ticks, full_starts = full_starts + excluded.full_starts,
			full_longest_s = max(full_longest_s, excluded.full_longest_s), held = max(held, excluded.held),
			heavy_held = max(heavy_held, excluded.heavy_held), sum_charge = max(sum_charge, excluded.sum_charge),
			waiting = max(waiting, excluded.waiting), unleased = max(unleased, excluded.unleased),
			sampled_s = sampled_s + excluded.sampled_s, normal_s = normal_s + excluded.normal_s,
			warning_s = warning_s + excluded.warning_s, danger_s = danger_s + excluded.danger_s,
			pressure_max = max(pressure_max, excluded.pressure_max), zone_max = max(zone_max, excluded.zone_max),
			heavy_running = max(heavy_running, excluded.heavy_running), light_running = max(light_running, excluded.light_running),
			gate_waiting = max(gate_waiting, excluded.gate_waiting)`,
		r.At, r.Load1, r.NCPU, r.Mem, r.Measured, full, r.FullTicks, r.FullStarts, r.FullLongestS, r.Held, r.HeavyHeld,
		r.SumCharge, r.Waiting, r.Unleased, r.SampledS, r.NormalS, r.WarningS, r.DangerS, r.PressureMax, r.ZoneMax,
		r.HeavyRunning, r.LightRunning, r.GateWaiting); err != nil {
		return fmt.Errorf("insert host minute: %w", err)
	}
	return nil
}

// PruneMinutes deletes the minute rows before the cutoff (unix ms).
func (s *resourcesStore) PruneMinutes(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM host_minutes WHERE at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("prune host minutes: %w", err)
	}
	return res.RowsAffected()
}
