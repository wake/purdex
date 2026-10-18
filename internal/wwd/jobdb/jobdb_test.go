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

// fakeExec records every statement and answers the routines of wwdctl.sql from its own lists.
type fakeExec struct {
	mu        sync.Mutex
	sql       []string
	dead      []bool             // for each statement: was its context already cancelled
	cancel    context.CancelFunc // called when failOn matches, as a caller cancelling half way would
	hold      chan struct{}      // when set, wwd_list_dbs waits for it to be closed (a Sweep that is half way)
	held      chan struct{}      // closed once a Run is waiting on hold
	remaining string             // what wwd_kill_user answers; "0" when empty
	dbs       []string
	users     []string // "user<TAB>host"
	failOn    string
	failError error
}

func (f *fakeExec) Run(ctx context.Context, sql string) (string, error) {
	if f.hold != nil && strings.Contains(sql, "wwd_list_dbs") {
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
		return "", f.failError
	}
	switch {
	case strings.Contains(sql, "wwd_kill_user"):
		if f.remaining != "" {
			return f.remaining + "\n", nil
		}
		return "0\n", nil
	case strings.Contains(sql, "wwd_list_dbs"):
		return strings.Join(f.dbs, "\n"), nil
	case strings.Contains(sql, "wwd_list_users"):
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
	if !dbNameRe.MatchString(a) || !userNameRe.MatchString(m.User("j_abc.1")) || len(m.User("j_abc.1")) != 31 {
		t.Fatalf("db %q user %q", a, m.User("j_abc.1"))
	}
}

func TestEveryStatementIsACallOfAWwdctlRoutine(t *testing.T) {
	// the administrator account holds EXECUTE on wwdctl and nothing else: a raw CREATE, GRANT, DROP, KILL or SELECT would be refused
	f := &fakeExec{dbs: []string{New(f0(), Config{}).Name("j_old")}, users: []string{New(f0(), Config{}).User("j_old") + "\tlocalhost"}, remaining: "0"}
	m := New(f, Config{})
	ctx := context.Background()
	if _, err := m.Create(ctx, "j_1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Drop(ctx, "j_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Sweep(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.sql {
		if !strings.HasPrefix(s, "CALL wwdctl.wwd_") {
			t.Errorf("not a call of a wwdctl routine: %q", s)
		}
	}
}

func f0() *fakeExec { return &fakeExec{} }

func TestCreateMakesTheDatabaseThenAUserPerHostForThatDatabaseAlone(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{Host: "127.0.0.1", Port: 3306})
	info, err := m.Create(context.Background(), "j_test1")
	if err != nil {
		t.Fatal(err)
	}
	db, user := m.Name("j_test1"), m.User("j_test1")
	created := f.sql[strings.Count(f.all(), "\n")+1-4:] // the last four: create_db and a create_user per host (two hosts)
	want := []string{
		"CALL wwdctl.wwd_create_db('" + db + "')",
		"CALL wwdctl.wwd_create_user('" + user + "','" + info.Password + "','localhost','" + db + "')",
		"CALL wwdctl.wwd_create_user('" + user + "','" + info.Password + "','127.0.0.1','" + db + "')",
	}
	got := f.sql[len(f.sql)-3:]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statement %d = %q, want %q (all: %q)", i, got[i], want[i], created)
		}
	}
	if len(info.Password) != 48 || info.Database != db || info.User != user || info.Host != "127.0.0.1" || info.Port != 3306 {
		t.Fatalf("info = %+v", info)
	}
}

func TestCreateClearsWhatAnEarlierRunOfTheSameJobLeft(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{})
	if _, err := m.Create(context.Background(), "j_test1"); err != nil {
		t.Fatal(err)
	}
	drop, create := strings.Index(f.all(), "wwd_drop_db"), strings.Index(f.all(), "wwd_create_db")
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

func TestCreateFailureDropsWhatWasMadeAndTheErrorHasNoPassword(t *testing.T) {
	f := &fakeExec{failOn: "wwd_create_user", failError: errors.New("boom")}
	m := New(f, Config{})
	_, err := m.Create(context.Background(), "j_test1")
	if err == nil {
		t.Fatal("expected an error")
	}
	last := f.sql[len(f.sql)-1]
	if !strings.Contains(last, "wwd_drop_db") {
		t.Fatalf("a half-made database must not be left:\n%s", f.all())
	}
	for _, s := range f.sql {
		if strings.Contains(s, "wwd_create_user") {
			pw := strings.Split(s, "'")[3]
			if strings.Contains(err.Error(), pw) {
				t.Fatalf("the error holds the password: %v", err)
			}
		}
	}
}

func TestCreateCleanUpSurvivesACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeExec{failOn: "wwd_create_user", failError: errors.New("cancelled"), cancel: cancel}
	m := New(f, Config{})
	if _, err := m.Create(ctx, "j_test1"); err == nil {
		t.Fatal("expected an error")
	}
	n := 0
	for i := len(f.sql) - 1; i >= 0 && strings.Contains(f.sql[i], "wwd_drop") || i >= 0 && strings.Contains(f.sql[i], "wwd_kill"); i-- {
		n++
		if f.dead[i] {
			t.Fatalf("the clean-up ran on a cancelled context: %q", f.sql[i])
		}
	}
	if n == 0 {
		t.Fatalf("clean-up missing:\n%s", f.all())
	}
}

func TestDropEndsTheConnectionsThenDropsTheUsersThenTheDatabase(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{})
	if err := m.Drop(context.Background(), "j_test1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CALL wwdctl.wwd_kill_user('" + m.User("j_test1") + "')",
		"CALL wwdctl.wwd_drop_user('" + m.User("j_test1") + "','localhost')",
		"CALL wwdctl.wwd_drop_user('" + m.User("j_test1") + "','127.0.0.1')",
		"CALL wwdctl.wwd_drop_db('" + m.Name("j_test1") + "')",
	}
	if strings.Join(f.sql, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements:\n%s\nwant:\n%s", f.all(), strings.Join(want, "\n"))
	}
}

func TestDropTriesEveryStatementAndReportsTheFirstError(t *testing.T) {
	f := &fakeExec{failOn: "wwd_drop_user", failError: errors.New("locked")}
	m := New(f, Config{})
	if err := m.Drop(context.Background(), "j_test1"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.all(), "wwd_drop_db") {
		t.Fatalf("the database must go even when a user could not:\n%s", f.all())
	}
}

func TestAKillThatFailsOrLeavesConnectionsIsReported(t *testing.T) {
	f := &fakeExec{failOn: "wwd_kill_user", failError: errors.New("ERROR 1370 execute denied")}
	if err := New(f, Config{}).Drop(context.Background(), "j"); err == nil || !strings.Contains(err.Error(), "end the connections") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.all(), "wwd_drop_db") {
		t.Fatalf("the rest of the clean-up must go on:\n%s", f.all())
	}
	g := &fakeExec{remaining: "2"}
	if err := New(g, Config{}).Drop(context.Background(), "j"); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("err = %v", err)
	}
}

func TestSweepDropsOnlyOurLeftoversThatNoLiveJobOwns(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	live, dead := m.Name("j_live"), m.Name("j_dead")
	f := &fakeExec{dbs: []string{live, dead}, users: []string{m.User("j_live") + "\tlocalhost", m.User("j_dead") + "\tlocalhost", m.User("j_dead") + "\t10.0.0.5"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), []string{"j_live"})
	if err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	for _, want := range []string{"wwd_drop_db('" + dead + "')", "wwd_drop_user('" + m.User("j_dead") + "','localhost')",
		"wwd_drop_user('" + m.User("j_dead") + "','10.0.0.5')", "wwd_kill_user('" + m.User("j_dead") + "')"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q:\n%s", want, sql)
		}
	}
	for _, keep := range []string{live, m.User("j_live")} {
		if strings.Contains(sql, "wwd_drop_db('"+keep) || strings.Contains(sql, "wwd_drop_user('"+keep) || strings.Contains(sql, "wwd_kill_user('"+keep) {
			t.Fatalf("%s must stay:\n%s", keep, sql)
		}
	}
	if k, d := strings.Index(sql, "wwd_kill_user('"+m.User("j_dead")), strings.Index(sql, "wwd_drop_db('"+dead); k > d {
		t.Fatalf("in a Sweep, the users' connections end before any database goes:\n%s", sql)
	}
	if len(dropped) != 1 || dropped[0] != dead {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestSweepNeverPutsAHostWithQuotesIntoAStatement(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	f := &fakeExec{users: []string{m.User("j_dead") + "\tx'; DROP DATABASE mysql; --", m.User("j_dead") + "\t%"}}
	m = New(f, Config{})
	if _, err := m.Sweep(context.Background(), nil); err == nil {
		t.Fatal("a user whose host cannot be handled must be reported")
	}
	if strings.Contains(f.all(), "DROP DATABASE mysql") || strings.Contains(f.all(), "'%'") {
		t.Fatalf("a host read back from the server was used as it is:\n%s", f.all())
	}
}

func TestSweepIgnoresNamesThatAreNotOfOurShape(t *testing.T) {
	f := &fakeExec{dbs: []string{"mysql", "wwd_notours", "inwin_local", "wwd_" + strings.Repeat("A", 32)}, users: []string{"root\tlocalhost", "wwdu_x\tlocalhost"}}
	if _, err := New(f, Config{}).Sweep(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.all(), "wwd_drop") || strings.Contains(f.all(), "wwd_kill") {
		t.Fatalf("a name of another shape was touched:\n%s", f.all())
	}
}

func TestUserHostsWithQuotesOrWildcardsAreRefused(t *testing.T) {
	for _, h := range []string{"localhost' IDENTIFIED BY 'x'; DROP DATABASE mysql; --", "%", "a b", "a\nb", "h`", "x\\y"} {
		f := &fakeExec{}
		m := New(f, Config{UserHosts: []string{"localhost", h}})
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

func TestASweepInProgressHoldsBackACreate(t *testing.T) {
	f := &fakeExec{hold: make(chan struct{}), held: make(chan struct{})}
	m := New(f, Config{})
	sweepDone := make(chan struct{})
	go func() { _, _ = m.Sweep(context.Background(), nil); close(sweepDone) }()
	<-f.held
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

func TestEnvNamesTheDatabaseAndKeepsThePasswordOutOfTheName(t *testing.T) {
	info := Info{Host: "127.0.0.1", Port: 3306, Database: "wwd_x", User: "wwd_u", Password: "secret"}
	got := strings.Join(info.Env(), "\n")
	for _, want := range []string{"DB_CONNECTION=mysql", "DB_HOST=127.0.0.1", "DB_PORT=3306", "DB_DATABASE=wwd_x", "DB_USERNAME=wwd_u", "DB_PASSWORD=secret"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestWPTestsEnvUsesTheNamesAtInwinPluginReads(t *testing.T) {
	info := Info{Host: "100.64.0.6", Port: 3307, Database: "wwd_x", User: "wwdu_u", Password: "secret"}
	got := strings.Join(info.WPTestsEnv(), "\n")
	for _, want := range []string{"WP_TESTS_DB_NAME=wwd_x", "WP_TESTS_DB_HOST=100.64.0.6:3307", "WP_TESTS_DB_USER=wwdu_u", "WP_TESTS_DB_PASSWORD=secret", "WP_PHPUNIT__TABLE_PREFIX=wptests_"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if v := strings.Join(Info{Host: "::1", Port: 3307}.WPTestsEnv(), "\n"); !strings.Contains(v, "WP_TESTS_DB_HOST=[::1]:3307") {
		t.Fatalf("env = %q", v)
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
	const sql = "CALL wwdctl.wwd_create_user('u','sekrit0123','localhost','d')"
	out, err := ex.Run(context.Background(), sql)
	if err != nil || out != "one\ntwo\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args"))
	if args := strings.Split(strings.TrimSpace(string(b)), "\n"); args[0] != "--defaults-extra-file=/etc/wwd/admin.cnf" {
		t.Fatalf("args = %q", args)
	}
	if strings.Contains(string(b), "sekrit0123") || strings.Contains(string(b), "CALL") {
		t.Fatalf("the statement, and so the password, must not be an argument: %q", b)
	}
	if in, _ := os.ReadFile(filepath.Join(dir, "stdin")); !strings.Contains(string(in), sql) {
		t.Fatalf("stdin = %q", in)
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

// ---- the SQL file and the Go code must agree ------------------------------------------------------------------------------

func routines(sql string) map[string]string {
	re := regexp.MustCompile("(?s)PROCEDURE wwdctl\\.(wwd_[a-z_]+)\\((.*?)\\)\\s+SQL SECURITY DEFINER\\s+BEGIN(.*?)\nEND\\$\\$")
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(sql, -1) {
		out[m[1]] = m[2] + "\n" + m[3]
	}
	return out
}

func TestWwdctlSQLDefinesEveryRoutineTheGoCodeCallsAndNoOther(t *testing.T) {
	defined := routines(ControlSQL)
	used := map[string]bool{}
	src, err := os.ReadFile("jobdb.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`call\("(wwd_[a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		used[m[1]] = true
	}
	for r := range used {
		if _, ok := defined[r]; !ok {
			t.Errorf("jobdb.go calls %s, wwdctl.sql does not define it", r)
		}
	}
	for r := range defined {
		if !used[r] {
			t.Errorf("wwdctl.sql defines %s, nothing calls it: every routine is a right of the administrator account", r)
		}
	}
	if len(defined) != 7 {
		t.Errorf("routines = %d, want 7", len(defined))
	}
}

func TestWwdctlSQLChecksEveryArgumentAnchoredCaseSensitiveAndByLength(t *testing.T) {
	for name, body := range routines(ControlSQL) {
		if strings.HasPrefix(name, "wwd_list_") {
			continue // no arguments
		}
		// every IN parameter is checked against a pattern before it reaches a statement
		for _, p := range regexp.MustCompile(`IN (p_[a-z]+)`).FindAllStringSubmatch(body, -1) {
			chk := regexp.MustCompile(`REGEXP_LIKE\(` + p[1] + `, '\\\\A[^']*\\\\z', 'c'\)`)
			if !chk.MatchString(body) {
				t.Errorf("%s: %s is not checked with an anchored, case-sensitive REGEXP_LIKE(..., 'c')", name, p[1])
			}
			if !strings.Contains(body, "CHAR_LENGTH("+p[1]+")") {
				t.Errorf("%s: %s has no length check", name, p[1])
			}
		}
		if !strings.Contains(body, "SIGNAL SQLSTATE '45000'") {
			t.Errorf("%s: no SIGNAL on a bad argument", name)
		}
	}
	code := stripComments(ControlSQL)
	if strings.Count(code, "SQL SECURITY DEFINER") != 7 || strings.Count(code, "DEFINER=`root`@`localhost`") != 7 {
		t.Error("every routine runs as root, on purpose: the account that calls it has only EXECUTE")
	}
	if strings.Contains(strings.ToUpper(code), "GRANT OPTION") || strings.Contains(code, "*.*") {
		t.Error("nothing here may grant a global privilege or the right to grant")
	}
}

func TestWwdctlSQLPatternsAreTheShapesOfTheGoNames(t *testing.T) {
	for _, c := range []struct{ sqlPattern, goPattern string }{
		{`\\Awwd_[0-9a-f]{32}\\z`, dbNameRe.String()},
		{`\\Awwdu_[0-9a-f]{26}\\z`, userNameRe.String()},
		{`\\A[A-Za-z0-9_.:-]{1,64}\\z`, ""},
		{`\\A[0-9a-f]{48}\\z`, ""},
	} {
		if !strings.Contains(ControlSQL, c.sqlPattern) {
			t.Errorf("wwdctl.sql lacks the pattern %s", c.sqlPattern)
		}
		if c.goPattern != "" {
			// the Go regexp is ^...$ and the SQL one \A...\z: the same body
			a := strings.TrimSuffix(strings.TrimPrefix(c.goPattern, "^"), "$")
			b := strings.TrimSuffix(strings.TrimPrefix(c.sqlPattern, `\\A`), `\\z`)
			if a != b {
				t.Errorf("Go %s and SQL %s differ", c.goPattern, c.sqlPattern)
			}
		}
	}
	// the user is the first 26 hex of its database's 32: the SQL checks that binding with the same offsets
	if !strings.Contains(ControlSQL, "SUBSTRING(p_db, 5, 26) <> SUBSTRING(p_user, 6, 26)") {
		t.Error("the user-belongs-to-its-database check is missing")
	}
	m := New(&fakeExec{}, Config{})
	if m.Name("j_x")[4:30] != m.User("j_x")[5:31] {
		t.Error("Go's names do not have the binding the SQL checks")
	}
}

// stripComments drops the "--" comment lines: the checks are about the statements, not about what the comments say.
func stripComments(sql string) string {
	var out []string
	for _, l := range strings.Split(sql, "\n") {
		if t := strings.TrimSpace(l); !strings.HasPrefix(t, "--") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestCreateStopsWhenWhatAnEarlierRunLeftCannotBeCleared(t *testing.T) {
	// wwd_create_db leaves an existing database as it is: going on after a failed clean-up would give the new user the old data
	f := &fakeExec{failOn: "wwd_drop_db", failError: errors.New("lost connection")}
	if _, err := New(f, Config{}).Create(context.Background(), "j_test1"); err == nil || !strings.Contains(err.Error(), "clear what an earlier run") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(f.all(), "wwd_create_db") || strings.Contains(f.all(), "wwd_create_user") {
		t.Fatalf("nothing may be made on a database that may still be there:\n%s", f.all())
	}
}

func TestAHostLongerThanTheRoutinesAcceptIsRefusedAtTheConfiguration(t *testing.T) {
	long := strings.Repeat("a", 65)
	f := &fakeExec{}
	m := New(f, Config{UserHosts: []string{"localhost", long}})
	if _, err := m.Create(context.Background(), "j"); err == nil {
		t.Fatal("a 65 character host must be refused: the routines take 64")
	}
	if len(f.sql) != 0 {
		t.Fatalf("nothing may be sent: %v", f.sql)
	}
	if _, err := New(&fakeExec{}, Config{UserHosts: []string{strings.Repeat("a", 64)}}).Create(context.Background(), "j"); err != nil {
		t.Fatalf("64 is fine: %v", err)
	}
}

func TestSweepKeepsTheDatabaseOfAUserItCannotDropAndSaysSo(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	long := strings.Repeat("h", 100) // a host an older version accepted (up to 255) and the routines now refuse
	f := &fakeExec{dbs: []string{m.Name("j_stuck"), m.Name("j_dead")}, users: []string{m.User("j_stuck") + "\t" + long, m.User("j_dead") + "\tlocalhost"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "drop it by hand") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(f.all(), "wwd_drop_db('"+m.Name("j_stuck")+"')") {
		t.Fatalf("the database of a user that stays was dropped:\n%s", f.all())
	}
	if len(dropped) != 1 || dropped[0] != m.Name("j_dead") {
		t.Fatalf("dropped = %v: the other job's database must still go", dropped)
	}
}

func TestSweepKeepsTheDatabaseOfAUserWhoseConnectionsWillNotEnd(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	f := &fakeExec{remaining: "3", dbs: []string{m.Name("j_dead")}, users: []string{m.User("j_dead") + "\tlocalhost"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), nil)
	if err == nil || len(dropped) != 0 {
		t.Fatalf("dropped %v err %v", dropped, err)
	}
}

// Every call the package makes has its arguments in the order of the routine's parameters in wwdctl.sql, and each argument is of the
// shape that parameter is checked for there: the SQL signature is the contract, the Go call has to follow it.
func TestEveryCallPassesItsArgumentsInTheOrderOfTheSQLSignature(t *testing.T) {
	sigRe := regexp.MustCompile("(?m)PROCEDURE wwdctl\\.(wwd_[a-z_]+)\\((.*)\\)$")
	params := map[string][]string{}
	for _, m := range sigRe.FindAllStringSubmatch(ControlSQL, -1) {
		var names []string
		for _, p := range regexp.MustCompile(`IN (p_[a-z]+)`).FindAllStringSubmatch(m[2], -1) {
			names = append(names, p[1])
		}
		params[m[1]] = names
	}
	shape := map[string]*regexp.Regexp{
		"p_db": dbNameRe, "p_user": userNameRe, "p_pass": regexp.MustCompile(`^[0-9a-f]{48}$`), "p_host": cfgHostRe,
	}
	f := &fakeExec{dbs: []string{New(f0(), Config{}).Name("j_old")}, users: []string{New(f0(), Config{}).User("j_old") + "\tlocalhost"}}
	m := New(f, Config{})
	ctx := context.Background()
	if _, err := m.Create(ctx, "j_1"); err != nil {
		t.Fatal(err)
	}
	_ = m.Drop(ctx, "j_1")
	_, _ = m.Sweep(ctx, nil)
	callRe := regexp.MustCompile(`^CALL wwdctl\.(wwd_[a-z_]+)\((.*)\)$`)
	seen := map[string]bool{}
	for _, s := range f.sql {
		c := callRe.FindStringSubmatch(s)
		if c == nil {
			t.Errorf("not a call: %q", s)
			continue
		}
		var args []string
		if c[2] != "" {
			args = strings.Split(strings.Trim(c[2], "'"), "','")
		}
		want, ok := params[c[1]]
		if !ok || len(args) != len(want) {
			t.Errorf("%s: %d arguments for the parameters %v", c[1], len(args), want)
			continue
		}
		for i, a := range args {
			if !shape[want[i]].MatchString(a) {
				t.Errorf("%s: argument %d (%q) is not a %s", c[1], i+1, a, want[i])
			}
		}
		seen[c[1]] = true
	}
	for r := range params {
		if !seen[r] {
			t.Errorf("the routine %s was never called by Create, Drop or Sweep", r)
		}
	}
}
