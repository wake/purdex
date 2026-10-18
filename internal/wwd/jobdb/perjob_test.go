package jobdb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A caller with a deadline is not held past it by an operation that holds the lock (#3372).
func TestOperationsGiveUpWaitingForTheLockWhenTheContextEnds(t *testing.T) {
	ex := &fakeExec{}
	m := New(ex, Config{})
	m.sem <- struct{}{} // another operation is stuck inside
	for name, run := range map[string]func(ctx context.Context) error{
		"Create": func(ctx context.Context) error { _, err := m.Create(ctx, "j_a"); return err },
		"Drop":   func(ctx context.Context) error { return m.Drop(ctx, "j_a") },
		"Sweep":  func(ctx context.Context) error { _, err := m.Sweep(ctx, nil); return err },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		err := run(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: err = %v, want a deadline error", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s waited %v", name, d)
		}
	}
	if ex.all() != "" {
		t.Errorf("nothing may be sent while the lock is not held:\n%s", ex.all())
	}
	<-m.sem
	if err := m.Drop(context.Background(), "j_a"); err != nil {
		t.Errorf("after the lock is free: %v", err)
	}
}

func TestPerJobCreateReturnsBothEnvNamings(t *testing.T) {
	ex := &fakeExec{}
	p := NewPerJob(New(ex, Config{Host: "100.64.0.6", Port: 3307}))
	env, err := p.Create(context.Background(), "j_a")
	if err != nil {
		t.Fatal(err)
	}
	m := New(ex, Config{})
	for k, want := range map[string]string{
		"DB_DATABASE": m.Name("j_a"), "DB_USERNAME": m.User("j_a"), "DB_HOST": "100.64.0.6", "DB_PORT": "3307",
		"WP_TESTS_DB_NAME": m.Name("j_a"), "WP_TESTS_DB_USER": m.User("j_a"), "WP_TESTS_DB_HOST": "100.64.0.6:3307",
		"WP_PHPUNIT__TABLE_PREFIX": "wptests_",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if env["DB_PASSWORD"] == "" || env["DB_PASSWORD"] != env["WP_TESTS_DB_PASSWORD"] {
		t.Errorf("the two passwords must be the same non-empty value: %q / %q", env["DB_PASSWORD"], env["WP_TESTS_DB_PASSWORD"])
	}
}

func TestPerJobDropAndSweepReachTheManager(t *testing.T) {
	ex := &fakeExec{}
	p := NewPerJob(New(ex, Config{}))
	if err := p.Drop(context.Background(), "j_a"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ex.all(), "wwd_drop") {
		t.Errorf("Drop sent no drop statement:\n%s", ex.all())
	}
	if err := p.Sweep(context.Background(), []string{"j_a"}); err != nil {
		t.Fatal(err)
	}
}
