package jobdb

import (
	"context"
	"strings"
	"testing"
)

// A job is named by "source/id": one id from two origins is two databases and two users.
func TestTwoKeysWithOneIDHaveTwoNames(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	a, b := "air26/j_same", "mserv/j_same"
	if m.Name(a) == m.Name(b) || m.User(a) == m.User(b) {
		t.Fatalf("one id from two connections must be two names: %q %q", m.Name(a), m.Name(b))
	}
	if !dbNameRe.MatchString(m.Name(a)) || !userNameRe.MatchString(m.User(a)) {
		t.Fatalf("the shape: %q %q", m.Name(a), m.User(a))
	}
	// the key gives other names than the bare id did
	if m.Name(a) == legacyName(a) || m.User(a) == legacyUser(a) {
		t.Fatalf("a key and its bare id must not name the same database")
	}
}

func TestIDOfSplitsAtTheLastSlash(t *testing.T) {
	for key, want := range map[string]string{"air26/j_x": "j_x", "a/b/j_x": "j_x", "j_x": "j_x", "": "", "air26/": ""} {
		if got := idOf(key); got != want {
			t.Errorf("idOf(%q) = %q, want %q", key, got, want)
		}
	}
}

// Dropping one job's database leaves the other's alone, and Create clears only what an earlier run of the same key left.
func TestDropAndCreateTouchOnlyTheirOwnKey(t *testing.T) {
	f := &fakeExec{}
	m := New(f, Config{})
	a, b := "air26/j_same", "mserv/j_same"
	if err := m.Drop(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	sql := f.all()
	if !strings.Contains(sql, "wwd_drop_db('"+m.Name(a)+"')") || strings.Contains(sql, m.Name(b)) || strings.Contains(sql, m.User(b)) {
		t.Fatalf("Drop(%s) must touch only its own names:\n%s", a, sql)
	}
	f.sql = nil
	if _, err := m.Create(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.all(), m.Name(a)) || strings.Contains(f.all(), m.User(a)) {
		t.Fatalf("Create(%s) must not drop or make the names of %s:\n%s", b, a, f.all())
	}
}

// Sweep: with one of two jobs of the same id live, only the other's database goes.
func TestSweepWithOneOfTwoSameIDJobsLiveDropsTheOther(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	a, b := "air26/j_same", "mserv/j_same"
	f := &fakeExec{dbs: []string{m.Name(a), m.Name(b)}, users: []string{m.User(a) + "\tlocalhost", m.User(b) + "\tlocalhost"}}
	m = New(f, Config{})
	dropped, err := m.Sweep(context.Background(), []string{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != m.Name(b) {
		t.Fatalf("dropped %v, want only %s", dropped, m.Name(b))
	}
	if strings.Contains(f.all(), m.Name(a)) || strings.Contains(f.all(), m.User(a)) {
		t.Fatalf("the live job's names must not be touched:\n%s", f.all())
	}
}

// A database an earlier wwd named from the id alone belongs to a job that is still held (a quarantined one): its key is live, so the old
// name stays; with its key not live it goes like any leftover.
func TestSweepKeepsTheOldNameOfALiveKey(t *testing.T) {
	m := New(&fakeExec{}, Config{})
	key := "air26/j_old"
	oldDB, oldUser := legacyName(key), legacyUser(key)
	for name, tc := range map[string]struct {
		live     []string
		wantDrop bool
	}{"key live": {[]string{key}, false}, "key not live": {nil, true}, "another origin's same id is live": {[]string{"mserv/j_old"}, false}} {
		f := &fakeExec{dbs: []string{oldDB}, users: []string{oldUser + "\tlocalhost"}}
		m = New(f, Config{})
		dropped, err := m.Sweep(context.Background(), tc.live)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(dropped) == 1 && dropped[0] == oldDB; got != tc.wantDrop {
			t.Errorf("%s: dropped %v, want dropped=%v", name, dropped, tc.wantDrop)
		}
		// the old user goes with the old database, or stays with it
		if got := strings.Contains(f.all(), "wwd_drop_user('"+oldUser+"'"); got != tc.wantDrop {
			t.Errorf("%s: the old user dropped = %v, want %v:\n%s", name, got, tc.wantDrop, f.all())
		}
	}
}
