//go:build mserv && linux

package jobdb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WWD-4d-5 scenario J: jobdb against the MySQL dev instance of mserv, through the wwd_admin account (scripts/wwd-mysql-setup.sh).
//
//	GOOS=linux GOARCH=amd64 go test -c -tags mserv -o wwd-jobdb-mserv.test ./internal/wwd/jobdb/
//	WWD_MSERV_E2E=1 ./wwd-jobdb-mserv.test -test.v
//
// It makes databases and users named like a real job's from the ids j_4d5-J1..J3 and drops them again; nothing else is touched.

func e2e(t *testing.T) (*Manager, Config) {
	t.Helper()
	if os.Getenv("WWD_MSERV_E2E") != "1" {
		t.Skip("set WWD_MSERV_E2E=1 on mserv")
	}
	cfg := Config{Host: "100.64.0.6", Port: 3307, UserHosts: []string{"localhost", "127.0.0.1", "100.64.0.6"}}
	df := filepath.Join(os.Getenv("HOME"), ".config", "wwd", "mysql-admin.cnf")
	return New(CLI{DefaultsFile: df}, cfg), cfg
}

// asJob runs a statement as the job's user over TCP to the address the job is given (the address at-inwin's phpunit config pins).
func asJob(info Info, sql string) (string, error) {
	cmd := exec.Command("mysql", "-N", "-B", "-h", info.Host, "-P", "3307", "-u", info.User, "-e", sql)
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+info.Password)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func leftovers(t *testing.T, m *Manager) (dbs, users string) {
	t.Helper()
	d, err := m.ex.Run(context.Background(), call("wwd_list_dbs"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.ex.Run(context.Background(), call("wwd_list_users"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(d), strings.TrimSpace(u)
}

func TestMServJobDatabaseLifeCycle(t *testing.T) {
	m, _ := e2e(t)
	ctx := context.Background()
	t.Cleanup(func() {
		for _, id := range []string{"j_4d5-J1", "j_4d5-J2", "j_4d5-J3"} {
			_ = m.Drop(ctx, id)
		}
	})
	if d, u := leftovers(t, m); d != "" || u != "" {
		t.Fatalf("the instance is not clean before the test: dbs %q users %q", d, u)
	}

	// 1. a job's database over the tailnet address: its own database works, the others are not there
	info, err := m.Create(ctx, "j_4d5-J1")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := asJob(info, "CREATE TABLE "+info.Database+".t (a INT); INSERT INTO "+info.Database+".t VALUES (7); SELECT a FROM "+info.Database+".t"); err != nil || !strings.Contains(out, "7") {
		t.Fatalf("the job's user in its own database: %v %s", err, out)
	}
	out, _ := asJob(info, "SELECT schema_name FROM information_schema.schemata")
	for _, l := range strings.Fields(out) {
		if l != "information_schema" && l != "performance_schema" && l != info.Database && !strings.HasPrefix(l, "mysql:") {
			t.Errorf("the job's user can see the database %q", l)
		}
	}
	for _, sql := range []string{"SELECT 1 FROM inwin_local.wp_options LIMIT 1", "SELECT user FROM mysql.user", "CREATE DATABASE wwd_evil"} {
		if out, err := asJob(info, sql); err == nil {
			t.Errorf("the job's user could run %q: %s", sql, out)
		}
	}
	t.Logf("J1: %s as %s over %s:3307 works and is private", info.Database, info.User, info.Host)

	// 2. a Create of the same id starts from an empty database
	info2, err := m.Create(ctx, "j_4d5-J1")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := asJob(info2, "SHOW TABLES FROM "+info2.Database); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("a re-run does not start empty: %v %q", err, out)
	}
	if _, err := asJob(info, "SELECT 1"); err == nil {
		t.Fatal("the old password still works after a Create of the same id")
	}

	// 3. Drop ends a connection that is held open, then the user and the database are gone
	held := exec.Command("mysql", "-N", "-B", "-h", info2.Host, "-P", "3307", "-u", info2.User, "-e", "SELECT SLEEP(25)")
	held.Env = append(os.Environ(), "MYSQL_PWD="+info2.Password)
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	start := time.Now()
	if err := m.Drop(ctx, "j_4d5-J1"); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- held.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = held.Process.Kill()
		t.Error("the held connection was not ended by Drop")
	}
	t.Logf("J3: Drop ended the held connection and removed the user and database in %s", time.Since(start).Round(time.Millisecond))
	if d, u := leftovers(t, m); d != "" || u != "" {
		t.Fatalf("Drop left dbs %q users %q", d, u)
	}

	// 4. Sweep removes what no live job owns and keeps what one does
	for _, id := range []string{"j_4d5-J2", "j_4d5-J3"} {
		if _, err := m.Create(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	dropped, err := m.Sweep(ctx, []string{"j_4d5-J3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != m.Name("j_4d5-J2") {
		t.Fatalf("dropped %v, want only J2's database", dropped)
	}
	d, u := leftovers(t, m)
	if !strings.Contains(d, m.Name("j_4d5-J3")) || strings.Contains(d, m.Name("j_4d5-J2")) || !strings.Contains(u, m.User("j_4d5-J3")) || strings.Contains(u, m.User("j_4d5-J2")) {
		t.Fatalf("after the Sweep: dbs %q users %q", d, u)
	}
	if err := m.Drop(ctx, "j_4d5-J3"); err != nil {
		t.Fatal(err)
	}
	if d, u := leftovers(t, m); d != "" || u != "" {
		t.Fatalf("the end left dbs %q users %q", d, u)
	}
}
