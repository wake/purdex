# Daemon Hook Pipeline Lag — Production-Grade Root Cause Analysis

> **Status**: analysis doc (not a spec — feeds into action items below)
> **Worktree**: `daemon-perf-fastpath` / branch `worktree-daemon-perf-fastpath`
> **Base**: `origin/main` @ alpha.268
> **Investigation date**: 2026-04-30
> **Codex job**: `task-mokpcqt9-f3tolm` (8m 20s, effort=high)
> **Trigger context**: W6-3+W6-4 ProbeIntent first PR mlab live verify revealed user-perceived "3 second lag" from hook trigger to SPA tab status update; W6-3 ProbeIntent itself stayed within spec ≤2s but the surrounding pipeline added unaccounted-for time.

---

## 1. Observed symptoms

User on mlab (M4 / 16GB / SSD), running codex pane:

- **"按 Enter ping → SPA 看到 codex icon"** ≈ **3 秒**（兩次測試一致）
- **"`/exit` 或 `^c` → SPA 看到 terminal icon"** ≈ **3-4 秒**

Daemon log slice (one PdxUserPromptSubmit chain, grep `[hook] / [derive] / [handler] / [probe] / [broadcast]`):

```
07:20:10  [hook] trigger session=purdex-sync agent=codex purdex_name=PdxUserPromptSubmit chain_id=83ab82dc
07:20:10  [derive] verify_passed status=running
07:20:10  [handler] frame_apply lifecycle=PdxUserPromptSubmit decision=updated_frame
07:20:12  [handler] projection_built top_status=running pane_id=%11           ← +2s gap (B 段)
07:20:12  [probe] recordHookAt
07:20:14  [probe-intent] start arm pid=16596 generation=3
07:20:15  [broadcast] decision=broadcasted reason=session_code_resolved        ← +3s from frame_apply (E 段)
```

Two consistent gaps reproducible across multiple chains:
- **B**: `frame_apply → projection_built` ≈ 1-2s
- **E**: `projection_built → broadcast` ≈ 1-2s

W6-3 ProbeIntent detector (`arm → emit signal → status=error`) measured ~1-2s, **within spec ≤2s** — not the source of the user-perceived lag.

---

## 2. Root cause

### 2.1 真正主犯：`resolveSessionCode` in hook hot path

`emitHookToSession` (`internal/module/agent/handler.go:480, 494`) calls `resolveSessionCode(tmuxSession)` on **every hook event**. That function calls `m.sessions.ListSessions()` directly — bypassing the existing `cachedListSessions()` debounce that `/api/sessions` HTTP handler uses.

`ListSessions()` (`internal/module/session/service.go:15-44`) executes:
1. `tmux list-sessions` × 1 (`internal/tmux/executor.go:81`)
2. `meta.CleanOrphans()` write × 1 (`internal/module/session/service.go:21`)
3. `applyActivePaneMetadata()` × S sessions (`internal/module/session/service.go:44`)
4. Inside each: `ActivePaneMetadata()` runs **7 × `tmux display-message`** (`internal/tmux/executor.go:115`)
5. Plus S × `meta.GetMeta()` reads

**Total per hook**: `1 + 7×S` tmux subprocess invocations. With S=5 live sessions (typical user setup) = **36 tmux exec calls**. Each tmux subprocess on macOS = 30-80ms (process spawn + tmux client→server roundtrip). Lower bound: 36 × 30ms = **1.08s**. Upper bound under load: 36 × 80ms = **2.88s**. This matches the observed +1-3s gap exactly.

### 2.2 B 段次因：`projectionForSession` 走 per-pane tmux exec

`handler.handleEvent` calls `m.projectionForSession(req.TmuxSession)` (`handler.go:308`) after `applyFrameEvent` already produced a pane-scoped projection. The second call is **necessary** semantically (pane-scope → session-scope upgrade needed for multi-pane sessions) but its implementation walks `frames.ListAll → BuildSessionProjections → per-projection resolvePaneSession`.

`resolvePaneSession` (`internal/module/agent/module.go:605`) issues another `tmux display-message "#{session_name}"` per pane (`internal/tmux/executor.go:268`). With P=5 live panes = 5 more tmux subprocesses ≈ **150-400ms** added to B 段.

### 2.3 D 段：W6-3 dispatcher target lookup 持 `m.mu`

`probeIntentDispatcher.applyIntentLifecycle` (`internal/module/agent/probe_intent_dispatcher.go:262`) calls `lookupTopFrameForSessionLocked` inside the `m.mu` critical section. That helper transitively calls `projectionForSession → resolvePaneSession → tmux exec` — **all under the global lock**. Other goroutines (other hook handlers, probe callbacks, rename) convoy behind it.

This is W6-3 自己引入的 lock convoy；the dispatcher fundamentally needs to know `(paneID, senderPID)` to arm/match detectors, but the hook caller (`manageActivityWatch`) **already has** `projection.TopFrame` in scope — no need to re-query.

### 2.4 E 段次因：sqlite `synchronous=FULL` + 沒 `busy_timeout`

`internal/store/{meta,agent_event,frames,trace}.go` 的 DSN 只開 `journal_mode(wal)`，沒設 `synchronous` (預設 FULL — 每次 commit fsync) 或 `busy_timeout`。File-backed pool 也沒 cap (`SetMaxOpenConns` 只在 `:memory:` test 設 1)。

但這不是主因：`sync=FULL` 在 SSD 是 ms 級不是秒級；`busy_timeout` 沒設只會立即 abort（`SQLITE_BUSY` error），log 沒 abort path 表示沒撞到。Tail 治理價值約 **20-150ms** average，contention/checkpoint 高峰可達 200ms+。

### 2.5 已排除（非主因）

- `pdx hook` CLI cold start：80-250ms 額外 tax 在 hook 進入 daemon **之前**；user log 已從 `[hook] trigger` 對齊，不解釋 daemon 內 3s。
- HTTP handler sync trace enqueue：`hookTraceSink.Enqueue` 是 channel send，不等 db write (`internal/module/agent/trace.go:106`).
- SPA WebSocket flow control：broadcaster non-blocking、滿了就 drop (`internal/core/events.go:28, 110`)，不會 block handler.
- GC / goroutine scheduling：通常 ms 級，不會穩定每 hook 都貢獻 2s 分段。
- W6-3 detector 1Hz polling：影響 arm 後 recovery latency，不是 hook 主路徑。

---

## 3. Production-grade fix proposals

所有方案都必須在 production 環境（含 Linux daemon、power loss、並發、跨版本 schema）安全。

### 3.1 #1 — `resolveSessionCode` fast path **(highest ROI)**

**Design**:
- 在 `internal/module/session/` 加 `LookupCodeByName(name string) (code string, ok bool)` — 只做「session name → tmux session ID → code」解析；**不**跑 `CleanOrphans`、`ActivePaneMetadata`、full `SessionInfo` hydration
- agent module type-assert 新介面，miss 時 fallback 舊路徑
- rename atomic flow 同步更新 lookup state；create/delete 時 invalidate

**Production safety**:
- 純讀路徑，不改狀態機
- code 仍由 tmux session ID 決定（authoritative source 不變）
- macOS / Linux 都只依賴 tmux 基本命令
- rename invalidation 走既有 `RenameSessionAtomic` hook

**Risk matrix**: low
- 主要 regression：rename 後 cache stale → 解法是 rename atomic path 同步 invalidate + miss-refresh 補底
- 跨版本 schema：無
- Observability：保留現有 log path

**Effort**: 4-6h / **預期收益**: **1.0-2.5s** / **與 W6-3 dependency**: 可獨立 ship

### 3.2 #2 — `agent_frames` 加 `tmux_session` 欄位 persist

**Design**:
- `internal/store/frames.go` schema migration：加 `tmux_session TEXT NOT NULL DEFAULT ''`
- `applyFrameEvent` 寫入 `req.TmuxSession`
- 加 `frames.ListBySession(session)` 或 `ProjectSession(session)`
- `handler.handleEvent` 改用 session-scoped 直接 query，不再 `ListAll + per-pane resolvePaneSession`

**Production safety**:
- 比純 in-memory cache 穩：可重播、可 restart、可在 `RenameSessionAtomic` 內 atomic 更新
- Migration 採 dual-read：`tmux_session=''` 舊 row fallback 舊路徑，新 hook 寫入後逐步 cover
- Schema migration 走既有 migrateFramesDB 順序（idempotent）

**Risk matrix**: medium
- 改 schema：要寫 migration test、historical replay test
- rename flow：sql update + cache invalidation 須一起做（atomic）
- Observability：trace store 可考慮加 `tmux_session` 欄位輔助 debug

**Effort**: 8-14h / **預期收益**: **0.5-1.5s** / **與 W6-3 dependency**: 改的 `handler.go` / `module.go` rename 區與 W6-3 #3 high overlap → **應在 W6-3 + #1 ship 後做**

### 3.3 #3 — W6-3 dispatcher target injection（hook path 不再走 lookup）

**Design**:
- `manageActivityWatch(session, agentType, newStatus)` signature 加 optional `target ProbeIntentTarget{paneID, senderPID}` 參數
- caller (`handler.handleEvent` line ~413) 從 `projection.TopFrame` 取 target 直接傳
- `dispatcher.applyStatus` 收 target：若 caller 有提供就直接用；nil 才走慢速 `lookupTopFrameForSessionLocked`（replay path 保留）
- target 在 m.mu 外讀取，generation/cancel/active-set 仍在 m.mu 內保護

**Production safety**:
- target value 來自同一次 projection，比重新查更一致（避免 hook 與 lookup 之間 race window）
- replay path 仍走慢速 lookup（daemon restart 沒 caller context）
- Lifecycle 5 case + reconcile + replay race + cross-provider 全部不變

**Risk matrix**: medium
- W6-3 邏輯面修改 → 補 lifecycle / rename / replay race tests
- 與 W6-3 spec §5.4 對齊（不破壞「lifecycle 單一進入點」原則 — caller 提供 target 是 hint 不是 bypass）

**Effort**: 4-8h / **預期收益**: **0.5-1.5s**（消除 lock convoy） / **與 W6-3 dependency**: **建議與 W6-3 一起 bundle**（避免 W6-3 ship 後自己引入 lock convoy 再修）

### 3.4 #4 — sqlite `busy_timeout + pool cap`

**Design**:
- 4 個 store DSN 加 `busy_timeout(5000)` pragma
- File-backed pool: `agent DB SetMaxOpenConns(4) + SetMaxIdleConns(4)`、`meta DB 2/2`
- 不改 `synchronous`（保 FULL）

**Production safety**:
- `busy_timeout` 不改 durability，只是讓 SQLITE_BUSY 等而非 abort（避免 transient contention 中斷）
- Pool cap 對 WAL 多 reader 並發友善；既有 `UpsertIfUnchanged` / `DeleteIfUnchanged` 已用 optimistic concurrency 不依賴單連線序列化 (`internal/store/frames.go:263, 363`)
- SQLite WAL 多 reader / 單 writer 模型不變

**Risk matrix**: low
- 無 schema / API 改動
- 只可能因 pool 放大導致 fd 用量略增（cap=4 不顯著）

**Effort**: 2-4h / **預期收益**: **20-150ms** average / 可壓 200ms+ tail / **與 W6-3 dependency**: 完全獨立

### 3.5 #5 — sqlite `synchronous=NORMAL` **(NOT recommended for now)**

**Design**: DSN 加 `synchronous(NORMAL)` pragma — fsync 從 per-page write 降到 per-transaction commit。

**Trade-off**:
- **不損壞 DB / 不改一致性**：integrity 保證不變
- **Power loss 可能丟最後幾筆已回應的 WAL commit**：durability 降級
- 大多 OLTP service 都用 NORMAL，但 purdex 這類 dev 工具 + 觀測性 store 對 last-commit durability 沒明確要求

**Recommendation**: **暫不做**。User 明確要求 production-grade、不依賴「dev 接受短暫 inconsistency」取捨。即使 codex 確認 NORMAL 是 durability 而非 integrity tradeoff，仍視為 future-consideration。Stability-first 優先 #4 即可。

---

## 4. Out of scope（不修）

- **SPA render path**：lag 發生在 daemon broadcast 之前，SPA 收到 event 後 render 是 ms 級
- **WebSocket subscriber flow control**：non-blocking drop semantics 已正確 (`internal/core/events.go:28, 110`)
- **Tailscale / network**：user 與 daemon 同機，無網路 hop
- **Codex detector 1Hz poll**：影響 arm 後 recovery latency，不是 hook 主路徑
- **`pdx hook` CLI cold start**：80-250ms 額外 tax 在 daemon 之外
- **`broadcast async + coalesce`**：codex 明確不建議（會引入 out-of-order / drop-more 風險，對 lag 幾乎沒幫助）

---

## 5. 推薦執行順序

| Order | Item | Effort | Risk | 收益 | Bundle |
|---|---|---|---|---|---|
| 1 | **#1** `resolveSessionCode` fast path | 4-6h | low | 1.0-2.5s | 獨立 hotfix PR |
| 2 | **#3** W6-3 dispatcher target injection | 4-8h | med | 0.5-1.5s | **bundle 進 W6-3 PR** |
| 3 | **#4** sqlite `busy_timeout + pool cap` | 2-4h | low | 20-150ms tail | 獨立或併 #1 PR |
| 4 | #2 `tmux_session` persist + session-scoped query | 8-14h | med | 0.5-1.5s | W6-3 + #1 ship 後做 |
| - | #5 `synchronous=NORMAL` | 1-2h | med (durability) | 30-150ms tail | **不做**（production-grade 約束）|

**Total expected improvement**: 1.5s-4s reduction (hook → broadcast 從 3-4s 降到 ~0.5-1s)。

---

## 6. 並行作業排程

兩個 worktree 並行：

### 6.1 W6-3+W6-4 worktree (`lights-w6-3-codex-error`)

- 主任務：W6-3 收尾（P2-T6 signal log race fix + #3 dispatcher target injection + P2-T8 mlab live verify + PR + 兩輪 review + bump）
- bundle #3 是**新增 scope**，理由：避免 W6-3 ship 後自己引入 lock convoy 再修；改動 mechanical（manageActivityWatch + dispatcher.applyStatus signature）

### 6.2 daemon-perf worktree (`daemon-perf-fastpath`，本文件所在)

- 主任務：#1 fast-path + #4 sqlite tuning（一個 PR 或拆兩個都可）
- 與 W6-3 重疊：**`internal/module/agent/handler.go` 同檔不同函式**
  - W6-3 #3 改 line ~413 (manageActivityWatch caller)
  - #1 改 line ~480-494 (emitHookToSession + resolveSessionCode)
  - 兩函式相距 70+ lines，git auto-merge 應 OK，但 review 期間互相 rebase 一次

### 6.3 後續（W6-3 + #1+#4 ship 後）

- W6-LightsUI（issue #762，3 個 SPA 燈號 polish）
- #2 `tmux_session` persist（需 schema migration，scope 較大）
- W6-1 cc running spinner / W6-2 cc idle PostCompact / W6-5 / W6-6 等 W6 子項
- W5 燈號 bug 工作池

---

## 7. 引用

- Codex job `task-mokpcqt9-f3tolm` (8m 20s, effort=high, model=spark)
- Codex session ID: `019ddba1-a2f7-7813-8cd2-37a40c876e9c`
- Codex log: `/Users/wake/.claude/plugins/data/codex-openai-codex/state/purdex-4f74e01e473c9395/jobs/task-mokpcqt9-f3tolm.log`
- 觀察 daemon log: `/tmp/pdx-w6-3.log` (mlab, build commit `a3a2b2fa` from `worktree-lights-w6-3-codex-error`)

---

## 8. 文獻

- W1 audit: `docs/specs/2026-04-28-hook-status-audit-spec.md`（hook → status → 燈號對齊 SOT）
- W6-3 spec: `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md`（dispatcher 5-case lifecycle）
- W6-3 plan: `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md`（P1+P2 任務拆分）
