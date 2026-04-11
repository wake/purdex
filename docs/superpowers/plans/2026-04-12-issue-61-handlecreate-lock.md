# Plan — Issue #61: handleCreate Concurrency Lock

## Files

1. `internal/module/session/module.go` — add `createMu sync.Mutex` + import `sync`
2. `internal/module/session/handler.go` — take lock in `handleCreate` critical section
3. `internal/module/session/handler_test.go` — new test `TestHandlerCreateSessionConcurrentSameName`

## Steps

### Step 1 (TDD RED)

Append to `handler_test.go`:

```go
// TestHandlerCreateSessionConcurrentSameName asserts that N concurrent POSTs
// with the same session name result in exactly one 201 and N-1 409s, with
// no duplicate entry in the underlying store. Without createMu, the
// HasSession→NewSession window can let two creates slip through.
func TestHandlerCreateSessionConcurrentSameName(t *testing.T) {
    mod, _, _ := newTestModule(t)
    mux := http.NewServeMux()
    mod.RegisterRoutes(mux)

    const N = 50
    start := make(chan struct{})
    var wg sync.WaitGroup
    codes := make([]int, N)
    bodies := make([]string, N)

    for i := 0; i < N; i++ {
        i := i
        wg.Add(1)
        go func() {
            defer wg.Done()
            <-start // release all goroutines simultaneously
            req := httptest.NewRequest(
                http.MethodPost, "/api/sessions",
                strings.NewReader(`{"name":"dup","cwd":"/tmp"}`),
            )
            req.Header.Set("Content-Type", "application/json")
            w := httptest.NewRecorder()
            mux.ServeHTTP(w, req)
            codes[i] = w.Code
            bodies[i] = w.Body.String()
        }()
    }
    close(start)
    wg.Wait()

    var created, conflict, other int
    for i, c := range codes {
        switch c {
        case http.StatusCreated:
            created++
        case http.StatusConflict:
            conflict++
        default:
            other++
            t.Logf("unexpected status %d body=%q", c, bodies[i])
        }
    }
    assert.Equal(t, 1, created, "exactly one request should succeed")
    assert.Equal(t, N-1, conflict, "other requests should return 409")
    assert.Equal(t, 0, other, "no unexpected statuses")

    // Underlying store must contain exactly one session named "dup"
    sessions, err := mod.ListSessions()
    require.NoError(t, err)
    assert.Len(t, sessions, 1, "no duplicate session should exist in store")
    if len(sessions) == 1 {
        assert.Equal(t, "dup", sessions[0].Name)
    }
}
```

Required imports (if not present): `sync`.

Run: `go test ./internal/module/session -run TestHandlerCreateSessionConcurrentSameName -race -count=10`
Expect: **FAIL** on at least one of the 10 runs (race window before fix).

**Why deterministic-ish:** FakeExecutor's `sync.Mutex` protects each individual op but *not* the handler-level TOCTOU between `HasSession` and `NewSession`. With N=50 + simultaneous release, two goroutines will routinely interleave past `HasSession`, causing `FakeExecutor.NewSession` to append a duplicate `sessionOrder` entry. The resulting `ListSessions` length >= 2 is a **deterministic logic failure** not dependent on `-race`. `-race` is added mainly to catch unexpected shared-state mutations.

### Step 2 (TDD GREEN)

**`module.go`**:
- Add `"sync"` import
- Add `createMu sync.Mutex` field to `SessionModule` struct (place near other mutable state)

**`handler.go`**:
- At the top of `handleCreate`, after all input validation (JSON decode, name regex, cwd default, mode validation) but before `HasSession`, insert:
  ```go
  m.createMu.Lock()
  defer m.createMu.Unlock()
  ```
- No other changes to the handler body.

**Why hold the lock across tmux roundtrips:** `HasSession`, `NewSession`, `ListSessions` each shell out (~few ms each); in production the whole critical section is ~10-30ms. This would matter if create were hot, but it's a human-driven operation (< 1 Hz), so correctness wins over throughput. If creation volume grows, revisit with per-name keyed locks.

Run: `go test ./internal/module/session -run TestHandlerCreateSessionConcurrentSameName -race -count=20`
Expect: **PASS** all 20 runs.

### Step 3 — Regression

`go test ./internal/module/session/... -race` — all existing tests must pass.
`go test ./... -race` — full tree check.

### Step 4 — Format + vet

`gofmt -w internal/module/session/module.go internal/module/session/handler.go internal/module/session/handler_test.go`
`go vet ./internal/module/session/...`

### Step 5 — Commit + PR

```
git add internal/module/session/module.go internal/module/session/handler.go internal/module/session/handler_test.go docs/superpowers/specs/... docs/superpowers/plans/...
git commit -m "fix(session): guard handleCreate with mutex against same-name race (#61)"
git push -u origin worktree-issue-61-handlecreate-lock
gh pr create --title "fix(session): guard handleCreate with mutex (#61)" --body "..."
```

### Step 6 — Three-dimension review (parallel subagents)

- Attack: look for deadlock with other locks, test flakiness, missed field
- Defense: verify scope + idiomatic Go, invariants
- Size/responsibility: diff should be ~15 lines of production + test

### Step 7 — Merge + bump

- Apply high-confidence / low-complexity / test-related findings
- Squash merge → close #61
- Bump to `1.0.0-alpha.84`, update CHANGELOG, push main

### Follow-up note (not for this PR)

`handleRename` has a similar TOCTOU shape: it checks whether the target name exists then renames, without a lock. Same low frequency + same fix pattern applies. If accepting, file a new issue rather than expanding this PR's scope.
