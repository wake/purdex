package jobdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeExec records every statement and answers the two listing queries from its own lists.
type fakeExec struct {
	sql       []string
	dbs       []string
	users     []string
	failOn    string
	failError error
}

func (f *fakeExec) Run(_ context.Context, sql string) (string, error) {
	f.sql = append(f.sql, sql)
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		return "", f.failError
	}
	switch {
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
	if !regexp.MustCompile(`^wwd_[0-9a-f]{16}$`).MatchString(a) || len(m.User("j_abc.1")) > 32 {
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
	f := &fakeExec{dbs: []string{live, dead, "wwd_notours", "mysql", "appdb"}, users: []string{m.User("j_live"), m.User("j_dead"), "root"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), []string{"j_live"})
	if err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	if !strings.Contains(sql, "DROP DATABASE IF EXISTS `"+dead+"`") || !strings.Contains(sql, "DROP USER IF EXISTS '"+m.User("j_dead")+"'@'localhost'") {
		t.Fatalf("the dead job's database and user must go:\n%s", sql)
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

func TestCLIExecRunsMysqlWithTheDefaultsFileAndTheStatementAsOneArgument(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE/args\"\necho one\necho two\n"
	if err := os.WriteFile(filepath.Join(dir, "mysql"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE", dir)
	ex := CLI{Binary: filepath.Join(dir, "mysql"), DefaultsFile: "/etc/wwd/admin.cnf"}
	out, err := ex.Run(context.Background(), "SELECT 1; DROP DATABASE x")
	if err != nil || out != "one\ntwo\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args"))
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if args[0] != "--defaults-extra-file=/etc/wwd/admin.cnf" || args[len(args)-1] != "SELECT 1; DROP DATABASE x" {
		t.Fatalf("args = %q", args)
	}
	for _, a := range args {
		if strings.Contains(a, "password") {
			t.Fatalf("no password on the command line: %q", args)
		}
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
