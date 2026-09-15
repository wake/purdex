# P-B.3 — Host "Nex" page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each host a **Nex** sub-page that shows whether the embedded Nexen engine is running (and why not), lets the user edit `[nex]` (persisted, restart to apply), and lists that host's executions with open/terminate/archive — and make the daemon survive a broken `[nex]`.

**Architecture:** Daemon side, three contained changes: the nex module's `Init` records its error instead of killing the daemon and mounts a 503 fallback; `/api/info.nex` grows `ready`/`init_error`/`effective` via a small `core.StatusReporter` seam; `PUT /api/config` accepts a full `nex` object after `NexConfig.Validate`. SPA side, one host built-in sub-page (`localId: 'nex'`) composed of three independent cards, each with its own data source and tests, sharing the P-B.1 client (`spa/src/lib/nex/*`) and P-B.2's `openSingletonTab({kind:'execution', …, host})`.

**Tech Stack:** Go 1.26 (net/http, testify) / React 19 / Zustand 5 / Vitest + Testing Library / Phosphor icons.

**Spec:** `docs/specs/2026-09-15-pb-execution-pane-spec.md` §4.4 (all), §4.5 rows "Host has no nex" and "503 nex_unavailable", §5 I8, I9, I10 (table half), §6 acceptance 1–3, 8.

## Global Constraints

- nex `Init` failure is **not fatal**: `initErr` recorded, `m.sys` left zero, every `/api/nex/…` answers `503 {"error": "<initErr>", "code": "nex_unavailable"}`; `Start`/`Stop`/`Close` are no-ops in that state (I8). `NexConfig.Validate` errors stay fatal at config load (unchanged).
- `/api/info.nex` = `{configured, mounted, ready, init_error, effective}`; `effective` is the expanded config as passed to `nexen.Options` (`claude_bin` is the configured string, `""` when Nexen resolves it lazily), `null` when not mounted. `core` must not import `internal/module/nex` (cycle) — use an interface.
- `PUT /api/config {"nex": {...}}` validates with `NexConfig.Validate(home)` → 400 naming the key; persists on success; **nothing applied live** (I9). A `nex: null` body is 400 (`nex must be an object`).
- SPA: every `/api/nex/…` call goes through `spa/src/lib/nex/nex-api.ts` (Bearer + `X-Pdx-Client`); the site-wide SSE (`/api/nex/v1/events`, no `execution_id`) is a refresh signal only — frame contents are never applied.
- Host sub-page id `nex`, order 6, label key `hosts.nex`; new strings in both `spa/src/locales/en.json` and `zh-TW.json` (`{{name}}` interpolation).
- Sandbox profile choices are the constant list `['', 'readonly', 'standard', 'trusted', 'handoff']` (mirrors `sandbox.ValidName` in Nexen; capabilities only lists *clamped* profiles).
- Go: `go test ./internal/module/nex/ ./internal/core/` from the worktree; `go build ./...`; gofmt. SPA: `cd spa && npx vitest run <file>`, `pnpm run lint`, `pnpm run build`.
- One commit per task, `git commit --only <files>`; English code/comments; files ≤ ~300 lines; tests next to the file.

---

## File map

| File | Responsibility |
|---|---|
| `internal/module/nex/module.go` | soft-fail Init, fallback handler, `Status()` |
| `internal/module/nex/status.go` | `Status()` map builder (kept out of module.go) |
| `internal/core/core.go` | `StatusReporter` interface + `ModuleStatus(name)` |
| `internal/core/info_handler.go` | extended `nex` object |
| `internal/core/config_handler.go` | `nex` accepted + validated |
| `spa/src/lib/host-api.ts` | `NexConfig`, `NexEffective`, `NexInfo` types; `ConfigData.nex?`; `HostInfo.nex?` (in `useHostStore.ts`) |
| `spa/src/components/hosts/nex/NexEngineStatus.tsx` | status card |
| `spa/src/components/hosts/nex/NexConfigForm.tsx` | config card + restart-required notice |
| `spa/src/components/hosts/nex/nex-config-diff.ts` | `restartRequired(config.nex, info.nex.effective)` pure helper |
| `spa/src/components/hosts/nex/NexExecutionsTable.tsx` | executions card + site-wide SSE refresh |
| `spa/src/components/hosts/nex/NexHostSection.tsx` | composes the three cards; registered as built-in host sub-page |
| `spa/src/lib/register-modules/index.tsx` | `setHostBuiltinSections` gains `nex` |

---

### Task 1 (Go): nex `Init` soft-fail + 503 fallback + `Status()`

**Files:**
- Modify: `internal/module/nex/module.go` (`Module` struct, `Init`, `RegisterRoutes`, `Start`, `Stop`, `Close`)
- Create: `internal/module/nex/status.go`
- Test: `internal/module/nex/module_test.go` (add), `internal/module/nex/status_test.go` (new)

**Interfaces:**
- Produces:

```go
// module.go
type Module struct { …existing…; initErr error; expanded pdxconfig.NexConfig }
func (m *Module) Status() map[string]any   // {"ready": bool, "init_error": string, "effective": map|nil}
// status.go
func buildStatus(initErr error, opts nexen.Options, expanded pdxconfig.NexConfig, pathPrefix string, assembled bool) map[string]any
```

`effective` keys (all strings/lists, JSON-friendly): `data_dir`, `claude_bin`, `cswap_bin`, `max_profile`, `default_profile`, `repo_roots`, `service_roots`, `path_prefix`, `lease_ttl`, `interrupt`, `turn` (durations as `time.Duration.String()` of `opts.Config.*`).

- [ ] **Step 1: Failing tests**

Append to `module_test.go` (reuse `newTestCore`, `newFakeAssemble`, `noopEngine`, `fakeAssembleRecord` already in the file; look at `TestInitHandsBuildOptionsToAssembleAndCreatesDataDir` ~line 203 for how a valid config is built):

```go
// TestInitAssembleFailureIsSoft is spec I8: an engine that fails to assemble
// leaves the daemon alive — Init returns nil, the module records the error,
// every /api/nex path answers 503 nex_unavailable, and Start/Stop/Close are
// no-ops.
func TestInitAssembleFailureIsSoft(t *testing.T) {
	cfg := validNexConfig(t) // helper: Enabled=true, one repo root under t.TempDir(), DataDir under t.TempDir(); write it if the file has no equivalent
	c := newTestCore(cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom: store locked"))

	require.NoError(t, m.Init(c), "assemble failure must not fail Init")
	require.Error(t, m.initErr)
	assert.Contains(t, m.initErr.Error(), "nex: init: assembling engine: boom: store locked")

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, path := range []string{"/api/nex/v1/capabilities", "/api/nex/v1/executions", "/api/nex/v1/events?execution_id=x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, path)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"), path)
		var body map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), path)
		assert.Equal(t, "nex_unavailable", body["code"], path)
		assert.Contains(t, body["error"], "boom: store locked", path)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/nex/v1/executions", strings.NewReader("{}")))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	assert.NoError(t, m.Start(context.Background()))
	assert.NoError(t, m.Stop(context.Background()))
	assert.NoError(t, m.Close())

	st := m.Status()
	assert.Equal(t, false, st["ready"])
	assert.Contains(t, st["init_error"], "boom: store locked")
	assert.Nil(t, st["effective"])
}

// TestInitDataDirFailureIsSoft: the data_dir mkdir path takes the same route.
func TestInitDataDirFailureIsSoft(t *testing.T) {
	cfg := validNexConfig(t)
	blocker := filepath.Join(t.TempDir(), "file-not-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	cfg.DataDir = blocker // <DataDir>/nex cannot be created under a regular file
	c := newTestCore(cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	require.NoError(t, m.Init(c))
	require.Error(t, m.initErr)
	assert.Contains(t, m.initErr.Error(), "creating data_dir")
}

// TestInitValidateFailureStaysFatal pins the division of labour: a static
// shape error is still an Init error (config load already rejects it; this
// is the belt to that braces).
func TestInitValidateFailureStaysFatal(t *testing.T) {
	cfg := validNexConfig(t)
	cfg.Nex.RepoRoots = nil
	cfg.Nex.ServiceRoots = nil
	c := newTestCore(cfg)
	m := New()
	m.logf = func(string, ...any) {}
	err := m.Init(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one root required")
}

// TestStatusReadyReportsEffective: a healthy Init reports the expanded config.
func TestStatusReadyReportsEffective(t *testing.T) {
	cfg := validNexConfig(t)
	cfg.Nex.ClaudeBin = "" // lazy → reported as ""
	cfg.Nex.Sandbox.MaxProfile = "handoff"
	cfg.Nex.Timeouts.LeaseTTL = "90s"
	c := newTestCore(cfg)
	m := New()
	m.logf = func(string, ...any) {}
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	require.NoError(t, m.Init(c))
	st := m.Status()
	assert.Equal(t, true, st["ready"])
	assert.Equal(t, "", st["init_error"])
	eff, ok := st["effective"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(cfg.DataDir, "nex"), eff["data_dir"])
	assert.Equal(t, "", eff["claude_bin"])
	assert.Equal(t, "handoff", eff["max_profile"])
	assert.Equal(t, "1m30s", eff["lease_ttl"])
	assert.Equal(t, cfg.Nex.RepoRoots, eff["repo_roots"])
}
```

And in `TestInitRealAssembleFailures` (~line 503): the table currently asserts `err := m.Init(c)` contains substrings. Change the assertion to `require.NoError(t, m.Init(c)); require.Error(t, m.initErr); for _, sub := range tt.wantSubs { assert.Contains(t, m.initErr.Error(), sub) }` — those cases are exactly the environment errors soft-fail absorbs (keep `"nex: init:"` in the recorded error's prefix).

- [ ] **Step 2: Run to fail** — `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && go test ./internal/module/nex/ -run 'TestInit|TestStatus' 2>&1 | tail -20` → compile errors (`initErr`, `Status` undefined).

- [ ] **Step 3: Implement**

`module.go`:

```go
type Module struct {
	core       *core.Core
	sys        engine
	opts       nexen.Options
	expanded   pdxconfig.NexConfig // [nex] after "~" expansion; what Status reports as effective
	pathPrefix string
	// initErr is why the engine is not running (spec §4.4.1 soft-fail). Set
	// by Init for every failure past static validation; nil when the engine
	// assembled. RegisterRoutes mounts the 503 fallback when it is set.
	initErr error

	assemble assembleFn
	isDir    func(string) bool
	logf     func(string, ...any)
}
```

`Init`: keep the `Validate` error return as is (fatal). After that, replace each `return fmt.Errorf("nex: init: …")` (buildOptions, MkdirAll, assemble) with `return m.softFail(fmt.Errorf("nex: init: …"))`, and store `m.expanded = n` right after `Expanded(home)`:

```go
// softFail records why the engine is unavailable and reports success to the
// core: a broken [nex] must not take the terminal daemon down (spec §4.4.1,
// I8). The 503 fallback handler and Status() surface the error instead.
func (m *Module) softFail(err error) error {
	m.initErr = err
	m.sys = engine{}
	m.logf("nex: init failed (engine unavailable, /api/nex answers 503): %v", err)
	return nil
}
```

`RegisterRoutes`:

```go
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.initErr != nil {
		mux.Handle(RoutePrefix+"/", unavailableHandler(m.initErr))
		return
	}
	mux.Handle(RoutePrefix+"/", http.StripPrefix(RoutePrefix, recoverer(m.logf, m.sys.handler)))
}

// unavailableHandler answers every request under RoutePrefix with the same
// structured error shape Nexen uses, so one client-side parser covers both.
func unavailableHandler(initErr error) http.Handler {
	body, _ := json.Marshal(map[string]string{"error": initErr.Error(), "code": "nex_unavailable"})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(body)
	})
}
```

`Start`: `if m.initErr != nil { return nil }` first. `Stop`/`Close` already guard on nil funcs.

`status.go`:

```go
package nex

import (
	"lab.protype.tw/wake/nexen"
	pdxconfig "github.com/wake/purdex/internal/config"
)

// Status is what GET /api/info reports under "nex" beyond configured/mounted
// (spec §4.4.2): whether the engine is serving, why not, and the expanded
// config it was assembled with. Read by core through core.StatusReporter so
// core never imports this package.
func (m *Module) Status() map[string]any {
	return buildStatus(m.initErr, m.opts, m.expanded, m.pathPrefix, m.sys.handler != nil)
}

func buildStatus(initErr error, opts nexen.Options, expanded pdxconfig.NexConfig, pathPrefix string, assembled bool) map[string]any {
	st := map[string]any{"ready": initErr == nil && assembled, "init_error": ""}
	if initErr != nil {
		st["init_error"] = initErr.Error()
	}
	if !assembled || opts.Config == nil {
		st["effective"] = nil
		return st
	}
	cfg := opts.Config
	st["effective"] = map[string]any{
		"data_dir":        cfg.DataDir,
		"claude_bin":      opts.ClaudeBin, // "" = Nexen resolves lazily from PATH
		"cswap_bin":       cfg.CswapBin,
		"max_profile":     cfg.Sandbox.MaxProfile,
		"default_profile": cfg.Sandbox.DefaultProfile,
		"repo_roots":      expanded.RepoRoots,
		"service_roots":   expanded.ServiceRoots,
		"path_prefix":     pathPrefix,
		"lease_ttl":       cfg.LeaseTTL.String(),
		"interrupt":       cfg.InterruptTimeout.String(),
		"turn":            cfg.TurnTimeout.String(),
	}
	return st
}
```

> `nexconfig.Duration` — check whether it has a `String()` method (`grep -n "func (d Duration)" ~/Workspace/wake/nexen/config/*.go`); if not, use `time.Duration(cfg.LeaseTTL).String()`. `status_test.go`: one table test over `buildStatus` (nil vs error, assembled vs not, `ClaudeBin` empty vs set).

- [ ] **Step 4: Run to pass** — `go test ./internal/module/nex/ 2>&1 | tail -5 && gofmt -l internal/module/nex/` (no output from gofmt).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add internal/module/nex/module.go internal/module/nex/status.go internal/module/nex/module_test.go internal/module/nex/status_test.go && git commit --only internal/module/nex/module.go internal/module/nex/status.go internal/module/nex/module_test.go internal/module/nex/status_test.go -m "feat(daemon): nex init soft-fails with a 503 fallback and reports Status()"
```

---

### Task 2 (Go): `core.StatusReporter` + `/api/info.nex` extension

**Files:**
- Modify: `internal/core/core.go` (after `Mounted`), `internal/core/info_handler.go`
- Test: `internal/core/info_handler_test.go`

**Interfaces:**
- Produces:

```go
// core.go
// StatusReporter is the optional interface a module implements to publish
// runtime facts through GET /api/info (nex is the first).
type StatusReporter interface{ Status() map[string]any }
// ModuleStatus returns Status() of the named module, or nil,false when the
// module is not mounted or does not report.
func (c *Core) ModuleStatus(name string) (map[string]any, bool)
```

`/api/info.nex` becomes `{"configured", "mounted", "ready", "init_error", "effective"}`; when the module is mounted but not a `StatusReporter` (test stubs), `ready = mounted`, `init_error = ""`, `effective = nil`.

- [ ] **Step 1: Failing test** — add to `info_handler_test.go` next to `TestInfoEndpoint_NexConfiguredAndMounted`:

```go
type statusStubModule struct {
	stubModule
	status map[string]any
}

func (m *statusStubModule) Status() map[string]any { return m.status }

func TestInfoEndpoint_NexStatusFields(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	c.AddModule(&statusStubModule{
		stubModule: stubModule{name: "nex"},
		status: map[string]any{
			"ready": false, "init_error": "nex: init: assembling engine: boom",
			"effective": nil,
		},
	})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	nex := body["nex"].(map[string]any)
	assert.Equal(t, true, nex["configured"])
	assert.Equal(t, true, nex["mounted"])
	assert.Equal(t, false, nex["ready"])
	assert.Equal(t, "nex: init: assembling engine: boom", nex["init_error"])
	assert.Nil(t, nex["effective"])
}

func TestInfoEndpoint_NexMountedWithoutStatusReporter(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{Nex: config.NexConfig{Enabled: true}}})
	c.AddModule(&stubModule{name: "nex"})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	nex := body["nex"].(map[string]any)
	assert.Equal(t, true, nex["ready"])
	assert.Equal(t, "", nex["init_error"])
	assert.Nil(t, nex["effective"])
}
```

Also extend `TestInfoEndpoint_NexConfiguredButNotMounted` to assert `ready == false`, `init_error == ""`, `effective == nil`.

- [ ] **Step 2: Run to fail** — `go test ./internal/core/ -run TestInfoEndpoint 2>&1 | tail -10`.

- [ ] **Step 3: Implement**

`core.go`:

```go
// StatusReporter is the optional interface a module implements to publish
// runtime facts through GET /api/info. nex is the first: whether its engine
// is serving, why not, and the config it was assembled with (spec §4.4.2).
type StatusReporter interface{ Status() map[string]any }

// ModuleStatus returns the named module's Status(), or nil,false when the
// module is not mounted or does not implement StatusReporter.
func (c *Core) ModuleStatus(name string) (map[string]any, bool) {
	for _, m := range c.modules {
		if m.Name() != name {
			continue
		}
		if r, ok := m.(StatusReporter); ok {
			return r.Status(), true
		}
		return nil, false
	}
	return nil, false
}
```

`info_handler.go` `handleInfo`:

```go
	mounted := c.Mounted("nex")
	nex := map[string]any{
		"configured": nexEnabled,
		"mounted":    mounted,
		"ready":      mounted,
		"init_error": "",
		"effective":  nil,
	}
	if st, ok := c.ModuleStatus("nex"); ok {
		for k, v := range st {
			nex[k] = v
		}
	}
	info := map[string]any{ …, "nex": nex }
```

- [ ] **Step 4: Run to pass** — `go test ./internal/core/ 2>&1 | tail -5 && gofmt -l internal/core/`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add internal/core/core.go internal/core/info_handler.go internal/core/info_handler_test.go && git commit --only internal/core/core.go internal/core/info_handler.go internal/core/info_handler_test.go -m "feat(daemon): /api/info.nex reports ready, init_error and effective config"
```

---

### Task 3 (Go): `PUT /api/config` accepts `nex`

**Files:**
- Modify: `internal/core/config_handler.go`
- Test: `internal/core/config_handler_test.go` (replace `TestPutConfigRejectsNexObject`/`…Null`; keep `TestPutConfigWithFullNexSectionPersistsNexByteIdentical` — a PUT without `nex` must still leave the block untouched)

**Interfaces:** request field `Nex *config.NexConfig `json:"nex,omitempty"`` (full object). Validation: `req.Nex.Validate(home)` with `home, _ := os.UserHomeDir()`; error → 400 with the validator's message. `{"nex": null}` → 400 `nex must be an object`. Persisted via `UpdateConfig` (`cfg.Nex = *req.Nex`). Not applied live (I9).

- [ ] **Step 1: Failing tests** — replace the two reject tests with:

```go
func TestPutConfigNexValidAndPersists(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("bind = \"127.0.0.1\"\n"), 0644))
	c := newTestCore()
	c.CfgPath = cfgPath
	root := t.TempDir()
	body := fmt.Sprintf(`{"nex":{"enabled":true,"repo_roots":[%q],"sandbox":{"max_profile":"handoff","default_profile":"standard"},"timeouts":{"lease_ttl":"90s"}}}`, root)
	rec := httptest.NewRecorder()
	c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	c.CfgMu.RLock()
	assert.True(t, c.Cfg.Nex.Enabled)
	assert.Equal(t, []string{root}, c.Cfg.Nex.RepoRoots)
	assert.Equal(t, "handoff", c.Cfg.Nex.Sandbox.MaxProfile)
	assert.Equal(t, "90s", c.Cfg.Nex.Timeouts.LeaseTTL)
	c.CfgMu.RUnlock()
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "max_profile = \"handoff\"")
	var got config.Config
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.True(t, got.Nex.Enabled)
}

func TestPutConfigNexInvalidReturns400AndLeavesFileUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	original := "bind = \"127.0.0.1\"\n\n[nex]\nenabled = false\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(original), 0644))
	c := newTestCore()
	c.CfgPath = cfgPath
	cases := []struct{ name, body, want string }{
		{"enabled without roots", `{"nex":{"enabled":true}}`, "at least one root required"},
		{"relative claude_bin", `{"nex":{"enabled":false,"claude_bin":"bin/claude"}}`, "nex.claude_bin"},
		{"bad duration", `{"nex":{"enabled":false,"timeouts":{"turn":"soon"}}}`, "nex.timeouts.turn"},
		{"unknown profile", `{"nex":{"enabled":false,"sandbox":{"max_profile":"yolo"}}}`, "nex.sandbox.max_profile"},
		{"null", `{"nex":null}`, "nex must be an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.handlePutConfig(rec, httptest.NewRequest("PUT", "/api/config", strings.NewReader(tc.body)))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.want)
			data, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, original, string(data), "invalid nex must not touch config.toml")
		})
	}
}
```

> `{"nex": null}` decodes into a nil `*NexConfig` — indistinguishable from "absent" with a plain pointer. Keep the raw-message field to detect it: `Nex json.RawMessage `json:"nex"`` and decode it into `config.NexConfig` yourself; `len(raw) > 0 && string(raw) == "null"` → 400.

- [ ] **Step 2: Run to fail** — `go test ./internal/core/ -run TestPutConfigNex 2>&1 | tail -10`.

- [ ] **Step 3: Implement** — in `handlePutConfig`, replace the rejection block:

```go
	var nexUpdate *config.NexConfig
	if len(req.Nex) > 0 {
		if bytes.Equal(bytes.TrimSpace(req.Nex), []byte("null")) {
			http.Error(w, "nex must be an object", http.StatusBadRequest)
			return
		}
		var n config.NexConfig
		if err := json.Unmarshal(req.Nex, &n); err != nil {
			http.Error(w, "invalid nex: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Static shape validation only (spec §4.4.1 division of labour);
		// whether the engine can actually assemble is Init's business after
		// the restart the UI asks for.
		home, _ := os.UserHomeDir()
		if err := n.Validate(home); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		nexUpdate = &n
	}
	…
	err := c.UpdateConfig(func(cfg *config.Config) error {
		…
		if nexUpdate != nil {
			cfg.Nex = *nexUpdate // persisted, never applied live (I9)
		}
		return nil
	})
```

Keep `Nex json.RawMessage `json:"nex"`` in `configUpdateRequest` and update its comment.

- [ ] **Step 4: Run to pass** — `go test ./internal/core/ 2>&1 | tail -5 && gofmt -l internal/core/ && go build ./...`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add internal/core/config_handler.go internal/core/config_handler_test.go && git commit --only internal/core/config_handler.go internal/core/config_handler_test.go -m "feat(daemon): PUT /api/config accepts a validated [nex] section (restart to apply)"
```

---

### Task 4 (SPA): types + `NexEngineStatus` card

**Files:**
- Modify: `spa/src/lib/host-api.ts` (`ConfigData`), `spa/src/stores/useHostStore.ts` (`HostInfo.nex?`)
- Create: `spa/src/components/hosts/nex/NexEngineStatus.tsx`
- Test: `spa/src/components/hosts/nex/NexEngineStatus.test.tsx`
- Locales: `spa/src/locales/en.json`, `zh-TW.json`

**Interfaces:**
- Produces:

```ts
// host-api.ts
export interface NexSandboxConfig { max_profile: string; default_profile: string }
export interface NexTimeoutsConfig { lease_ttl: string; interrupt: string; turn: string }
export interface NexConfig {
  enabled: boolean; repo_roots: string[]; service_roots: string[]
  claude_bin: string; cswap_bin: string; path_prepend: string[]
  sandbox: NexSandboxConfig; timeouts: NexTimeoutsConfig
}
export interface NexEffective {
  data_dir: string; claude_bin: string; cswap_bin: string; max_profile: string; default_profile: string
  repo_roots: string[]; service_roots: string[]; path_prefix: string; lease_ttl: string; interrupt: string; turn: string
}
export interface NexInfo { configured: boolean; mounted: boolean; ready: boolean; init_error: string; effective: NexEffective | null }
export interface ConfigData { …existing; nex?: NexConfig }
// useHostStore.ts
export interface HostInfo { …existing; nex?: NexInfo }
// NexEngineStatus.tsx
export interface NexEngineStatusProps { hostId: string; info: NexInfo | null; onRefresh: () => void }
export default function NexEngineStatus(p: NexEngineStatusProps): JSX.Element
```

The card fetches `fetchNexHost` + `fetchNexCapabilities` itself **only when `info.ready`**; the parent owns `info` (one `/api/info` fetch shared with the config card).

i18n (en / zh-TW), under `hosts.nex.*`:

```
hosts.nex                      "Nex"                                  Nex
hosts.nex.status.title         "Engine"                               引擎
hosts.nex.status.disabled      "Disabled"                             未啟用
hosts.nex.status.not_running   "Enabled in config, not running — restart the daemon"  設定已啟用但未執行，請重啟 daemon
hosts.nex.status.unavailable   "Unavailable"                          無法使用
hosts.nex.status.ready         "Ready"                                就緒
hosts.nex.status.phase         "Phase"                                階段
hosts.nex.status.account       "Account"                              帳號
hosts.nex.status.quota_5h      "5-hour window"                        5 小時額度
hosts.nex.status.quota_7d      "7-day window"                         7 天額度
hosts.nex.status.quota_unknown "unknown"                              未知
hosts.nex.status.roots         "Roots"                                根目錄
hosts.nex.status.profiles      "Profiles"                             Profile
hosts.nex.status.lease_ttl     "Lease TTL"                            Lease TTL
hosts.nex.status.providers     "Providers"                            Provider
hosts.nex.status.refresh       "Refresh"                              重新整理
hosts.nex.status.effective     "Running with"                         目前生效
```

- [ ] **Step 1: Failing test**

```tsx
// spa/src/components/hosts/nex/NexEngineStatus.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import NexEngineStatus from './NexEngineStatus'
import * as api from '../../../lib/nex/nex-api'
import type { NexInfo } from '../../../lib/host-api'

vi.mock('../../../lib/nex/nex-api', () => ({ fetchNexHost: vi.fn(), fetchNexCapabilities: vi.fn() }))

const ready: NexInfo = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d/nex', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/Users/w/Workspace'], service_roots: [], path_prefix: '/opt/bin', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }

beforeEach(() => {
  vi.mocked(api.fetchNexHost).mockReset().mockResolvedValue({ active_account: 'wake@example.com', quota: { five_hour_pct: 12.5, seven_day_pct: 80, resets_at: 0, source: 'cswap' } })
  vi.mocked(api.fetchNexCapabilities).mockReset().mockResolvedValue({ phase: 'P1a', host_id: 'mlab', verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [], sandbox_profiles: ['readonly', 'standard'], sandbox_default_profile: 'standard', sandbox_max_profile: 'handoff', roots: [{ path: '/Users/w/Workspace', kind: 'dev' }], lease: { ttl_seconds: 120, scope: 'execution', renew: { method: 'POST', path: '' }, release: { method: 'DELETE', path: '' } }, send: { delivery: [], max_text_bytes: 65536 } })
})

describe('NexEngineStatus', () => {
  it('shows Disabled and fetches nothing when not configured', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: false, mounted: false, ready: false, init_error: '', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/disabled/i)
    expect(api.fetchNexHost).not.toHaveBeenCalled()
  })

  it('shows "not running" when configured but not mounted', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: true, mounted: false, ready: false, init_error: '', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/not running/i)
  })

  it('shows Unavailable with the init error and skips Nexen calls', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: true, mounted: true, ready: false, init_error: 'nex: init: assembling engine: boom', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/unavailable/i)
    expect(screen.getByText(/boom/)).toBeInTheDocument()
    expect(api.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('when ready, renders effective config, account, quota bars, roots, profiles', async () => {
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/ready/i)
    await waitFor(() => expect(screen.getByText('wake@example.com')).toBeInTheDocument())
    expect(screen.getByText('P1a')).toBeInTheDocument()
    expect(screen.getByText('12.5%')).toBeInTheDocument()
    expect(screen.getByText('80%')).toBeInTheDocument()
    expect(screen.getByText('/Users/w/Workspace')).toBeInTheDocument()
    expect(screen.getByText(/handoff/)).toBeInTheDocument()
    expect(screen.getByText('/d/nex')).toBeInTheDocument()
  })

  it('renders quota as unknown when null (never 0)', async () => {
    vi.mocked(api.fetchNexHost).mockResolvedValueOnce({ active_account: '', quota: null })
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={() => {}} />)
    await waitFor(() => expect(screen.getAllByText(/unknown/i).length).toBeGreaterThan(0))
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
  })

  it('Refresh calls onRefresh and refetches', async () => {
    const onRefresh = vi.fn()
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={onRefresh} />)
    await waitFor(() => expect(api.fetchNexHost).toHaveBeenCalledTimes(1))
    screen.getByRole('button', { name: /refresh/i }).click()
    expect(onRefresh).toHaveBeenCalled()
    await waitFor(() => expect(api.fetchNexHost).toHaveBeenCalledTimes(2))
  })
})
```

- [ ] **Step 2: Run to fail** — `cd …/spa && npx vitest run src/components/hosts/nex/NexEngineStatus.test.tsx`.

- [ ] **Step 3: Implement** — types as above; component: badge (`data-testid="nex-status-badge"`) from `info` (`!configured → disabled`, `configured && !mounted → not_running`, `mounted && !ready → unavailable` + `init_error` in a `<pre>`, `ready → ready`); a `useEffect` keyed on `[hostId, info?.ready, tick]` that, when ready, `Promise.all([fetchNexHost, fetchNexCapabilities])` into local state (errors → `console.warn` + leave the row empty, never crash the card); `Field` rows from `../form-fields` for phase, account, quota (two `<div>` bars with the pct text; `quota === null` → `quota_unknown` text), roots list, profiles (`max/default` from `effective`), lease TTL, providers, and the `effective` block (`data_dir`, `claude_bin || 'claude (via PATH)'`, `path_prefix`). `Refresh` button (`ArrowsClockwise`) bumps `tick` and calls `onRefresh`.

- [ ] **Step 4: Run to pass** — the test file + `src/locales` tests.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/host-api.ts spa/src/stores/useHostStore.ts spa/src/components/hosts/nex/NexEngineStatus.tsx spa/src/components/hosts/nex/NexEngineStatus.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit --only spa/src/lib/host-api.ts spa/src/stores/useHostStore.ts spa/src/components/hosts/nex/NexEngineStatus.tsx spa/src/components/hosts/nex/NexEngineStatus.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Nex engine status card + nex config/info types"
```

---

### Task 5 (SPA): `NexConfigForm` + restart-required helper

**Files:**
- Create: `spa/src/components/hosts/nex/nex-config-diff.ts`, `spa/src/components/hosts/nex/NexConfigForm.tsx`
- Test: `spa/src/components/hosts/nex/nex-config-diff.test.ts`, `spa/src/components/hosts/nex/NexConfigForm.test.tsx`
- Locales: en / zh-TW

**Interfaces:**

```ts
// nex-config-diff.ts
export const SANDBOX_PROFILES = ['', 'readonly', 'standard', 'trusted', 'handoff'] as const
export function emptyNexConfig(): NexConfig
export function restartRequired(saved: NexConfig | undefined, info: NexInfo | null): boolean
  // true when saved.enabled !== info.mounted, or (mounted && effective && any of:
  //   repo_roots/service_roots (order-insensitive, after trimming), claude_bin, cswap_bin,
  //   sandbox.max_profile/default_profile, timeouts.* ('' saved vs Nexen default in effective counts as equal
  //   ONLY for timeouts — compare timeouts only when the saved string is non-empty) differ)
// NexConfigForm.tsx
export interface NexConfigFormProps { hostId: string; config: NexConfig | undefined; info: NexInfo | null; onSaved: (cfg: ConfigData) => void }
export default function NexConfigForm(p: NexConfigFormProps): JSX.Element
```

i18n `hosts.nex.config.*`: `title` "Configuration/設定", `enabled` "Enabled/啟用", `repo_roots` "Repo roots/Repo 根目錄", `service_roots` "Service roots/服務根目錄", `path_prepend` "PATH prepend/PATH 前置", `claude_bin` "claude binary/claude 執行檔", `cswap_bin` "cswap binary/cswap 執行檔", `max_profile` "Max profile/最高 profile", `default_profile` "Default profile/預設 profile", `lease_ttl` "Lease TTL", `interrupt` "Interrupt timeout/中斷逾時", `turn` "Turn timeout/回合逾時", `nexen_default` "Nexen default/Nexen 預設", `add` "Add/新增", `remove` "Remove/移除", `save` "Save/儲存", `saving` "Saving…/儲存中…", `saved` "Saved/已儲存", `restart_required` "Restart the daemon on {{host}} for changes to take effect (pdx stop && pdx start)./請在 {{host}} 重啟 daemon 才會生效（pdx stop && pdx start）。", `error` "Could not save: {{message}}/無法儲存：{{message}}".

- [ ] **Step 1: Failing tests**

```ts
// nex-config-diff.test.ts
import { describe, it, expect } from 'vitest'
import { restartRequired, emptyNexConfig } from './nex-config-diff'
import type { NexInfo } from '../../../lib/host-api'

const eff = { data_dir: '/d', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: '', repo_roots: ['/a', '/b'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' }
const info = (over: Partial<NexInfo> = {}): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: eff, ...over })
const saved = (over = {}) => ({ ...emptyNexConfig(), enabled: true, repo_roots: ['/b', '/a'], sandbox: { max_profile: 'handoff', default_profile: '' }, ...over })

describe('restartRequired', () => {
  it('false when saved matches effective (root order ignored, empty timeouts ignored)', () => {
    expect(restartRequired(saved(), info())).toBe(false)
  })
  it('true when enabled differs from mounted', () => {
    expect(restartRequired(saved({ enabled: false }), info())).toBe(true)
    expect(restartRequired(saved(), info({ mounted: false, ready: false, effective: null }))).toBe(true)
  })
  it('true when roots, bins, profiles or a non-empty timeout differ', () => {
    expect(restartRequired(saved({ repo_roots: ['/a'] }), info())).toBe(true)
    expect(restartRequired(saved({ claude_bin: '/x/claude' }), info())).toBe(true)
    expect(restartRequired(saved({ sandbox: { max_profile: 'standard', default_profile: '' } }), info())).toBe(true)
    expect(restartRequired(saved({ timeouts: { lease_ttl: '90s', interrupt: '', turn: '' } }), info())).toBe(true)
    expect(restartRequired(saved({ timeouts: { lease_ttl: '2m', interrupt: '', turn: '' } }), info())).toBe(false) // 2m == 2m0s
  })
  it('false when nothing is saved and nex is not mounted (fresh host)', () => {
    expect(restartRequired(undefined, info({ configured: false, mounted: false, ready: false, effective: null }))).toBe(false)
  })
})
```

> Duration equality: normalise both sides with a tiny parser (`Xh Ym Zs` → seconds; Go prints `2m0s`, the user may type `2m`). Put `parseGoDuration(s): number | null` in the same helper file with two tests.

```tsx
// NexConfigForm.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import NexConfigForm from './NexConfigForm'
import { emptyNexConfig } from './nex-config-diff'
import * as hostApi from '../../../lib/host-api'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>('../../../lib/host-api')
  return { ...actual, hostFetch: vi.fn() }
})

const info = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/a'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }
const saved = { ...emptyNexConfig(), enabled: true, repo_roots: ['/a'], sandbox: { max_profile: 'handoff', default_profile: 'standard' } }

beforeEach(() => vi.mocked(hostApi.hostFetch).mockReset())

describe('NexConfigForm', () => {
  it('mirrors the saved config into the fields', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    expect((screen.getByLabelText(/enabled/i) as HTMLInputElement).checked).toBe(true)
    expect(screen.getByDisplayValue('/a')).toBeInTheDocument()
    expect((screen.getByLabelText(/max profile/i) as HTMLSelectElement).value).toBe('handoff')
  })

  it('PUTs the whole nex object and shows restart-required after a change', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: { ...saved, sandbox: { max_profile: 'handoff', default_profile: 'readonly' } } }), { status: 200 }))
    const onSaved = vi.fn()
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={onSaved} />)
    fireEvent.change(screen.getByLabelText(/default profile/i), { target: { value: 'readonly' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const [, path, init] = vi.mocked(hostApi.hostFetch).mock.calls[0]
    expect(path).toBe('/api/config')
    expect(init?.method).toBe('PUT')
    const body = JSON.parse(init!.body as string)
    expect(body.nex.sandbox.default_profile).toBe('readonly')
    expect(body.nex.repo_roots).toEqual(['/a'])
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(screen.getByTestId('nex-restart-required')).toBeInTheDocument()
  })

  it('shows the validator message next to the offending field on 400', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response('nex.timeouts.turn: time: invalid duration "soon"', { status: 400 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.change(screen.getByLabelText(/turn timeout/i), { target: { value: 'soon' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(screen.getByTestId('field-error-timeouts.turn')).toHaveTextContent(/invalid duration/))
  })

  it('adds and removes list entries', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.click(screen.getAllByRole('button', { name: /^add$/i })[0]) // repo roots
    const inputs = screen.getAllByPlaceholderText('/absolute/path')
    fireEvent.change(inputs[inputs.length - 1], { target: { value: '/b' } })
    expect(screen.getByDisplayValue('/b')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /remove/i })[0])
    expect(screen.queryByDisplayValue('/a')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to fail** — `npx vitest run src/components/hosts/nex`.

- [ ] **Step 3: Implement** — `nex-config-diff.ts` as specified (`emptyNexConfig` = all fields empty/false; `parseGoDuration`; `restartRequired`). `NexConfigForm.tsx`: local `draft: NexConfig` initialised from `config ?? emptyNexConfig()` and re-synced when `config` changes **unless dirty** (the `EditorHomePathHostSection` pattern: `dirtyRef`); controls: checkbox (`<label>` with text so `getByLabelText(/enabled/i)` works), three list editors (input `placeholder="/absolute/path"`, `Add`/`Remove` buttons), two text inputs for bins, two `<select>`s over `SANDBOX_PROFILES` (empty option labelled `nexen_default`), three duration inputs; `Save` → `hostFetch(hostId, '/api/config', { method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ nex: trimmed(draft) }) })`; 200 → `onSaved(await res.json())`, `dirty=false`, show `saved`; 400 → text; if it starts with `nex.<key>` map to `data-testid="field-error-<key>"` under that field, else a general `error` line. `restartRequired(config, info)` → `data-testid="nex-restart-required"` notice with `t('hosts.nex.config.restart_required', { host })`.

- [ ] **Step 4: Run to pass** — `npx vitest run src/components/hosts/nex src/locales`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/hosts/nex/nex-config-diff.ts spa/src/components/hosts/nex/nex-config-diff.test.ts spa/src/components/hosts/nex/NexConfigForm.tsx spa/src/components/hosts/nex/NexConfigForm.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit --only spa/src/components/hosts/nex/nex-config-diff.ts spa/src/components/hosts/nex/nex-config-diff.test.ts spa/src/components/hosts/nex/NexConfigForm.tsx spa/src/components/hosts/nex/NexConfigForm.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Nex config form with restart-required detection"
```

---

### Task 6 (SPA): `NexExecutionsTable`

**Files:**
- Create: `spa/src/components/hosts/nex/NexExecutionsTable.tsx`
- Test: `spa/src/components/hosts/nex/NexExecutionsTable.test.tsx`
- Locales: en / zh-TW

**Interfaces:**

```ts
export interface NexExecutionsTableProps { hostId: string; enabled: boolean }   // enabled = info.ready
export const LIST_REFRESH_DEBOUNCE_MS = 500
export default function NexExecutionsTable(p: NexExecutionsTableProps): JSX.Element
```

Behaviour: on mount (and when `enabled` flips true) `listExecutions(hostId, { includeArchived, limit: 100 })`; one site-wide `openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => schedule refetch (debounced 500 ms), onStatus: … })` while mounted and enabled — **frame contents never applied**; closed on unmount/disable. Columns: state dot, short id (`exc_…` first 12 chars, full in `title`), provider · profile, cwd basename (full in `title`), brief first line (≤ 80 chars), observers, lease holder (`(you)` when principal ends with `/${getNexClientId()}`), last_turn_reason, `updated_at` relative. Row actions: **Open** → `useTabStore.getState().openSingletonTab({ kind: 'execution', executionId, host: hostId })` + `setActiveTab`; **Terminate** (two-click confirm; `attachControl` → `terminateExecution` → `releaseLease`); **Archive/Unarchive** → `archiveExecution(hostId, id, archived)`. `include archived` checkbox; manual Refresh button. Errors from actions → inline line with `NexApiError.code`.

i18n `hosts.nex.executions.*`: `title` "Executions/執行體", `empty` "No executions yet — start one with pdx nex delegate./尚無執行體，用 pdx nex delegate 建立。", `include_archived` "Show archived/顯示已歸檔", `open` "Open/開啟", `terminate` "Terminate/終止", `terminate_confirm` "Confirm terminate/確認終止", `archive` "Archive/歸檔", `unarchive` "Unarchive/取消歸檔", `refresh` "Refresh/重新整理", `col.state` "State/狀態", `col.id` "ID", `col.provider` "Provider", `col.cwd` "Directory/目錄", `col.brief` "Brief/摘要", `col.observers` "Observers/觀察者", `col.lease` "Lease", `col.last_turn` "Last turn/上一輪", `col.updated` "Updated/更新", `you` "(you)/（你）", `action_failed` "{{action}} failed: {{code}}/{{action}} 失敗：{{code}}".

- [ ] **Step 1: Failing test**

```tsx
// NexExecutionsTable.test.tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import NexExecutionsTable, { LIST_REFRESH_DEBOUNCE_MS } from './NexExecutionsTable'
import * as api from '../../../lib/nex/nex-api'
import * as sse from '../../../lib/nex/nex-sse'
import type { NexSseOptions } from '../../../lib/nex/nex-sse'

const mockOpenSingletonTab = vi.fn(() => 'tab-1')
const mockSetActiveTab = vi.fn()
vi.mock('../../../stores/useTabStore', () => ({ useTabStore: { getState: () => ({ openSingletonTab: mockOpenSingletonTab, setActiveTab: mockSetActiveTab }) } }))
vi.mock('../../../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
vi.mock('../../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me' }))

const row = (over = {}) => ({ id: 'exc_0123456789abcdef', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'first line\nsecond', labels: {}, created_at: 0, updated_at: Date.now(), duration_ms: null, event_count: 3, observers: 1, archived: false, effective_profile: 'standard', last_turn_reason: 'completed', lease: { principal_id: 'pdx:mlab/t-me', expires_at: 1 }, ...over })
let sseOpts: NexSseOptions | null
let sseClose: ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.useFakeTimers()
  sseOpts = null; sseClose = vi.fn()
  vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [row()], next_cursor: '' })
  vi.mocked(api.attachControl).mockReset().mockResolvedValue({ mode: 'control', lease_id: 'ls', expires_at: 1 })
  vi.mocked(api.terminateExecution).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
  vi.mocked(api.archiveExecution).mockReset().mockResolvedValue(undefined)
  mockOpenSingletonTab.mockClear(); mockSetActiveTab.mockClear()
})
afterEach(() => vi.useRealTimers())

describe('NexExecutionsTable', () => {
  it('lists executions with (you) on my lease and opens a host-scoped execution pane', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('exc_01234567')).toBeInTheDocument()
    expect(screen.getByText(/\(you\)/)).toBeInTheDocument()
    expect(screen.getByText('first line')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /open/i }))
    expect(mockOpenSingletonTab).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_0123456789abcdef', host: 'h' })
    expect(mockSetActiveTab).toHaveBeenCalledWith('tab-1')
  })

  it('opens one site-wide SSE as a refresh signal, debounces refetch, never applies frames', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(sseOpts!.url).toBe('/api/nex/v1/events')
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    act(() => {
      sseOpts!.onFrame({ id: '1', event: 'execution.delegated', data: '{}' })
      sseOpts!.onFrame({ id: '2', event: 'execution.running', data: '{}' })
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS + 1) })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('does nothing while disabled and closes the SSE on unmount', async () => {
    const { unmount, rerender } = render(<NexExecutionsTable hostId="h" enabled={false} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.listExecutions).not.toHaveBeenCalled()
    rerender(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(1)
  })

  it('terminate needs confirmation, then takes a lease, terminates and releases', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', 'ls'))
    expect(api.releaseLease).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', 'ls')
  })

  it('archive toggles and include-archived re-queries', async () => {
    render(<NexExecutionsTable hostId="h" enabled />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: /^archive$/i }))
    await waitFor(() => expect(api.archiveExecution).toHaveBeenCalledWith('h', 'exc_0123456789abcdef', false))
    fireEvent.click(screen.getByLabelText(/show archived/i))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(vi.mocked(api.listExecutions).mock.calls.at(-1)![1]).toMatchObject({ includeArchived: true })
  })
})
```

> `archiveExecution(hostId, id, undo)` — `undo=false` archives, `true` unarchives (P-B.1 signature).

- [ ] **Step 2: Run to fail** — `npx vitest run src/components/hosts/nex/NexExecutionsTable.test.tsx`.
- [ ] **Step 3: Implement** as described (keep the component ≤ 300 lines; if the row grows, split `NexExecutionRow.tsx`).
- [ ] **Step 4: Run to pass** — the test file + `src/locales`.
- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/hosts/nex/NexExecutionsTable.tsx spa/src/components/hosts/nex/NexExecutionsTable.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit --only spa/src/components/hosts/nex/NexExecutionsTable.tsx spa/src/components/hosts/nex/NexExecutionsTable.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json -m "feat(spa): Nex executions table with site-wide SSE refresh"
```

---

### Task 7 (SPA): `NexHostSection` + host sub-page registration

**Files:**
- Create: `spa/src/components/hosts/nex/NexHostSection.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx` (`setHostBuiltinSections`, ~line 457)
- Test: `spa/src/components/hosts/nex/NexHostSection.test.tsx`; `spa/src/lib/dispatch-settings-contributions.test.ts` (the `defs('overview', …)` list at ~line 313 gains `'nex'` if that test enumerates the built-ins)

**Interfaces:** `export function NexHostSection({ hostId }: { hostId: string })` — fetches `/api/info` (→ `info.nex`) and `/api/config` (→ `config.nex`) on mount / hostId change / refresh; renders `NexEngineStatus`, `NexConfigForm` (`onSaved` updates `config`), `NexExecutionsTable enabled={info?.nex?.ready ?? false}`; a host that is offline shows the existing `hosts.load_failed` line.

- [ ] **Step 1: Failing test** — render with `hostFetch` mocked for `/api/info` and `/api/config`; assert the three cards' titles appear, that `/api/info` was fetched once, that saving through the form (mock PUT) updates the restart notice, and that `Refresh` on the status card refetches `/api/info`. Plus a registry test: after `dispatchSettingsContributions()` the host built-in list contains `localId: 'nex'` at order 6 (follow how `dispatch-settings-contributions.test.ts` asserts the others).
- [ ] **Step 2: Run to fail.**
- [ ] **Step 3: Implement** — section + `{ localId: 'nex', labelKey: 'hosts.nex', order: 6, component: NexHostSection }` appended to `setHostBuiltinSections`.
- [ ] **Step 4: Run to pass** — `npx vitest run src/components/hosts/nex src/lib/dispatch-settings-contributions.test.ts src/components/HostPage.test.tsx`.
- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/hosts/nex/NexHostSection.tsx spa/src/components/hosts/nex/NexHostSection.test.tsx spa/src/lib/register-modules/index.tsx spa/src/lib/dispatch-settings-contributions.test.ts && git commit --only spa/src/components/hosts/nex/NexHostSection.tsx spa/src/components/hosts/nex/NexHostSection.test.tsx spa/src/lib/register-modules/index.tsx spa/src/lib/dispatch-settings-contributions.test.ts -m "feat(spa): Host → Nex sub-page"
```

---

### Task 8: Full verification

- [ ] Go: `go build ./... && go test ./... 2>&1 | tail -15` (whole daemon) and `gofmt -l internal cmd` empty.
- [ ] SPA: `npx vitest run 2>&1 | tail -6 && pnpm run lint && pnpm run build 2>&1 | tail -3`.
- [ ] `go run ./cmd/pdx version` still works (binary builds).
- [ ] Fix anything red in a `fix(…)` commit naming the cause.

---

## Self-review notes

- Spec coverage: §4.4.1 → T1; §4.4.2 → T2 + T3 + T4 types; §4.4.3 cards 1/2/3 → T4/T5/T6; registration → T7; §4.5 "Host has no nex" (`nex_disabled`, P-B.2) shows the copy that points here; "503 nex_unavailable" → T1 + T4 badge; I8 → T1 test; I9 → T3 tests; I10 (table half) → T6 first test.
- Types consistent: `NexConfig`/`NexInfo`/`NexEffective` defined once in `host-api.ts` (T4) and consumed by T5/T6/T7; `restartRequired(saved, info)` signature identical in T5 definition and T7 use.
- Go seam: `core` only knows `StatusReporter`; nex implements `Status()`; no import cycle (T1 vs T2 test stubs are independent).
