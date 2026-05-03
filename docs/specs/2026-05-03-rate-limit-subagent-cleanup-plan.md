# Rate-limit subagent cleanup + notification debounce — Implementation Plan

> **Status**：v3（plan-review round 2 收 1 P1 + 3 P2 + 1 nit；v3 全採納；待 round 3 final approve）
>
> v2 → v3 修：
> - **P1 Phase 2 restore WAL**：restore 前必先 `rm -f .db-wal .db-shm`，避免新 `.db` 與舊 WAL 不一致；指令補進 §2 流程
> - **P2 §7 test 命名語意過強**：`TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace` → `TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace`。`FrameTraceMeta.Reason` 比對證明的是「沒走 detach branch」而非嚴格「mutate 沒被 call」；前者已足以鎖住 spec §3.2 行為契約
> - **P2 AC 表 phase/task 編號 stale**：v2 把 P3-T1+T2 合併、P3-T3→T2、T4→T3、T5→T4；AC table 對應修正（AC5→P3-T1 / AC6→P3-T3 / AC7→P3-T2 / AC8→P3-T4 / AC9→P3-T4）
> - **nit §7 helper 名稱**：example 改用既有 helper 名稱 `newTestModule` / `seedFrame` / `seedFrameWithSubagents` / `m.frames.GetByIdentity`，避免 subagent 誤找

> **Previous status**：v2（plan-review round 1 收 3 P1 + 5 P2 + 4 fact + 2 nit；v2 全採納）
>
> v1 → v2 修：
> - **P1.1 §7 test instrumentation**: `counterFrameStore` 設計不可行（`Module.frames` 是 `*store.FramesStore` concrete type，無 interface 可 wrap）。改用 `applyFrameEvent` 回傳的 `FrameTraceMeta.Reason` 直接驗證 — `Misses_NoMutation` assert `Reason ∈ {"parent_frame_found", "daemon_restart_recovery", "no_parent_fallback"}`（generic post-switch path），`Hits` assert `Reason == "native_subagent_detached_on_stop_failure"`. 完全可從單 unit test 驗證、無需 store-level fake.
> - **P1.2 §4 P3-T2**: `shouldNotify` signature 必須加 `detail` 或 `errorString` 參數（既有 `ShouldNotifyParams` 沒帶 `detail`）。改動表更新含 caller signature 變更.
> - **P1.3 Phase 2 backup**: `.mode insert <table>` 不可行（無對應表）。改用 `sqlite3 .backup` 整庫備份；補 restore SQL.
> - P2: P1-T3 拆兩 commit (case-split-only / native-detach-impl) 降 review surface
> - P2: P3-T1 + P3-T2 合併（key builder + state 沒行為，與 shouldNotify integration 同 commit 較自然）
> - P2: P3-T2 明說 `eventName` 用 normalized（與 dispatcher 既有 normalize pattern 對齊；`PdxStopFailure` 與 `StopFailure` 共 bucket — acceptable）
> - P2: §4 Round 2 adversarial 防守 focus 加具體 spec drift gate（PR-A diff 不 touch SPA / PR-B 不 touch daemon）
> - P2: Phase 2 重寫 restart 行為描述（不靠 in-memory map reload，靠下次 projection 從 DB 投影）
> - fact 採納（無修改），nit 修正 case count + `seenAtRef` 描述


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
- **State 為 module-level 不進 zustand**（spec §4.3）— dispatcher-private ephemeral state，無 cross-component subscription、無持久化；test 用 `__resetDebounceStateForTests()` 清狀態

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

### P1-T3a — `applyFrameEvent` 拆 `LifecycleStopFailure` 獨立 case（split-only，零行為改動）

**目標**：把現行 `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` 拆成兩個 case，body 完全相同（從聯合 case 完整複製）。**這 commit 不引入新邏輯**，純結構手術，方便 P1-T3b 在乾淨基礎上加 native detach 邏輯。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go` | line 288 從聯合 case 拆成 `case agentpkg.LifecycleStopFailure:` 與 `case agentpkg.LifecycleStop:` 兩 case，body 各自一份完整副本（duplication 暫時接受 — P1-T3b 馬上會在 StopFailure 分支加新邏輯，並在 frame==nil 走 fallthrough 收回 duplication） |
| 無 test 改動 | 既有 `frame_ops_l2_test.go` / `handler_test.go` 全套既有 test 做 regression guard |

**TDD 步驟**：
1. 拆 case，body 重複
2. 跑 `go test ./internal/module/agent/...` 全套 — 期望全綠（零行為改動）
3. 跑 `go test ./internal/module/agent/... -count=10 -race`
4. Lint + vet
5. **Commit**: `refactor(daemon): split LifecycleStopFailure case from LifecycleStop (no behaviour change)`

### P1-T3b — `LifecycleStopFailure` 加 native detach 邏輯 + fallthrough

**目標**：spec §3.2 全套。在 P1-T3a 的乾淨基礎上：
- `case LifecycleStopFailure:` — 新增 `frame != nil` 分支：pre-check + mutate + trace reason `native_subagent_detached_on_stop_failure`；mismatched / no agent_id → break；frame == nil → `fallthrough` 進 `LifecycleStop` body 收回 P1-T3a 的 duplication
- `case LifecycleStop:` body 不變（已 P1-T3a 拆出）

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go` | `LifecycleStopFailure` body 重寫成 spec §3.2 完整 snippet：(1) `frame != nil` 分支 pre-check via `findNativeRefByID` (Phase P1-T2 已加) (2) 命中 → `mutateSubagentsWithRetry(LifecycleSubagentStop, ref)` → 報 `Decision="updated_frame"` `Reason="native_subagent_detached_on_stop_failure"` 或 `Reason="frame_missing"`（concurrent delete） (3) 不命中 / 缺 / 空 agent_id → `break`（走 generic post-switch） (4) `frame == nil` → `fallthrough` 進 `LifecycleStop` 既有 codex/proxy detach 路徑（P1-T3a 拆出的副本被 fallthrough 收掉）|
| `internal/module/agent/frame_ops_test.go` | + 6 case，per spec §6.1（直接表）：<br>`TestStopFailure_NativeDetach_Hits` — assert `meta.Reason == "native_subagent_detached_on_stop_failure"` + DB ref 確實被移除<br>`TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace` — assert `meta.Reason ∈ {"parent_frame_found", "daemon_restart_recovery", "no_parent_fallback"}` + DB Status=error / LastSeenAt 已 refresh（per §7 instrumentation）<br>`TestStopFailure_NoAgentId_LegacyBehaviour` — payload 缺 `agent_id` field；同 Misses behaviour<br>`TestStopFailure_EmptyAgentId_LegacyBehaviour` — payload `agent_id: ""`；同 Misses behaviour<br>`TestStopFailure_PreservesProxyRefs` — frame 同時有 native + proxy；只 native 被 detach<br>`TestStopFailure_FrameNil_FallthroughToProxyDetach` — frame==nil，走原 codex turn-aware proxy detach；assert reason `proxy_subagent_detached_on_stop` (or family)<br>`TestStopFailure_FrameDeletedMidFlight` — pre-check pass 但 mutate 回 `applied=false`；trace reason `frame_missing` |

**TDD 步驟**：
1. 寫 7 個 test case（紅）
2. 加 native detach 邏輯（綠）
3. 加 fallthrough，把 P1-T3a 的副本收掉（綠）
4. 跑 `go test ./internal/module/agent/... -run TestStopFailure -v` 全綠
5. 跑 `go test ./internal/module/agent/... -count=10 -race` race-mode 10 輪
6. 跑 `go test ./internal/module/agent/...` 全套確保 `frame_ops_l2_test.go` 既有 L2 test 零 regression（spec AC2）
7. Lint + vet
8. **Commit**: `feat(daemon): native subagent detach on PdxStopFailure (closes dthn-class accumulation)`

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

PR-A merge 後依 spec §5 操作序執行。**這不是 plan task**；主 session 在 PR-A merge 後手動執行。後續 Phase 3 PR-B 不依賴 cleanup 結果（PR-B 是 SPA-only）。

```bash
# 1. 確認版本
pdx --version  # 應 ≥ 下一個 alpha

# 2. 確認沒有第二個 daemon 在跑
pgrep -lf 'pdx serve'   # 只有一個 PID

# 3. 停 daemon
brew services stop pdx  # 或 kill <pid>

# 4. 整庫備份（PR-A merge 是不可逆 SQL UPDATE 前的最後保障）
mkdir -p ~/.config/pdx/backups
TS=$(date +%Y-%m-%d-%H%M%S)
sqlite3 ~/.config/pdx/agent_events.db ".backup '/Users/wake/.config/pdx/backups/agent_events-pre-cleanup-${TS}.db'"
# Verify backup readable
sqlite3 ~/.config/pdx/backups/agent_events-pre-cleanup-${TS}.db "SELECT COUNT(*) FROM agent_frames;"

# 5. 檢查影響面
sqlite3 ~/.config/pdx/agent_events.db "SELECT pane_id, agent_type, json_array_length(subagents_json) AS n_refs FROM agent_frames WHERE json_array_length(subagents_json) > 100 ORDER BY n_refs DESC;"

# 6. Cleanup（transaction）
sqlite3 ~/.config/pdx/agent_events.db <<'EOF'
BEGIN;
UPDATE agent_frames SET subagents_json='[]'
WHERE json_array_length(subagents_json) > 100;
COMMIT;
EOF

# 7. 啟 daemon — 注意：daemon restart 不會「預載」in-memory frame state；
#    下一次 hook event / projection 查詢觸發 m.frames.ListByPane / GetByIdentity
#    才會從 DB 投影出當前狀態。SPA 透過下一次 hook broadcast 收到清空後的 subagents。
brew services start pdx

# 8. 觀察 1 個 cc Task cycle 後再次 query 確認沒再累積
sqlite3 ~/.config/pdx/agent_events.db "SELECT pane_id, json_array_length(subagents_json) FROM agent_frames WHERE json_array_length(subagents_json) > 0;"
```

**Restore 步驟**（若 cleanup 後出現問題需回滾）：

```bash
# 停 daemon
brew services stop pdx
# 移除舊 WAL/SHM — 否則新 .db 與舊 WAL 不一致，啟動可能讀到 stale state
rm -f ~/.config/pdx/agent_events.db-wal ~/.config/pdx/agent_events.db-shm
# 整庫覆蓋（最後備份的時間戳依 step 4 實際輸出）
cp ~/.config/pdx/backups/agent_events-pre-cleanup-2026-05-03-XXXXXX.db ~/.config/pdx/agent_events.db
# 啟動 — daemon 第一次寫入時會重建空的 .db-wal / .db-shm
brew services start pdx
```

**WAL 檔注意**：
- `.backup` API 產生 source DB 的一致 snapshot（含 WAL 中已提交內容），備份時 daemon 已停所以新寫入也不會發生 — `.backup` 本身正確
- **Restore 時必須先刪舊 `.db-wal` / `.db-shm`**，因為這兩檔是 persistent state 的一部分；單獨覆蓋 `.db` 會讓新 DB 與舊 WAL 不一致，啟動讀到混合狀態（rare 但 destructive）
- 不需要在 `.backup` 前強制 `WAL checkpoint`；`.backup` 已是正確 snapshot 路徑（見 [SQLite backup API](https://www.sqlite.org/backup.html) + [WAL doc](https://www.sqlite.org/wal.html)）

---

## 3. Phase 3：PR-B SPA notification debounce（Subagent B）

### P3-T1 — `buildDebounceKey` + state Map + `shouldNotify` 整合 sliding debounce（合併原 v1 P3-T1 + P3-T2）

**目標**：spec §4.1 / §4.2 / §4.3 / §4.6。建立 dispatcher-private 狀態 + key builder + sliding gate 整合到 `shouldNotify`。一個 commit — key builder + state 沒有獨立行為，與 shouldNotify integration 同 commit 較自然。

**Signature change**：既有 `ShouldNotifyParams`（[useNotificationDispatcher.ts:55](spa/src/hooks/useNotificationDispatcher.ts) 確認）**沒有 `detail`**，只有 `notificationSilent`。本 task 必須加新欄位 — 兩選一：

- **選項 A（推薦）**：`ShouldNotifyParams` 加 `errorString?: string`，由 caller (line 113-128) 從 `event.detail?.error` 抽出、normalize 成 string 後傳入。優點：`shouldNotify` 簽章接收 plain string、好 unit test
- **選項 B**：傳整個 `event.detail`，`shouldNotify` 內 `String(detail?.error ?? '')`。簽章彈性但 caller 邊界較髒

選項 A，理由：與既有 `notificationSilent` boolean 對齊（caller 抽出後傳 plain primitive），測試 setup 簡潔。

**Event name 用 normalized 還是 raw**：spec 說 `raw_event_name`，但 dispatcher [既有 normalize pattern](spa/src/hooks/useNotificationDispatcher.ts:65) 在 `shouldNotify` 入口先 normalize。**plan v2 決策**：用 normalized，與既有 dispatcher 行為對齊。語意：`PdxStopFailure` 與 legacy `StopFailure`（W2 transition 期間）共 debounce bucket — 可接受，因為這兩個其實是同一事件的兩個命名 phase。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/hooks/useNotificationDispatcher.ts` | ~50 LOC：<br>(1) `const ERROR_NOTIFY_WINDOW_MS = 60_000`<br>(2) `const errorDebounceState = new Map<string, { silentUntil: number }>()`<br>(3) `function buildDebounceKey(ck: string, eventName: string, errorString: string): string { return JSON.stringify([ck, eventName, errorString]) }`<br>(4) `export function __resetDebounceStateForTests()` 清 Map（測試專用 export，`__` + `ForTests` 命名標 deliberate test seam）<br>(5) `ShouldNotifyParams` 加 `errorString?: string`<br>(6) `useNotificationDispatcher` 主體 caller 從 `event.detail?.error` 抽 → `String(... ?? '')` → 傳入 `shouldNotify`<br>(7) `shouldNotify` 函式體加 sliding gate（per spec §4.6 snippet 但用 `errorString` 參數而非 `args.detail?.error`）|
| `spa/src/hooks/useNotificationDispatcher.test.ts` | + per spec §6.2 全表 quantitative cases：<br>`debounce__first_error_passes`<br>`debounce__second_error_within_window_blocked_and_extends` — `vi.useFakeTimers()` + `vi.setSystemTime()` 控時鐘<br>`debounce__error_after_silence_window_passes`<br>`debounce__storm_100_events_yields_one_notification`（AC5 anchor）<br>`debounce__different_keys_independent`<br>`debounce__key_uses_json_array_not_pipe_join`（spec key collision rebuke test）<br>+ `describe('buildDebounceKey')` 4 sub-case（正常 / 含 `\|` / undefined / null）|

**Test Instrumentation 細節**：
- `vi.useFakeTimers()` + `vi.setSystemTime()`：避實時 60s 等待 / flake
- 100-event storm test：迴圈內 `vi.advanceTimersByTime(600)` 模擬 1.67 Hz；assert 只第一筆 `shouldNotify` return true
- key collision test：`ck="a|b"` + `eventName="c"` + `errorString="d|e"` vs `ck="a"` + `eventName="b|c"` + `errorString="d|e"`，assert 不同 JSON output

**TDD 步驟**：
1. 寫 10 個 test case（含 buildDebounceKey 4 sub-case）（Red）
2. 加 const + helper + reset export + signature change + caller wiring + shouldNotify gate（Green）
3. 跑 `cd spa && pnpm install && npx vitest run --reporter verbose useNotificationDispatcher` 全綠
4. 跑 `cd spa && pnpm run lint && pnpm run build` 確保 type 正確（signature change ripple 不破其他 caller）
5. **Commit**: `feat(spa): trailing-edge sliding debounce for error notifications`

### P3-T2 — Cleanup 兩 path（per-session / TTL）

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

### P3-T3 — Unread badge / waiting / non-error 隔離測試

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

### P3-T4 — PR-B push + Round 1 codex review

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
- 攻擊 focus：「race condition、null payload、time-based fake timer flake、Map memory leak、cleanup helper 反向 parse 失敗、空 agent_id / 異常 JSON 字符的 buildDebounceKey 行為、StopFailure trigger 與 SubagentStop 同 ID 同時 race 的 idempotency」
- 防守 focus：「**diff 邊界守住**：PR-A diff 不應 touch `spa/`；PR-B diff 不應 touch `internal/` 或 `cmd/`。**Invariant 守住**：IsProxy attach/detach 路徑零修改、unread boolean 寫入路徑零修改、L2 turn-aware proxy detach 零 regression、`frame_ops_l2_test.go` 既有測試全綠、既有 trace reason 字面零碰撞、`useNotificationDispatcher` 既有 caller signature 改動 ripple 沒破其他元件。**Spec drift 守住**：spec §3 / §4 邊界、Out-of-scope §9 沒被偷渡」
- 體質 focus：「frame_ops.go 拆 case 後檔案大小、test instrumentation 是否複雜化過頭、SRP、命名一致性、`__resetDebounceStateForTests` 是否暴露過多測試 hook、PR-A 與 PR-B 各自 LOC 是否真的在 spec AC9 cap 內」

**Finding 處理**：依 `feedback_dev_process.md` — 嚴重性信心 / 關聯 / 複雜度三維表；高關聯 + 高信心 + 低複雜聯集全修；低關聯 + 中高複雜延後（gh issue）；只有 trade-off 不確定時停下找 user。

**Convergence 規範**（per `feedback_codex_review_termination.md`）：no critical / P1 + known issue 已 follow-up issue 即可 ship。

---

## 5. Acceptance gates（map to spec §7）

| AC | Validated by | Phase |
|---|---|---|
| AC1 dthn 不再累積 | Phase 2 SQL cleanup + 1 Task cycle 觀察 + Phase 1 fixture replay test | P1-T4 + Phase 2 |
| AC2 frame==nil 路徑零 regression | `go test ./internal/module/agent/... -run "L2|Stop"` 既有 L2 test 全綠 | P1-T3a + P1-T3b |
| AC3 no/empty agent_id legacy preserve | `TestStopFailure_NoAgentId_LegacyBehaviour` + `TestStopFailure_EmptyAgentId_LegacyBehaviour` | P1-T3b |
| AC4 no phantom detach broadcast | `TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace` 三條 assertion | P1-T3b |
| AC5 ≥98.8% suppression | `debounce__storm_100_events_yields_one_notification` | P3-T1 |
| AC6 unread badge unaffected | `debounce__unread_badge_unaffected` | P3-T3 |
| AC7 cleanup 清 state | `debounce__clear_session_resets` + `debounce__remove_host_resets` | P3-T2 |
| AC8 lint / type clean | `go vet ./...` + `pnpm run lint` + `pnpm run build` | P1-T5, P3-T4 |
| AC9 LOC cap | PR diff `+/- LOC` ≤ 500 (PR-A) / ≤ 300 (PR-B) | P1-T5, P3-T4 |

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

## 7. Test Instrumentation Strategy（spec §6.1 codex round-3 P2 + plan round-1 P1.1）

**問題**：spec §6.1 `TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace` 的 assertion 「subagents bit-identical / no-mutate-call」需要可驗證機制。原 v1 草案 `counterFrameStore` fake **不可行** — `internal/module/agent/module.go:31` 是 `frames *store.FramesStore` concrete type，沒有 `store.FrameStore` interface 可包；`fakes_test.go` 也沒有 frame-store fake 可擴展。

**Plan v2 decision**：用 **`applyFrameEvent` 回傳的 `FrameTraceMeta.Reason` 行為差異**驗證。spec §3.2 兩 path 自然 emit 不同 trace reason：

| Path | Decision | Reason | Notes |
|---|---|---|---|
| `Hits`（agent_id 命中 native ref） | `updated_frame` | `native_subagent_detached_on_stop_failure` | 新增 reason |
| `Misses_NoMutation`（agent_id 不命中 / 缺 / 空） | `updated_frame` | `parent_frame_found` 或 `daemon_restart_recovery` 或 `no_parent_fallback` | 既有 reason，由 generic post-switch path 在 `frame_ops.go:771-775` 賦值 |
| `FrameDeletedMidFlight` | `skipped` | `frame_missing` | 既有 reason |
| `FrameNil_FallthroughToProxyDetach` | depends on proxy detach inner branch | `proxy_subagent_detached_on_stop` 或 `proxy_subagent_stop_no_match` 等 | 既有 reason，零修改 |

**Test pattern**：直接呼叫 `m.applyFrameEvent(req, result, ts)` → 取回傳 `FrameTraceMeta` → assert `Reason` 值。無需 store-level wrap 或 spy；既有 `frame_ops_test.go` 已用這 pattern（多處）。

```go
func TestStopFailure_NativeDetach_Misses_NoNativeDetachTrace(t *testing.T) {
    m := newTestModule(t)
    frame := seedFrame(t, m, "cc", agent.StatusRunning)
    // seed frame with a native ref whose ID will NOT match the StopFailure payload
    seedFrameWithSubagents(t, m, frame, []agent.SubagentRef{{ID: "no-match-id"}})

    req := buildPdxStopFailureRequest(frame.PaneID, "different-id", frame.PID, frame.ProcessStartTime)
    result := agent.DeriveResult{Valid: true, Status: agent.StatusError, Detail: map[string]any{"agent_id": "different-id", "error": "rate_limit"}}
    preTs := time.Now().UnixNano()
    _, meta, err := m.applyFrameEvent(req, result, preTs)
    require.NoError(t, err)

    // (a) ref-presence pre-check did NOT match → no native detach trace step:
    require.NotEqual(t, "native_subagent_detached_on_stop_failure", meta.Reason)
    // (b) generic post-switch path executed (legacy behaviour preserved):
    require.Equal(t, "updated_frame", meta.Decision)
    require.Contains(t, []string{"parent_frame_found", "daemon_restart_recovery", "no_parent_fallback"}, meta.Reason)
    // (c) frame Status now error, LastSeenAt refreshed (verify via DB read):
    refreshed, err := m.frames.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
    require.NoError(t, err)
    require.NotNil(t, refreshed)
    require.Equal(t, agent.StatusError, refreshed.Status)
    require.GreaterOrEqual(t, refreshed.LastSeenAt, preTs)
    // ref still present (because not matched)
    require.Len(t, refreshed.Subagents, 1)
    require.Equal(t, "no-match-id", refreshed.Subagents[0].ID)
}
```

```go
func TestStopFailure_NativeDetach_Hits(t *testing.T) {
    m := newTestModule(t)
    frame := seedFrame(t, m, "cc", agent.StatusRunning)
    seedFrameWithSubagents(t, m, frame, []agent.SubagentRef{{ID: "match-id"}})

    req := buildPdxStopFailureRequest(frame.PaneID, "match-id", frame.PID, frame.ProcessStartTime)
    result := agent.DeriveResult{Valid: true, Status: agent.StatusError, Detail: map[string]any{"agent_id": "match-id", "error": "rate_limit"}}
    _, meta, err := m.applyFrameEvent(req, result, time.Now().UnixNano())
    require.NoError(t, err)
    require.Equal(t, "updated_frame", meta.Decision)
    require.Equal(t, "native_subagent_detached_on_stop_failure", meta.Reason)
    refreshed, err := m.frames.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
    require.NoError(t, err)
    require.NotNil(t, refreshed)
    require.Empty(t, refreshed.Subagents)  // ref actually removed
}
```

**Cross-validation**：兩 test reciprocally guarantee — `Hits` 沒 emit 新 reason 時就是 bug 在於 pre-check 過嚴；`Misses` emit 新 reason 時就是 bug 在於 pre-check 沒擋住誤判。Reason mismatch 是直接、確定的失敗信號。

**測試保證範圍誠實標示**：`FrameTraceMeta.Reason` 比對證明的是「沒走 detach branch」，**不嚴格**證明「`mutateSubagentsWithRetry` 沒被呼叫」（理論上 miss-ref mutate 也會寫回等價 slice + refresh LastSeenAt，但 spec §3.2 設計 break 路徑就是不 call mutate，所以行為契約已鎖住）。要進一步嚴格驗證 mutate-not-called 需要 instrumentation 級改動（`Module.frames` 抽 interface）— 本 PR 不擴大 scope。

**Placement / helper 真實位置**：
- `newTestModule(t)` — `internal/module/agent/handler_test.go`
- `seedFrame(t, m, agentType, status)` 與 `seedFrameWithSubagents(t, m, frame, refs)` — `internal/module/agent/frame_ops_test.go`
- `buildPdxStopFailureRequest(...)` 不存在；實作時 inline 構造 `EventRequest`（payload shape per spec §1.2 fixture）或新建 helper

**實作前必跑**：subagent 在 P1-T3b 開頭先 `grep -n "func newTestModule\|func seedFrame" internal/module/agent/`，確認既有 signature；範例 code 是 illustrative，最終 call site 以 grep 結果為準。

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
