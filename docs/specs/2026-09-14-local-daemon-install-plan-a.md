# Local Daemon Install — Plan A (Go daemon) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Mini daemon everything the app needs to install a daemon on
another machine: a cross-compiled `pdx` download endpoint with integrity
headers, a `pdx version --json` identity command, version/hash on
`/api/health`, dev mode on by default, and a pid file that can no longer go
invisible.

**Architecture:** Build identity moves into a leaf package `internal/buildinfo`
so `core` and `dev` both read it without a cycle. The `go build` plumbing
already inside `handleDaemonRebuild` is extracted into `buildBinary`, which
the new `handleDaemonDownload` reuses with `GOOS`/`GOARCH`/`CGO_ENABLED=0`.
The dev-mode env gate becomes `internal/devmode.Enabled()` (`!= "0"`).

**Tech Stack:** Go 1.25 (net/http `ServeMux` method patterns,
`http.NewResponseController`, `crypto/sha256`), `go test ./...`

**Spec:** `docs/specs/2026-09-14-local-daemon-install-spec.md` (v3) — §2 is
this plan's contract; §1 decisions are fixed.

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again,
  commit. One task = one commit.
- **Commit messages in English**; every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01JYDubMeVHmpGkgjyRo5bFN
  ```
- **Verification:** `go test ./...` must be green after every task; `go vet ./...` clean.
- **Worktree:** all commands run from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/local-daemon-install`
  (prefix every Bash call with `cd <that path> &&`; absolute paths in
  Edit/Write must include `.claude/worktrees/local-daemon-install/`).
- **Do not change `/api/dev/daemon/rebuild` behaviour.** Its existing tests
  in `internal/module/dev/daemon_test.go` must pass with no edits beyond the
  `BakedInHash → buildinfo.Hash` rename.
- **Dev-mode semantics (spec D6):** `PDX_DEV_MODE` unset or any value other
  than `"0"` ⇒ enabled. Existing tests that assert "unset ⇒ disabled" are
  updated to assert `"0"` ⇒ disabled; that is the intended change.
- **`go build` in tests** runs against a throwaway module in `t.TempDir()`
  (see `TestHandleDaemonRebuild_BuildsInTempRepo`) — never against the real
  repo, never with the real `git`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/buildinfo/buildinfo.go` *(new)* | `Hash`, `Version` link-time vars |
| `internal/devmode/devmode.go` *(new)* + `_test.go` | `Enabled()` gate |
| `Makefile` *(modify)* | inject both vars |
| `cmd/pdx/version.go` *(new)* + `version_test.go` | `pdx version [--json]` |
| `cmd/pdx/main.go` *(modify)* | route `version`; usage line; `serve` exits when pid lock held |
| `cmd/pdx/daemon.go` *(modify)* | `releasePidLock` never unlinks; SIGKILL branch never unlinks |
| `cmd/pdx/daemon_test.go` *(modify)* | pid-file-permanent tests |
| `internal/core/info_handler.go` *(modify)* + `info_handler_test.go` *(new)* | health/info read `buildinfo` |
| `internal/module/dev/build.go` *(new)* | `buildTarget`, `buildBinary` |
| `internal/module/dev/daemon.go` *(modify)* | rebuild uses `buildBinary`; `buildinfo.Hash` |
| `internal/module/dev/download.go` *(new)* + `download_test.go` | `handleDaemonDownload` |
| `internal/module/dev/module.go` *(modify)* | route + `devmode.Enabled()` + `gitHeadFn` |
| `internal/module/dev/module_test.go`, `daemon_test.go` *(modify)* | gate tests flipped to `"0"` |
| `internal/module/agent/probe_orchestrator.go` *(modify)* | `isDevMode` → `devmode.Enabled()` |

---

### Task 1: `internal/buildinfo` + Makefile + rename `BakedInHash`

**Files:**
- Create: `internal/buildinfo/buildinfo.go`
- Modify: `Makefile`
- Modify: `internal/module/dev/daemon.go` (all `BakedInHash` reads), `internal/module/dev/daemon_test.go` (same)

**Interfaces:**
- Produces: `buildinfo.Hash string`, `buildinfo.Version string` (both default `"unknown"`).

- [ ] **Step 1: Create the package**

```go
// internal/buildinfo/buildinfo.go
// Package buildinfo holds the identity baked into a pdx binary at link time.
// It is a leaf package (stdlib only) so both core and the dev module can read
// it without an import cycle.
//
// Set via:
//
//	-ldflags "-X github.com/wake/purdex/internal/buildinfo.Hash=<short sha> \
//	          -X github.com/wake/purdex/internal/buildinfo.Version=<VERSION>"
package buildinfo

var (
	// Hash is the short git commit hash the binary was built from.
	Hash = "unknown"
	// Version is the contents of the VERSION file at build time.
	Version = "unknown"
)
```

- [ ] **Step 2: Makefile injects both**

Replace the `HASH`/`LDFLAGS` lines in `Makefile` with:

```make
HASH := $(shell git log -1 --format=%h 2>/dev/null)
VERSION := $(shell cat VERSION 2>/dev/null)
LDFLAGS := -X github.com/wake/purdex/internal/buildinfo.Hash=$(HASH) -X github.com/wake/purdex/internal/buildinfo.Version=$(VERSION)
```

- [ ] **Step 3: Rename in the dev module**

In `internal/module/dev/daemon.go`: delete the `BakedInHash` var and its
comment; add `"github.com/wake/purdex/internal/buildinfo"` to the imports;
replace every `BakedInHash` with `buildinfo.Hash` (two in
`handleDaemonCheck`). In `handleDaemonRebuild` the ldflags string becomes:

```go
ldflags := "-X github.com/wake/purdex/internal/buildinfo.Hash=" + hash +
	" -X github.com/wake/purdex/internal/buildinfo.Version=" + m.readVersionFile()
```

and add to `daemon.go`:

```go
// readVersionFile returns the trimmed VERSION file, or "unknown".
func (m *DevModule) readVersionFile() string {
	data, err := os.ReadFile(m.versionFile)
	if err != nil {
		return "unknown"
	}
	if v := strings.TrimSpace(string(data)); v != "" {
		return v
	}
	return "unknown"
}
```

(`m.versionFile` already exists on `DevModule`; the test-constructed
`&DevModule{repoRoot: dir}` leaves it empty, which `os.ReadFile("")` turns
into an error → `"unknown"`. Fine.)

In `internal/module/dev/daemon_test.go` replace `BakedInHash` with
`buildinfo.Hash` (import the package). Run `rg -n BakedInHash` — must be
empty.

- [ ] **Step 4: Verify**

Run: `cd <worktree> && go build ./... && go test ./internal/module/dev/ ./cmd/pdx/`
Expected: PASS. Then `make build && ./bin/pdx status` still works (binary links).

- [ ] **Step 5: Commit**

```bash
git add internal/buildinfo Makefile internal/module/dev/daemon.go internal/module/dev/daemon_test.go
git commit -m "refactor(build): move baked-in hash to internal/buildinfo and inject version"
```

---

### Task 2: `internal/devmode.Enabled()` — dev mode on by default

**Files:**
- Create: `internal/devmode/devmode.go`, `internal/devmode/devmode_test.go`
- Modify: `internal/module/dev/module.go:166,177`, `internal/module/agent/probe_orchestrator.go:59`
- Modify tests: `internal/module/dev/module_test.go:177-190`, `internal/module/dev/daemon_test.go` (the `t.Setenv("PDX_DEV_MODE","1")` lines may stay), `internal/module/agent/handler_devlog_test.go:118-127` (comment only)

**Interfaces:**
- Produces: `devmode.Enabled() bool`.

- [ ] **Step 1: Failing test**

```go
// internal/devmode/devmode_test.go
package devmode

import "testing"

func TestEnabled(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"unset", false, "", true},
		{"one", true, "1", true},
		{"zero", true, "0", false},
		{"false-word", true, "false", true}, // only "0" disables
		{"empty-string", true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("PDX_DEV_MODE", c.value)
			} else {
				t.Setenv("PDX_DEV_MODE", "")
				// t.Setenv cannot unset; emulate unset via os.Unsetenv after Setenv registered the restore.
				unsetForTest(t)
			}
			if got := Enabled(); got != c.want {
				t.Fatalf("Enabled() with %q(set=%v) = %v, want %v", c.value, c.set, got, c.want)
			}
		})
	}
}
```

and in the same test file:

```go
import "os"

func unsetForTest(t *testing.T) {
	t.Helper()
	if err := os.Unsetenv("PDX_DEV_MODE"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/devmode/` → FAIL (package does not exist / `Enabled` undefined).

- [ ] **Step 3: Implement**

```go
// internal/devmode/devmode.go
// Package devmode answers one question — are developer features on? — from
// the PDX_DEV_MODE environment variable. Purdex is single-user, so the
// default is ON; only an explicit PDX_DEV_MODE=0 turns them off (spec D6).
// The env is read on every call so tests can flip it with t.Setenv.
package devmode

import "os"

// Enabled reports whether dev features (dev-update endpoints, verbose probe
// logging) are on. Unset, "1", or anything except "0" ⇒ true.
func Enabled() bool { return os.Getenv("PDX_DEV_MODE") != "0" }
```

- [ ] **Step 4: Wire the two call sites**

`internal/module/dev/module.go`: import `github.com/wake/purdex/internal/devmode`;
in `RegisterRoutes` replace `if os.Getenv("PDX_DEV_MODE") != "1" {` with
`if !devmode.Enabled() {` and update the comment block to say
"Layer 2 (`devmode.Enabled()`, on unless `PDX_DEV_MODE=0`)". In `Start`
replace the same condition and change the log line to
`"[dev] update endpoints disabled (PDX_DEV_MODE=0)"`.

`internal/module/agent/probe_orchestrator.go`: replace the body of
`isDevMode` with `return devmode.Enabled()` and shorten its comment to
"isDevMode defers to devmode.Enabled(); kept as a local name so call sites
read naturally." (import `devmode`; drop `os` if now unused).

- [ ] **Step 5: Flip the existing gate tests**

`internal/module/dev/module_test.go`: rename
`TestRegisterRoutes_DisabledByDefault` → `TestRegisterRoutes_DisabledWithZero`
and change its `t.Setenv("PDX_DEV_MODE", "")` to `"0"`. Add:

```go
func TestRegisterRoutes_EnabledWhenUnset(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "")
	if err := os.Unsetenv("PDX_DEV_MODE"); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/dev/update/check", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Fatalf("dev routes must be registered when PDX_DEV_MODE is unset (spec D6)")
	}
}
```

`internal/module/agent/handler_devlog_test.go:118` comment: change "with
PDX_DEV_MODE unset" to "with PDX_DEV_MODE=0" (the test body already sets
`"0"`).

- [ ] **Step 6: Verify**

Run: `go test ./internal/devmode/ ./internal/module/dev/ ./internal/module/agent/` → PASS.
Run: `rg -n 'PDX_DEV_MODE' --glob '*.go' --glob '!*_test.go' internal cmd` → only `internal/devmode/devmode.go`.

- [ ] **Step 7: Commit**

```bash
git add internal/devmode internal/module/dev/module.go internal/module/dev/module_test.go internal/module/agent/probe_orchestrator.go internal/module/agent/handler_devlog_test.go
git commit -m "feat(devmode): dev features on by default, PDX_DEV_MODE=0 disables"
```

---

### Task 3: `pdx version [--json]`

**Files:**
- Create: `cmd/pdx/version.go`, `cmd/pdx/version_test.go`
- Modify: `cmd/pdx/main.go:39-40` (usage), `:44-63` (switch)

**Interfaces:**
- Produces: `runVersion(args []string, w io.Writer)`; JSON shape
  `{"version","hash","goos","goarch"}` — consumed verbatim by Plan B's
  `local-daemon.ts`.

- [ ] **Step 1: Failing tests**

```go
// cmd/pdx/version_test.go
package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/wake/purdex/internal/buildinfo"
)

func TestRunVersion_Plain(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "d5147bc", "1.0.0-alpha.334"

	var out bytes.Buffer
	runVersion(nil, &out)
	want := "pdx 1.0.0-alpha.334 (d5147bc) " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	if out.String() != want {
		t.Fatalf("plain output = %q, want %q", out.String(), want)
	}
}

func TestRunVersion_JSON(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "d5147bc", "1.0.0-alpha.334"

	var out bytes.Buffer
	runVersion([]string{"--json"}, &out)
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, out.String())
	}
	want := map[string]string{
		"version": "1.0.0-alpha.334", "hash": "d5147bc",
		"goos": runtime.GOOS, "goarch": runtime.GOARCH,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != 4 {
		t.Errorf("unexpected keys: %v", got)
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./cmd/pdx/ -run TestRunVersion` → FAIL (`runVersion` undefined).

- [ ] **Step 3: Implement**

```go
// cmd/pdx/version.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/wake/purdex/internal/buildinfo"
)

// versionInfo is the identity of this binary. The JSON form is consumed by
// the Electron app to inspect an on-disk daemon without starting it.
type versionInfo struct {
	Version string `json:"version"`
	Hash    string `json:"hash"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
}

// runVersion prints the binary identity. `--json` selects the machine form.
// It never fails: an unknown identity prints "unknown".
func runVersion(args []string, w io.Writer) {
	info := versionInfo{
		Version: buildinfo.Version,
		Hash:    buildinfo.Hash,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
	for _, a := range args {
		if a == "--json" {
			_ = json.NewEncoder(w).Encode(info)
			return
		}
	}
	fmt.Fprintf(w, "pdx %s (%s) %s/%s\n", info.Version, info.Hash, info.GOOS, info.GOARCH)
}
```

In `cmd/pdx/main.go` add to the switch (before `default`):

```go
	case "version":
		runVersion(os.Args[2:], os.Stdout)
```

and add `version` to the usage `Commands:` line.

- [ ] **Step 4: Verify**

Run: `go test ./cmd/pdx/ -run TestRunVersion` → PASS. `make build && ./bin/pdx version --json` prints the current short hash and `1.0.0-alpha.333`.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/version.go cmd/pdx/version_test.go cmd/pdx/main.go
git commit -m "feat(cli): add pdx version [--json]"
```

---

### Task 4: `/api/health` and `/api/info` read `buildinfo`

**Files:**
- Modify: `internal/core/info_handler.go:16-29,44-56`
- Create: `internal/core/info_handler_test.go`

**Interfaces:**
- Produces: health JSON gains `"version"` and `"hash"`; `/api/info.purdex_version` is `buildinfo.Version`; `core.Version` is deleted.

- [ ] **Step 1: Failing test**

```go
// internal/core/info_handler_test.go
package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/buildinfo"
)

func TestHandleHealth_CarriesBuildIdentity(t *testing.T) {
	oldH, oldV := buildinfo.Hash, buildinfo.Version
	t.Cleanup(func() { buildinfo.Hash, buildinfo.Version = oldH, oldV })
	buildinfo.Hash, buildinfo.Version = "abc1234", "9.9.9"

	c := &Core{}
	c.Pairing.Set(StateNormal)
	w := httptest.NewRecorder()
	c.HandleHealth(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["mode"] != "normal" {
		t.Fatalf("existing fields regressed: %v", body)
	}
	if body["hash"] != "abc1234" || body["version"] != "9.9.9" {
		t.Fatalf("health = %v, want hash abc1234 / version 9.9.9", body)
	}
}
```

If `Core.Pairing` cannot be used on a zero `Core` (check `internal/core/core.go`
for how `Pairing` is initialised — if it is a pointer, construct it the way
`New`/tests in `internal/core` do), adapt the constructor line; the assertion
on `mode` may be dropped if constructing pairing state is heavy — the point
of the test is `hash`/`version`.

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/core/ -run TestHandleHealth` → FAIL (no `hash` key).

- [ ] **Step 3: Implement**

In `internal/core/info_handler.go`: delete the `Version` var and its comment;
import `github.com/wake/purdex/internal/buildinfo`; `HandleHealth` becomes

```go
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"mode":    c.Pairing.Get().String(),
		"version": buildinfo.Version,
		"hash":    buildinfo.Hash,
	})
```

and in `handleInfo` `"purdex_version": Version,` → `"purdex_version": buildinfo.Version,`.
Run `rg -n 'core\.Version|Version = "dev"' --glob '*.go'` → empty (fix any
other reader the same way).

- [ ] **Step 4: Verify**

Run: `go test ./internal/core/ ./cmd/pdx/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/info_handler.go internal/core/info_handler_test.go
git commit -m "feat(core): expose build version and hash on /api/health, drop core.Version"
```

---

### Task 5: Permanent pid file; `serve` exits when the lock is held

**Files:**
- Modify: `cmd/pdx/daemon.go:166-172` (`releasePidLock`), `:314-318` (SIGKILL branch of `runStop`)
- Modify: `cmd/pdx/main.go:114-120`
- Modify: `cmd/pdx/daemon_test.go` (extend `TestPidFileLockAndUnlock`; add interleaving test)

**Interfaces:**
- `releasePidLock(f *os.File, pidPath string)` keeps its signature (the
  `pidPath` argument becomes unused but stays to avoid touching call sites).

- [ ] **Step 1: Failing tests**

Append to `cmd/pdx/daemon_test.go`:

```go
func TestReleasePidLock_KeepsFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")
	f, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	releasePidLock(f, pidPath)
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("pid file must survive release (spec §2.4b): %v", err)
	}
	if running, _ := isDaemonRunning(pidPath); running {
		t.Fatal("released pid file must read as not running")
	}
}

// A second starter that opened the pid file before the first released it
// must still be visible to isDaemonRunning afterwards. With unlink-on-release
// the second locker held an invisible inode; with a permanent file it holds
// the same inode everyone opens.
func TestPidLock_OpenBeforeReleaseStaysVisible(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")
	first, err := acquirePidLock(pidPath, 111)
	if err != nil {
		t.Fatal(err)
	}
	// Second party opens (but cannot yet lock) while first holds it.
	second, err := os.OpenFile(pidPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	releasePidLock(first, pidPath)
	if err := syscall.Flock(int(second.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("second flock after release: %v", err)
	}
	second.Truncate(0)
	second.Seek(0, 0)
	fmt.Fprintf(second, "%d", 222)
	second.Sync()

	running, pid := isDaemonRunning(pidPath)
	if !running || pid != 222 {
		t.Fatalf("isDaemonRunning = (%v, %d), want (true, 222)", running, pid)
	}
}
```

(add `"fmt"` and `"syscall"` to the test imports if missing).

- [ ] **Step 2: Run to fail**

Run: `go test ./cmd/pdx/ -run 'TestReleasePidLock_KeepsFile|TestPidLock_OpenBeforeReleaseStaysVisible'` → both FAIL (file removed / second locker invisible).

- [ ] **Step 3: Implement**

`cmd/pdx/daemon.go`:

```go
// releasePidLock drops the flock and closes the file. The pid file itself is
// permanent: unlinking it (in any order relative to the unlock) lets a
// concurrently starting serve flock an inode that is no longer reachable by
// path, after which `pdx status`/`stop` report "not running" for a live
// daemon. isDaemonRunning decides by lock state, not existence, so an
// unlocked leftover reads as stopped.
func releasePidLock(f *os.File, _ string) {
	if f != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}
```

In `runStop`, delete the `os.Remove(pidPath)` after the SIGKILL sleep (the
kernel drops the flock when the process dies).

`cmd/pdx/main.go` pid lock block:

```go
	pidFile, pidErr := acquirePidLock(pidPath, os.Getpid())
	if pidErr != nil {
		// Two daemons on one data_dir would share the SQLite files. Refuse.
		log.Fatalf("pid lock: %v — refusing to start a second daemon on %s", pidErr, cfg.DataDir)
	}
	defer releasePidLock(pidFile, pidPath)
```

- [ ] **Step 4: Verify**

Run: `go test ./cmd/pdx/` → PASS (including the pre-existing
`TestPidFileLockAndUnlock`, whose "re-acquire after release" still works).
Manual sanity on the Mini is **not** done in this task — the running daemon
is the old binary; deployment happens after merge.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/daemon.go cmd/pdx/main.go cmd/pdx/daemon_test.go
git commit -m "fix(daemon): keep the pid file permanent and refuse to serve when the lock is held"
```

---

### Task 6: Extract `buildBinary` from the rebuild handler

**Files:**
- Create: `internal/module/dev/build.go`, `internal/module/dev/build_test.go`
- Modify: `internal/module/dev/daemon.go:86-150`

**Interfaces:**
- Produces:
  ```go
  type buildTarget struct{ GOOS, GOARCH string } // zero value = host build
  func (m *DevModule) buildBinary(ctx context.Context, t buildTarget, hash, version, out string, sink func(line string)) error
  ```
  Runs `go build -ldflags "-X …buildinfo.Hash=<hash> -X …buildinfo.Version=<version>" -o <out> ./cmd/pdx`
  in `m.repoRoot`, env = `os.Environ()` plus `GOOS`/`GOARCH`/`CGO_ENABLED=0`
  when `t` is non-zero. Every stdout/stderr line goes to `sink`. Returns
  the `cmd.Wait` error (wrapped with `"go build: "`).

- [ ] **Step 1: Failing test**

```go
// internal/module/dev/build_test.go
package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeThrowawayModule creates a module that builds at ./cmd/pdx and
// returns its root. Shared by build and download tests.
func writeThrowawayModule(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildBinary_HostTarget(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	m := &DevModule{repoRoot: dir}
	out := filepath.Join(dir, "out-host")
	var lines []string
	err := m.buildBinary(context.Background(), buildTarget{}, "abc", "1.2.3", out, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("buildBinary: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output missing: %v", err)
	}
}

func TestBuildBinary_CrossTargetProducesForeignArch(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	m := &DevModule{repoRoot: dir}
	out := filepath.Join(dir, "out-cross")
	err := m.buildBinary(context.Background(), buildTarget{GOOS: "linux", GOARCH: "amd64"}, "abc", "1.2.3", out, func(string) {})
	if err != nil {
		t.Fatalf("cross build: %v", err)
	}
	head := make([]byte, 20)
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil {
		t.Fatal(err)
	}
	// ELF magic + e_machine 0x3E (x86-64) at offset 18 (little endian).
	if string(head[:4]) != "\x7fELF" || head[18] != 0x3e || head[19] != 0x00 {
		t.Fatalf("expected linux/amd64 ELF, got header % x", head)
	}
}

func TestBuildBinary_CompileErrorIsReported(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	m := &DevModule{repoRoot: dir}
	var lines []string
	err := m.buildBinary(context.Background(), buildTarget{}, "abc", "1.2.3", filepath.Join(dir, "out"), func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "undefined") {
		t.Fatalf("compiler output not streamed to sink: %v", lines)
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/module/dev/ -run TestBuildBinary` → FAIL (`buildBinary` undefined).

- [ ] **Step 3: Implement `build.go`**

```go
// internal/module/dev/build.go
package dev

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// buildTarget names a GOOS/GOARCH pair. The zero value means "the host".
type buildTarget struct {
	GOOS, GOARCH string
}

func (t buildTarget) isHost() bool { return t.GOOS == "" && t.GOARCH == "" }

// buildBinary runs `go build ./cmd/pdx` in the repo root, writing the binary
// to out and streaming every compiler line to sink. hash/version are baked
// into internal/buildinfo. Cross builds set GOOS/GOARCH and CGO_ENABLED=0
// (modernc sqlite is pure Go, so the host toolchain's C SDK is irrelevant).
// It does not consult git: callers pass the identity they already captured.
func (m *DevModule) buildBinary(ctx context.Context, t buildTarget, hash, version, out string, sink func(line string)) error {
	ldflags := "-X github.com/wake/purdex/internal/buildinfo.Hash=" + hash +
		" -X github.com/wake/purdex/internal/buildinfo.Version=" + version
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", ldflags, "-o", out, "./cmd/pdx")
	cmd.Dir = m.repoRoot
	// Inherit env so GOCACHE / PATH / HOME work; do not scrub.
	cmd.Env = os.Environ()
	if !t.isHost() {
		cmd.Env = append(cmd.Env, "GOOS="+t.GOOS, "GOARCH="+t.GOARCH, "CGO_ENABLED=0")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return err
	}
	stream := func(src io.Reader, done chan<- struct{}) {
		defer close(done)
		sc := bufio.NewScanner(src)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			sink(sc.Text())
		}
	}
	doneOut, doneErr := make(chan struct{}), make(chan struct{})
	go stream(stdout, doneOut)
	go stream(stderr, doneErr)
	<-doneOut
	<-doneErr
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Make the rebuild handler use it**

In `handleDaemonRebuild` (`daemon.go`), replace everything from
`hashCtx, hashCancel := …` through the `cmd.Wait()` error check with:

```go
	hashCtx, hashCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hashCancel()
	hashOut, _ := exec.CommandContext(hashCtx, "git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output()
	hash := strings.TrimSpace(string(hashOut))

	if err := m.buildBinary(ctx, buildTarget{}, hash, m.readVersionFile(), newPath, func(line string) {
		writeEvent(daemonRebuildEvent{Type: "log", Line: line})
	}); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
```

Keep the existing "Fix 1" comment about baking the hash; drop the now-dead
"Fix 2" pipe comments. Remove imports that become unused in `daemon.go`
(`bufio`, `io`) — `go vet` will tell you.

- [ ] **Step 5: Verify**

Run: `go test ./internal/module/dev/` → PASS, **including every pre-existing
`TestHandleDaemonRebuild_*` unchanged**. `go vet ./internal/module/dev/` clean.

- [ ] **Step 6: Commit**

```bash
git add internal/module/dev/build.go internal/module/dev/build_test.go internal/module/dev/daemon.go
git commit -m "refactor(dev): extract buildBinary from the daemon rebuild handler"
```

---

### Task 7: `GET /api/dev/daemon/download`

**Files:**
- Create: `internal/module/dev/download.go`, `internal/module/dev/download_test.go`
- Modify: `internal/module/dev/module.go` (route; `gitHeadFn` field + default in `Init`)

**Interfaces:**
- Consumes: `buildBinary` (Task 6), `daemonRebuildMu` (existing), `m.readVersionFile()` (Task 1).
- Produces: route `GET /api/dev/daemon/download?goos=&goarch=`; response
  headers `X-Pdx-Hash`, `X-Pdx-Version`, `X-Pdx-Sha256`, `Content-Length`,
  `Content-Type: application/octet-stream`,
  `Content-Disposition: attachment; filename="pdx"`; errors are JSON
  `{"error":…,"detail"?:…}` with 400/409/500. Plan B consumes exactly these.
- New field on `DevModule`: `gitHeadFn func() string` (short hash of HEAD or
  `""`), defaulted in `Init` to a 3-second `git -C repoRoot log -1 --format=%h`.

- [ ] **Step 1: Failing tests**

```go
// internal/module/dev/download_test.go
package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDownloadServer(t *testing.T, repoRoot, head string) *httptest.Server {
	t.Helper()
	m := &DevModule{repoRoot: repoRoot, versionFile: filepath.Join(repoRoot, "VERSION"), gitHeadFn: func() string { return head }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dev/daemon/download", m.handleDaemonDownload)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDownload_RejectsBadTarget(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "abc1234")
	for _, q := range []string{"", "goos=darwin", "goarch=arm64", "goos=plan9&goarch=arm64", "goos=darwin&goarch=mips"} {
		resp, err := http.Get(srv.URL + "/api/dev/daemon/download?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, resp.StatusCode)
		}
	}
}

func TestDownload_NoGitHashIs500(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "git hash") {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestDownload_BuildsServesAndCaches(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("7.7.7\n"), 0644)
	srv := newDownloadServer(t, dir, "abc1234")

	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Pdx-Hash") != "abc1234" || resp.Header.Get("X-Pdx-Version") != "7.7.7" {
		t.Fatalf("identity headers: %v", resp.Header)
	}
	sum := sha256.Sum256(body)
	if resp.Header.Get("X-Pdx-Sha256") != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 header mismatch")
	}
	if resp.ContentLength != int64(len(body)) {
		t.Fatalf("Content-Length %d, body %d", resp.ContentLength, len(body))
	}
	if resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("content-type %q", resp.Header.Get("Content-Type"))
	}
	if string(body[:4]) != "\x7fELF" {
		t.Fatalf("not an ELF (linux/amd64 requested)")
	}

	artifact := filepath.Join(dir, "bin", "dist", "pdx-linux-amd64-abc1234")
	st, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("artifact missing: %v", err)
	}
	if _, err := os.Stat(artifact + ".tmp"); !os.IsNotExist(err) {
		t.Fatal(".tmp left behind")
	}

	// Second request is a cache hit: served byte-for-byte, artifact untouched.
	time.Sleep(20 * time.Millisecond)
	resp2, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(body2) != string(body) {
		t.Fatal("cache hit served different bytes")
	}
	st2, _ := os.Stat(artifact)
	if !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("cache hit rebuilt the artifact")
	}
}

func TestDownload_PrunesStaleArtifactsForSameTargetOnly(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	dist := filepath.Join(dir, "bin", "dist")
	os.MkdirAll(dist, 0755)
	stale := filepath.Join(dist, "pdx-linux-amd64-old0000")
	other := filepath.Join(dist, "pdx-darwin-arm64-old0000")
	tmp := filepath.Join(dist, "pdx-linux-amd64-zzz.tmp")
	for _, p := range []string{stale, other, tmp} {
		os.WriteFile(p, []byte("x"), 0755)
	}
	srv := newDownloadServer(t, dir, "abc1234")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale same-target artifact not pruned")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("other-target artifact must be kept")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Error("*.tmp must be kept")
	}
}

func TestDownload_BuildFailureIs500WithDetail(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){ undefined() }\n")
	srv := newDownloadServer(t, dir, "abc1234")
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "build failed" || !strings.Contains(body["detail"], "undefined") {
		t.Fatalf("body = %v", body)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "bin", "dist")); len(entries) != 0 {
		t.Fatalf("dist not clean after failure: %v", entries)
	}
}

func TestDownload_409WhileRebuildMutexHeld(t *testing.T) {
	dir := writeThrowawayModule(t, "package main\nfunc main(){}\n")
	srv := newDownloadServer(t, dir, "abc1234")
	daemonRebuildMu.Lock()
	defer daemonRebuildMu.Unlock()
	resp, err := http.Get(srv.URL + "/api/dev/daemon/download?goos=linux&goarch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
}

func TestRegisterRoutes_DownloadIsGated(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "0")
	m := &DevModule{}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/dev/daemon/download?goos=darwin&goarch=arm64", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run to fail**

Run: `go test ./internal/module/dev/ -run 'TestDownload|TestRegisterRoutes_DownloadIsGated'` → FAIL (undefined `handleDaemonDownload`, `gitHeadFn`).

- [ ] **Step 3: Module wiring**

`internal/module/dev/module.go`: add field `gitHeadFn func() string` to
`DevModule` (comment: "short hash of HEAD, "" when unavailable; injected by
tests"). In `Init`, after the `hashFn` default:

```go
	if m.gitHeadFn == nil {
		m.gitHeadFn = m.gitHead
	}
```

and add the method (near `gitHash`):

```go
// gitHead returns the short hash of HEAD, or "" if git is unavailable.
// Bounded so a wedged git cannot hold the download mutex.
func (m *DevModule) gitHead() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Register the route after the daemon check route:

```go
	mux.HandleFunc("GET /api/dev/daemon/download", m.handleDaemonDownload)
```

- [ ] **Step 4: Implement `download.go`**

```go
// internal/module/dev/download.go
package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadBudget bounds build + transfer for one download request. The
// server has no global WriteTimeout, so the transfer needs its own deadline
// or a stalled reader would hold daemonRebuildMu indefinitely.
const downloadBudget = 6 * time.Minute

var allowedTargets = map[string]map[string]bool{
	"darwin": {"arm64": true, "amd64": true},
	"linux":  {"arm64": true, "amd64": true},
}

// handleDaemonDownload serves a pdx binary cross-compiled for ?goos=&goarch=.
// Artifacts are cached under bin/dist/pdx-<goos>-<goarch>-<hash>; the whole
// request runs under daemonRebuildMu so a /rebuild cannot exec the server
// mid-transfer and two downloads cannot race in bin/dist.
func (m *DevModule) handleDaemonDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	goos, goarch := q.Get("goos"), q.Get("goarch")
	if !allowedTargets[goos][goarch] {
		writeJSONError(w, http.StatusBadRequest, "unsupported target", "")
		return
	}

	if !daemonRebuildMu.TryLock() {
		writeJSONError(w, http.StatusConflict, "build in progress", "")
		return
	}
	defer daemonRebuildMu.Unlock()

	// Identity is captured exactly once and reused for the cache key, the
	// ldflags and the headers.
	hash := m.gitHeadFn()
	if hash == "" {
		writeJSONError(w, http.StatusInternalServerError, "git hash unavailable", "")
		return
	}
	version := m.readVersionFile()

	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, downloadBudget)
	defer cancel()
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	deadline := time.Now().Add(downloadBudget)
	_ = http.NewResponseController(w).SetWriteDeadline(deadline)

	distDir := filepath.Join(m.repoRoot, "bin", "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "mkdir", err.Error())
		return
	}
	name := fmt.Sprintf("pdx-%s-%s-%s", goos, goarch, hash)
	artifact := filepath.Join(distDir, name)

	if _, err := os.Stat(artifact); err != nil {
		tmp := artifact + ".tmp"
		ring := newRingBuffer(4 * 1024)
		err := m.buildBinary(ctx, buildTarget{GOOS: goos, GOARCH: goarch}, hash, version, tmp, ring.WriteLine)
		if err != nil {
			os.Remove(tmp)
			writeJSONError(w, http.StatusInternalServerError, "build failed", ring.String())
			return
		}
		if err := os.Rename(tmp, artifact); err != nil {
			os.Remove(tmp)
			writeJSONError(w, http.StatusInternalServerError, "publish failed", err.Error())
			return
		}
	}

	pruneStaleArtifacts(distDir, goos, goarch, name)

	f, err := os.Open(artifact)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "open artifact", err.Error())
		return
	}
	defer f.Close()
	sum, err := fileSHA256(f)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "hash artifact", err.Error())
		return
	}
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="pdx"`)
	w.Header().Set("X-Pdx-Hash", hash)
	w.Header().Set("X-Pdx-Version", version)
	w.Header().Set("X-Pdx-Sha256", sum)
	http.ServeContent(w, r, "pdx", st.ModTime(), f)
}

// pruneStaleArtifacts removes every pdx-<goos>-<goarch>-* in dir except keep
// and any *.tmp, so the directory holds one artifact per target.
func pruneStaleArtifacts(dir, goos, goarch, keep string) {
	prefix := fmt.Sprintf("pdx-%s-%s-", goos, goarch)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if n == keep || !strings.HasPrefix(n, prefix) || strings.HasSuffix(n, ".tmp") {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}

// fileSHA256 hashes f and rewinds it.
func fileSHA256(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSONError(w http.ResponseWriter, status int, msg, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]string{"error": msg}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ringBuffer keeps the last n bytes of build output for error details.
type ringBuffer struct {
	max int
	buf []byte
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

func (r *ringBuffer) WriteLine(line string) {
	r.buf = append(r.buf, line...)
	r.buf = append(r.buf, '\n')
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
}

func (r *ringBuffer) String() string { return string(r.buf) }
```

Notes for the implementer:
- `http.ServeContent` sets `Content-Length` from the seeker and honours
  `Range`; that is why the sha256 pass rewinds the file.
- `SetWriteDeadline` returning `http.ErrNotSupported` (e.g. under some test
  recorders) is ignored on purpose — `httptest.NewServer` supports it.

- [ ] **Step 5: Verify**

Run: `go test ./internal/module/dev/` → all PASS (download, build, rebuild,
module tests). `go vet ./...` clean. `go test ./...` green.

Manual smoke on the Mini **from the worktree, on a spare port** so the live
daemon is untouched (the worktree has its own repo root and `bin/`):

```bash
cd <worktree> && make build
PDX_DEV_MODE=1 ./bin/pdx serve --config /dev/null 2>/dev/null &   # only if serve accepts a missing config; otherwise skip this smoke — the unit tests cover it
```

If a spare-port serve is not trivially possible, skip; the tests exercise the
real handler through `httptest`.

- [ ] **Step 6: Commit**

```bash
git add internal/module/dev/download.go internal/module/dev/download_test.go internal/module/dev/module.go
git commit -m "feat(dev): GET /api/dev/daemon/download serves a cross-compiled pdx with integrity headers"
```

---

### Task 8: Final sweep

- [ ] Run the full suite: `go test ./... && go vet ./...` → green.
- [ ] `rg -n 'BakedInHash|core\.Version|== "1"' --glob '*.go' internal cmd` → no dev-mode `== "1"` reads outside tests, no old symbols.
- [ ] `make build && ./bin/pdx version --json` → shows the worktree HEAD short hash and `1.0.0-alpha.333`.
- [ ] Open the PR (Plan A) with the PR description listing: the D6 semantic change (`PDX_DEV_MODE=0` now the only off switch), the pid-file-permanent change and the new `serve` refusal, the new endpoint contract (headers + error shapes) for Plan B.

## Self-review against spec §2

| Spec | Task |
|---|---|
| §2.1 buildinfo + Makefile + rename | 1 |
| §2.2 `pdx version [--json]` | 3 |
| §2.3 health + `/api/info` + drop `core.Version` | 4 |
| §2.4 devmode `!= "0"` in both call sites | 2 |
| §2.4b pid file permanent, serve exits | 5 |
| §2.5 download: mutex whole request, identity once, cache, prune, headers, sha256, deadline, 409/400/500 | 7 (mutex/identity/cache/prune/headers/deadline), 6 (`buildBinary`, CGO_ENABLED=0) |
| §2.6 tests | 2, 3, 4, 5, 6, 7 |
