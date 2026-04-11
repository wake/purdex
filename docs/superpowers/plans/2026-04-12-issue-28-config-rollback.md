# Plan — Issue #28: handlePutConfig Rollback

## Target Files

1. `internal/core/config_handler.go` — add snapshot + rollback
2. `internal/core/config_handler_test.go` — add `TestPutConfigRollsBackOnWriteFailure`

## Implementation Steps

### Step 1 (TDD RED) — Add failing test

In `internal/core/config_handler_test.go`, append:

```go
func TestPutConfigRollsBackOnWriteFailure(t *testing.T) {
    // Create a regular file to use as a "blocker" — any path under it
    // triggers ENOTDIR on write, portable across macOS/Linux/CI.
    tmpDir := t.TempDir()
    blocker := filepath.Join(tmpDir, "blocker")
    require.NoError(t, os.WriteFile(blocker, []byte("x"), 0644))

    c := newTestCore()
    c.CfgPath = filepath.Join(blocker, "config.toml") // parent is a file

    // Snapshot expected original state
    originalPresets := append([]config.Preset(nil), c.Cfg.Stream.Presets...)
    originalCCCommands := append([]string(nil), c.Cfg.Detect.CCCommands...)
    originalPollInterval := c.Cfg.Detect.PollInterval
    originalSizingMode := c.Cfg.Terminal.SizingMode
    originalCfgPtr := c.Cfg

    var callbackCalled int
    c.OnConfigChange(func() { callbackCalled++ })

    body := `{
      "stream":{"presets":[{"name":"new","command":"x"}]},
      "detect":{"cc_commands":["aider"],"poll_interval":99},
      "terminal":{"sizing_mode":"terminal-first"}
    }`
    req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body))
    rec := httptest.NewRecorder()
    c.handlePutConfig(rec, req)

    // 1. Status 500
    assert.Equal(t, http.StatusInternalServerError, rec.Code)
    assert.Contains(t, rec.Body.String(), "failed to save config")

    // 2. In-memory state fully rolled back
    c.CfgMu.RLock()
    assert.Equal(t, originalPresets, c.Cfg.Stream.Presets, "Stream.Presets should be rolled back")
    assert.Equal(t, originalCCCommands, c.Cfg.Detect.CCCommands, "Detect.CCCommands should be rolled back")
    assert.Equal(t, originalPollInterval, c.Cfg.Detect.PollInterval, "Detect.PollInterval should be rolled back")
    assert.Equal(t, originalSizingMode, c.Cfg.Terminal.SizingMode, "Terminal.SizingMode should be rolled back")
    c.CfgMu.RUnlock()

    // 3. Callback not called (no actual change)
    assert.Equal(t, 0, callbackCalled, "OnConfigChange must not fire on rollback")

    // 4. Pointer identity preserved (other goroutines hold c.Cfg)
    assert.Same(t, originalCfgPtr, c.Cfg, "c.Cfg pointer must not be swapped")

    // 5. Recovery: a successful PUT after rollback must still work
    tmpDir2 := t.TempDir()
    c.CfgPath = filepath.Join(tmpDir2, "config.toml")
    body2 := `{"detect":{"cc_commands":["aider"]}}`
    req2 := httptest.NewRequest("PUT", "/api/config", strings.NewReader(body2))
    rec2 := httptest.NewRecorder()
    c.handlePutConfig(rec2, req2)
    assert.Equal(t, http.StatusOK, rec2.Code)
    c.CfgMu.RLock()
    assert.Equal(t, []string{"aider"}, c.Cfg.Detect.CCCommands)
    c.CfgMu.RUnlock()
}
```

Run: `cd .../issue-28-config-rollback && go test ./internal/core -run TestPutConfigRollsBackOnWriteFailure` → expect **FAIL** (because rollback not yet implemented).

### Step 2 (TDD GREEN) — Implement rollback

Edit `internal/core/config_handler.go`. Place snapshot **after** the `sizing_mode` validation (validation early-returns should not waste a snapshot). Replace the current mutate + write block:

```go
// Snapshot BEFORE any mutation for rollback on writeConfig failure.
// Shallow copy is sufficient because all mutations below replace slice
// headers wholesale (never mutate in place); if a future field adds
// in-place mutation (append/map update), rollback will silently break.
snapshot := *c.Cfg

detectChanged := false

if req.Stream != nil {
    c.Cfg.Stream = *req.Stream
}
if req.Detect != nil {
    if req.Detect.CCCommands != nil {
        c.Cfg.Detect.CCCommands = *req.Detect.CCCommands
        detectChanged = true
    }
    if req.Detect.PollInterval != nil && *req.Detect.PollInterval > 0 {
        c.Cfg.Detect.PollInterval = *req.Detect.PollInterval
        detectChanged = true
    }
}
if req.Terminal != nil && req.Terminal.SizingMode != "" {
    c.Cfg.Terminal.SizingMode = req.Terminal.SizingMode
}

// Write back to config file
if c.CfgPath != "" {
    if err := config.WriteFile(c.CfgPath, *c.Cfg); err != nil {
        *c.Cfg = snapshot // rollback in-memory state on write failure
        c.CfgMu.Unlock()
        http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
        return
    }
}
```

Key notes:
- `snapshot` taken **before** any mutation
- Rollback uses `*c.Cfg = snapshot` (not `c.Cfg = &snapshot`) to preserve pointer identity
- `detectChanged` still only affects callback path, which is outside the lock and unchanged

Run: `go test ./internal/core -run TestPutConfigRollsBackOnWriteFailure` → expect **PASS**.

### Step 3 — Regression check

Run package tests: `go test ./internal/core/...` → all existing tests must pass. Then run full module: `go test ./...` since this is a hot handler path.

### Step 4 — Lint + vet

```
gofmt -w internal/core/config_handler.go internal/core/config_handler_test.go
go vet ./internal/core/...
```

### Step 5 — Commit & push

```
git add internal/core/config_handler.go internal/core/config_handler_test.go docs/superpowers/specs/2026-04-12-issue-28-config-rollback.md docs/superpowers/plans/2026-04-12-issue-28-config-rollback.md
git commit -m "fix: rollback in-memory config on writeConfig failure (#28)"
git push -u origin worktree-issue-28-config-rollback
```

### Step 6 — Open PR

```
gh pr create --title "fix: rollback in-memory config on writeConfig failure (#28)" \
  --body "Closes #28. Snapshots c.Cfg before mutating, restores on writeConfig error..."
```

### Step 7 — Three-dimension review

Run in parallel:
- Attack agent — look for race conditions, missed fields, aliasing bugs
- Defense agent — verify invariants hold, architecture consistency
- File-size / responsibility agent — check the diff is focused

### Step 8 — Merge + close + bump

- Address high-confidence / low-complexity / test-related findings
- Merge PR
- Bump `VERSION` + `package.json` + `spa/package.json` to next alpha
- Update `CHANGELOG.md`
- Close issue #28 (via "Closes #28" in PR body)

## Out of Scope

- Refactoring `config.WriteFile` atomicity
- Other handlers' rollback patterns
