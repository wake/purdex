# Rate-limit subagent cleanup + notification debounce — Implementation Plan

> **Status**：v1（plan 初稿，待 codex round 1 review）
>
> **依賴 spec**：`docs/specs/2026-05-03-rate-limit-subagent-cleanup-spec.md` v3 + follow-ups（spec round 3 codex review approve to enter plan，2 個非阻塞點已 inline 修正）
> **Worktree**：`.claude/worktrees/rate-limit-cleanup` / branch `worktree-rate-limit-cleanup`
> **Base**：`origin/main` @ alpha.289 (`401a785c chore: bump 1.0.0-alpha.289`)
> **拆分**：兩 PR — PR-A daemon native detach (correctness)；PR-B SPA error notification debounce (UX policy)。每 task 獨立 commit。Phase 1 PR-A 與 Phase 3 PR-B 之間插 Phase 2 operational SQL cleanup（一次性手動，不是 task）

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1 / §3 / §4）

兩 PR 解 dthn-class symptom（cc 主 frame `subagents_json` 累積到 3944 native refs，配合 rate-limit 風暴每 1.5 Hz 桌面 notification 騷擾）：

**PR-A daemon correctness**（spec §3）：
- #1：cc / codex / opencode 三家 `PdxStopFailure` derive 加帶 `agent_id`（`raw["agent_id"]` → `Detail["agent_id"]`，nil-safe）
- #2：新 helper `findNativeRefByID(refs, id) int` — 純讀，pre-check 「frame.Subagents 是否含此 native ID」
- #3：`applyFrameEvent` switch 把 `LifecycleStopFailure` 從 `LifecycleStop` 共用 case 拆成獨立 case，frame != nil + agent_id 命中時走 `mutateSubagentsWithRetry(LifecycleSubagentStop, ref)`；agent_id 缺失或不命中 → break；frame == nil → fallthrough to `LifecycleStop` 既有 codex/proxy 路徑
- #4：新 trace reason `native_subagent_detached_on_stop_failure`；既有 `frame_missing` reuse for concurrent SessionEnd race

**PR-B SPA UX**（spec §4）：
- #5：`useNotificationDispatcher.ts` 新 module-level Map + `buildDebounceKey([ck, eventName, errorString])` JSON.stringify
- #6：`shouldNotify` 對 `derived === 'error'` 走 trailing-edge sliding debounce，windowMs=60_000；新事件 within window → `silentUntil = now + WINDOW_MS` 延長 + return false；window 過 → notify + reset
- #7：cleanup 兩 path：`clearSession` / `removeHost` 用 JSON.parse decode 第一元素比對；TTL self-cleanup `silentUntil < now - 5×WINDOW_MS`

**Out-of-scope reaffirm**（spec §9）：
- 不擋 `derived='waiting'` notification
- 不擋 `unread` badge
- 不動 daemon broadcast suppression
- 不為 native ref 加 `last_seen_at` schema migration（一次性 SQL cleanup 已足）
- 不擴 opencode plugin payload 加 `agent_id`（plugin template change，另案）

### 0.2 估計

- **PR-A**：production ~80 LOC（cc/codex/opencode derive +6 / findNativeRefByID +12 / handler switch +50 / trace const +1）+ test ~360 LOC = ~440 LOC，遠低於 spec AC9 的 500 LOC cap
- **PR-B**：production ~70 LOC（dispatcher state +20 / buildDebounceKey +5 / shouldNotify +20 / cleanup hooks +25）+ test ~190 LOC = ~260 LOC，低於 spec AC9 的 300 LOC cap
- **時間**：PR-A subagent TDD ~3 hr / 兩輪 codex review ~2 hr / mlab live verify ~30 min；PR-B subagent TDD ~2 hr / 兩輪 codex review ~1.5 hr。總 ~9 hr active work，跨 1-2 個 session

### 0.3 鎖序與不變式（per spec §3 / §4）

**PR-A**：
- **`mutateSubagentsWithRetry.applied=true` 不代表 ref 移除**（spec §2.3）— pre-check `findNativeRefByID >= 0` 後才呼叫 mutate；mutate 回 `applied=false` 是 race（concurrent SessionEnd），trace reason `frame_missing`
- **空 agent_id 走 break legacy path**（spec §3.2 / §6.1）— `findNativeRefByID("")` early return -1，與「agent_id 不命中」同 path（preserves v0 status update + LastSeenAt refresh + broadcast）
- **Native match by ID alone**（spec §2.4）— `subagentRefMatches` 比對 native ref 只看 ID；`Type: frame.AgentType` 是 informational，不影響 match
- **frame == nil 路徑零修改**（spec §3.2）— fallthrough 進 `LifecycleStop` body 後第一行 `if frame != nil { break }` 永不觸發（因為 fallthrough 條件就是 frame == nil），既有 codex turn-aware proxy detach + cc/opencode wildcard process-level detach 完全保留
- **`LifecycleStop` 路徑零修改**：當 `LifecycleStop` 直接命中 case body（未從 StopFailure fallthrough）— 因為 spec §3.2 只在 `case LifecycleStopFailure` 加新分支，`case LifecycleStop` 直接 fall through 進 body 用既有邏輯。Go switch 沒 implicit fallthrough，每個 case 是 self-contained body
- **trace reason 不重命名**：spec §3.5 — `native_subagent_detached_on_stop_failure` 是新；`frame_missing` 是 reuse；其他既有 reason 字面零碰撞

**PR-B**：
- **不污染 `unread`**（spec §4.5）— debounce 只走 `shouldNotify` 的 OS notification path；`useAgentStore.handleNormalizedEvent` 內的 `unread[ck]=true` 路徑零修改
- **不擋非 error**（spec §4.5）— `shouldNotify` 內 `if (derived === 'error')` gate
- **Key 對稱**（spec §4.4 / §4.6）— 編碼 `buildDebounceKey([ck, eventName, errorString])` 與解碼 `JSON.parse(rawKey)[0/1/2]` 共用同一 helper / 反向操作；測試 `debounce__key_uses_json_array_not_pipe_join` 鎖定
- **State 為 module-level 不進 zustand**（spec §4.3）— 模仿既有 `seenAtRef` pattern；test 用 `__resetDebounceStateForTests()` 清狀態

### 0.4 Subagent 切分

| Phase | Owner | Files | 並行性 |
|---|---|---|---|
| Phase 1 PR-A | Subagent A | `internal/agent/{cc,codex,opencode}/status.go` + `internal/module/agent/frame_ops.go` + `internal/module/agent/handler_test.go` | 序列（同 daemon code，避 file 寫衝突） |
| Phase 2 dthn cleanup | 主 session（手動） | DB SQL only | 在 PR-A merge 後 |
| Phase 3 PR-B | Subagent B | `spa/src/hooks/useNotificationDispatcher.ts` + `spa/src/hooks/useNotificationDispatcher.test.ts` | 與 Phase 1 互不衝突；可並行；spec § 11 建議序列降風險 |

每 task **獨立 commit**，PR 不 squash 在單一 commit（用 GitHub squash-merge 收尾）。

---

## 1. Phase 1：PR-A daemon native detach（Subagent A）

### P1-T1 — 三家 provider derive 加 `agent_id` 進 Detail

**目標**：`internal/agent/cc/status.go:83`、`internal/agent/codex/status.go:67`、`internal/agent/opencode/status.go:27` 三處 `PdxStopFailure` case 在 Detail map 加 `"agent_id": raw["agent_id"]`。nil-safe（`raw["agent_id"]` 缺鍵回 nil）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/cc/status.go` | line 87-90 Detail map 加 `"agent_id": raw["agent_id"]` |
| `internal/agent/codex/status.go` | line 67 PdxStopFailure case 同樣加 |
| `internal/agent/opencode/status.go` | line 27 PdxStopFailure case 同樣加 |
| `internal/agent/cc/status_test.go` | + `TestDeriveCC_PdxStopFailure_AgentIdInDetail`：raw `{"agent_id":"x","error":"rate_limit"}` → `Detail["agent_id"]=="x"`；raw 無 agent_id → `Detail["agent_id"]==nil` |
| `internal/agent/codex/status_test.go` | + 同上 codex 版（fixture 用 `parseRawJSON` helper if exists, else inline `json.RawMessage`） |
| `internal/agent/opencode/status_test.go` | + 同上 opencode 版 |

**TDD 步驟**：
1. 寫 6 個 derive test case (3 provider × 2 case: with / without agent_id)（全 Red）
2. 三檔 derive 加 `"agent_id": raw["agent_id"]`（Green）
3. 跑 `go test ./internal/agent/... -run "TestDerive.*StopFailure" -v` 全綠
4. **Commit**: `feat(daemon): cc/codex/opencode derive surface agent_id in PdxStopFailure detail`

### P1-T2 — `findNativeRefByID` 純函式 helper + 測試

**目標**：`internal/module/agent/frame_ops.go` 加新 helper，placement adjacent to existing `findProxyRefByBroker:947`。Pure read, no side effects.

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go` | +12 LOC：`findNativeRefByID(refs []agentpkg.SubagentRef, id string) int` — `id == ""` early return -1；linear scan 比 `!r.IsProxy && r.ID == id`；首中 return index；無中 return -1。Placement: 緊接 `findProxyRefByBroker:947` 之後。Doc comment 引 spec §2.3 解釋為何不能用 `mutateSubagentsWithRetry` 的「空 mutation」當證據 |
| `internal/module/agent/frame_ops_test.go` | + 4 case：<br>`TestFindNativeRefByID_HitsNativeRef` — `[{ID:"x", IsProxy:false}]` + id="x" → 0<br>`TestFindNativeRefByID_SkipsProxyRefWithSameID` — `[{ID:"x", IsProxy:true}]` + id="x" → -1<br>`TestFindNativeRefByID_EmptyIdReturnsNotFound` — id="" → -1（任何 refs）<br>`TestFindNativeRefByID_NotFoundReturnsNegativeOne` — `[{ID:"y"}]` + id="x" → -1 |

**TDD 步驟**：
1. 寫 4 個 test case（全 Red）
2. 加 helper 實作（Green）
3. 跑 `go test ./internal/module/agent/... -run TestFindNativeRefByID -v` 全綠
4. **Commit**: `feat(daemon): add findNativeRefByID pure helper for pre-check ref presence`

### P1-T3 — `applyFrameEvent` 拆 `LifecycleStopFailure` 獨立 case + native detach 邏輯

**目標**：spec §3.2 全套。把現行 `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` 拆成：
- `case agentpkg.LifecycleStopFailure:` — 新增 `frame != nil` 分支：pre-check + mutate + trace reason `native_subagent_detached_on_stop_failure`；mismatched / no agent_id → break；frame == nil → `fallthrough`
- `case agentpkg.LifecycleStop:` — 既有 body 完整保留（從原本聯合 case 抄出來）

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go` | line 288 拆 case；新 `LifecycleStopFailure` body ~50 LOC（per spec §3.2 全文）；trace reason 常數 placement: `internal/module/agent/trace.go` 或 `frame_ops.go` 內聯（檢查既有 reason 怎麼放）。原 line 288-424 body 改 placement 到新 `case LifecycleStop:` 下；fallthrough 從 LifecycleStopFailure no-frame branch 進入 |
| `internal/module/agent/frame_ops_test.go` | + 6 case，per spec §6.1（直接表）：<br>`TestStopFailure_NativeDetach_Hits`<br>`TestStopFailure_NativeDetach_Misses_NoMutation` — 三條 assertion：(a) FrameTraceMeta.Reason ≠ `"native_subagent_detached_on_stop_failure"`; (b) trace pipeline emit legacy `UpdateHookPath` step (透過 spy on `m.frames.UpsertIfUnchanged` call counter — 0 invocation 時等同無 mutate); (c) `frame.Status` post = `error`, `LastSeenAt` 已 refresh<br>`TestStopFailure_NoAgentId_LegacyBehaviour` — payload 缺 `agent_id` field<br>`TestStopFailure_EmptyAgentId_LegacyBehaviour` — payload `agent_id: ""`<br>`TestStopFailure_PreservesProxyRefs` — frame 同時有 native + proxy；只 native 被 detach<br>`TestStopFailure_FrameNil_FallthroughToProxyDetach` — frame==nil，走原 codex turn-aware path<br>`TestStopFailure_FrameDeletedMidFlight` — pre-check pass 但 mutate 回 false (concurrent delete)；trace reason `frame_missing` |

**Test Instrumentation 細節（addresses spec §6.1 codex round-3 P2）**：
- spy/call-counter 於 `m.frames.UpsertIfUnchanged`：用 fake store wrapping production interface，於 test setup 注入；計 `UpsertIfUnchanged` 被呼叫次數
- 測試 `Misses_NoMutation` assertion (b) 寫法：`require.Equal(t, 0, fakeStore.UpsertCallCount, "mutateSubagentsWithRetry must not run when ref absent")`
- 既有 fakes 在 `internal/module/agent/fakes_test.go`，verify call-counter 可加上去；若沒有則 inline test fake

**TDD 步驟**：
1. 寫 7 個 test case（全 Red），包含 spy fake 的 call-counter setup
2. 拆 case 結構（先讓 `case LifecycleStop:` 與 `case LifecycleStopFailure:` 各自編譯通過、body 都 break）
3. 加 `LifecycleStopFailure` 內的 pre-check + mutate + trace reason 邏輯（Green）
4. 加 fallthrough（Green）
5. 跑 `go test ./internal/module/agent/... -run TestStopFailure -v` 全綠
6. 跑 `go test ./internal/module/agent/... -count=10 -race` race-mode 10 輪確保拆 case 沒引入 race
7. 跑 `go test ./internal/module/agent/...` 全套確保 `frame_ops_l2_test.go` 既有 L2 test 零 regression（spec AC2）
8. Lint + vet
9. **Commit**: `feat(daemon): native subagent detach on PdxStopFailure (closes dthn-class accumulation)`

### P1-T4 — Fixture replay regression guard

**目標**：spec §6.1 `TestStopFailure_FixtureReplay_DthnPayload` — 把真實 dthn payload 存進 fixture，replay 確認 detach。R1 risk mitigation：版本相依 regression guard，cc 上游 payload shape drift 時 CI 抓得到。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/testdata/dthn_stopfailure_2026_05_03.json`（新檔） | 從 dthn `agent_trace_steps` 抄一個真實 PdxStopFailure trigger payload（chain `c02151f0...` 或同等代表性樣本），sanitize 掉 cwd 等 environment-specific 欄位但保留 schema |
| `internal/module/agent/frame_ops_test.go` | + `TestStopFailure_FixtureReplay_DthnPayload` — load fixture, 構 frame with seeded native ref `{ID: <fixture's agent_id>, IsProxy: false}`, replay; assert ref removed + trace reason `native_subagent_detached_on_stop_failure` |

**TDD 步驟**：
1. 從 dthn DB 抽一個 representative chain 的 trigger payload；存 testdata
2. 寫 fixture replay test（Red）
3. 因為 P1-T3 實作完，應直接 Green（這 test 是 regression guard，不驅動實作）
4. 跑 `go test ./internal/module/agent/... -run TestStopFailure_FixtureReplay -v`
5. **Commit**: `test(daemon): fixture replay regression guard for dthn PdxStopFailure shape`

### P1-T5 — PR-A push + Round 1 codex review

**步驟**：
1. 跑全 daemon test sweep：`go test ./... -count=1` + `go test ./internal/module/agent/... -count=10 -race` + `go vet ./...`
2. Push branch
3. `gh pr create` — title `feat(daemon): PdxStopFailure native subagent detach (rate-limit cleanup)`；body 引 spec / dthn data / trace reason / risk mitigation / AC checklist
4. **委派 codex round 1 standard review**: `/codex:review --base main --background`，focus 含「StopFailure case 拆分後 fallthrough 行為、findNativeRefByID 與既有 helper 一致性、test instrumentation 是否真的擋得住 phantom broadcast」
5. 收 finding 表 → 處理（高關聯/高信心/低複雜全修；其他開 issue）
6. 押進 Round 2 三平行 adversarial：攻擊（race/邊界/誤判）+ 防守（spec drift / native detach 是否擴張過頭）+ 體質（檔案大小 / 拆 case 後可讀性）

---

## 2. Phase 1 → Phase 2 過渡：PR-A merge + dthn 現場 SQL cleanup（operational）

PR-A merge 後依 spec §5 操作序：

```bash
# 1. 確認版本
pdx --version  # 應 ≥ 下一個 alpha

# 2. 確認沒有第二個 daemon 在跑
pgrep -lf 'pdx serve'   # 只有一個 PID

# 3. 停 daemon
brew services stop pdx  # 或 kill <pid>

# 4. 備份
mkdir -p ~/.config/pdx/backups
sqlite3 ~/.config/pdx/agent_events.db <<'EOF' > ~/.config/pdx/backups/2026-05-03-pre-cleanup.sql
.headers on
.mode insert agent_frames_pre_cleanup_backup
SELECT pane_id, frame_id, agent_type, subagents_json, last_seen_at
FROM agent_frames
WHERE json_array_length(subagents_json) > 100;
EOF

# 5. 檢查影響面
sqlite3 ~/.config/pdx/agent_events.db "SELECT pane_id, agent_type, json_array_length(subagents_json) AS n_refs FROM agent_frames WHERE json_array_length(subagents_json) > 100 ORDER BY n_refs DESC;"

# 6. Cleanup（transaction）
sqlite3 ~/.config/pdx/agent_events.db <<'EOF'
BEGIN;
UPDATE agent_frames SET subagents_json='[]'
WHERE json_array_length(subagents_json) > 100;
COMMIT;
EOF

# 7. 啟 daemon
brew services start pdx

# 8. 觀察 1 個 cc Task cycle 後再次 query 確認沒再累積
```

**不是 plan task，主 session 在 PR-A merge 後手動執行**。後續 Phase 3 PR-B 不依賴 cleanup 結果（PR-B 是 SPA-only）。

---

## 3. Phase 3：PR-B SPA notification debounce（Subagent B）

### P3-T1 — `buildDebounceKey` + module-level state Map + `__resetDebounceStateForTests`

**目標**：spec §4.2 / §4.3 / §4.6。建立 dispatcher-private 狀態管理 + key builder。test reset helper 必要（避免 module-level Map 跨 test 污染）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/hooks/useNotificationDispatcher.ts` | +25 LOC：`const ERROR_NOTIFY_WINDOW_MS = 60_000`；`const errorDebounceState = new Map<string, { silentUntil: number }>()`；`function buildDebounceKey(ck, eventName, detailError)` JSON.stringify 三元 array；`export function __resetDebounceStateForTests()`（測試專用 export，名稱以 `__` 前綴 + `ForTests` 後綴標 deliberate test seam）|
| `spa/src/hooks/useNotificationDispatcher.test.ts` | + `describe('buildDebounceKey')`：4 case — 正常字串 / 含 `\|` 字元 / detailError undefined→empty / detailError null→empty |

**TDD 步驟**：
1. 寫 4 個 buildDebounceKey test（Red）
2. 加 const + helper + reset export（Green）
3. 跑 `cd spa && npx vitest run --reporter verbose useNotificationDispatcher` 全綠
4. **Commit**: `feat(spa): debounce key builder + dispatcher state map for error notifications`

### P3-T2 — `shouldNotify` 整合 sliding debounce

**目標**：spec §4.1 / §4.6。在 `shouldNotify` 既有檢查鏈尾端、`return true` 之前加 error-debounce gate。trailing-edge sliding：window 內 silentUntil 持續延長並 return false；window 過則 notify + reset。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/hooks/useNotificationDispatcher.ts` | +20 LOC：`shouldNotify` 函式體加段（per spec §4.6 整段 snippet）；signature 不變（既有 args 已含 `derived` / `eventName` / `compositeKey` / `detail`）|
| `spa/src/hooks/useNotificationDispatcher.test.ts` | + per spec §6.2 quantitative cases：<br>`debounce__first_error_passes`<br>`debounce__second_error_within_window_blocked_and_extends` — 用 `vi.useFakeTimers()` + `vi.setSystemTime()` 控時鐘<br>`debounce__error_after_silence_window_passes`<br>`debounce__storm_100_events_yields_one_notification`（AC5 anchor）<br>`debounce__different_keys_independent`（host / session / eventName / errorString 四個維度）<br>`debounce__key_uses_json_array_not_pipe_join`（spec key collision rebuke test）|

**Test Instrumentation 細節**：
- `vi.useFakeTimers()` + `vi.setSystemTime(Date.now() + N_ms)` 控制 `Date.now()`，避免實時 60s flake
- 100-event storm test：迴圈內 `vi.advanceTimersByTime(600)` 模擬 1.67 Hz；assert 只有第一筆 return true
- key collision test：`compositeKey="a|b"` + `eventName="c"` + `error="d|e"` vs 同 `JSON.stringify` 輸出，assert 不同 keys

**TDD 步驟**：
1. 寫 6 個 sliding debounce test case（Red）
2. 改 `shouldNotify` 加 gate（Green）
3. 跑 `cd spa && npx vitest run useNotificationDispatcher` 全綠
4. 跑 `cd spa && npx vitest run --reporter verbose --testNamePattern="storm_100"` 確認量化 test 通過
5. **Commit**: `feat(spa): trailing-edge sliding debounce for error notifications`

### P3-T3 — Cleanup 兩 path（per-session / TTL）

**目標**：spec §4.4。`clearSession` / `removeHost` 觸發時清相關 entries；`shouldNotify` 熱路徑做 TTL self-cleanup（stale entries `silentUntil < now - 5×WINDOW_MS` 移除）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/hooks/useNotificationDispatcher.ts` | +25 LOC：`function purgeDebounceForCompositeKey(ck: string)` 與 `function purgeDebounceForHost(hostId: string)` — 用 `JSON.parse(rawKey)[0]` 反解第一元素比對；註冊到 useAgentStore 的 `clearSession` / `removeHost` actions（既有 store 在 `useAgentStore.ts:102` / `:196`）。TTL self-cleanup 在 `shouldNotify` 內：第一次進 error gate 時順手 sweep 過期 keys |
| `spa/src/hooks/useNotificationDispatcher.test.ts` | + 3 case：<br>`debounce__clear_session_resets`<br>`debounce__remove_host_resets`<br>`debounce__ttl_cleanup`（fake timer 推進 5×WINDOW_MS 後檢查 Map.size 縮減） |
| `spa/src/stores/useAgentStore.ts` | （若必要）on `clearSession` / `removeHost` action 內呼叫 dispatcher 的 purge helper — **但盡量不從 store 反向依賴 dispatcher**；改成 dispatcher 內訂閱 store changes 較乾淨。具體實作走訂閱模式 vs reverse import 由 plan-review 決定（plan 默認訂閱模式） |

**Test Instrumentation 細節**：
- 訂閱模式：`useEffect`-like 在 dispatcher 初始化時 `useAgentStore.subscribe((state, prevState) => { /* diff sessions/hosts → purge */ })`
- TTL：`shouldNotify` error gate 開頭加 `for (const [k, v] of errorDebounceState) if (v.silentUntil < now - 5 * WINDOW_MS) errorDebounceState.delete(k)`；amortised cost 可接受（Map 規模封頂於 active sessions × event types）

**TDD 步驟**：
1. 寫 3 個 cleanup test case（Red）
2. 加 purge helpers（Green） + dispatcher subscribe wiring
3. 加 TTL self-cleanup（Green）
4. 跑 vitest 全綠
5. **Commit**: `feat(spa): debounce cleanup on clearSession/removeHost + TTL self-cleanup`

### P3-T4 — Unread badge / waiting / non-error 隔離測試

**目標**：spec AC6 / AC7 / §4.5 — debounce **不能** mask `unread` 寫入、不能擋 `waiting`、不能影響 hook broadcast。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/hooks/useNotificationDispatcher.test.ts` | + 2 case：<br>`debounce__waiting_not_debounced` — 同 session 連 5 次 `derived='waiting'` 都 return true<br>`debounce__unread_badge_unaffected` — error 被 debounce 後 `useAgentStore.unread[ck]` 仍 true（assertion 不在 dispatcher，是檢查 store；測試需 inject 經 `handleNormalizedEvent` 路徑） |

**TDD 步驟**：
1. 寫 2 個 case（Red）
2. （應該無實作改動 — 既有設計已隔離）（Green 直接）
3. 跑 vitest
4. **Commit**: `test(spa): debounce isolation guards (waiting / unread badge)`

### P3-T5 — PR-B push + Round 1 codex review

**步驟**：
1. 跑全 SPA test sweep：`cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build`
2. Push branch
3. `gh pr create` — title `feat(spa): trailing-edge sliding debounce for error notifications`；body 引 spec / windowMs rationale / AC checklist
4. **委派 codex round 1 standard review**: `/codex:review --base main --background`，focus 「sliding 語意 vs 直覺、key collision、cleanup 訂閱模式 vs reverse import、fake timer 是否真避免 flake」
5. 收 finding → 處理
6. 押 Round 2 adversarial：攻擊（key edge case / race / TTL 漏網之魚）+ 防守（debounce 是否該擋 unread / 是否該擋 waiting / 設計與 spec drift）+ 體質（dispatcher.ts 是否 SRP）

---

## 4. PR-A / PR-B Review 流程（共用）

兩 PR 各自走兩輪 codex review（per CLAUDE.md）。

**Round 1 standard review**：
- 觸發：`/codex:review --base main --background`
- focus：跨模型差異化 — go race、type 一致性、JSON marshal 邊界、test 是否覆蓋實作所有 branch、spec drift 檢查

**Round 2 三平行 adversarial review**：
- 觸發：`/codex:adversarial-review --base main --background <focus>` 三次（攻擊/防守/體質）
- 攻擊 focus：「race condition、null payload、time-based fake timer flake、Map memory leak、cleanup helper 反向 parse 失敗」
- 防守 focus：「PR-A 有沒有越界做 PR-B 的事；PR-B 有沒有不該 mask 的事；spec §3 / §4 邊界是否守住；既有 invariant（IsProxy / unread boolean / lifecycle / trace pipeline）零修改」
- 體質 focus：「frame_ops.go 拆 case 後檔案大小、test instrumentation 是否複雜化過頭、SRP、命名一致性」

**Finding 處理**：依 `feedback_dev_process.md` — 嚴重性信心 / 關聯 / 複雜度三維表；高關聯 + 高信心 + 低複雜聯集全修；低關聯 + 中高複雜延後（gh issue）；只有 trade-off 不確定時停下找 user。

**Convergence 規範**（per `feedback_codex_review_termination.md`）：no critical / P1 + known issue 已 follow-up issue 即可 ship。

---

## 5. Acceptance gates（map to spec §7）

| AC | Validated by | Phase |
|---|---|---|
| AC1 dthn 不再累積 | Phase 2 SQL cleanup + 1 Task cycle 觀察 + Phase 1 fixture replay test | P1-T4 + Phase 2 |
| AC2 frame==nil 路徑零 regression | `go test ./internal/module/agent/... -run "L2|Stop"` 既有 L2 test 全綠 | P1-T3 |
| AC3 no/empty agent_id legacy preserve | `TestStopFailure_NoAgentId_LegacyBehaviour` + `TestStopFailure_EmptyAgentId_LegacyBehaviour` | P1-T3 |
| AC4 no phantom detach broadcast | `TestStopFailure_NativeDetach_Misses_NoMutation` 三條 assertion | P1-T3 |
| AC5 ≥98.8% suppression | `debounce__storm_100_events_yields_one_notification` | P3-T2 |
| AC6 unread badge unaffected | `debounce__unread_badge_unaffected` | P3-T4 |
| AC7 cleanup 清 state | `debounce__clear_session_resets` + `debounce__remove_host_resets` | P3-T3 |
| AC8 lint / type clean | `go vet ./...` + `pnpm run lint` + `pnpm run build` | P1-T5, P3-T5 |
| AC9 LOC cap | PR diff `+/- LOC` ≤ 500 (PR-A) / ≤ 300 (PR-B) | P1-T5, P3-T5 |

---

## 6. Risk register（map to spec §8）+ plan-level mitigation

| # | Risk | Mitigation |
|---|---|---|
| R1 cc payload shape drift | `TestStopFailure_FixtureReplay_DthnPayload`（P1-T4）+ Round 2 adversarial 攻擊 focus 含「fixture replay 用真實 cc 9.x payload，未來 cc 10.x 改 schema 該 test 會失敗即時告警」 | P1-T4 |
| R2 false-positive native detach | pre-check 已封；Round 2 adversarial 攻擊 focus 包含「兩 subagent 同 ID」邊界 | P1-T3 |
| R3 60s window 漏 second wave storm | spec 已接受；PR-B `__resetDebounceStateForTests` + ttl_cleanup test 確保不致永久 mask | P3-T3 |
| R4 module Map memory leak | TTL test + cleanup test 雙覆蓋 | P3-T3 |
| R5 同 frame agent_id 碰撞 | cc 上游 invariant；不在我們 scope；spec §8 R5 |  |
| R6 daemon restart projection replay 大 frame 慢 | Phase 2 SQL cleanup 是 expected primary path；PR-A 阻止 re-accumulate；長期看是否需 schema-level GC 視 issue tracker | Phase 2 + 後續 issue |
| R7 opencode emission 不 emit agent_id | spec §2.2 / §9 已接受 no-op；derive change 仍 ship 為 future-proof | P1-T1 |

---

## 7. Test Instrumentation Strategy（spec §6.1 codex round-3 P2）

**問題**：spec §6.1 `TestStopFailure_NativeDetach_Misses_NoMutation` 的 assertion (a) 「subagents bit-identical」不夠 — miss-ref 走 mutate 也會寫回相同 slice。

**Plan-level decision**：用 **fake frame store with call counter**：

```go
type counterFrameStore struct {
    inner store.FrameStore
    UpsertCallCount int
    GetCallCount    int
}
func (c *counterFrameStore) UpsertIfUnchanged(f store.Frame, expected int64) (bool, store.Frame, error) {
    c.UpsertCallCount++
    return c.inner.UpsertIfUnchanged(f, expected)
}
// ... GetByIdentity / ListByPane 透傳 ...
```

於 test setup wrap production store；assertion 寫 `require.Equal(t, 0, fakeStore.UpsertCallCount, "no UpsertIfUnchanged call expected when ref absent")`。

**Placement**：`internal/module/agent/fakes_test.go`（既有 test fakes 集中地）— 加 `counterFrameStore` type，方便其他 test 復用 call counter pattern。

**驗證**：`TestStopFailure_NativeDetach_Misses_NoMutation` 用此 fake；`TestStopFailure_NativeDetach_Hits` 反向用同 fake assert UpsertCallCount > 0（cross-validate fake 本身正確）。

---

## 8. Convergence checklist（plan freeze）

進實作前必滿足：

- [x] spec v3 + follow-ups 三輪 codex review approve to enter plan
- [ ] 本 plan v1 → codex round 1 review 收斂（task #4）
- [ ] 必要時 plan v2 → codex round 2 review 0 blocker（依風險判斷）
- [ ] 啟 P1-T1 subagent

PR push 前必滿足：

- [ ] PR-A 全套測試綠 + race-mode 10 輪 + lint + vet
- [ ] PR-B 全套測試綠 + lint + build
- [ ] 改動 LOC 在 spec AC9 cap 內
- [ ] commit history 每 task 獨立 commit

PR merge 前必滿足：

- [ ] Round 1 codex standard review 收斂
- [ ] Round 2 三平行 adversarial review 收斂
- [ ] 高關聯 / 高信心 / 低複雜 finding 全修
- [ ] 其他 finding 開 issue 追蹤
- [ ] AC1-AC9 對應 test 全綠
