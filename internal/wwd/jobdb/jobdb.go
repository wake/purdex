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
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

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

// Manager creates and removes the per-job databases.
type Manager struct {
	ex  Exec
	cfg Config
}

// New returns a Manager that sends its statements to ex.
func New(ex Exec, cfg Config) *Manager { return &Manager{ex: ex, cfg: cfg.withDefaults()} }

// Info is what a job needs to connect.
type Info struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// Env is the job's environment entries ("NAME=value", for the manifest runner's TaskEnv). TODO(first-run): the variable names are
// the usual Laravel ones; the task that uses them decides.
func (i Info) Env() []string {
	return []string{"DB_CONNECTION=mysql", "DB_HOST=" + i.Host, "DB_PORT=" + strconv.Itoa(i.Port), "DB_DATABASE=" + i.Database,
		"DB_USERNAME=" + i.User, "DB_PASSWORD=" + i.Password}
}

var (
	dbNameRe   = regexp.MustCompile(`^wwd_[0-9a-f]{16}$`)
	userNameRe = regexp.MustCompile(`^wwdu_[0-9a-f]{12}$`)
)

func digest(jobID string) string {
	h := sha256.Sum256([]byte(jobID))
	return hex.EncodeToString(h[:])
}

// Name is the job's database name: opaque, stable for one job id, and always matched by dbNameRe.
func (m *Manager) Name(jobID string) string { return "wwd_" + digest(jobID)[:16] }

// User is the job's MySQL user (at most 17 characters, MySQL allows 32).
func (m *Manager) User(jobID string) string { return "wwdu_" + digest(jobID)[:12] }

// Create makes the job's database and user. Whatever an earlier run of the same job id left is dropped first, so the job starts
// from an empty database; if a later statement fails, what was made is dropped again.
func (m *Manager) Create(ctx context.Context, jobID string) (Info, error) {
	_ = m.Drop(ctx, jobID)
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return Info{}, err
	}
	info := Info{Host: m.cfg.Host, Port: m.cfg.Port, Database: m.Name(jobID), User: m.User(jobID), Password: hex.EncodeToString(pw)}
	stmts := []string{"CREATE DATABASE `" + info.Database + "`"}
	for _, h := range m.cfg.UserHosts {
		stmts = append(stmts,
			fmt.Sprintf("CREATE USER '%s'@'%s' IDENTIFIED BY '%s'", info.User, h, info.Password),
			fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s'", info.Database, info.User, h))
	}
	for _, s := range stmts {
		if _, err := m.ex.Run(ctx, s); err != nil {
			_ = m.Drop(ctx, jobID)
			return Info{}, fmt.Errorf("jobdb: %s: %w", firstWords(s), err)
		}
	}
	return info, nil
}

// Drop removes the job's database and users. It tries every statement and returns the first error.
func (m *Manager) Drop(ctx context.Context, jobID string) error {
	return m.dropNames(ctx, m.Name(jobID), m.User(jobID))
}

func (m *Manager) dropNames(ctx context.Context, db, user string) error {
	var first error
	stmts := []string{"DROP DATABASE IF EXISTS `" + db + "`"}
	for _, h := range m.cfg.UserHosts {
		stmts = append(stmts, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'", user, h))
	}
	for _, s := range stmts {
		if _, err := m.ex.Run(ctx, s); err != nil && first == nil {
			first = fmt.Errorf("jobdb: %s: %w", firstWords(s), err)
		}
	}
	return first
}

// Sweep removes the databases and users this package made for jobs that are not in live (a crash or a restart left them). Only
// names of exactly our shape are touched: another database that happens to begin with wwd_ is not.
func (m *Manager) Sweep(ctx context.Context, live []string) (dropped []string, err error) {
	keepDB, keepUser := map[string]bool{}, map[string]bool{}
	for _, id := range live {
		keepDB[m.Name(id)], keepUser[m.User(id)] = true, true
	}
	dbOut, err := m.ex.Run(ctx, `SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'wwd\_%'`)
	if err != nil {
		return nil, fmt.Errorf("jobdb: list databases: %w", err)
	}
	userOut, err := m.ex.Run(ctx, `SELECT DISTINCT user FROM mysql.user WHERE user LIKE 'wwdu\_%'`)
	if err != nil {
		return nil, fmt.Errorf("jobdb: list users: %w", err)
	}
	var first error
	note := func(e error) {
		if e != nil && first == nil {
			first = e
		}
	}
	for _, db := range lines(dbOut) {
		if dbNameRe.MatchString(db) && !keepDB[db] {
			_, e := m.ex.Run(ctx, "DROP DATABASE IF EXISTS `"+db+"`")
			note(e)
			if e == nil {
				dropped = append(dropped, db)
			}
		}
	}
	for _, u := range lines(userOut) {
		if userNameRe.MatchString(u) && !keepUser[u] {
			for _, h := range m.cfg.UserHosts {
				_, e := m.ex.Run(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'", u, h))
				note(e)
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

// firstWords names a statement in an error without its password.
func firstWords(s string) string {
	f := strings.Fields(s)
	if len(f) > 2 {
		f = f[:2]
	}
	return strings.Join(f, " ")
}

// CLI runs statements with the mysql client. DefaultsFile (an administrator's [client] section: user, password, socket or host)
// is passed as --defaults-extra-file, so no secret is on the command line.
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
	args = append(args, "-N", "-B", "-e", sql)
	cmd := execstat.CommandContext(ctx, "wwd.jobdb", bin, args...)
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
