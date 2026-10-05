# Daemon Restart Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A daemon can be restarted from the SPA, from the host page (R1), the Development page (R2) and the Nex config screen (R3). The daemon restarts itself by re-exec, and the SPA confirms the restart through a per-process `boot_id`.

**Architecture:** The daemon gains `POST /api/daemon/restart`. It answers `202 {boot_id}`, runs the normal shutdown sequence, and re-execs the same binary path with the argv and environment the process started with. `/api/health` reports `boot_id`. The SPA has one shared action (`restartDaemon`). It uses the Electron IPC for an App-managed local daemon and the API for every other host, then polls health until a new `boot_id` appears. A per-host zustand store holds the in-progress state that the three entry points share.

**Tech Stack:** Go (net/http, syscall.Exec), React 19 + zustand 5 + Vitest, Electron IPC (unchanged).

**Spec:** `docs/specs/2026-10-06-daemon-restart-spec.md`

## Global Constraints

- Auth for `POST /api/daemon/restart` is the general chain (host token, `TokenAuth`). It must work in non-dev mode, so it is a core route and not part of the `dev` module.
- Reply `202 {"boot_id": "<current>"}` and flush **before** the shutdown starts. A second request gets `409` with `error: "restart_in_progress"`.
- Re-exec: the same executable path, read from disk at exec time, plus the same argv and the same environment. On exec failure, log it and exit non-zero.
- Log one line: `daemon restart requested by <remote addr>`.
- SPA timeout is **60 s**. The timeout message names `~/.config/pdx/logs/pdx.log` on that host.
- Copy (zh-TW, verbatim from the spec):
  - `重新啟動 daemon`
  - `立即重啟`
  - `重啟中…`
  - `daemon 已重新啟動`
  - `daemon 沒有在 60 秒內回來`
  - `N 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）`
- Mutation testing is a deliverable:
  - Dropping the boot-id comparison must turn the success test red.
  - Dropping the running-worker count must turn the confirm test red.
- Repo rules:
  - TDD, and one commit per task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Use pnpm, never npm install.
  - Run SPA tests with `cd spa && npx vitest run <file>`.
  - Type-check with `cd spa && npx tsc --noEmit -p tsconfig.app.json`. A bare `tsc --noEmit` is a no-op here.
  - Lint with `cd spa && pnpm run lint`.
  - Test Go with `go test ./cmd/pdx/ ./internal/core/`.
  - Never `go build ./cmd/pdx` into the repo root, because a tracked `pdx` binary lives there. Use `go build -o /dev/null ./cmd/pdx`.
- Every Bash command in a subagent starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/daemon-restart && `. Every Edit/Write path is absolute under that worktree.

## Derived decisions (coordinator-approved 2026-10-06; recorded in the spec §2/§3.1/§3.2/§3.3)

| # | Decision | Why |
|---|---|---|
| D1 | `boot_id` goes on **`/api/health`**, not `/api/info`. | Health is unauthenticated, so it works for a host in pairing mode. It is cheap, while `/api/info` execs `tmux -V`. It is also already the probe the SPA and `pdx start` use for "is it up". |
| D2 | `boot_id` is 16 hex chars from `crypto/rand`, generated in `core.New`. | It must be new on every process start, and a re-exec runs `New` again. |
| D3 | Re-exec uses the path, argv and env **captured at the top of `runServe`**, before `locale.EnsureUTF8`, `tmuxenv.Prepare` and nex's PATH policy mutate the process env (`os.Setenv` in `internal/locale/locale.go:72`, `internal/tmuxenv/tmuxenv.go:113-146`, `internal/module/nex/pathpolicy.go:76`). | Using `os.Environ()` at exec time would stack nex `path_prepend` entries on every restart, and would keep an old `path_prepend` after the very config change the restart is for. |
| D4 | A SIGINT/SIGTERM that arrives during a restart's shutdown turns it into a plain stop (no re-exec). A second signal still exits immediately. | `pdx stop` sends SIGTERM and waits for the pid lock to go free. Re-exec'ing after it would make `pdx stop` wait 30 s and then SIGKILL. |
| D5 | The `409` body also carries `boot_id`, and the SPA treats a 409 as "follow the restart already under way". | A second client clicking during a restart should see the same outcome, not an error. |
| D6 | `404` on the endpoint means the daemon predates this feature. The SPA reports that the daemon on this host is too old for remote restart and to run `pdx stop && pdx start` there. | Air26 and older hosts will show the button before they are updated. |
| D7 | Running workers are counted fresh when the confirm opens: `GET /api/nex/v1/executions?state=running`, following `next_cursor`, within a 3 s budget. Nex reported but not ready → 0. No Nex info, or the list fails or times out → **unknown**, and the dialog shows `無法確認是否有 worker 正在執行；若有，重啟會中斷它們這一輪（之後可以繼續對話）`. | The cached per-host list exists only while some view subscribes, so on the host page it is usually empty. Hiding the warning when we could not check would understate the risk. |
| D8 | R1: the button sits in the **Daemon 設定** section (`hosts.daemon_config`). It shows while the host is connected **or** a restart of it is in progress. | §2 places `purdex_version` in "daemon config", but it actually lives in "System Info" (`OverviewSection.tsx:309`). §3.2 names "daemon config" as the section, so we follow §3.2. Staying visible mid-restart keeps the spinner when the host drops to `reconnecting`. |
| D9 | R2: managed + alive shows the restart button **even when Update also shows**. It replaces the old "Update, else Restart — never both" rule. | Spec §3.2: "whenever the local daemon is alive and managed". |
| D10 | R2: if the daemon is managed + alive but **not in the host list**, R2 keeps today's direct IPC restart, with no confirm and no boot-id check. | There is no `hostId`, so there is no per-host state, no API base for health and no worker list. This app has no terminals or workers there to warn about. |
| D11 | Toast and notice messages name the host (`{{host}}：daemon 已重新啟動`). | The toast is global, and several hosts can be restarted. |
| D12 | Without a restart hook installed (unit tests, or a boot where `os.Executable` failed), the endpoint answers `503 {"error":"restart_unavailable"}`. | A 202 that never restarts would leave the SPA waiting 60 s for nothing. |

## Review Focus

1. **The old process keeps answering health with the old `boot_id`** for up to the 10 s shutdown budget (`StopModules` runs before `srv.Shutdown`). The SPA must not call that success. Pinned in Task 5: "health keeps returning the old boot id → timeout".
2. **The env mutated after boot** (nex PATH prepend, `LANG`, `TMUX` dropped) must not leak into the re-exec. Pinned in Task 3: "a mutation after capture does not reach the plan".
3. **`pdx stop` during a restart** must stop, not restart — including a signal at the very tail (during `CloseModules`, or after `serveAndWait` returned but before exec). Pinned in Task 2 ("signal during CloseModules → nil") and Task 3 (`restartStillWanted` drains a late signal after `signal.Stop`).
4. **The host drops to `reconnecting` mid-restart.** The R1 button must stay visible with its spinner, not vanish. Pinned in Task 8.
5. **A daemon too old for the endpoint (404)** must give a clear message, not a raw `HTTP 404`. Pinned in Task 5 (`unsupported`) and Task 6 (message text).
6. **A hung step** (`localDaemonStatus` IPC or the POST never answering) must still end in the 60 s timeout, not spin forever. Pinned in Task 5 (one 60 s deadline over the whole action).
7. **A host removed while its restart runs** must not send the POST or health probes to another daemon. `hostFetch` falls back to the active host for an unknown id; Task 5 uses `pinnedHostFetch`.

## Phases → PRs

Each PR is ≤ 800 diff lines and ≤ 20 files.

| Phase | PR | Tasks | Ships |
|---|---|---|---|
| A | daemon | 1–4 | endpoint, boot_id, re-exec, integration test |
| B | SPA logic | 5–6 | `restartDaemon`, `countRunningWorkers`, `useDaemonRestartStore` (no UI yet) |
| C | SPA UI | 7–10 | `RestartDaemonButton` + R1, R3, R2 + i18n |

One bump PR after all three merge.

---

## Phase A — daemon

### Task 1: `boot_id` on health + `POST /api/daemon/restart` handler

**Files:**
- Create: `internal/core/restart.go`
- Create: `internal/core/restart_test.go`
- Modify: `internal/core/core.go`. Add fields to `Core` (after `bootNex`, ~line 66), set `BootID` in `New` (~line 79), and register the route in `RegisterCoreRoutes` (~line 248).
- Modify: `internal/core/info_handler.go:20-28` (`HandleHealth` adds `boot_id`)
- Modify: `internal/core/info_handler_test.go` (health carries `boot_id`)
- Modify: `cmd/pdx/http_chain_test.go` (the route is behind TokenAuth)

**Interfaces:**
- Produces:
  - `(*core.Core).BootID string`, exported and read-only after `New`.
  - `func (c *Core) SetRestartHook(fn func())`. Call it before serving. `fn` must not block.
  - Route `POST /api/daemon/restart`. It answers 202 `{"boot_id"}`, 409 `{"error":"restart_in_progress","boot_id"}` or 503 `{"error":"restart_unavailable"}`.
  - Health JSON gains `"boot_id"`.

- [ ] **Step 1: Write the failing tests** in `internal/core/restart_test.go`.

```go
package core

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body
}

func TestBootID_NewPerCore(t *testing.T) {
	a := New(CoreDeps{Config: &config.Config{}})
	b := New(CoreDeps{Config: &config.Config{}})
	assert.Len(t, a.BootID, 16)
	assert.NotEqual(t, a.BootID, b.BootID, "every process start (New) must get a fresh boot id")
}

func TestDaemonRestart_Replies202BeforeHook(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	var atHook struct {
		code    int
		body    string
		flushed bool
	}
	c.SetRestartHook(func() {
		atHook.code, atHook.body, atHook.flushed = rec.Code, rec.Body.String(), rec.Flushed
	})
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))

	assert.Equal(t, http.StatusAccepted, atHook.code, "202 must be written before the hook fires")
	assert.True(t, atHook.flushed, "reply must be flushed before the hook fires")
	assert.Contains(t, atHook.body, `"boot_id":"`+c.BootID+`"`)
}

func TestDaemonRestart_SecondRequest409(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	calls := 0
	c.SetRestartHook(func() { calls++ })
	c.handleDaemonRestart(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/daemon/restart", nil))

	rec := httptest.NewRecorder()
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))
	assert.Equal(t, http.StatusConflict, rec.Code)
	body := decode(t, rec)
	assert.Equal(t, "restart_in_progress", body["error"])
	assert.Equal(t, c.BootID, body["boot_id"], "409 carries the boot id so a second client can follow the restart")
	assert.Equal(t, 1, calls, "hook fires once")
}

func TestDaemonRestart_NoHook503(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	c.handleDaemonRestart(rec, httptest.NewRequest("POST", "/api/daemon/restart", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "restart_unavailable", decode(t, rec)["error"])
}

func TestDaemonRestart_LogsRequester(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	c := New(CoreDeps{Config: &config.Config{}})
	c.SetRestartHook(func() {})
	req := httptest.NewRequest("POST", "/api/daemon/restart", nil)
	req.RemoteAddr = "100.64.0.4:51234"
	c.handleDaemonRestart(httptest.NewRecorder(), req)
	assert.Equal(t, 1, strings.Count(buf.String(), "daemon restart requested by 100.64.0.4:51234"))
}
```

Append the following to `internal/core/info_handler_test.go`:

```go
func TestHandleHealth_CarriesBootID(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, httptest.NewRequest("GET", "/api/health", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, c.BootID, body["boot_id"])
}
```

Append the following to `cmd/pdx/http_chain_test.go`. It uses the real `RegisterCoreRoutes` so it proves the route sits behind the general chain:

```go
func TestDaemonRestartRequiresHostToken(t *testing.T) {
	c := newTestCore(&config.Config{Token: "host-token"})
	c.SetRestartHook(func() {})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	h := newOuterHandler(c, mux, nil)

	if rec := doRequest(t, h, "POST", "/api/daemon/restart", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", rec.Code)
	}
	if rec := doRequest(t, h, "POST", "/api/daemon/restart", "host-token"); rec.Code != http.StatusAccepted {
		t.Fatalf("host token: got %d, want 202", rec.Code)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `go test ./internal/core/ ./cmd/pdx/ -run 'BootID|DaemonRestart|HandleHealth_CarriesBootID'`
  Expected: build failure (`c.BootID`, `SetRestartHook` and `handleDaemonRestart` are undefined).

- [ ] **Step 3: Implement.** Create `internal/core/restart.go`:

```go
// internal/core/restart.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"
)

// newBootID returns 16 hex chars that are new on every process start: a
// restart re-execs and runs New again, so the SPA can tell "the daemon came
// back" from "the old process is still answering" (daemon restart spec §3.1).
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; time still differs per start.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// SetRestartHook installs what POST /api/daemon/restart calls once its 202
// is flushed. serve installs a non-blocking trigger of the shutdown sequence
// (cmd/pdx). Must be called before the server starts; fn must not block —
// the HTTP shutdown waits for this handler to return.
func (c *Core) SetRestartHook(fn func()) { c.restartHook = fn }

// handleDaemonRestart is POST /api/daemon/restart (spec §3.1): 202 with the
// current boot id, flushed, then the hook. A second request while one is
// under way gets 409 with the same boot id so its client can follow the
// restart already in flight. No hook → 503: a 202 here would promise a
// restart nothing will perform.
func (c *Core) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if c.restartHook == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_unavailable"})
		return
	}
	if !c.restarting.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_in_progress", "boot_id": c.BootID})
		return
	}
	log.Printf("daemon restart requested by %s", r.RemoteAddr)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"boot_id": c.BootID})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	c.restartHook()
}
```

In `internal/core/core.go`, add `"sync/atomic"` to the imports. Add the following to the end of the `Core` struct:

```go
	// BootID is new on every process start (newBootID, set in New) and is
	// reported by /api/health; never mutated after New.
	BootID string
	// restartHook / restarting back POST /api/daemon/restart (restart.go).
	restartHook func()
	restarting  atomic.Bool
```

In `New`, add `BootID: newBootID(),` to the returned struct literal. In `RegisterCoreRoutes`, add:

```go
	mux.HandleFunc("POST /api/daemon/restart", c.handleDaemonRestart)
```

In `HandleHealth`, add `"boot_id": c.BootID,` to the map.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `go test ./internal/core/ ./cmd/pdx/`
  Expected: PASS, with no other test regressed. `TestHealthEndpoint` still passes, because it only asserts `ok` and the absence of `tmux`.

- [ ] **Step 5: Commit.**
  `git add internal/core/restart.go internal/core/restart_test.go internal/core/core.go internal/core/info_handler.go internal/core/info_handler_test.go cmd/pdx/http_chain_test.go`
  Then: `git commit -m "feat(daemon): POST /api/daemon/restart and boot_id on health"`

### Task 2: `serveAndWait` takes a restart trigger

**Files:**
- Modify: `cmd/pdx/shutdown.go` (the signature, the select, the watcher and the return)
- Modify: `cmd/pdx/shutdown_test.go`. `harness.run` passes `h.restart`, `newHarness` makes the channel, and the new tests go in.

**Interfaces:**
- Consumes: none.
- Produces:
  - `var errRestart = errors.New("restart requested")`.
  - New signature: `func serveAndWait(srv server, ln net.Listener, sig <-chan os.Signal, restart <-chan struct{}, cancel context.CancelFunc, target shutdownTarget, budget time.Duration, logf func(string, ...any), exit func(int)) error`. It returns `errRestart` when the restart trigger started the sequence and no signal cancelled it.

- [ ] **Step 1: Write the failing tests.** In `shutdown_test.go`, add `restart chan struct{}` to `harness`, set `restart: make(chan struct{}, 1)` in `newHarness`, and change `run` to `return serveAndWait(h.srv, nil, h.sig, h.restart, h.cancel, h.target, budget, h.logf, h.exit)`. Then add:

```go
func TestServeAndWait_RestartRunsSequenceAndReturnsErrRestart(t *testing.T) {
	h := newHarness()
	h.restart <- struct{}{}
	err := h.run(testBudget)
	if !errors.Is(err, errRestart) {
		t.Fatalf("serveAndWait returned %v, want errRestart", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	if len(h.exited()) != 0 {
		t.Fatalf("exit called: %v", h.exited())
	}
}

func TestServeAndWait_SignalDuringRestartCancelsRestart(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.stopHook = func() { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	<-entered
	h.sig <- syscall.SIGTERM // `pdx stop` while the restart's shutdown runs
	waitForLog(t, h, "exiting instead of restarting")
	close(unblock)
	if err := <-out; err != nil {
		t.Fatalf("serveAndWait returned %v, want nil (a stop, not a restart)", err)
	}
	if len(h.exited()) != 0 {
		t.Fatalf("first signal must not force exit: %v", h.exited())
	}
}

func TestServeAndWait_TwoSignalsDuringRestartExitImmediately(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.stopHook = func() { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	<-entered
	h.sig <- syscall.SIGTERM
	waitForLog(t, h, "exiting instead of restarting")
	h.sig <- syscall.SIGTERM
	waitForExit(t, h, 130)
	close(unblock)
	<-out
}
```

```go
// codex plan review #1: a signal at the TAIL of the sequence (during
// CloseModules) must still cancel the restart. The decision may only be
// read once the watcher has exited, or a signal the watcher has taken but
// not yet recorded is lost and the daemon re-execs under `pdx stop`.
func TestServeAndWait_SignalDuringCloseModulesCancelsRestart(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.closeHook = func() { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	<-entered
	h.sig <- syscall.SIGTERM
	waitForLog(t, h, "exiting instead of restarting")
	close(unblock)
	if err := <-out; err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
}
```

Before writing these, read the existing harness fakes (`fakeTarget`, `recorder.names`, and the blocking-StopModules/CloseModules tests around `shutdown_test.go:373` and `:411`). Reuse the hook names they already have, and add `waitForLog`/`waitForExit` helpers only if equivalents are not there yet. The four tests above are the behaviour to pin. Adapt their hook plumbing to the harness as it exists.

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `go test ./cmd/pdx/ -run ServeAndWait`
  Expected: compile error (the argument count, `errRestart` is undefined).

- [ ] **Step 3: Implement** in `cmd/pdx/shutdown.go`. Add `"sync/atomic"` to the imports and the sentinel:

```go
// errRestart is what serveAndWait returns when a restart request — not a
// signal, not a Serve failure — started the sequence and no signal arrived
// during it: the caller re-execs once its own deferred cleanup has run
// (daemon restart spec §3.1). Like http.ErrServerClosed, it is an outcome,
// not a failure.
var errRestart = errors.New("restart requested")
```

Add the parameter `restart <-chan struct{}` after `sig`. A nil channel never fires, so callers without restart pass nil. Extend the doc comment with: "A restart trigger behaves like Serve-first for signal counting, except that the first signal also cancels the restart: `pdx stop` during a restart must stop the daemon, not see it come back."

Change the trigger select and the watcher like this:

```go
	restartTriggered := false
	var restartCancelled atomic.Bool
	select {
	case s := <-sig:
		signalTriggered = true
		logf("received %v, shutting down...", s)
	case err = <-serveErr:
		serveReturned = true
	case <-restart:
		restartTriggered = true
		logf("restart requested, shutting down...")
	}
```

```go
		if !signalTriggered {
			select {
			case s := <-sig:
				if restartTriggered {
					restartCancelled.Store(true)
					logf("received %v during restart; exiting instead of restarting (send again to exit immediately)", s)
				} else {
					logf("received %v during shutdown; send again to exit immediately", s)
				}
			case <-done:
				return
			}
		}
```

The watcher must be **joined** before the restart decision is read (codex plan review #1). Today `done` is closed by a `defer`. Replace that with an explicit close plus a wait on a `watcherDone` channel the watcher closes on every exit path:

```go
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		// ... existing watcher body, with the restart branch above ...
	}()
```

After `CloseModules`, replace the old `defer close(done)` behaviour with:

```go
	close(done)
	<-watcherDone // a signal the watcher took is now recorded in restartCancelled
	if restartTriggered && !restartCancelled.Load() {
		return errRestart
	}
```

This goes before the existing `ErrServerClosed` check. In the restart path `err` is `http.ErrServerClosed` from `<-serveErr`.

Make sure every early return in the function still closes `done`. Today there is none between the watcher start and the end. If you restructure, keep it that way, or the watcher join deadlocks. A signal that arrives **after** the watcher has exited stays buffered in `sig`. Task 3's `restartStillWanted` takes it.

In `cmd/pdx/main.go`, temporarily pass `nil` as the new argument so the build stays green. Task 3 wires the real channel.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `go test ./cmd/pdx/ -run ServeAndWait -count=3 -race`
  Expected: PASS, including every pre-existing ServeAndWait test.

- [ ] **Step 5: Commit.**
  `git add cmd/pdx/shutdown.go cmd/pdx/shutdown_test.go cmd/pdx/main.go`
  Then: `git commit -m "feat(daemon): serveAndWait restart trigger; a signal during restart stops instead"`

### Task 3: Capture the boot command, re-exec after a clean shutdown

**Files:**
- Create: `cmd/pdx/reexec.go`
- Create: `cmd/pdx/reexec_test.go`
- Modify: `cmd/pdx/main.go`. Change `case "serve"` (line 45), add capture at the top of `runServe` (line 79), install the hook after `c := core.New(...)` (~line 186), and use the result of `serveAndWait` (~line 262).

**Interfaces:**
- Consumes: `errRestart` and the new `serveAndWait` signature (Task 2), plus `(*core.Core).SetRestartHook` (Task 1).
- Produces:
  - `type reexecPlan struct{ path string; argv, env []string }`
  - `func captureReexecPlan(executable func() (string, error), args, env []string) (*reexecPlan, error)`
  - `func reexec(p *reexecPlan, execFn func(string, []string, []string) error, logf func(string, ...any), exit func(int))`
  - `func restartStillWanted(sig <-chan os.Signal, stop func(), logf func(string, ...any)) bool`
  - `runServe` now returns `*reexecPlan` (nil means a normal exit).

- [ ] **Step 1: Write the failing tests** in `cmd/pdx/reexec_test.go`.

```go
package main

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestCaptureReexecPlan_CopiesBootState(t *testing.T) {
	args := []string{"/opt/pdx", "serve", "--config", "/c.toml"}
	env := []string{"PDX_DEV_MODE=1", "PATH=/usr/bin"}
	p, err := captureReexecPlan(func() (string, error) { return "/opt/pdx", nil }, args, env)
	if err != nil {
		t.Fatal(err)
	}
	// A mutation after capture (nex PATH policy, locale, tmuxenv) must not reach the plan.
	env[1] = "PATH=/nex/prepend:/usr/bin"
	args[3] = "/other.toml"
	if !reflect.DeepEqual(p.env, []string{"PDX_DEV_MODE=1", "PATH=/usr/bin"}) {
		t.Fatalf("env = %v, want the boot env", p.env)
	}
	if !reflect.DeepEqual(p.argv, []string{"/opt/pdx", "serve", "--config", "/c.toml"}) {
		t.Fatalf("argv = %v, want the boot argv", p.argv)
	}
	if p.path != "/opt/pdx" {
		t.Fatalf("path = %q", p.path)
	}
}

func TestCaptureReexecPlan_ExecutableError(t *testing.T) {
	if _, err := captureReexecPlan(func() (string, error) { return "", errors.New("nope") }, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestReexec_ExecsSamePathArgvEnv(t *testing.T) {
	p := &reexecPlan{path: "/opt/pdx", argv: []string{"/opt/pdx", "serve"}, env: []string{"PDX_DEV_MODE=1"}}
	var gotPath string
	var gotArgv, gotEnv []string
	exitCode := -1
	reexec(p, func(path string, argv, env []string) error {
		gotPath, gotArgv, gotEnv = path, argv, env
		return errors.New("exec format error") // a real exec does not return
	}, func(string, ...any) {}, func(c int) { exitCode = c })
	if gotPath != p.path || !reflect.DeepEqual(gotArgv, p.argv) || !reflect.DeepEqual(gotEnv, p.env) {
		t.Fatalf("exec(%q, %v, %v), want the plan", gotPath, gotArgv, gotEnv)
	}
	if exitCode != 1 {
		t.Fatalf("exit(%d) after a failed exec, want 1", exitCode)
	}
}

func TestReexec_LogsFailure(t *testing.T) {
	var logs []string
	reexec(&reexecPlan{path: "/x"}, func(string, []string, []string) error { return errors.New("boom") },
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }, func(int) {})
	if len(logs) == 0 || !containsStr(logs[len(logs)-1], "boom") {
		t.Fatalf("logs = %v, want the exec error", logs)
	}
}
```

If the package has no `containsStr` helper, use `strings.Contains`.

```go
// codex plan review #1: a signal that lands after serveAndWait's watcher
// has exited sits in sigCh. The last gate before exec stops delivery first
// (from then on SIGTERM takes its default action and ends the process),
// then takes anything already buffered: one pending → stop, not restart.
func TestRestartStillWanted_StopsDeliveryThenDrains(t *testing.T) {
	sig := make(chan os.Signal, 1)
	var order []string
	stop := func() { order = append(order, "stop") }
	if !restartStillWanted(sig, stop, func(string, ...any) {}) {
		t.Fatal("no pending signal → restart still wanted")
	}
	if len(order) != 1 {
		t.Fatalf("stop not called: %v", order)
	}

	sig <- syscall.SIGTERM
	var logs []string
	if restartStillWanted(sig, func() {}, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }) {
		t.Fatal("a pending signal must turn the restart into a stop")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "exiting instead of restarting") {
		t.Fatalf("logs = %v", logs)
	}
}
```

Add `"os"`, `"strings"` and `"syscall"` to the test imports.

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `go test ./cmd/pdx/ -run 'Reexec'`
  Expected: compile error (undefined).

- [ ] **Step 3: Implement** `cmd/pdx/reexec.go`:

```go
// cmd/pdx/reexec.go
package main

// reexecPlan is how this serve process was started — captured at the top of
// runServe, before locale.EnsureUTF8, tmuxenv.Prepare and nex's PATH policy
// change the process env — so a restart (POST /api/daemon/restart) execs
// the same command line in the same environment: PDX_DEV_MODE survives, and
// nex's path_prepend is applied once by the new image rather than stacked on
// the old one's. path is resolved at boot but read from disk at exec, so a
// binary swapped in since is what runs (daemon restart spec §3.1).
type reexecPlan struct {
	path string
	argv []string
	env  []string
}

func captureReexecPlan(executable func() (string, error), args, env []string) (*reexecPlan, error) {
	path, err := executable()
	if err != nil {
		return nil, err
	}
	return &reexecPlan{
		path: path,
		argv: append([]string(nil), args...),
		env:  append([]string(nil), env...),
	}, nil
}

// reexec replaces this process with the plan. It runs after runServe has
// returned, i.e. after the stores are closed and the pid lock released; the
// new image re-takes the lock through mustAcquirePidLock's retry. The pid
// stays the same, so `pdx stop/status` and the App's ownership record stay
// valid. exec returns only on failure: log it and exit non-zero (the SPA
// then sees the host stay down and points at this log).
func reexec(p *reexecPlan, execFn func(string, []string, []string) error, logf func(string, ...any), exit func(int)) {
	logf("restart: exec %s", p.path)
	err := execFn(p.path, p.argv, p.env)
	logf("restart: exec %s failed: %v", p.path, err)
	exit(1)
}

// restartStillWanted is the last gate before re-exec (spec D4). stop ends
// signal delivery to sig — from then on SIGINT/SIGTERM take their default
// action and end the process, so a `pdx stop` racing the exec still stops
// it — and then any signal that arrived after serveAndWait's watcher had
// exited is taken from the buffer: one pending means the operator asked to
// stop, so no re-exec.
func restartStillWanted(sig <-chan os.Signal, stop func(), logf func(string, ...any)) bool {
	stop()
	select {
	case s := <-sig:
		logf("received %v before restart; exiting instead of restarting", s)
		return false
	default:
		return true
	}
}
```

(add `import "os"` to `reexec.go`)

Wire it in `cmd/pdx/main.go`:

```go
	case "serve":
		if plan := runServe(os.Args[2:]); plan != nil {
			reexec(plan, syscall.Exec, log.Printf, os.Exit)
		}
```

In `runServe`:
- Change the signature to `func runServe(args []string) *reexecPlan`.
- As the **first statement after the recover defer**: `boot, bootErr := captureReexecPlan(os.Executable, os.Args, os.Environ())`.
- After `c := core.New(...)`:

```go
	// POST /api/daemon/restart → restartCh → serveAndWait runs the normal
	// shutdown; the re-exec happens in main once this function's defers
	// (stores, pid lock) have run. No boot plan → no hook → the endpoint
	// answers 503 rather than accepting a restart nothing can perform.
	restartCh := make(chan struct{}, 1)
	if bootErr != nil {
		log.Printf("restart: unavailable (%v)", bootErr)
	} else {
		c.SetRestartHook(func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		})
	}
```

At the end:

```go
	err = serveAndWait(srv, listener, sigCh, restartCh, cancel, c, core.ShutdownBudget, log.Printf, os.Exit)
	if errors.Is(err, errRestart) {
		if restartStillWanted(sigCh, func() { signal.Stop(sigCh) }, log.Printf) {
			return boot
		}
		return nil
	}
	if err != nil {
		log.Printf("server error: %v", err)
	}
	return nil
```

Add `"errors"` to the imports. `err` is already declared in `runServe`. Every other `return` path in `runServe` is a `log.Fatalf`, so no other return statements are needed.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `go test ./cmd/pdx/ && go vet ./cmd/pdx/ && go build -o /dev/null ./cmd/pdx`
  Expected: PASS.

- [ ] **Step 5: Commit.**
  `git add cmd/pdx/reexec.go cmd/pdx/reexec_test.go cmd/pdx/main.go`
  Then: `git commit -m "feat(daemon): re-exec with the boot command after a restart's clean shutdown"`

### Task 4: Integration test — real serve, restart, same pid, new boot_id

**Files:**
- Create: `cmd/pdx/restart_integration_test.go` (`//go:build integration`)

**Interfaces:**
- Consumes: everything from Tasks 1–3.
- Produces: none (test only).

The test binary is its own helper process. `TestMain` dispatches to `main()` when `PDX_RESTART_HELPER=1`. The re-exec runs the test binary again with the same argv and env, so the re-exec'd image dispatches the same way, and no `go build` is needed.

- [ ] **Step 1: Write the test.**

```go
//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// (codex plan review #3: codexbroker's default socket glob reads the real
// /var/folders/*/*/T; PDX_CODEX_* point it at the temp dir. Before relying on
// the env list in isolatedEnv, read registerServeModules (cmd/pdx/main.go
// ~267-300) and add an override for anything else that reaches outside
// HOME/TMUX_TMPDIR/data_dir.)

// TestMain doubles as the daemon: the integration test starts this test
// binary with PDX_RESTART_HELPER=1 and `serve` args, and a restart re-execs
// the same argv+env, so the new image lands here again.
func TestMain(m *testing.M) {
	if os.Getenv("PDX_RESTART_HELPER") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func bootID(base string) (string, error) {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(base + "/api/health")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		BootID string `json:"boot_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.BootID, nil
}

func waitBootID(t *testing.T, base, not string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if id, err := bootID(base); err == nil && id != "" && id != not {
			return id
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no boot id other than %q within %v", not, timeout)
	return ""
}

// isolatedEnv is os.Environ() minus every key the helper daemon must not
// inherit, plus the overrides — built explicitly rather than appended so no
// duplicate key decides which value wins (codex plan review #3).
func isolatedEnv(dir string) []string {
	drop := map[string]bool{"HOME": true, "TMUX": true, "TMUX_PANE": true, "TMUX_TMPDIR": true,
		"PDX_DEV_MODE": true, "PDX_RESTART_HELPER": true, "PDX_CODEX_STATE_ROOT": true, "PDX_CODEX_SOCKET_ROOTS": true}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	codex := filepath.Join(dir, "codex")
	os.MkdirAll(codex, 0700)
	return append(env,
		"PDX_RESTART_HELPER=1",
		"HOME="+dir,               // every ~/... a module touches (~/.claude, ~/.codex, …)
		"TMUX_TMPDIR="+dir,        // tmux calls hit a private, absent server — never the user's sessions
		"PDX_CODEX_STATE_ROOT="+codex,   // codexbroker state …
		"PDX_CODEX_SOCKET_ROOTS="+codex, // … and its socket glob (default globs the real /var/folders/*/*/T)
		"PDX_DEV_MODE=0",          // observable env: dev routes stay off across the re-exec (D3)
	)
}

func devCheckStatus(t *testing.T, base string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/api/dev/daemon/check", nil)
	req.Header.Set("Authorization", "Bearer itest-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRestart_ReexecKeepsPidNewBootID(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	cfgPath := filepath.Join(dir, "config.toml")
	// [dev] update = true mounts the dev module; PDX_DEV_MODE=0 keeps its
	// routes unregistered. If the re-exec lost the boot env, the new image
	// would see PDX_DEV_MODE unset (= on) and /api/dev/daemon/check would appear.
	cfg := fmt.Sprintf("bind = \"127.0.0.1\"\nport = %d\ndata_dir = %q\ntoken = \"itest-token\"\n\n[dev]\nupdate = true\nrepo_root = %q\n", port, dir, dir)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "serve", "--config", cfgPath)
	cmd.Env = isolatedEnv(dir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// One waiter for the child's whole life: if the original process ever
	// exits, this fires — exec keeps the process, a crash-and-respawn does not.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "serve.log"))
			t.Logf("serve.log:\n%s", b)
		}
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	first := waitBootID(t, base, "", 30*time.Second)
	if code := devCheckStatus(t, base); code != http.StatusNotFound {
		t.Fatalf("before restart: /api/dev/daemon/check = %d, want 404 (PDX_DEV_MODE=0)", code)
	}

	req, _ := http.NewRequest("POST", base+"/api/daemon/restart", nil)
	req.Header.Set("Authorization", "Bearer itest-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		BootID string `json:"boot_id"`
	}
	json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || accepted.BootID != first {
		t.Fatalf("restart: %d %q, want 202 with boot id %q", resp.StatusCode, accepted.BootID, first)
	}

	second := waitBootID(t, base, first, 60*time.Second)
	if second == first {
		t.Fatal("boot id unchanged")
	}
	// Same process: the child we started never exited (exec replaces the
	// image in place; a waiter on it would have fired on any exit).
	select {
	case err := <-exited:
		t.Fatalf("original process exited during restart: %v", err)
	default:
	}
	// Same environment: dev routes are still off (D3).
	if code := devCheckStatus(t, base); code != http.StatusNotFound {
		t.Fatalf("after restart: /api/dev/daemon/check = %d, want 404 — the boot env (PDX_DEV_MODE=0) was not kept", code)
	}
	pidData, err := os.ReadFile(filepath.Join(dir, "pdx.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := strconv.Atoi(strings.TrimSpace(string(pidData))); got != cmd.Process.Pid {
		t.Fatalf("pid file = %d, want %d (unchanged across restart)", got, cmd.Process.Pid)
	}
}
```

- [ ] **Step 2: Run it.**
  Run: `go test -tags integration ./cmd/pdx/ -run TestRestart_ReexecKeepsPidNewBootID -count=1 -v`
  Expected: PASS. If it fails, read the `serve.log` the cleanup dumps.
  - If a module refuses to boot in the isolated HOME, fix it through the test env or config, not by weakening the assertions.
  - Then run plain `go test ./cmd/pdx/` and confirm the untagged suite does not see `TestMain`.

- [ ] **Step 3: Mutation check.**
  - Temporarily make `reexec` drop `PDX_DEV_MODE` from `p.env` before calling `execFn`. Rerun the integration test. The "after restart" dev-route assertion must FAIL.
  - Revert. Record the red output for the PR body.

- [ ] **Step 4: Commit.**
  `git add cmd/pdx/restart_integration_test.go`
  Then: `git commit -m "test(daemon): integration — restart keeps the pid and changes boot_id"`

**Phase A gate:**
- `go test ./... 2>&1 | tail -20`
- `go vet ./cmd/pdx/ ./internal/core/`
- the integration test above

Then open the PR, run codex R1 + R2, and merge.

---

## Phase B — SPA logic (no UI)

### Task 5: `lib/daemon-restart.ts` — the shared restart action and worker count

**Files:**
- Create: `spa/src/lib/daemon-restart.ts`
- Create: `spa/src/lib/daemon-restart.test.ts`

**Interfaces:**
- Consumes:
  - `pinnedHostFetch` (`lib/host-api`). It is **not** `hostFetch`: `hostFetch` falls back to the active host for an unknown id, so a host removed mid-restart would send the POST or the probes to another daemon (codex plan review #5).
  - `findHostByEndpoint` and `useHostStore` (`stores/useHostStore`)
  - `useNexHostStore` (`ensure`, `byHost[id].info`)
  - `isNexReady` (`components/hosts/nex/nex-ready`)
  - `listExecutions` (`lib/nex/nex-api`)
  - `window.electronAPI.localDaemonStatus` / `localDaemonRestart`
- Produces:
  - `RESTART_TIMEOUT_MS = 60_000`, `RESTART_POLL_MS = 1_000`, `HEALTH_PROBE_TIMEOUT_MS = 3_000`, `WORKER_COUNT_TIMEOUT_MS = 3_000`, `MAX_WORKER_PAGES = 20`
  - `class DaemonRestartError extends Error { kind: 'timeout' | 'unsupported' | 'request' }`
  - `interface RestartDeps { readBootId(hostId: string): Promise<string | null>; postRestart(hostId: string): Promise<string>; isManagedLocal(hostId: string): Promise<boolean>; localRestart(): Promise<ElectronLocalDaemonResult> }`
  - `readBootId(hostId): Promise<string | null>`
  - `postRestart(hostId): Promise<string>` resolves the pre-restart boot id and throws `DaemonRestartError`.
  - `isManagedLocal(hostId): Promise<boolean>`
  - `restartDaemon(hostId, deps?: Partial<RestartDeps>): Promise<ElectronLocalDaemonResult | null>`. It resolves once a **different** boot id answers health, and the value is the IPC result on the IPC path.
    - **One 60 s deadline covers the whole action**: the status IPC, the POST or IPC restart, and the polling (codex plan review #4).
    - Timing uses real `setTimeout`, so the tests use `vi.useFakeTimers()`.
  - `countRunningWorkers(hostId, timeoutMs?): Promise<number | null>`.
    - `null` means unknown.
    - More than `MAX_WORKER_PAGES` pages also counts as unknown, because a truncated count would under-report (codex plan review #6).

- [ ] **Step 1: Write the failing tests** in `spa/src/lib/daemon-restart.test.ts`.

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  restartDaemon, postRestart, readBootId, isManagedLocal, countRunningWorkers,
  DaemonRestartError, RESTART_TIMEOUT_MS, RESTART_POLL_MS, MAX_WORKER_PAGES, type RestartDeps,
} from './daemon-restart'
import * as hostApi from './host-api'
import * as nexApi from './nex/nex-api'
import { useHostStore } from '../stores/useHostStore'
import { useNexHostStore } from '../stores/useNexHostStore'

vi.mock('./host-api', async (orig) => ({ ...(await orig<typeof import('./host-api')>()), pinnedHostFetch: vi.fn() }))
vi.mock('./nex/nex-api', async (orig) => ({ ...(await orig<typeof import('./nex/nex-api')>()), listExecutions: vi.fn() }))

const deps = (over: Partial<RestartDeps>): Partial<RestartDeps> => ({
  readBootId: async () => null,
  postRestart: async () => 'old',
  isManagedLocal: async () => false,
  localRestart: async () => { throw new Error('unused') },
  ...over,
})

/** Settle-state probe that also attaches a handler at once (no unhandled-rejection noise). */
function track<T>(p: Promise<T>) {
  const s: { done: boolean; value?: T; error?: unknown } = { done: false }
  p.then((v) => { s.done = true; s.value = v }, (e) => { s.done = true; s.error = e })
  return s
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

beforeEach(() => {
  vi.mocked(hostApi.pinnedHostFetch).mockReset()
  vi.mocked(nexApi.listExecutions).mockReset()
  useHostStore.getState().reset()
})

describe('restartDaemon — success needs a NEW boot id', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('resolves once health answers a different boot id', async () => {
    const ids = ['old', 'old', 'new']
    const s = track(restartDaemon('h1', deps({ readBootId: async () => ids.shift() ?? 'new' })))
    await vi.advanceTimersByTimeAsync(3 * RESTART_POLL_MS)
    expect(s).toMatchObject({ done: true, value: null })
  })

  it('times out at 60 s when health keeps answering the old boot id', async () => {
    // The old process answers health through its whole shutdown budget.
    // Dropping the boot-id comparison turns this red (mutation deliverable).
    const s = track(restartDaemon('h1', deps({ readBootId: async () => 'old' })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS - 1)
    expect(s.done).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    expect(s.error).toBeInstanceOf(DaemonRestartError)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it('an unreachable host (null) is not success', async () => {
    const s = track(restartDaemon('h1', deps({ readBootId: async () => null })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it.each([
    ['the status IPC', { isManagedLocal: () => new Promise<boolean>(() => {}) }],
    ['the POST', { postRestart: () => new Promise<string>(() => {}) }],
    ['the IPC restart', { isManagedLocal: async () => true, localRestart: () => new Promise<ElectronLocalDaemonResult>(() => {}) }],
  ])('%s hanging still ends in the 60 s timeout', async (_, over) => {
    const s = track(restartDaemon('h1', deps(over)))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it('stops probing health once timed out', async () => {
    const probe = vi.fn(async () => 'old')
    track(restartDaemon('h1', deps({ readBootId: probe })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    const n = probe.mock.calls.length
    await vi.advanceTimersByTimeAsync(10 * RESTART_POLL_MS)
    expect(probe.mock.calls.length).toBe(n)
  })
})

describe('restartDaemon — path choice', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('managed local daemon → IPC, never the API', async () => {
    const post = vi.fn()
    const result = { url: 'http://127.0.0.1:7860', token: 't', hash: 'h', version: 'v', hostname: 'air' }
    const ids = ['old', 'new']
    const s = track(restartDaemon('h1', deps({
      isManagedLocal: async () => true,
      localRestart: async () => result,
      postRestart: post,
      readBootId: async () => ids.shift() ?? 'new',
    })))
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS)
    expect(s.value).toBe(result)
    expect(post).not.toHaveBeenCalled()
  })

  it('any other host → API', async () => {
    const post = vi.fn(async () => 'old')
    const local = vi.fn()
    track(restartDaemon('h1', deps({ postRestart: post, localRestart: local, readBootId: async () => 'new' })))
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS)
    expect(post).toHaveBeenCalledWith('h1')
    expect(local).not.toHaveBeenCalled()
  })

  it('IPC failure → request error with its message', async () => {
    const s = track(restartDaemon('h1', deps({ isManagedLocal: async () => true, localRestart: async () => { throw new Error('cannot restart: external') } })))
    await vi.advanceTimersByTimeAsync(0)
    expect(s.error).toMatchObject({ kind: 'request', message: expect.stringContaining('cannot restart: external') })
  })
})

describe('postRestart', () => {
  it('202 → the pre-restart boot id', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(202, { boot_id: 'b1' }))
    await expect(postRestart('h1')).resolves.toBe('b1')
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0].slice(0, 2)).toEqual(['h1', '/api/daemon/restart'])
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0][2]).toMatchObject({ method: 'POST' })
  })
  it('409 with boot id → follows the restart already under way', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(409, { error: 'restart_in_progress', boot_id: 'b1' }))
    await expect(postRestart('h1')).resolves.toBe('b1')
  })
  it('404 → unsupported (daemon predates the endpoint)', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(new Response('404 page not found', { status: 404 }))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'unsupported' })
  })
  it('503 → request error carrying the daemon error text', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(503, { error: 'restart_unavailable' }))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'request', message: 'restart_unavailable' })
  })
  it('a host no longer configured (pinned fetch rejects) → request error, nothing sent elsewhere', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockRejectedValueOnce(new Error('host h1 is not configured'))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'request', message: 'host h1 is not configured' })
  })
})

describe('readBootId', () => {
  it('reads boot_id from /api/health through the pinned fetch', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(200, { ok: true, boot_id: 'b9' }))
    await expect(readBootId('h1')).resolves.toBe('b9')
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0].slice(0, 2)).toEqual(['h1', '/api/health'])
  })
  it('no boot_id / non-200 / throw → null', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(200, { ok: true }))
    await expect(readBootId('h1')).resolves.toBeNull()
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(500, {}))
    await expect(readBootId('h1')).resolves.toBeNull()
    vi.mocked(hostApi.pinnedHostFetch).mockRejectedValueOnce(new Error('down'))
    await expect(readBootId('h1')).resolves.toBeNull()
  })
})

describe('isManagedLocal', () => {
  const st = (o: Partial<ElectronLocalDaemonStatus>) => ({ managed: 'managed', config: { bind: '100.64.0.4', port: 7860, token: 't' }, ...o }) as ElectronLocalDaemonStatus
  it('true only when managed and the host is at the local daemon endpoint', async () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.4:7860', token: 't', hostname: 'air' })
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: async () => st({}), localDaemonRestart: vi.fn() } as typeof window.electronAPI
    await expect(isManagedLocal(id)).resolves.toBe(true)
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: async () => st({ managed: 'external' }) } as typeof window.electronAPI
    await expect(isManagedLocal(id)).resolves.toBe(false)
  })
  it('false without Electron', async () => {
    window.electronAPI = undefined
    await expect(isManagedLocal('h1')).resolves.toBe(false)
  })
})

describe('countRunningWorkers', () => {
  const setNex = (hostId: string, info: unknown) =>
    useNexHostStore.setState({ byHost: { [hostId]: { info } } } as never)
  beforeEach(() => {
    vi.spyOn(useNexHostStore.getState(), 'ensure').mockResolvedValue()
  })
  it('counts running executions across pages', async () => {
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions)
      .mockResolvedValueOnce({ items: [{ state: 'running' }, { state: 'running' }] as never, next_cursor: 'c2' })
      .mockResolvedValueOnce({ items: [{ state: 'running' }] as never, next_cursor: '' })
    await expect(countRunningWorkers('h1')).resolves.toBe(3)
    expect(vi.mocked(nexApi.listExecutions).mock.calls[0][1]).toMatchObject({ state: 'running' })
    expect(vi.mocked(nexApi.listExecutions).mock.calls[1][1]).toMatchObject({ state: 'running', cursor: 'c2' })
  })
  it('more pages than MAX_WORKER_PAGES → null (a truncated count would under-report)', async () => {
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions).mockResolvedValue({ items: [{ state: 'running' }] as never, next_cursor: 'more' })
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
    expect(nexApi.listExecutions).toHaveBeenCalledTimes(MAX_WORKER_PAGES)
  })
  it('nex not ready → 0 (no workers can run)', async () => {
    setNex('h1', { ready: false, mounted: false, configured: false })
    await expect(countRunningWorkers('h1')).resolves.toBe(0)
    expect(nexApi.listExecutions).not.toHaveBeenCalled()
  })
  it('no nex info, or the list fails → null (unknown)', async () => {
    setNex('h1', null)
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions).mockRejectedValueOnce(new Error('503'))
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
  })
})
```

Before writing, check the `NexHostEntry` fields in `lib/nex/nex-host-reducer.ts`. The `setNex` shortcut sets only `info`, and the real entry may need `phase` as well. Adapt the fixtures to the real shapes and keep the assertions. `registerLocalHost` returns the host id (`useHostStore.ts:194`).

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/lib/daemon-restart.test.ts`
  Expected: FAIL (module not found).

- [ ] **Step 3: Implement** `spa/src/lib/daemon-restart.ts`:

```ts
// spa/src/lib/daemon-restart.ts — the one restart action behind the three
// entry points (daemon restart spec §3.2): the App-managed local daemon goes
// through the Electron IPC, every other host through POST /api/daemon/restart.
// Done means health answers with a DIFFERENT boot_id — the old process keeps
// answering through its shutdown budget, so "the host answered" proves nothing.
// Every request goes through pinnedHostFetch: a host removed mid-restart must
// not have its POST or probes land on whatever host is active instead.
import { pinnedHostFetch } from './host-api'
import { listExecutions } from './nex/nex-api'
import { findHostByEndpoint, useHostStore } from '../stores/useHostStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { isNexReady } from '../components/hosts/nex/nex-ready'

export const RESTART_TIMEOUT_MS = 60_000 // the window `pdx start` waits for health
export const RESTART_POLL_MS = 1_000
export const HEALTH_PROBE_TIMEOUT_MS = 3_000
export const WORKER_COUNT_TIMEOUT_MS = 3_000
export const MAX_WORKER_PAGES = 20

export type RestartFailure = 'timeout' | 'unsupported' | 'request'

export class DaemonRestartError extends Error {
  kind: RestartFailure
  constructor(kind: RestartFailure, message = '') {
    super(message)
    this.name = 'DaemonRestartError'
    this.kind = kind
  }
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

export async function readBootId(hostId: string): Promise<string | null> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), HEALTH_PROBE_TIMEOUT_MS)
  try {
    const res = await pinnedHostFetch(hostId, '/api/health', { signal: ctl.signal })
    if (!res.ok) return null
    const body = (await res.json()) as { boot_id?: unknown }
    return typeof body.boot_id === 'string' && body.boot_id !== '' ? body.boot_id : null
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

/** POST /api/daemon/restart → the boot id the restart replaces. */
export async function postRestart(hostId: string): Promise<string> {
  let res: Response
  try {
    res = await pinnedHostFetch(hostId, '/api/daemon/restart', { method: 'POST' })
  } catch (err) {
    throw new DaemonRestartError('request', errText(err))
  }
  // A daemon older than the endpoint: the mux has no such route (spec D6).
  if (res.status === 404 || res.status === 405) throw new DaemonRestartError('unsupported', `HTTP ${res.status}`)
  const body = (await res.json().catch(() => null)) as { boot_id?: unknown; error?: unknown } | null
  // 409: another client's restart is already under way — follow it to the same finish line (spec D5).
  if ((res.status === 202 || res.status === 409) && typeof body?.boot_id === 'string') return body.boot_id
  throw new DaemonRestartError('request', typeof body?.error === 'string' ? body.error : `HTTP ${res.status}`)
}

/** The host is the local daemon the App manages (same bind:port), and the IPC can restart it. */
export async function isManagedLocal(hostId: string): Promise<boolean> {
  const api = window.electronAPI
  if (!api?.localDaemonStatus || !api.localDaemonRestart) return false
  const st = await api.localDaemonStatus().catch(() => null)
  if (!st || st.managed !== 'managed' || !st.config) return false
  return findHostByEndpoint(useHostStore.getState().hosts, st.config.bind, st.config.port)?.id === hostId
}

export interface RestartDeps {
  readBootId: (hostId: string) => Promise<string | null>
  postRestart: (hostId: string) => Promise<string>
  isManagedLocal: (hostId: string) => Promise<boolean>
  localRestart: () => Promise<ElectronLocalDaemonResult>
}

const defaultDeps: RestartDeps = {
  readBootId,
  postRestart,
  isManagedLocal,
  localRestart: () => {
    const fn = window.electronAPI?.localDaemonRestart
    return fn ? fn() : Promise.reject(new Error('local daemon IPC unavailable'))
  },
}

const TIMED_OUT = Symbol('timed-out')

/**
 * Restart `hostId`'s daemon and wait for it to come back. Resolves with the
 * IPC result on the managed-local path (the caller re-registers the host
 * with it, as the Development page always has), null on the API path.
 * Rejects with DaemonRestartError: 'request' (the call failed), 'unsupported'
 * (daemon too old), 'timeout' (no new boot id within 60 s). The 60 s cover
 * the whole action — a hung status IPC, POST or IPC restart included.
 */
export async function restartDaemon(hostId: string, over: Partial<RestartDeps> = {}): Promise<ElectronLocalDaemonResult | null> {
  const d = { ...defaultDeps, ...over }
  let expired = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const deadline = new Promise<typeof TIMED_OUT>((r) => { timer = setTimeout(() => r(TIMED_OUT), RESTART_TIMEOUT_MS) })
  const attempt = (async (): Promise<ElectronLocalDaemonResult | null | typeof TIMED_OUT> => {
    let before: string | null
    let ipc: ElectronLocalDaemonResult | null = null
    if (await d.isManagedLocal(hostId)) {
      before = await d.readBootId(hostId)
      ipc = await d.localRestart().catch((err) => { throw new DaemonRestartError('request', errText(err)) })
    } else {
      before = await d.postRestart(hostId)
    }
    while (!expired) {
      await sleep(RESTART_POLL_MS)
      if (expired) break
      const now = await d.readBootId(hostId)
      if (now !== null && now !== before) return ipc
    }
    return TIMED_OUT
  })()
  attempt.catch(() => {}) // a failure after the deadline won the race is not unhandled
  try {
    const r = await Promise.race([attempt, deadline])
    if (r === TIMED_OUT) throw new DaemonRestartError('timeout')
    return r
  } finally {
    expired = true
    clearTimeout(timer)
  }
}

/**
 * Workers in `running` on the host, for the confirm text (spec §3.2, D7).
 * Nex reported but not ready → 0 (no turn can run). No Nex info, the list
 * failed, more than MAX_WORKER_PAGES pages, or over the budget → null:
 * unknown, which the dialog words as a warning rather than hiding it.
 */
export async function countRunningWorkers(hostId: string, timeoutMs = WORKER_COUNT_TIMEOUT_MS): Promise<number | null> {
  const work = (async (): Promise<number | null> => {
    await useNexHostStore.getState().ensure(hostId)
    const info = useNexHostStore.getState().byHost[hostId]?.info ?? null
    if (info === null) return null
    if (!isNexReady(info)) return 0
    let n = 0
    let cursor: string | undefined
    for (let page = 0; page < MAX_WORKER_PAGES; page++) {
      const p = await listExecutions(hostId, { state: 'running', cursor })
      n += p.items.filter((e) => e.state === 'running').length
      if (!p.next_cursor) return n
      cursor = p.next_cursor
    }
    return null
  })().catch(() => null)
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((r) => { timer = setTimeout(() => r(null), timeoutMs) })
  try {
    return await Promise.race([work, timeout])
  } finally {
    clearTimeout(timer)
  }
}
```

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/lib/daemon-restart.test.ts && npx tsc --noEmit -p tsconfig.app.json`
  Expected: PASS.

- [ ] **Step 5: Mutation check (deliverable).**
  - Change `if (now !== null && now !== before) return ipc` to `if (now !== null) return ipc`.
  - Run the file. The "times out at 60 s when health keeps answering the old boot id" test must FAIL.
  - Revert. Record the red output for the PR body.

- [ ] **Step 6: Commit.**
  `git add spa/src/lib/daemon-restart.ts spa/src/lib/daemon-restart.test.ts`
  Then: `git commit -m "feat(spa): restartDaemon — IPC for the managed local daemon, API otherwise; done on a new boot_id"`

### Task 6: `useDaemonRestartStore` — per-host in-progress state, toast and notice

**Files:**
- Create: `spa/src/stores/useDaemonRestartStore.ts`
- Create: `spa/src/stores/useDaemonRestartStore.test.ts`
- Modify: `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`. Add the result keys here. `locale-completeness.test.ts` requires both locales.

**Interfaces:**
- Consumes: `restartDaemon` and `DaemonRestartError` (Task 5), `useUndoToast`, `useI18nStore`, `useHostStore.registerLocalHost`, `useNexHostStore.invalidate`.
- Produces:
  - `useDaemonRestartStore` with:
    - `restarting: Record<string, true>`
    - `settled: Record<string, number>`, bumped after every attempt, success or failure, so views re-read
    - `restart(hostId: string, hostName: string): Promise<void>`, a no-op while that host is already restarting
  - i18n keys `hosts.restart.done`, `hosts.restart.timeout`, `hosts.restart.failed`, `hosts.restart.unsupported`

i18n values:

| key | zh-TW | en |
|---|---|---|
| `hosts.restart.done` | `{{host}}：daemon 已重新啟動` | `{{host}}: daemon restarted` |
| `hosts.restart.timeout` | `{{host}}：daemon 沒有在 60 秒內回來，請查看該主機的 ~/.config/pdx/logs/pdx.log` | `{{host}}: the daemon did not come back within 60 seconds — check ~/.config/pdx/logs/pdx.log on that host` |
| `hosts.restart.failed` | `{{host}}：重啟失敗（{{error}}），請查看該主機的 ~/.config/pdx/logs/pdx.log` | `{{host}}: restart failed ({{error}}) — check ~/.config/pdx/logs/pdx.log on that host` |
| `hosts.restart.unsupported` | `{{host}}：這台 daemon 版本不支援遠端重啟，請在該主機執行 pdx stop && pdx start` | `{{host}}: this daemon is too old for remote restart — run pdx stop && pdx start on that host` |

- [ ] **Step 1: Write the failing tests** in `spa/src/stores/useDaemonRestartStore.test.ts`.

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useDaemonRestartStore } from './useDaemonRestartStore'
import { useUndoToast } from './useUndoToast'
import { useI18nStore } from './useI18nStore'
import { useNexHostStore } from './useNexHostStore'
import { useHostStore } from './useHostStore'
import * as restartLib from '../lib/daemon-restart'
import { DaemonRestartError } from '../lib/daemon-restart'

vi.mock('../lib/daemon-restart', async (orig) => ({ ...(await orig<typeof import('../lib/daemon-restart')>()), restartDaemon: vi.fn() }))

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useDaemonRestartStore.setState({ restarting: {}, settled: {} })
  useUndoToast.setState({ toast: null, notice: null })
  vi.mocked(restartLib.restartDaemon).mockReset()
})

describe('useDaemonRestartStore', () => {
  it('marks the host restarting while the action runs, then clears it', async () => {
    let finish!: () => void
    vi.mocked(restartLib.restartDaemon).mockReturnValueOnce(new Promise((r) => { finish = () => r(null) }))
    const p = useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useDaemonRestartStore.getState().restarting.h1).toBe(true)
    expect(useDaemonRestartStore.getState().restarting.h2).toBeUndefined()
    finish(); await p
    expect(useDaemonRestartStore.getState().restarting.h1).toBeUndefined()
    expect(useDaemonRestartStore.getState().settled.h1).toBe(1)
  })

  it('a second restart of the same host while one runs is a no-op', async () => {
    vi.mocked(restartLib.restartDaemon).mockReturnValueOnce(new Promise(() => {}))
    void useDaemonRestartStore.getState().restart('h1', 'mlab')
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(restartLib.restartDaemon).toHaveBeenCalledTimes(1)
  })

  it('success → toast, nex info re-read', async () => {
    const invalidate = vi.spyOn(useNexHostStore.getState(), 'invalidate').mockResolvedValue()
    vi.mocked(restartLib.restartDaemon).mockResolvedValueOnce(null)
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useUndoToast.getState().toast?.message).toBe('mlab：daemon 已重新啟動')
    expect(invalidate).toHaveBeenCalledWith('h1')
  })

  it('IPC result re-registers the local host', async () => {
    const reg = vi.spyOn(useHostStore.getState(), 'registerLocalHost').mockReturnValue('h1')
    vi.mocked(restartLib.restartDaemon).mockResolvedValueOnce({ url: 'http://127.0.0.1:7860', token: 't', hash: 'x', version: 'v', hostname: 'air' })
    await useDaemonRestartStore.getState().restart('h1', 'air')
    expect(reg).toHaveBeenCalledWith({ url: 'http://127.0.0.1:7860', token: 't', hostname: 'air' })
  })

  it.each([
    [new DaemonRestartError('timeout'), 'mlab：daemon 沒有在 60 秒內回來，請查看該主機的 ~/.config/pdx/logs/pdx.log'],
    [new DaemonRestartError('request', 'Failed to fetch'), 'mlab：重啟失敗（Failed to fetch），請查看該主機的 ~/.config/pdx/logs/pdx.log'],
    [new DaemonRestartError('unsupported', 'HTTP 404'), 'mlab：這台 daemon 版本不支援遠端重啟，請在該主機執行 pdx stop && pdx start'],
  ])('failure %# → persistent notice', async (err, text) => {
    vi.mocked(restartLib.restartDaemon).mockRejectedValueOnce(err)
    await useDaemonRestartStore.getState().restart('h1', 'mlab')
    expect(useUndoToast.getState().notice?.message).toBe(text)
    expect(useDaemonRestartStore.getState().restarting.h1).toBeUndefined()
    expect(useDaemonRestartStore.getState().settled.h1).toBe(1)
  })
})
```

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/stores/useDaemonRestartStore.test.ts`
  Expected: FAIL (module not found).

- [ ] **Step 3: Implement** `spa/src/stores/useDaemonRestartStore.ts`:

```ts
// spa/src/stores/useDaemonRestartStore.ts — per-host "restart in progress"
// shared by the three restart entry points (daemon restart spec §3.3): while
// a host restarts, every entry point for it shows the spinner and is
// disabled. `settled` bumps after every attempt so views that show daemon
// facts (Overview info, Development status) re-read them.
import { create } from 'zustand'
import { restartDaemon, DaemonRestartError } from '../lib/daemon-restart'
import { useI18nStore } from './useI18nStore'
import { useUndoToast } from './useUndoToast'
import { useHostStore } from './useHostStore'
import { useNexHostStore } from './useNexHostStore'

interface DaemonRestartState {
  restarting: Record<string, true>
  settled: Record<string, number>
  restart: (hostId: string, hostName: string) => Promise<void>
}

function failureText(err: unknown, host: string): string {
  const t = useI18nStore.getState().t
  if (err instanceof DaemonRestartError && err.kind === 'timeout') return t('hosts.restart.timeout', { host })
  if (err instanceof DaemonRestartError && err.kind === 'unsupported') return t('hosts.restart.unsupported', { host })
  return t('hosts.restart.failed', { host, error: err instanceof Error ? err.message : String(err) })
}

export const useDaemonRestartStore = create<DaemonRestartState>()((set, get) => ({
  restarting: {},
  settled: {},
  restart: async (hostId, hostName) => {
    if (get().restarting[hostId]) return
    set((s) => ({ restarting: { ...s.restarting, [hostId]: true } }))
    try {
      const ipc = await restartDaemon(hostId)
      // Same as the Development page's own restart always did: the IPC hands back the daemon's url/token.
      if (ipc) useHostStore.getState().registerLocalHost({ url: ipc.url, token: ipc.token, hostname: ipc.hostname })
      // /api/info re-read: the Nex page's restart_required hint goes away (spec §3.3).
      void useNexHostStore.getState().invalidate(hostId)
      useUndoToast.getState().show(useI18nStore.getState().t('hosts.restart.done', { host: hostName }))
    } catch (err) {
      useUndoToast.getState().show(failureText(err, hostName), undefined, undefined, { persistent: true })
    } finally {
      set((s) => {
        const restarting = { ...s.restarting }
        delete restarting[hostId]
        return { restarting, settled: { ...s.settled, [hostId]: (s.settled[hostId] ?? 0) + 1 } }
      })
    }
  },
}))
```

Add the four i18n keys from the table above to both locale files, next to the other `hosts.*` keys.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/stores/useDaemonRestartStore.test.ts src/locales/locale-completeness.test.ts && npx tsc --noEmit -p tsconfig.app.json`
  Expected: PASS.

- [ ] **Step 5: Commit.**
  `git add spa/src/stores/useDaemonRestartStore.ts spa/src/stores/useDaemonRestartStore.test.ts spa/src/locales/zh-TW.json spa/src/locales/en.json`
  Then: `git commit -m "feat(spa): useDaemonRestartStore — per-host restart state, result toast/notice"`

**Phase B gate:** `cd spa && npx vitest run && pnpm run lint && npx tsc --noEmit -p tsconfig.app.json`. Then open the PR, run codex R1 + R2, and merge.

---

## Phase C — SPA UI

### Task 7: `RestartDaemonButton` — the button, confirm and spinner

**Files:**
- Create: `spa/src/components/hosts/RestartDaemonButton.tsx`
- Create: `spa/src/components/hosts/RestartDaemonButton.test.tsx`
- Modify: `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

**Interfaces:**
- Consumes:
  - `countRunningWorkers` (Task 5)
  - `useDaemonRestartStore` (Task 6)
  - `ConfirmDialog` (`components/ConfirmDialog`). Its props are `testIdPrefix`, `title`, `body`, `confirmLabel`, `onCancel`, `onConfirm` and `children`.
  - `hostLabel` and `useHostLook` (`lib/host-look`)
- Produces: `RestartDaemonButton({ hostId, label?, testId? = 'restart-daemon', className? })`.
  - Test ids: `${testId}` (the button), `${testId}-confirm-dialog` / `-confirm` / `-cancel` (from ConfirmDialog with `testIdPrefix = ${testId}-confirm`), and `${testId}-workers` (the worker line).

i18n values:

| key | zh-TW | en |
|---|---|---|
| `hosts.restart.button` | `重新啟動 daemon` | `Restart daemon` |
| `hosts.restart.button_now` | `立即重啟` | `Restart now` |
| `hosts.restart.restarting` | `重啟中…` | `Restarting…` |
| `hosts.restart.confirm_title` | `重新啟動 {{host}} 的 daemon？` | `Restart the daemon on {{host}}?` |
| `hosts.restart.confirm_body` | `這台主機上的終端連線會中斷幾秒，之後自動接回；tmux session 不受影響。` | `Terminal connections on this host drop for a few seconds and come back by themselves; tmux sessions are not affected.` |
| `hosts.restart.confirm_workers` | `{{count}} 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）` | `{{count}} worker(s) running — restarting interrupts their current turn (the conversation can continue afterwards)` |
| `hosts.restart.confirm_workers_unknown` | `無法確認是否有 worker 正在執行；若有，重啟會中斷它們這一輪（之後可以繼續對話）` | `Could not check for running workers; any that are running will have their current turn interrupted (the conversation can continue afterwards)` |
| `hosts.restart.confirm` | `重新啟動` | `Restart` |

- [ ] **Step 1: Write the failing tests** in `RestartDaemonButton.test.tsx`.

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { RestartDaemonButton } from './RestartDaemonButton'
import { useDaemonRestartStore } from '../../stores/useDaemonRestartStore'
import { useI18nStore } from '../../stores/useI18nStore'
import * as restartLib from '../../lib/daemon-restart'

vi.mock('../../lib/daemon-restart', async (orig) => ({ ...(await orig<typeof import('../../lib/daemon-restart')>()), countRunningWorkers: vi.fn() }))

const restart = vi.fn(async () => {})
beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  restart.mockClear()
  useDaemonRestartStore.setState({ restarting: {}, settled: {}, restart })
  vi.mocked(restartLib.countRunningWorkers).mockReset()
})

async function openConfirm(workers: number | null) {
  vi.mocked(restartLib.countRunningWorkers).mockResolvedValueOnce(workers)
  render(<RestartDaemonButton hostId="h1" />)
  await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon')) })
  await screen.findByTestId('restart-daemon-confirm-dialog')
}

describe('RestartDaemonButton', () => {
  it('confirm names running workers when there are some', async () => {
    // Dropping the running-worker count turns this red (mutation deliverable).
    await openConfirm(2)
    expect(screen.getByTestId('restart-daemon-workers').textContent).toBe('2 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）')
    expect(screen.getByText('這台主機上的終端連線會中斷幾秒，之後自動接回；tmux session 不受影響。')).toBeTruthy()
  })

  it('no worker line with zero running workers', async () => {
    await openConfirm(0)
    expect(screen.queryByTestId('restart-daemon-workers')).toBeNull()
  })

  it('unknown worker count → the cautious line', async () => {
    await openConfirm(null)
    expect(screen.getByTestId('restart-daemon-workers').textContent).toContain('無法確認是否有 worker 正在執行')
  })

  it('confirm → store.restart(hostId, name), dialog closes', async () => {
    await openConfirm(0)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon-confirm-confirm')) })
    expect(restart).toHaveBeenCalledWith('h1', expect.any(String))
    expect(screen.queryByTestId('restart-daemon-confirm-dialog')).toBeNull()
  })

  it('cancel → no restart', async () => {
    await openConfirm(0)
    fireEvent.click(screen.getByTestId('restart-daemon-confirm-cancel'))
    expect(restart).not.toHaveBeenCalled()
  })

  it('while the host restarts: spinner text, disabled', () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    render(<RestartDaemonButton hostId="h1" />)
    const btn = screen.getByTestId('restart-daemon') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    expect(btn.textContent).toContain('重啟中…')
  })

  it('another host restarting does not disable this one', () => {
    useDaemonRestartStore.setState({ restarting: { h2: true } })
    render(<RestartDaemonButton hostId="h1" />)
    expect((screen.getByTestId('restart-daemon') as HTMLButtonElement).disabled).toBe(false)
  })

  it('custom label', () => {
    render(<RestartDaemonButton hostId="h1" label="立即重啟" testId="nex-restart-now" />)
    expect(screen.getByTestId('nex-restart-now').textContent).toBe('立即重啟')
  })

  it('unmount while counting does not open a dialog or throw', async () => {
    let resolve!: (n: number) => void
    vi.mocked(restartLib.countRunningWorkers).mockReturnValueOnce(new Promise((r) => { resolve = r }))
    const { unmount } = render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    unmount()
    await act(async () => { resolve(1) })
    await waitFor(() => expect(screen.queryByTestId('restart-daemon-confirm-dialog')).toBeNull())
  })
})
```

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/components/hosts/RestartDaemonButton.test.tsx`
  Expected: FAIL (module not found).

- [ ] **Step 3: Implement** `RestartDaemonButton.tsx`:

```tsx
// spa/src/components/hosts/RestartDaemonButton.tsx — the restart entry point
// rendered by the host page (R1), the Development page (R2) and the Nex
// config screen (R3) (daemon restart spec §3.2). Click → count running
// workers → confirm → useDaemonRestartStore.restart. While that host
// restarts, every instance for it shows the spinner and is disabled.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useDaemonRestartStore } from '../../stores/useDaemonRestartStore'
import { countRunningWorkers } from '../../lib/daemon-restart'
import { hostLabel, useHostLook } from '../../lib/host-look'
import { ConfirmDialog } from '../ConfirmDialog'

interface Props {
  hostId: string
  label?: string
  testId?: string
  className?: string
}

const btnClass = 'px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default inline-flex items-center gap-1'

export function RestartDaemonButton({ hostId, label, testId = 'restart-daemon', className }: Props) {
  const t = useI18nStore((s) => s.t)
  const name = hostLabel(hostId, useHostLook(hostId))
  const restarting = useDaemonRestartStore((s) => s.restarting[hostId] === true)
  const restart = useDaemonRestartStore((s) => s.restart)
  const [counting, setCounting] = useState(false)
  const [confirm, setConfirm] = useState<{ workers: number | null } | null>(null)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const open = async () => {
    setCounting(true)
    const workers = await countRunningWorkers(hostId)
    if (!mounted.current) return
    setCounting(false)
    setConfirm({ workers })
  }

  return (
    <>
      <button type="button" data-testid={testId} disabled={restarting || counting} onClick={() => void open()} className={className ?? btnClass}>
        {restarting
          ? <><ArrowsClockwise size={12} className="animate-spin" />{t('hosts.restart.restarting')}</>
          : (label ?? t('hosts.restart.button'))}
      </button>
      {confirm && (
        <ConfirmDialog
          testIdPrefix={`${testId}-confirm`}
          title={t('hosts.restart.confirm_title', { host: name })}
          body={t('hosts.restart.confirm_body')}
          confirmLabel={t('hosts.restart.confirm')}
          onCancel={() => setConfirm(null)}
          onConfirm={() => { setConfirm(null); void restart(hostId, name) }}
        >
          {confirm.workers !== 0 && (
            <p data-testid={`${testId}-workers`} className="mt-2 text-xs text-amber-400">
              {confirm.workers === null
                ? t('hosts.restart.confirm_workers_unknown')
                : t('hosts.restart.confirm_workers', { count: confirm.workers })}
            </p>
          )}
        </ConfirmDialog>
      )}
    </>
  )
}
```

Before writing, check that `t()` interpolates numbers (`{ count: 2 }`) the way it does strings. If it accepts only strings, pass `String(confirm.workers)`. Add the eight i18n keys from the table above to both locales.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/components/hosts/RestartDaemonButton.test.tsx src/locales/locale-completeness.test.ts`
  Expected: PASS.

- [ ] **Step 5: Mutation check (deliverable).**
  - Replace `confirm.workers !== 0 && (` with `false && (`, and separately make `open` always set `{ workers: 0 }`.
  - Run the file. "confirm names running workers when there are some" must FAIL each time.
  - Revert. Record both red outputs for the PR body.

- [ ] **Step 6: Commit.**
  `git add spa/src/components/hosts/RestartDaemonButton.tsx spa/src/components/hosts/RestartDaemonButton.test.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json`
  Then: `git commit -m "feat(spa): RestartDaemonButton — confirm with running-worker count, per-host spinner"`

### Task 8: R1 — host page (Overview → Daemon 設定)

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx`. The Daemon Config section is at ~line 279, and the info fetch effect is at ~line 56.
- Modify: `spa/src/components/hosts/OverviewSection.test.tsx`

**Interfaces:**
- Consumes: `RestartDaemonButton` (Task 7) and `useDaemonRestartStore` (`restarting`, `settled`; Task 6).
- Produces: nothing new.

- [ ] **Step 1: Write the failing tests** in `OverviewSection.test.tsx`. Read its existing setup first: how it seeds the host, the runtime status and the `host-api` mocks. Then add:

```tsx
describe('restart daemon (R1)', () => {
  it('shows the restart button for a connected host', async () => {
    // seed host h1 with runtime status 'connected' the way the file's other tests do
    renderOverview('h1', { status: 'connected' })
    expect(await screen.findByTestId('restart-daemon')).toBeTruthy()
  })

  it('no button for a disconnected host', async () => {
    renderOverview('h1', { status: 'disconnected' })
    await screen.findByText(/Daemon 設定|Daemon config/)
    expect(screen.queryByTestId('restart-daemon')).toBeNull()
  })

  it('stays (spinning) while its restart runs even if the host drops to reconnecting', async () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    renderOverview('h1', { status: 'reconnecting' })
    expect((await screen.findByTestId('restart-daemon') as HTMLButtonElement).disabled).toBe(true)
  })

  it('re-reads /api/info after a restart settles', async () => {
    renderOverview('h1', { status: 'connected' })
    const before = vi.mocked(hostApi.fetchInfo).mock.calls.length
    act(() => useDaemonRestartStore.setState({ settled: { h1: 1 } }))
    await waitFor(() => expect(vi.mocked(hostApi.fetchInfo).mock.calls.length).toBe(before + 1))
  })
})
```

`renderOverview` is shorthand. Use whatever the file's existing helper or setup is, and keep the four behaviours.

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.test.tsx`
  Expected: the new tests FAIL.

- [ ] **Step 3: Implement.** In `OverviewSection.tsx`, import `RestartDaemonButton` and `useDaemonRestartStore`, and add:

```tsx
  const restarting = useDaemonRestartStore((s) => s.restarting[hostId] === true)
  const settled = useDaemonRestartStore((s) => s.settled[hostId] ?? 0)
```

Add `settled` to the dependency array of the info/config fetch effect, so it becomes `[hostId, settled]`. A restart may have swapped in a new binary, which changes `purdex_version`.

At the end of the Daemon Config `<Section>` (after the `config ? … : …` block, still inside the Section), add:

```tsx
        {(runtime?.status === 'connected' || restarting) && (
          <div className="mt-3">
            <RestartDaemonButton hostId={hostId} />
          </div>
        )}
```

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.test.tsx`
  Expected: PASS, including all pre-existing tests.

- [ ] **Step 5: Commit.**
  `git add spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/OverviewSection.test.tsx`
  Then: `git commit -m "feat(spa): R1 — restart daemon on the host page"`

### Task 9: R3 — Nex config "立即重啟"

**Files:**
- Modify: `spa/src/components/hosts/nex/NexConfigForm.tsx`. The hint is at ~lines 169-173.
- Modify: `spa/src/components/hosts/nex/NexConfigForm.test.tsx`

**Interfaces:**
- Consumes: `RestartDaemonButton` (Task 7).
- Produces: test id `nex-restart-now`.

- [ ] **Step 1: Write the failing tests** in `NexConfigForm.test.tsx`. Follow the file's existing render helper and the `info` fixtures.

```tsx
describe('restart now (R3)', () => {
  it('appears next to the hint only when restart_required', () => {
    renderForm({ info: { ...readyInfo, restart_required: true } })
    const hint = screen.getByTestId('nex-restart-required')
    expect(within(hint).getByTestId('nex-restart-now').textContent).toBe('立即重啟')
  })
  it('absent without restart_required', () => {
    renderForm({ info: { ...readyInfo, restart_required: false } })
    expect(screen.queryByTestId('nex-restart-now')).toBeNull()
  })
  it('disabled while this host restarts', () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    renderForm({ hostId: 'h1', info: { ...readyInfo, restart_required: true } })
    expect((screen.getByTestId('nex-restart-now') as HTMLButtonElement).disabled).toBe(true)
  })
})
```

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/components/hosts/nex/NexConfigForm.test.tsx`
  Expected: the new tests FAIL.

- [ ] **Step 3: Implement.** Replace the hint block with:

```tsx
      {needsRestart && (
        <div data-testid="nex-restart-required" className="flex items-center justify-between gap-2 text-xs text-amber-400 bg-amber-500/10 rounded p-2 mb-3">
          <span>{t('hosts.nex.config.restart_required', { host: hostName })}</span>
          <RestartDaemonButton hostId={hostId} label={t('hosts.restart.button_now')} testId="nex-restart-now" />
        </div>
      )}
```

Update the file's header comment. It currently says a `pdx stop && pdx start` is owed. It should now say the hint offers "立即重啟", with `pdx stop && pdx start` as the manual fallback. The hint itself goes away when the store's `invalidate` re-reads `/api/info` after a successful restart (Task 6).

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/components/hosts/nex/`
  Expected: PASS.

- [ ] **Step 5: Commit.**
  `git add spa/src/components/hosts/nex/NexConfigForm.tsx spa/src/components/hosts/nex/NexConfigForm.test.tsx`
  Then: `git commit -m "feat(spa): R3 — restart now next to the Nex restart_required hint"`

### Task 10: R2 — Development page, local daemon

**Files:**
- Modify: `spa/src/components/settings/LocalDaemonSection.tsx`. Touch `showRestart` (~line 125), the button row (~line 249) and the refresh effect (~line 77).
- Modify: `spa/src/components/settings/LocalDaemonSection.test.tsx`

**Interfaces:**
- Consumes: `RestartDaemonButton` (Task 7) and `useDaemonRestartStore` (Task 6).
- Produces: test id `local-daemon-restart`.

Rules (spec §3.2 R2, D9, D10):
- **managed + alive + in the host list** → `<RestartDaemonButton hostId={registeredAs.id} label={t('settings.dev.local.btn.restart')} testId="local-daemon-restart" />`. It shows whether or not `restartPending` or `updateAvailable` hold. The Update button and the `restartPending` text are unchanged.
- **managed + alive + not in the host list** → today's direct button: `run('restart', () => api.localDaemonRestart?.())`, with no confirm.
- **external + a configured host at the same bind:port** → `<RestartDaemonButton hostId={registeredAs.id} … />`. `restartDaemon` picks the API path by itself, because `isManagedLocal` is false.
- **external without such a host** → no button. The existing external reason stays.
- While `registeredAs` is restarting, the section's other buttons are disabled too.
- When `settled[registeredAs.id]` changes, call `refresh()`.

- [ ] **Step 1: Write the failing tests** in `LocalDaemonSection.test.tsx`, reusing `status()`, `renderIt()` and the mocks at the top of the file.

```tsx
describe('restart (R2)', () => {
  const managedAlive = (o: Partial<ElectronLocalDaemonStatus> = {}) => status({
    managed: 'managed', alive: { pid: 1 },
    installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' },
    running: { version: '9', hash: 'bbb', url: 'http://100.64.0.9:7860' },
    config: { bind: '100.64.0.9', port: 7860, token: 'tok' }, ...o,
  })
  const addLocalHost = () => useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'tok', hostname: 'air-2026' })

  it('managed + alive + up to date → restart shown (no longer only when restartPending)', async () => {
    addLocalHost()
    mockStatus.mockResolvedValue(managedAlive())
    await renderIt('bbb')
    expect(screen.getByTestId('local-daemon-restart')).toBeTruthy()
  })

  it('managed + alive + update available → restart AND update both shown', async () => {
    addLocalHost()
    mockStatus.mockResolvedValue(managedAlive())
    await renderIt('ccc')
    expect(screen.getByTestId('local-daemon-restart')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Update' })).toBeTruthy()
  })

  it('managed + not alive → no restart', async () => {
    addLocalHost()
    mockStatus.mockResolvedValue(managedAlive({ alive: null, running: null }))
    await renderIt('bbb')
    expect(screen.queryByTestId('local-daemon-restart')).toBeNull()
  })

  it('managed + alive + not in host list → the direct IPC restart, no confirm', async () => {
    mockStatus.mockResolvedValue(managedAlive())
    mockRestart.mockResolvedValue(result)
    await renderIt('bbb')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Restart' })) })
    expect(mockRestart).toHaveBeenCalled()
  })

  it('external + configured host at its bind:port → restart via the shared button', async () => {
    addLocalHost()
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'not started by the app', config: { bind: '100.64.0.9', port: 7860, token: 'tok' } }))
    await renderIt('bbb')
    expect(screen.getByTestId('local-daemon-restart')).toBeTruthy()
  })

  it('external without a configured host → no restart, external reason kept', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'not started by the app', config: { bind: '100.64.0.9', port: 7860, token: 'tok' } }))
    await renderIt('bbb')
    expect(screen.queryByTestId('local-daemon-restart')).toBeNull()
    expect(screen.getByText(/not started by the app/)).toBeTruthy()
  })

  it('other buttons disabled while the local host restarts; status re-read when it settles', async () => {
    const id = addLocalHost()
    mockStatus.mockResolvedValue(managedAlive())
    await renderIt('bbb')
    act(() => useDaemonRestartStore.setState({ restarting: { [id]: true } }))
    expect((screen.getByRole('button', { name: 'Refresh' }) as HTMLButtonElement).disabled).toBe(true)
    const calls = mockStatus.mock.calls.length
    await act(async () => useDaemonRestartStore.setState({ restarting: {}, settled: { [id]: 1 } }))
    expect(mockStatus.mock.calls.length).toBe(calls + 1)
  })
})
```

Some existing tests assert the old "never both" rule. One example is "managed+running, same hash → Up to date", which may implicitly assert that no Restart is shown. Update those tests to the new rule, and say so in the commit body. Do not delete their other assertions. Add `useDaemonRestartStore.setState({ restarting: {}, settled: {} })` to `beforeEach`.

- [ ] **Step 2: Run them and confirm they fail.**
  Run: `cd spa && npx vitest run src/components/settings/LocalDaemonSection.test.tsx`
  Expected: the new tests FAIL.

- [ ] **Step 3: Implement.** In `LocalDaemonSection.tsx`:

```tsx
  const restartingLocal = useDaemonRestartStore((s) => (registeredAs ? s.restarting[registeredAs.id] === true : false))
  const settledLocal = useDaemonRestartStore((s) => (registeredAs ? s.settled[registeredAs.id] ?? 0 : 0))
```

- Change the refresh effect to `useEffect(() => { void refresh() }, [refresh, refreshKey, settledLocal])`.
- Change `disabled` to `busy !== null || restartingLocal`.
- Replace `showRestart` with:

```tsx
  // Spec 2026-10-06 §3.2 R2: managed + alive always offers restart (Update may show beside it).
  const showRestart = !!alive
```

In the managed button group, replace the `showRestart &&` button with:

```tsx
            {showRestart && (registeredAs
              ? <RestartDaemonButton hostId={registeredAs.id} label={t('settings.dev.local.btn.restart')} testId="local-daemon-restart" className={btnSecondary} />
              : <button onClick={() => void run('restart', () => api.localDaemonRestart?.())} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.restart')}</button>)}
```

After the managed group, add the external case:

```tsx
        {status?.managed === 'external' && registeredAs && (
          <RestartDaemonButton hostId={registeredAs.id} label={t('settings.dev.local.btn.restart')} testId="local-daemon-restart" className={btnSecondary} />
        )}
```

Update the component's header comment. It should no longer say "Update, else Restart — never both"; it now says restart is offered whenever the daemon is alive.

- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `cd spa && npx vitest run src/components/settings/`
  Expected: PASS.

- [ ] **Step 5: Commit.**
  `git add spa/src/components/settings/LocalDaemonSection.tsx spa/src/components/settings/LocalDaemonSection.test.tsx`
  Then: `git commit -m "feat(spa): R2 — local daemon restart whenever alive (managed) or registered (external)"`

**Phase C gate:**
- `cd spa && npx vitest run && pnpm run lint && npx tsc --noEmit -p tsconfig.app.json && pnpm run build`
- Open the PR, then run codex R1 + R2.
- **Real acceptance** (spec §4): restart mlab's daemon from the host page. Check that:
  - terminals reconnect;
  - `cat ~/.config/pdx/pdx.pid` is unchanged;
  - dev mode is still on;
  - `/api/health` has a new `boot_id`.

  This interrupts running workers on mlab. **Report to the coordinator (`mlab/_0le0d2`) first and wait for the slot.** Deploying the Phase A daemon binary to mlab is part of the same slot. The user checks Air26's App-managed path.

After all three PRs merge: one bump PR (`VERSION`, `package.json`, `spa/package.json`, `CHANGELOG.md`; fetch `origin/main`'s VERSION first). No codex review for the bump.
