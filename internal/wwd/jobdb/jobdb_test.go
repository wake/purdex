package jobdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeExec records every statement and answers the two listing queries from its own lists.
type fakeExec struct {
	mu        sync.Mutex
	sessions  []string      // ids answered to the processlist query
	stubborn  bool          // KILL leaves the sessions listed
	lists     int           // how many processlist queries were answered
	failList  int           // the processlist query with this number (1-based) fails
	hold      chan struct{} // when set, the database listing waits for it to be closed (a Sweep that is half way)
	held      chan struct{} // closed once a Run is waiting on hold
	sql       []string
	dead      []bool             // for each statement: was its context already cancelled
	cancel    context.CancelFunc // called when failOn matches, as a caller cancelling half way would
	dbs       []string
	users     []string
	failOn    string
	failError error
}

func (f *fakeExec) Run(ctx context.Context, sql string) (string, error) {
	if f.hold != nil && strings.Contains(sql, "information_schema.schemata") {
		close(f.held)
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sql = append(f.sql, sql)
	f.dead = append(f.dead, ctx.Err() != nil)
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		if f.cancel != nil {
			f.cancel()
		}
		if strings.Contains(f.failError.Error(), "Unknown thread id") {
			f.sessions = nil // it was gone before the KILL arrived
		}
		return "", f.failError
	}
	switch {
	case strings.Contains(sql, "information_schema.processlist"):
		f.lists++
		if f.failList > 0 && f.lists == f.failList {
			return "", errors.New("lost connection")
		}
		return strings.Join(f.sessions, "\n"), nil
	case strings.HasPrefix(sql, "KILL CONNECTION") && !f.stubborn:
		f.sessions = nil // the connections go when they are killed
		return "", nil
	case strings.Contains(sql, "information_schema.schemata"):
		return strings.Join(f.dbs, "\n"), nil
	case strings.Contains(sql, "mysql.user"):
		return strings.Join(f.users, "\n"), nil
	}
	return "", nil
}

func (f *fakeExec) all() string { return strings.Join(f.sql, "\n") }

func TestNamesAreOpaqueDeterministicAndShortEnough(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	a, b := m.Name("j_abc.1"), m.Name("j_abc.1")
	if a != b || a == m.Name("j_abc.2") {
		t.Fatalf("names must be stable per job and differ between jobs: %q %q", a, m.Name("j_abc.2"))
	}
	if !regexp.MustCompile(`^wwd_[0-9a-f]{32}$`).MatchString(a) || len(m.User("j_abc.1")) != 31 {
		t.Fatalf("db %q user %q", a, m.User("j_abc.1"))
	}
}

func TestCreateMakesADatabaseAndAUserThatSeesOnlyIt(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{Host: "127.0.0.1", Port: 3306})
	info, err := m.Create(context.Background(), "j_test1")
	if err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	db, user := m.Name("j_test1"), m.User("j_test1")
	for _, want := range []string{
		"CREATE DATABASE `" + db + "`",
		"CREATE USER '" + user + "'@'localhost' IDENTIFIED BY '" + info.Password + "'",
		"CREATE USER '" + user + "'@'127.0.0.1' IDENTIFIED BY '" + info.Password + "'",
		"GRANT ALL PRIVILEGES ON `" + db + "`.* TO '" + user + "'@'localhost'",
		"GRANT ALL PRIVILEGES ON `" + db + "`.* TO '" + user + "'@'127.0.0.1'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q in\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "*.*") {
		t.Fatalf("a job's user must not hold privileges on other databases:\n%s", sql)
	}
	if len(info.Password) < 32 || info.Database != db || info.User != user || info.Host != "127.0.0.1" || info.Port != 3306 {
		t.Fatalf("info = %+v", info)
	}
}

func TestCreateClearsWhatAnEarlierRunOfTheSameJobLeft(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{})
	if _, err := m.Create(context.Background(), "j_test1"); err != nil {
		t.Fatal(err)
	}
	drop, create := strings.Index(f.all(), "DROP DATABASE IF EXISTS"), strings.Index(f.all(), "CREATE DATABASE")
	if drop < 0 || create < 0 || drop > create {
		t.Fatalf("a re-run of the same job id must start from an empty database:\n%s", f.all())
	}
}

func TestEachJobGetsItsOwnPassword(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	a, _ := m.Create(context.Background(), "j_a")
	b, _ := m.Create(context.Background(), "j_b")
	if a.Password == b.Password {
		t.Fatal("passwords must be random per job")
	}
}

func TestCreateFailureDropsWhatWasMade(t *testing.T) {
	f := &fakeExec{failOn: "GRANT", failError: errors.New("boom")}
	m := New(f, Config{})
	if _, err := m.Create(context.Background(), "j_test1"); err == nil {
		t.Fatal("expected an error")
	}
	last := f.sql[len(f.sql)-1]
	if !strings.Contains(f.all(), "DROP USER IF EXISTS") || !strings.Contains(last, "DROP") {
		t.Fatalf("a half-made database must not be left:\n%s", f.all())
	}
}

func TestDropRemovesTheDatabaseAndBothUsers(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{})
	if err := m.Drop(context.Background(), "j_test1"); err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	for _, want := range []string{"DROP DATABASE IF EXISTS `" + m.Name("j_test1") + "`",
		"DROP USER IF EXISTS '" + m.User("j_test1") + "'@'localhost'", "DROP USER IF EXISTS '" + m.User("j_test1") + "'@'127.0.0.1'"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q in\n%s", want, sql)
		}
	}
}

func TestDropTriesEveryStatementAndReportsTheFirstError(t *testing.T) {
	f := &fakeExec{failOn: "DROP DATABASE", failError: errors.New("locked")}
	m := New(f, Config{})
	if err := m.Drop(context.Background(), "j_test1"); err == nil {
		t.Fatal("expected the error")
	}
	if !strings.Contains(f.all(), "DROP USER IF EXISTS") {
		t.Fatalf("the users must go even when the database could not:\n%s", f.all())
	}
}

func TestSweepDropsOnlyOurLeftoversThatNoLiveJobOwns(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	live, dead := m.Name("j_live"), m.Name("j_dead")
	f := &fakeExec{sessions: []string{"77"}, dbs: []string{live, dead, "wwd_notours", "mysql", "appdb"}, users: []string{m.User("j_live") + "\tlocalhost", m.User("j_dead") + "\tlocalhost", m.User("j_dead") + "\t10.0.0.%", "root\tlocalhost"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), []string{"j_live"})
	if err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	if !strings.Contains(sql, "DROP DATABASE IF EXISTS `"+dead+"`") || !strings.Contains(sql, "DROP USER IF EXISTS '"+m.User("j_dead")+"'@'localhost'") ||
		!strings.Contains(sql, "DROP USER IF EXISTS '"+m.User("j_dead")+"'@'10.0.0.%'") {
		t.Fatalf("the dead job's database and user must go:\n%s", sql)
	}
	if !strings.Contains(sql, "KILL CONNECTION 77") {
		t.Fatalf("a leftover user's open connections must be ended before it is dropped:\n%s", sql)
	}
	if k, d := strings.Index(sql, "KILL CONNECTION 77"), strings.Index(sql, "DROP DATABASE IF EXISTS `"+dead+"`"); k > d {
		t.Fatalf("in a Sweep, the users' connections end before any database goes:\n%s", sql)
	}
	for _, keep := range []string{live, m.User("j_live"), "wwd_notours", "`mysql`", "appdb", "'root'"} {
		if strings.Contains(sql, "DROP DATABASE IF EXISTS `"+keep+"`") || strings.Contains(sql, "DROP USER IF EXISTS '"+keep+"'") {
			t.Fatalf("%s must stay:\n%s", keep, sql)
		}
	}
	if len(dropped) != 1 || dropped[0] != dead {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestEnvNamesTheDatabaseAndKeepsThePasswordOutOfTheName(t *testing.T) {
	info := Info{Host: "127.0.0.1", Port: 3306, Database: "wwd_x", User: "wwd_u", Password: "secret"}
	got := strings.Join(info.Env(), "\n")
	for _, want := range []string{"DB_CONNECTION=mysql", "DB_HOST=127.0.0.1", "DB_PORT=3306", "DB_DATABASE=wwd_x", "DB_USERNAME=wwd_u", "DB_PASSWORD=secret"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestCLIExecSendsTheStatementOnStdinAndNotAsAnArgument(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE/args\"\ncat > \"$FAKE/stdin\"\necho one\necho two\n"
	if err := os.WriteFile(filepath.Join(dir, "mysql"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE", dir)
	ex := CLI{Binary: filepath.Join(dir, "mysql"), DefaultsFile: "/etc/wwd/admin.cnf"}
	const sql = "CREATE USER 'u'@'localhost' IDENTIFIED BY 'sekrit0123'"
	out, err := ex.Run(context.Background(), sql)
	if err != nil || out != "one\ntwo\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args"))
	if args := strings.Split(strings.TrimSpace(string(b)), "\n"); args[0] != "--defaults-extra-file=/etc/wwd/admin.cnf" {
		t.Fatalf("args = %q", args)
	}
	if strings.Contains(string(b), "sekrit0123") || strings.Contains(string(b), "CREATE") {
		t.Fatalf("the statement, and so the password, must not be an argument: %q", b)
	}
	in, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if !strings.Contains(string(in), sql) {
		t.Fatalf("stdin = %q", in)
	}
}

func TestCreateCleanUpSurvivesACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeExec{failOn: "GRANT", failError: errors.New("cancelled"), cancel: cancel}
	m := New(f, Config{})
	if _, err := m.Create(ctx, "j_test1"); err == nil {
		t.Fatal("expected an error")
	}
	n := 0
	for i, s := range f.sql {
		if strings.HasPrefix(s, "DROP") && i > 5 {
			n++
			if f.dead[i] {
				t.Fatalf("the clean-up ran on a cancelled context: %q", s)
			}
		}
	}
	if n == 0 {
		t.Fatalf("clean-up missing:\n%s", f.all())
	}
}

func TestCLIExecErrorCarriesMysqlsOwnMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mysql"), []byte("#!/bin/sh\necho 'ERROR 1045 denied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := CLI{Binary: filepath.Join(dir, "mysql")}.Run(context.Background(), "SELECT 1")
	if err == nil || !strings.Contains(err.Error(), "ERROR 1045 denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestSweepNeverPutsAHostWithQuotesIntoAStatement(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	f := &fakeExec{users: []string{m.User("j_dead") + "\tx'; DROP DATABASE mysql; --"}}
	m = New(f, Config{})
	if _, err := m.Sweep(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.all(), "DROP DATABASE mysql") {
		t.Fatalf("a host read back from the server was used as it is:\n%s", f.all())
	}
}

func TestUserHostsWithQuotesOrWildcardsAreRefused(t *testing.T) {
	for _, h := range []string{"localhost' IDENTIFIED BY 'x'; DROP DATABASE mysql; --", "%", "a b", "a\nb", "h`", "x\\y", ""} {
		f := &fakeExec{}
		m := New(f, Config{UserHosts: []string{"localhost", h}})
		if h == "" {
			continue // empty means the default hosts
		}
		if _, err := m.Create(context.Background(), "j"); err == nil {
			t.Errorf("%q: Create must refuse", h)
		}
		if m.Drop(context.Background(), "j") == nil {
			t.Errorf("%q: Drop must refuse", h)
		}
		if _, err := m.Sweep(context.Background(), nil); err == nil {
			t.Errorf("%q: Sweep must refuse", h)
		}
		if len(f.sql) != 0 {
			t.Errorf("%q: nothing may be sent: %v", h, f.sql)
		}
	}
}

func TestDropEndsTheUsersConnectionsBeforeDroppingTheUser(t *testing.T) {
	f := &fakeExec{sessions: []string{"11", "12", "13; DROP DATABASE x"}}
	m := New(f, Config{})
	if err := m.Drop(context.Background(), "j_test1"); err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	k1, k2, du := strings.Index(sql, "KILL CONNECTION 11"), strings.Index(sql, "KILL CONNECTION 12"), strings.Index(sql, "DROP USER")
	if k1 < 0 || k2 < 0 || du < 0 || k1 > du || k2 > du {
		t.Fatalf("connections must be killed before the user goes:\n%s", sql)
	}
	if strings.Contains(sql, "DROP DATABASE x") {
		t.Fatalf("a session id that is not a number was used:\n%s", sql)
	}
	if strings.Index(sql, "DROP DATABASE IF EXISTS") < du {
		t.Fatalf("the database goes last:\n%s", sql)
	}
}

func TestASweepInProgressHoldsBackACreate(t *testing.T) {
	f := &fakeExec{hold: make(chan struct{}), held: make(chan struct{})}
	m := New(f, Config{})
	sweepDone := make(chan struct{})
	go func() { _, _ = m.Sweep(context.Background(), nil); close(sweepDone) }()
	<-f.held // the Sweep is half way: it has listed nothing yet
	created := make(chan struct{})
	go func() { _, _ = m.Create(context.Background(), "j_new"); close(created) }()
	select {
	case <-created:
		t.Fatal("a Create ran while a Sweep was half way: the Sweep could remove what the Create made")
	case <-time.After(100 * time.Millisecond):
	}
	close(f.hold)
	<-sweepDone
	<-created
}

func TestAKillThatFailsOrDoesNotWorkIsReported(t *testing.T) {
	// a failure other than "the connection is already gone"
	f := &fakeExec{sessions: []string{"5"}, failOn: "KILL CONNECTION", failError: errors.New("ERROR 1095: You are not owner of thread 5")}
	if err := New(f, Config{}).Drop(context.Background(), "j"); err == nil || !strings.Contains(err.Error(), "KILL CONNECTION 5") {
		t.Fatalf("err = %v", err)
	}
	// the connection is gone by itself: not an error
	g := &fakeExec{sessions: []string{"5"}, failOn: "KILL CONNECTION", failError: errors.New("ERROR 1094: Unknown thread id: 5")}
	g.stubborn = false
	if err := New(g, Config{}).Drop(context.Background(), "j"); err != nil {
		t.Fatalf("an already gone connection is not a failure: %v", err)
	}
	// KILL answered but the session is still listed
	h := &fakeExec{sessions: []string{"5"}, stubborn: true}
	if err := New(h, Config{}).Drop(context.Background(), "j"); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("err = %v", err)
	}
	// every statement is still tried
	if !strings.Contains(h.all(), "DROP DATABASE IF EXISTS") {
		t.Fatalf("the rest of the clean-up must go on:\n%s", h.all())
	}
}

func TestAFailedKillStaysVisibleWhenTheSecondListingFailsToo(t *testing.T) {
	f := &fakeExec{sessions: []string{"5"}, failOn: "KILL CONNECTION", failError: errors.New("ERROR 1095: not owner"), failList: 2}
	err := New(f, Config{}).Drop(context.Background(), "j")
	if err == nil || !strings.Contains(err.Error(), "KILL CONNECTION 5") || !strings.Contains(err.Error(), "lost connection") {
		t.Fatalf("both failures must be in the error: %v", err)
	}
}
