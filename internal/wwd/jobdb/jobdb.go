// Package jobdb gives a job its own MySQL database (spec §5, tasks with db = "per-job", mserv): one database and one user per
// job, the user holding privileges on that database alone, a random password, and everything removed after the job. It talks to
// MySQL through the mysql client with an administrator's defaults file, so the module adds no driver and no password ever
// appears on a command line. Every name in a statement is derived from a hash of the job id and every secret is hex, so no
// statement holds anything a job id or a manifest could shape.
package jobdb

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// ControlSQL installs the routines every statement of this package is a call of, in the database wwdctl; the administrator account
// holds EXECUTE on that database and nothing else. scripts/wwd-mysql-setup.sh installs it; docs/wwd-deploy.md says how.
//
//go:embed wwdctl.sql
var ControlSQL string

// Exec runs one SQL statement as the administrator and returns its output, one row per line.
type Exec interface {
	Run(ctx context.Context, sql string) (string, error)
}

// Config says where the database is and how a job reaches it.
type Config struct {
	Host      string   // what a job connects to; default 127.0.0.1
	Port      int      // default 3306
	UserHosts []string // the hosts the job's user may connect from; default localhost and 127.0.0.1
}

func (c Config) withDefaults() Config {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 3306
	}
	if len(c.UserHosts) == 0 {
		c.UserHosts = []string{"localhost", "127.0.0.1"}
	}
	return c
}

// Manager creates and removes the per-job databases. Create, Drop and Sweep are serialized, so a Sweep never removes what a
// Create is making. The caller keeps two rules the Manager cannot enforce: Sweep runs before the worker takes its first job, and
// Drop runs only after every process of the job is gone (the runner proved that), because a process that is still there can
// connect again.
type Manager struct {
	mu  sync.Mutex
	ex  Exec
	cfg Config
	err error // why the configuration cannot be used; every operation returns it
}

// New returns a Manager that sends its statements to ex. A configuration whose hosts are not plain host names or addresses is
// not used: they go into statements, and the administrator's rights run them.
func New(ex Exec, cfg Config) *Manager {
	m := &Manager{ex: ex, cfg: cfg.withDefaults()}
	for _, h := range m.cfg.UserHosts {
		if !cfgHostRe.MatchString(h) {
			m.err = fmt.Errorf("jobdb: the user host %q is not a plain host name or address", h)
		}
	}
	return m
}

// Info is what a job needs to connect.
type Info struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// WPTestsEnv is the same database under the names at-inwin-plugin's phpunit config reads (tests/phpunit/wp-tests-config.php:
// WP_TESTS_DB_NAME, WP_TESTS_DB_HOST with the port, WP_TESTS_DB_USER, WP_TESTS_DB_PASSWORD; the table prefix stays wptests_). Whether
// a job can use them depends on that file's guard, which today refuses every value but its own fixed ones; it is that repo's
// decision to relax it.
func (i Info) WPTestsEnv() []string {
	return []string{"WP_TESTS_DB_NAME=" + i.Database, "WP_TESTS_DB_HOST=" + net.JoinHostPort(i.Host, strconv.Itoa(i.Port)),
		"WP_TESTS_DB_USER=" + i.User, "WP_TESTS_DB_PASSWORD=" + i.Password, "WP_PHPUNIT__TABLE_PREFIX=wptests_"}
}

// Env is the job's environment entries ("NAME=value", for the manifest runner's TaskEnv). TODO(first-run): the variable names are
// the usual Laravel ones; the task that uses them decides.
func (i Info) Env() []string {
	return []string{"DB_CONNECTION=mysql", "DB_HOST=" + i.Host, "DB_PORT=" + strconv.Itoa(i.Port), "DB_DATABASE=" + i.Database,
		"DB_USERNAME=" + i.User, "DB_PASSWORD=" + i.Password}
}

var (
	dbNameRe   = regexp.MustCompile(`^wwd_[0-9a-f]{32}$`)
	userNameRe = regexp.MustCompile(`^wwdu_[0-9a-f]{26}$`)
	cfgHostRe  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`) // a configured host: no wildcard either, and no longer than the routines accept (VARCHAR(64))
)

func digest(jobID string) string {
	h := sha256.Sum256([]byte(jobID))
	return hex.EncodeToString(h[:])
}

// Name is the job's database name: opaque, stable for one job id, 128 bits of its hash, and always matched by dbNameRe.
func (m *Manager) Name(jobID string) string { return "wwd_" + digest(jobID)[:32] }

// User is the job's MySQL user: 104 bits of the hash, 31 characters (MySQL allows 32).
func (m *Manager) User(jobID string) string { return "wwdu_" + digest(jobID)[:26] }

// Create makes the job's database and user. Whatever an earlier run of the same job id left is dropped first, so the job starts
// from an empty database; if a later statement fails, what was made is dropped again.
func (m *Manager) Create(ctx context.Context, jobID string) (Info, error) {
	if m.err != nil {
		return Info{}, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// the earlier run's leftovers must be gone before anything is made: wwd_create_db leaves an existing database as it is, so a
	// clean-up that failed would give the new user a database that still holds the old data
	if err := m.drop(ctx, jobID); err != nil {
		return Info{}, fmt.Errorf("jobdb: clear what an earlier run of %s left: %w", jobID, err)
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return Info{}, err
	}
	info := Info{Host: m.cfg.Host, Port: m.cfg.Port, Database: m.Name(jobID), User: m.User(jobID), Password: hex.EncodeToString(pw)}
	stmts := []string{call("wwd_create_db", info.Database)}
	for _, h := range m.cfg.UserHosts {
		stmts = append(stmts, call("wwd_create_user", info.User, info.Password, h, info.Database))
	}
	for _, s := range stmts {
		if _, err := m.ex.Run(ctx, s); err != nil {
			// the caller's context may be the very reason for the failure: the clean-up must not inherit its cancel
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_ = m.drop(cctx, jobID)
			cancel()
			return Info{}, fmt.Errorf("jobdb: %s: %w", firstWords(s), err)
		}
	}
	return info, nil
}

// Drop removes the job's database and users, and ends the user's open connections first (DROP USER leaves them alone). It tries
// every statement and returns the first error.
func (m *Manager) Drop(ctx context.Context, jobID string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.drop(ctx, jobID)
}

func (m *Manager) drop(ctx context.Context, jobID string) error {
	return m.dropNames(ctx, m.Name(jobID), m.User(jobID))
}

func (m *Manager) dropNames(ctx context.Context, db, user string) error {
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	// the connections of the user, then the user, then the database
	note(m.killConnections(ctx, user))
	for _, h := range m.cfg.UserHosts {
		note(m.dropUserAt(ctx, user, h))
	}
	note(m.dropDB(ctx, db))
	return first
}

// killConnections ends the open connections of a user: DROP USER leaves them running with the rights they had. The routine answers
// how many are still listed afterwards; anything but 0 is an error.
func (m *Manager) killConnections(ctx context.Context, user string) error {
	out, err := m.ex.Run(ctx, call("wwd_kill_user", user))
	if err != nil {
		return fmt.Errorf("jobdb: end the connections of %s: %w", user, err)
	}
	if n := strings.TrimSpace(out); n != "0" {
		return fmt.Errorf("jobdb: %q connection(s) of %s are still open after KILL", n, user)
	}
	return nil
}

func (m *Manager) dropUserAt(ctx context.Context, user, host string) error {
	if _, err := m.ex.Run(ctx, call("wwd_drop_user", user, host)); err != nil {
		return fmt.Errorf("jobdb: drop user: %w", err)
	}
	return nil
}

func (m *Manager) dropDB(ctx context.Context, db string) error {
	if _, err := m.ex.Run(ctx, call("wwd_drop_db", db)); err != nil {
		return fmt.Errorf("jobdb: drop database: %w", err)
	}
	return nil
}

// call is a statement that runs one of the routines of wwdctl.sql. Its arguments are hash-derived names, hex secrets and checked
// hosts; the routine checks them again, so a wrong one is refused there as well.
func call(routine string, args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + a + "'"
	}
	return "CALL wwdctl." + routine + "(" + strings.Join(q, ",") + ")"
}

// Sweep removes the databases and users this package made for jobs that are not in live (a crash or a restart left them). Only
// names of exactly our shape are touched: another database that happens to begin with wwd_ is not.
func (m *Manager) Sweep(ctx context.Context, live []string) (dropped []string, err error) {
	if m.err != nil {
		return nil, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keepDB, keepUser := map[string]bool{}, map[string]bool{}
	for _, id := range live {
		keepDB[m.Name(id)], keepUser[m.User(id)] = true, true
	}
	dbOut, err := m.ex.Run(ctx, call("wwd_list_dbs"))
	if err != nil {
		return nil, fmt.Errorf("jobdb: list databases: %w", err)
	}
	userOut, err := m.ex.Run(ctx, call("wwd_list_users"))
	if err != nil {
		return nil, fmt.Errorf("jobdb: list users: %w", err)
	}
	var first error
	note := func(e error) {
		if e != nil && first == nil {
			first = e
		}
	}
	// Every user/host pair that is really there, whatever hosts the config named when it was made. A user of our shape whose host the
	// routines would refuse cannot be removed here: it is reported, and the database it belongs to is not dropped (a grant on a
	// database name outlives the database, and would give the user the next database of that name).
	stuck := map[string]bool{}
	for _, l := range lines(userOut) {
		u, h, ok := strings.Cut(l, "\t")
		if !ok || !userNameRe.MatchString(u) || keepUser[u] {
			continue
		}
		if !cfgHostRe.MatchString(h) {
			stuck[u] = true
			note(fmt.Errorf("jobdb: the user %s has the host %q, which cannot be dropped here: drop it by hand", u, h))
			continue
		}
		if e := m.killConnections(ctx, u); e != nil {
			stuck[u] = true
			note(e)
		}
		note(m.dropUserAt(ctx, u, h))
	}
	for _, db := range lines(dbOut) {
		if dbNameRe.MatchString(db) && !keepDB[db] {
			if stuck["wwdu_"+db[4:30]] { // the user is the first 26 hex of its database's 32
				continue
			}
			e := m.dropDB(ctx, db)
			note(e)
			if e == nil {
				dropped = append(dropped, db)
			}
		}
	}
	sort.Strings(dropped)
	return dropped, first
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// firstWords names a statement in an error without its arguments: a CALL of wwd_create_user carries a password.
func firstWords(s string) string {
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// CLI runs statements with the mysql client. DefaultsFile (an administrator's [client] section: user, password, socket or host)
// is passed as --defaults-extra-file, and the statements are read from standard input, so no secret is on the command line.
type CLI struct {
	Binary       string // default "mysql"
	DefaultsFile string
	Timeout      time.Duration // per statement; default 30 s
}

// Run implements Exec.
func (c CLI) Run(ctx context.Context, sql string) (string, error) {
	bin, to := c.Binary, c.Timeout
	if bin == "" {
		bin = "mysql"
	}
	if to <= 0 {
		to = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	var args []string
	if c.DefaultsFile != "" {
		args = append(args, "--defaults-extra-file="+c.DefaultsFile) // must be the first option
	}
	// the statements go in on standard input: CREATE USER holds a password, and an argument shows in the process list
	args = append(args, "-N", "-B")
	cmd := execstat.CommandContext(ctx, "wwd.jobdb", bin, args...)
	cmd.Stdin = strings.NewReader(sql + ";\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}
