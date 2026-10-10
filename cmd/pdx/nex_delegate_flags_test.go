// cmd/pdx/nex_delegate_flags_test.go
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// delegateDaemon stands in for the daemon: GET /v1/capabilities answers caps, POST /v1/executions records the raw body.
type delegateDaemon struct {
	mu     sync.Mutex
	posts  []map[string]any
	caps   string
	paths  []string
	server *httptest.Server
}

func newDelegateDaemon(t *testing.T, caps string) *delegateDaemon {
	t.Helper()
	d := &delegateDaemon{caps: caps}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.paths = append(d.paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v1/capabilities"):
			_, _ = io.WriteString(w, d.caps)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/executions"):
			raw, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			d.posts = append(d.posts, m)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "exec-1", "state": "queued"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}

func (d *delegateDaemon) run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cfg, _ := nexBaseFromServer(t, d.server, "tok")
	probe, _ := countingProbe()
	var stdout, stderr bytes.Buffer
	code := runNex(append([]string{"delegate", "--cwd", "/x", "--brief", "b"}, args...), &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)
	return code, stderr.String()
}

const capsAll = `{"delegate":{"model_effort":true,"writable_paths":true}}`

// c933b0-122: --model / --effort / --writable reach the POST /v1/executions body (nexen's client builds it; purdex only hands argv over).
func TestRunNex_DelegateModelEffortWritableReachBody(t *testing.T) {
	d := newDelegateDaemon(t, capsAll)
	code, stderr := d.run(t, "--model", "opus", "--effort", "high", "--writable", "src", "--writable", "docs")
	if code != 0 {
		t.Fatalf("code = %d; stderr=%q", code, stderr)
	}
	if len(d.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(d.posts))
	}
	p := d.posts[0]
	if p["model"] != "opus" || p["effort"] != "high" {
		t.Errorf("model/effort = %v/%v, want opus/high", p["model"], p["effort"])
	}
	wp, _ := p["writable_paths"].([]any)
	if len(wp) != 2 || wp[0] != "src" || wp[1] != "docs" {
		t.Errorf("writable_paths = %v, want [src docs]", p["writable_paths"])
	}
}

// No flag, no new key: the body of an old caller does not change, and no capabilities request is made for it.
func TestRunNex_DelegateWithoutFlagsBodyUnchanged(t *testing.T) {
	d := newDelegateDaemon(t, `{}`)
	if code, stderr := d.run(t); code != 0 {
		t.Fatalf("code = %d; stderr=%q", code, stderr)
	}
	for _, k := range []string{"model", "effort", "writable_paths"} {
		if _, ok := d.posts[0][k]; ok {
			t.Errorf("body carries %q without the flag: %v", k, d.posts[0])
		}
	}
	for _, p := range d.paths {
		if strings.Contains(p, "capabilities") {
			t.Errorf("a plain delegate asked for capabilities: %v", d.paths)
		}
	}
}

// A daemon that does not declare the capability: the flag is an error and no execution request is sent.
func TestRunNex_DelegateFlagsRefusedWithoutCapability(t *testing.T) {
	cases := []struct {
		name string
		caps string
		args []string
		want string
	}{
		{"model without model_effort", `{"delegate":{}}`, []string{"--model", "opus"}, "model_effort"},
		{"effort without model_effort", `{"delegate":{"model_effort":"true"}}`, []string{"--effort", "high"}, "model_effort"},
		{"writable without writable_paths", `{"delegate":{"model_effort":true}}`, []string{"--writable", "src"}, "writable_paths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDelegateDaemon(t, tc.caps)
			code, stderr := d.run(t, tc.args...)
			if code == 0 {
				t.Fatalf("code = 0, want an error")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.want)
			}
			if len(d.posts) != 0 {
				t.Errorf("an execution was posted: %v", d.posts)
			}
		})
	}
}
