CREATE TABLE approval_requests (
			id                TEXT PRIMARY KEY,
			kind              TEXT    NOT NULL,
			host_id           TEXT    NOT NULL,
			origin_session_id TEXT    NOT NULL,
			origin_json       TEXT    NOT NULL,
			payload_json      TEXT    NOT NULL,
			request_hash      TEXT    NOT NULL,
			state             TEXT    NOT NULL,
			created_at        INTEGER NOT NULL,
			deadline_at       INTEGER NOT NULL,
			lease_until       INTEGER NOT NULL,
			decided_by_json   TEXT,
			decided_at        INTEGER NOT NULL DEFAULT 0,
			grant_json        TEXT
		);
CREATE INDEX approval_requests_state_created
			ON approval_requests (state, created_at);
CREATE TABLE relay_ops (
		id              TEXT PRIMARY KEY,
		kind            TEXT    NOT NULL,
		host_id         TEXT    NOT NULL,
		session_id      TEXT    NOT NULL,
		new_session_id  TEXT    NOT NULL DEFAULT '',
		ref             TEXT    NOT NULL,
		new_ref         TEXT    NOT NULL DEFAULT '',
		team_id         TEXT    NOT NULL DEFAULT '',
		request_id      TEXT    NOT NULL DEFAULT '',
		state           TEXT    NOT NULL,
		reason          TEXT    NOT NULL DEFAULT '',
		handoff_path    TEXT    NOT NULL,
		pruned          INTEGER NOT NULL DEFAULT 0,
		used_percentage REAL,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL
	);
CREATE INDEX relay_ops_session_state ON relay_ops (session_id, state);
CREATE UNIQUE INDEX relay_ops_one_open ON relay_ops (session_id)
		WHERE state NOT IN ('done', 'failed', 'cancelled');
CREATE TABLE session_lineage (
		session_id             TEXT PRIMARY KEY,
		predecessor_session_id TEXT    NOT NULL,
		predecessor_ref        TEXT    NOT NULL,
		op_id                  TEXT    NOT NULL,
		at                     INTEGER NOT NULL
	);
CREATE TABLE session_prefs (
		session_id        TEXT PRIMARY KEY,
		self_relay_paused INTEGER NOT NULL DEFAULT 0,
		updated_at        INTEGER NOT NULL
	);
CREATE TABLE teams (
		id              TEXT PRIMARY KEY,
		host_id         TEXT    NOT NULL,
		lead_session_id TEXT    NOT NULL,
		lead_ref        TEXT    NOT NULL,
		grant_json      TEXT    NOT NULL,
		request_id      TEXT    NOT NULL UNIQUE,
		created_at      INTEGER NOT NULL,
		ended_at        INTEGER NOT NULL DEFAULT 0,
		end_reason      TEXT    NOT NULL DEFAULT ''
	, lead_usage_pct REAL, lead_usage_window INTEGER NOT NULL DEFAULT 0, lead_usage_model TEXT NOT NULL DEFAULT '', lead_usage_effort TEXT NOT NULL DEFAULT '', lead_usage_at INTEGER NOT NULL DEFAULT 0, team_name TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX teams_one_live_per_lead ON teams (lead_session_id) WHERE ended_at = 0;
CREATE TABLE team_members (
		spawn_op      TEXT PRIMARY KEY,
		team_id       TEXT    NOT NULL,
		host_id       TEXT    NOT NULL,
		session_id    TEXT    NOT NULL,
		ref           TEXT    NOT NULL,
		title         TEXT    NOT NULL DEFAULT '',
		cwd           TEXT    NOT NULL,
		tmux_session  TEXT    NOT NULL,
		tmux_id       TEXT    NOT NULL DEFAULT '',
		tmux_instance TEXT    NOT NULL DEFAULT '',
		pane_id       TEXT    NOT NULL DEFAULT '',
		pid           INTEGER NOT NULL DEFAULT 0,
		proc_start    TEXT    NOT NULL DEFAULT '',
		model         TEXT    NOT NULL DEFAULT '',
		effort        TEXT    NOT NULL DEFAULT '',
		state         TEXT    NOT NULL,
		created_at    INTEGER NOT NULL,
		updated_at    INTEGER NOT NULL
	, usage_pct REAL, usage_window INTEGER NOT NULL DEFAULT 0, usage_model TEXT NOT NULL DEFAULT '', usage_effort TEXT NOT NULL DEFAULT '', usage_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX team_members_team ON team_members (team_id, state);
CREATE UNIQUE INDEX team_members_one_active ON team_members (session_id) WHERE state = 'active';
CREATE TABLE spawn_ops (id TEXT PRIMARY KEY, team_id TEXT NOT NULL,
		host_id TEXT NOT NULL, request_hash TEXT NOT NULL, origin_session_id TEXT NOT NULL,
		cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
		effort TEXT NOT NULL DEFAULT '', tmux_name TEXT NOT NULL,
		tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '', step TEXT NOT NULL, state TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		launched_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX spawn_ops_running ON spawn_ops (team_id) WHERE state = 'running';
CREATE INDEX approval_requests_state_decided
			ON approval_requests (state, decided_at);
CREATE TABLE tasks (
		team_id             TEXT    NOT NULL,
		seq                 INTEGER NOT NULL,
		subject             TEXT    NOT NULL,
		description         TEXT    NOT NULL DEFAULT '',
		done_when_json      TEXT    NOT NULL DEFAULT '[]',
		status              TEXT    NOT NULL,
		owner_key           TEXT    NOT NULL,
		blocked_by_json     TEXT    NOT NULL DEFAULT '[]',
		created_by_ref      TEXT    NOT NULL,
		spawn_op            TEXT    NOT NULL DEFAULT '',
		metadata_json       TEXT    NOT NULL DEFAULT '{}',
		last_report_kind    TEXT    NOT NULL DEFAULT '',
		last_report_summary TEXT    NOT NULL DEFAULT '',
		last_report_at      INTEGER NOT NULL DEFAULT 0,
		last_turn_summary   TEXT    NOT NULL DEFAULT '',
		last_turn_at        INTEGER NOT NULL DEFAULT 0,
		last_turn_seq       INTEGER NOT NULL DEFAULT 0,
		created_at          INTEGER NOT NULL,
		updated_at          INTEGER NOT NULL,
		PRIMARY KEY (team_id, seq)
	);
CREATE UNIQUE INDEX tasks_spawn_op ON tasks (spawn_op) WHERE spawn_op != '';
CREATE INDEX tasks_owner ON tasks (team_id, owner_key, status);
CREATE TABLE "reports" (
		id          TEXT    NOT NULL,
		team_id     TEXT    NOT NULL,
		task_seq    INTEGER NOT NULL,
		member_key  TEXT    NOT NULL,
		kind        TEXT    NOT NULL,
		summary     TEXT    NOT NULL,
		fields_json TEXT    NOT NULL DEFAULT '{}',
		body        TEXT    NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		PRIMARY KEY (team_id, member_key, id));
CREATE INDEX reports_task ON reports (team_id, task_seq, created_at);
