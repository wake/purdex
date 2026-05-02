# Lights Bash Sniff Delegating — Implementation Plan

> **Status**：v3（plan-review round 2 採納 1 high + 1 minor 全收斂；plan freeze）
>
> v1 → v2 修：B1 mark/unmark 不再 reuse `mutateSubagentsWithRetry`（updateSubagents 不支援 in-place mutation），改 mirror `upsertProxyRefForBroker:1245` pattern 走 dedicated UpsertIfUnchanged retry loop；B2 helper signature 加 `(senderPID, senderStartTime)` 並內部用 `GetByIdentity` 找 frame；F4 P3-T1 加非-cc agent_type explicit gating tests；F6 handler placement 明寫 separate adjacent block（不 nest 在 PathHint conditional 內）；F8 mlab live verify 必在 PR push 前完成並更新 spec L6 wording
>
> v2 → v3 修：F9 撤回——P6-T3 adversarial review 改回 always run（不 conditional）對齊 repo 兩輪 review 流程；test count/run regex 不一致 cleanup（P2 統一 12 cases + regex 加 `TestSubagentStop`；P3 統一 8 cases）
> **依賴 spec**：`docs/specs/2026-05-03-lights-bash-sniff-delegating-spec.md` v2 final（spec round 2 codex review 0 blocker）
> **Worktree**：`.claude/worktrees/lights-bash-sniff-delegating` / branch `worktree-lights-bash-sniff-delegating`
> **Base**：`origin/main` @ alpha.283（L2 `d9db812d` + bump `9374bf88`）
> **拆分**：4 個 phase 序列執行（資料 schema → daemon helpers → handler wiring → SPA 層）+ 整合驗證；每 task 獨立 commit；單一 subagent owner per phase（避 file 寫衝突）
> **Issue**：[#821](https://github.com/wake/purdex/issues/821)

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1 / §3）

Lights bash sniff delegating — 在 cc subagent 透過 Bash invoke `codex-companion.mjs` 時，於 cc 父 frame 的對應 SubagentRef 上掛 `Delegating=true` flag，SPA 渲染為橘色 dot；PostToolUse / PostToolUseFailure 配對清除；不污染 IsProxy invariant。

**核心 in-scope**（spec §3）：
- #1：`SubagentRef` 加 `Delegating bool` + `DelegatingToolUseIDs []string` 兩個 omitempty 欄位（§3.1）
- #2：`internal/module/agent/delegation_extractor.go` 純函式 `ExtractDelegationHint`（§3.4）含 token-boundary 檢測（§3.3）
- #3：`internal/module/agent/frame_ops.go` 兩 helper：`markDelegatingRef` / `unmarkDelegatingRef`（§3.5），用 `mutateSubagentsWithRetry` 走 optimistic concurrency
- #4：`internal/module/agent/handler.go` wiring 三事件：`PdxPreToolUse` (mark) / `PdxPostToolUse` + `PdxPostToolUseFailure` (unmark) — 緊鄰既有 PathHint 區塊（handler.go:195-202）
- #5：SPA 層更新 `SubagentRef` type、`SubagentDots.tsx` 渲染條件 `(is_proxy || delegating) ? PROXY_COLOR : NATIVE_COLOR`、新增 `data-delegating` 測試屬性

**Out-of-scope reaffirm**（spec §3.7）：
- 不解 codex 端真實 lifecycle（governance P3 才解）
- 不取代 governance phase
- 不改 `IsProxy` 任何 attach 路徑（保持 invariant）
- 不偵測 codex CLI 直接呼叫（非 codex-companion）
- 不處理 opencode 委派（agent_type gate）
- 不為 background Bash `BashOutput` 輪詢做特殊追蹤（spec L6 已知限制）
- 不處理 `PdxPostToolBatch`（Task tool semantic，非 Bash）

### 0.2 估計

- 總 production code：~210 行（subagent.go +8 / delegation_extractor.go +90 / frame_ops.go +70 / handler.go +35 / spa +10）
- 總 test code：~410 行（extractor +180 / frame_ops +130 / handler +60 / spa +40）
- 預估 PR diff：~620 raw production+tests / ≤700 effective（spec AC11 cap）+ ~850 spec/plan docs
- 預估時間：4-6 小時 subagent TDD（Phase 1 ~1.5hr / Phase 2 ~1.5hr / Phase 3 ~1.5hr / Phase 4 ~1hr）+ 1 輪 standard codex review + 1 輪 adversarial review (~2-3hr）+ mlab live verify ~30min

### 0.3 鎖序與不變式（per spec §3）

實作時必持守：
- **`IsProxy` 完全不動**：daemon 只在 `delegation_extractor` / `markDelegatingRef` / `unmarkDelegatingRef` / `handler` 三地對 `Delegating` + `DelegatingToolUseIDs` 兩個新欄位讀寫；既有 `findProxyParent` / `attachProxyRefWithRetry` / `removeProxyRefForSender*` / `canonicalizeDescendantsAfterUpsert` / `pruneDeadProxyRefs` / L2 turn-aware path **零修改**
- **Invariant `Delegating == len(DelegatingToolUseIDs) > 0`**：mark/unmark 兩 helper 必持守；slice append 走 dedupe；slice remove 走 linear scan；recompute Delegating 走 `len() > 0`
- **Dedicated retry loop（B1 修正）**：mark/unmark 兩 helper **不能** reuse `mutateSubagentsWithRetry`——既有 helper 經 `updateSubagents` 只支援 SubagentStart append-if-missing 與 SubagentStop remove-matching，**沒有 in-place mutation 既有 ref 欄位的能力**（`frame_ops.go:825-840`）。改 mirror `upsertProxyRefForBroker:1245-1297` 的 pattern：直接 `copy(next, current.Subagents)` → mutate `next[idx].DelegatingToolUseIDs / .Delegating` → `UpsertIfUnchanged(current, expected.LastSeenAt)`，衝突走 `GetByIdentity` reload 重試。`proxyUpsertMaxAttempts=3` 的 retry cap 套用相同值
- **Helper signature 含 frame identity（B2 修正）**：mark/unmark 兩 helper 接 `(paneID string, senderPID int, senderStartTime string, agentID, toolUseID string, broadcastTs int64) error`——內部用 `GetByIdentity(paneID, senderPID, senderStartTime)` 找 cc frame；session-code resolve 由 caller (handler) 做，helper 純粹做 frame mutation
- **token detection 純函式**：`containsCodexCompanionToken` 不依賴外部 state，可全 unit test 覆蓋；無 regex（避 ReDoS）
- **handler wiring 緊鄰 PathHint**：新邏輯放在 handler.go:195-202 既有 cc PdxPreToolUse / PdxPostToolUse 區塊**之後**（同 if 條件），確保不影響既有 PathHint 流；`PdxPostToolUseFailure` wiring 不需動 cc events catalog（已存在 line 125-131）
- **SPA 層分離測試屬性**：`data-is-proxy` 不動（保留既有測試介面），新加 `data-delegating`；測試 case 改 ref shape 而非改 attribute name
- **`mutateSubagentsWithRetry` race scope 不誇大**：只 cover「ref 已存在後的 concurrent writes」；PreToolUse 在 SubagentStart 之前到達 → 靜默 no-op（spec L7 / regression #12）

### 0.4 Spec 收斂歷程備註

Spec v1 → v2 共 1 輪 codex review 收斂（B1 token detection / B2 slice / B3 PostToolUseFailure / M1-M2 / F1-F5 / AC11 統一 / regression matrix +3 row / size estimate 修正）。Spec round 2 codex review 0 blocker，2 minor wording fix 已 inline 修正（spec §1 開頭 size estimate stale + §2.4 dispatch sites wording）。

**不再審 spec**，剩餘風險靠 PR review 兜底（spec §11 freeze checklist 最末條 — PR 兩輪 codex review）。

---

## 1. Phase 1：資料 schema + 純函式 extractor（Subagent A）

可獨立完成。Subagent A 拿 P1-T1 + P1-T2，每 task 獨立 commit。

### P1-T1 — `SubagentRef` 加兩個 omitempty 欄位

**目標**：在 `internal/agent/subagent.go::SubagentRef` 加 `Delegating bool` + `DelegatingToolUseIDs []string`，跟著既有 `SourceTurnID` 的 omitempty 模式（同檔頭註解），無 DB migration。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/subagent.go` | `SubagentRef` 加兩欄；header 註解加一段：「`Delegating` / `DelegatingToolUseIDs`：cc subagent 跑 Bash invoking codex-companion 時的旁路 flag，與 `IsProxy`（cross-type PPID 驗證）正交。Invariant：`Delegating == len(DelegatingToolUseIDs) > 0`，由 `markDelegatingRef`/`unmarkDelegatingRef` 共同維持」 |
| `internal/agent/subagent_test.go` | 加 `TestSubagentRef_DelegatingFields_OmitemptyJSON` —— Marshal 空值 ref，確認 `delegating` / `delegating_tool_use_ids` key 不出現；Marshal 帶值 ref 確認都出現 |

**TDD 步驟**：
1. 寫 test：`TestSubagentRef_DelegatingFields_OmitemptyJSON`（Red）
2. 加欄位 + JSON tag（Green）
3. Lint 跑 (Refactor)
4. **Commit**: `feat(daemon): SubagentRef adds Delegating + DelegatingToolUseIDs fields`

### P1-T2 — `delegation_extractor.go` 純函式 + token detection + 全 table-driven 測試

**目標**：實作 `ExtractDelegationHint` pure function（spec §3.4）+ `containsCodexCompanionToken` token-boundary helper（spec §3.3）。完全可單獨 unit test，無 daemon state 依賴。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/delegation_extractor.go`（新檔） | 約 90 LOC：`DelegationHint` struct（4 欄）、`ExtractDelegationHint(rawEvent, eventName) (DelegationHint, bool)`、`containsCodexCompanionToken(command string) bool`。常數復用 `MaxRawEventBytes` from `path_hint_extractor.go:15`；defenses 對齊（agent_id / tool_use_id 拒控制字元、bound 長度）。Switch 三 eventName：`PdxPreToolUse` / `PdxPostToolUse` / `PdxPostToolUseFailure`。Pre 命中要求 `IsCodexMark = containsCodexCompanionToken(command)`；Post 兩 event 統一回 `IsUnmark = true, IsCodexMark = false` |
| `internal/module/agent/delegation_extractor_test.go`（新檔） | 約 180 LOC：`TestExtractDelegationHint_Cases` table-driven 14 cases（spec §6.1 全表 + 邊界）；`TestContainsCodexCompanionToken_Cases` table-driven 9 cases（spec §3.3 全表 + 邊界 like `mjsx` / `mjs.bak` / EOF / 多 occurrence）|

**TDD 步驟**：
1. 寫 14 個 ExtractDelegationHint test case（全 Red）
2. 寫 9 個 containsCodexCompanionToken test case（Red）
3. 實作 `containsCodexCompanionToken`（先 Green token detection）
4. 實作 `ExtractDelegationHint` 解 raw JSON / 套 token check / 套 defenses（Green）
5. 跑 `go test ./internal/module/agent/... -run TestExtractDelegationHint -v` 全綠
6. Lint
7. **Commit**: `feat(daemon): add ExtractDelegationHint pure function with token-boundary detection`

---

## 2. Phase 2：daemon helpers — markDelegatingRef / unmarkDelegatingRef（Subagent B）

Phase 1 全 commit 後啟動，因為 P2-T1 要動 `frame_ops.go` 並讀 `SubagentRef.DelegatingToolUseIDs` 欄位。

### P2-T1 — `markDelegatingRef` + `unmarkDelegatingRef` helpers（dedicated retry loop）

**目標**：在 `internal/module/agent/frame_ops.go` 加兩 helper，**mirror `upsertProxyRefForBroker:1245-1297` 的 pattern**（B1 修正：不能 reuse `mutateSubagentsWithRetry`），自己跑 `UpsertIfUnchanged` retry；signature 含 frame identity (B2 修正)。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/frame_ops.go` | 約 90 LOC：兩 method 簽名：<br>`(m *Module) markDelegatingRef(paneID string, senderPID int, senderStartTime string, agentID, toolUseID string, broadcastTs int64) error`<br>`(m *Module) unmarkDelegatingRef(paneID string, senderPID int, senderStartTime string, agentID, toolUseID string, broadcastTs int64) error`<br>內部 pattern：(1) `m.frames.GetByIdentity(paneID, senderPID, senderStartTime)` 找 cc frame；nil → 靜默 no-op（spec L7 race：PreToolUse 在 SubagentStart 之前到達）。(2) for attempt in [0, proxyUpsertMaxAttempts) — `expected := current.LastSeenAt`、`copy(next, current.Subagents)`、線性 scan find `ref.ID == agentID` → 沒找到 → no-op（spec §3.5 race acceptable）；找到 → mutate `next[idx].DelegatingToolUseIDs` (mark: dedupe append / unmark: linear remove) + `next[idx].Delegating = len(next[idx].DelegatingToolUseIDs) > 0`。(3) `next` assign back, `current.LastSeenAt = broadcastTs`，`UpsertIfUnchanged(current, expected)` → ok return；衝突 reload via `GetByIdentity` retry。註解明寫「mirror upsertProxyRefForBroker pattern」+ 「Race scope: PreToolUse-before-SubagentStart silent no-op (spec L7)」 |
| `internal/module/agent/frame_ops_test.go` | 約 130 LOC，11 cases per spec §6.2 + 加 1（B2 verification）：`TestMarkDelegatingRef_AppendsToolUseIDOnMatchingRef` / `TestMarkDelegatingRef_DedupesRepeatedToolUseID` / `TestMarkDelegatingRef_NoOpWhenAgentIDNotFound` / `TestMarkDelegatingRef_NoOpWhenFrameMissing` / `TestMarkDelegatingRef_RetryOnUpsertConflict` (B2: simulate concurrent writer via `frames.UpsertIfUnchanged` mismatch, verify retry loop succeeds) / `TestUnmarkDelegatingRef_RemovesToolUseID` / `TestUnmarkDelegatingRef_DelegatingFalseWhenListEmpties` / `TestUnmarkDelegatingRef_DelegatingTrueWhenOthersStillActive` / `TestUnmarkDelegatingRef_NoOpWhenToolUseIDNotInList` / `TestUnmarkDelegatingRef_NoOpAfterSubagentStop` / `TestSubagentStopRemovesRefIncludingDelegatingFlag` / `TestDelegatingNativeRef_CoexistsWith_RealIsProxyRef_OnSameParent` |

**TDD 步驟**：
1. 寫 12 個 test case（全 Red）
2. 實作兩 helper（Green）
3. 跑 `go test ./internal/module/agent/... -run "TestMark|TestUnmark|TestDelegating|TestSubagentStop" -v` 全綠
4. 跑 `go test ./internal/module/agent/... -count=10 -race` 確保 dedicated retry loop concurrency 路徑不 panic
5. Lint
6. **Commit**: `feat(daemon): add markDelegatingRef + unmarkDelegatingRef helpers with optimistic concurrency`

---

## 3. Phase 3：handler wiring + integration tests（主 session）

Phase 2 全 commit 後啟動。主 session 接手 — 涉及 handler 跨多 lifecycle，需 careful integration。

### P3-T1 — `handler.go` wiring delegation extractor 到 cc PreTool/PostTool/PostToolFailure

**目標**：在 `handler.go:195-202` 既有 PathHint 處理區塊**緊鄰之後加 separate adjacent block**（F6 修正：**不 nest 在 PathHint conditional 內**——PathHint 依賴 `m.core / m.pathHintDedup / m.pathHintBuffer`，delegation 不需這些）；mark/unmark via Phase 2 helpers，把 `req.SenderPID / req.SenderStartTime` 傳入 helper（B2 修正）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/handler.go` | 約 40 LOC：在 PathHint block **之後**（不在 if 內）加新獨立 block：`if req.AgentType == "cc" && (req.PurdexName == "PdxPreToolUse" \|\| req.PurdexName == "PdxPostToolUse" \|\| req.PurdexName == "PdxPostToolUseFailure")`，呼叫 `ExtractDelegationHint(req.RawEvent, req.PurdexName)`；hint OK + IsCodexMark → 呼叫 `m.markDelegatingRef(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, hint.AgentID, hint.ToolUseID, broadcastTs)`；hint OK + IsUnmark → `m.unmarkDelegatingRef(...)` 同樣 6 參數。錯誤 log 但不 fail handler（fail-soft 對齊 PathHint pattern）。註解說明：「Delegation flag 與 PathHint 並列獨立；不依賴 path-hint infrastructure；同一 cc PreToolUse raw event 可同時 emit 兩種推導，extraction 互相獨立、dedup state 不影響」 |
| `internal/module/agent/handler_test.go` | 約 80 LOC integration test：<br>`TestHandleEvent_CCPreToolUseBashCodexCompanion_MarksDelegating` (e2e: inject PreToolUse fixture → assert frame.Subagents[i].Delegating==true)<br>`TestHandleEvent_CCPostToolUseBashAfterMark_UnmarksDelegating` (mark 後 inject PostToolUse → 確認 Delegating==false)<br>`TestHandleEvent_CCPostToolUseFailureBashAfterMark_UnmarksDelegating` (B3 path)<br>`TestHandleEvent_CCPreToolUseBashNonCodex_NoMark` (non-codex Bash → 不 mark)<br>`TestHandleEvent_CCPreToolUseBashTopLevelNoAgentID_NoMark` (agent_id="" → skip)<br>`TestHandleEvent_CCPreToolUseConcurrentTwoCodexBash_BothTracked` (B2 reverse-order verification)<br>**`TestHandleEvent_CodexAgentTypePreToolUseBashCodexCompanion_NoMark` (F4 — AC10 / regression #9 explicit handler-level gating)**<br>**`TestHandleEvent_OpencodeAgentTypePreToolUseBashCodexCompanion_NoMark` (F4 — AC10 / regression #10 explicit handler-level gating)** |

**TDD 步驟**：
1. 寫 8 個 integration test case（全 Red）—— 6 cc 路徑 + 2 非-cc agent_type gating
2. 加 wiring（Green）
3. 跑 `go test ./internal/module/agent/... -run "TestHandleEvent_(CC|Codex|Opencode).*Delegating|TestHandleEvent_(CC|Codex|Opencode).*NoMark" -v` 全綠
4. 跑全 `internal/module/agent/...` test suite 確保零 regression（特別 `frame_ops_l2_test.go` L2 turn-aware）
5. 跑 `go vet ./...` + lint
6. **Commit**: `feat(daemon): wire delegation extractor in handler for cc PreTool/PostTool/PostToolFailure`

---

## 4. Phase 4：SPA 層 — type + renderer + 測試（Subagent C）

可跟 Phase 2/3 並行（SPA 層獨立檔），但簡化起見序列做。

### P4-T1 — SPA `SubagentRef` type 加兩欄 + `SubagentDots` 渲染條件 + `data-delegating` attr

**目標**：spec §3.6 全套：`useAgentStore.ts` SubagentRef type 加兩 optional 欄；`SubagentDots.tsx` `dotStyle` 加 `delegating` 條件、新增 `data-delegating` attr、保留 `data-is-proxy` 不變、註解更新。

**改動**：

| 檔案 | 改動 |
|---|---|
| `spa/src/stores/useAgentStore.ts` | +3：SubagentRef type 加 `delegating?: boolean` 與 `delegating_tool_use_ids?: string[]` 兩 optional fields |
| `spa/src/components/SubagentDots.tsx` | +4：`dotStyle` 條件改 `(ref.is_proxy \|\| ref.delegating) ? PROXY_COLOR : NATIVE_COLOR`；JSX `<span>` 加 `data-delegating={ref.delegating ? 'true' : 'false'}`；上方註解 block（line 13-17）追加：「Delegating: cc native subagent 推測在 invoke codex-companion via Bash sniff（spec §3.6）；與 IsProxy 正交，OR 條件渲染相同橘色」 |
| `spa/src/components/SubagentDots.test.tsx` | 約 +40：4 個 colour case：`delegating=true, is_proxy=false → orange + data-delegating=true + data-is-proxy=false`；`delegating=false, is_proxy=true → orange + data-delegating=false + data-is-proxy=true`；`delegating=true, is_proxy=true → orange + 兩屬性都 true`；`delegating=false, is_proxy=false → blue + 兩屬性都 false` |

**TDD 步驟**：
1. 寫 4 個 SPA test（全 Red）
2. 改 type + renderer（Green）
3. 跑 `cd spa && pnpm install && npx vitest run --reporter verbose` 全綠
4. 跑 `cd spa && pnpm run lint` + `cd spa && pnpm run build` 確保無 type error
5. **Commit**: `feat(spa): SubagentDots renders orange when delegating flag set`

---

## 5. Phase 5：整合驗證 + mlab live verify（主 session）

Phase 1-4 全 commit 後執行。

### P5-T1 — 全套 regression sweep

**目標**：跑全 daemon test、SPA test、lint、build；確保 spec §6.5 既有 suite 全綠。

**步驟**：
1. `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-bash-sniff-delegating && go test ./... -count=1` — 全 daemon test 綠
2. `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-bash-sniff-delegating && go test ./internal/module/agent/... -count=10 -race` — race-mode 10 輪
3. `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-bash-sniff-delegating/spa && pnpm install && npx vitest run` — SPA 全綠
4. `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-bash-sniff-delegating/spa && pnpm run lint && pnpm run build` — 0 lint error / build 成功
5. `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-bash-sniff-delegating && go vet ./...` — 0 vet
6. **Commit (only if any auto-format changes)**: `chore: post-implementation sweep formatting`

### P5-T2 — mlab live verify（**必在 P6-T1 push PR 之前完成**，F8 修正）

**目標**：spec §6.3 background lifecycle observation；確認 foreground `/codex:rescue` 正確顯示橘色 + 結束變藍 + SubagentStop 消失；驗證 background 行為 (a) 還是 (b) 並 **on-spot 更新 spec §7 L6 wording**（因為 spec 在 PR diff 內，AC1b/AC2b 結論依賴此觀察）。

**步驟**：
1. mlab 端 `make build` daemon
2. 換上新 daemon binary
3. 在 cc session 跑 `/codex:rescue --wait` 簡單任務 → 觀察 SPA dot 變橘 → 任務完成後變藍 → cc subagent end 後消失
4. 在 cc session 跑 `/codex:rescue --background` → 觀察 dot 行為（flicker 或 stays orange？）
5. 在 cc session 跑 `/codex:adversarial-review --background` 三平行 → 觀察是否 3 個橘 dot
6. 在 cc subagent 內 `pnpm build`（非 codex Bash）→ 確認 dot 留藍
7. **on-spot Edit spec §7 L6 wording with real result**（不是「post-merge 更新」）：若 background lifecycle 是 (a)（flicker only），把 L6 描述定型為 confirmed limitation + 開 follow-up issue 追蹤 BashOutput tracking；若是 (b)（stays orange），把 AC1b/AC2b 升級為 unconditional pass + 移除 L6 conditional wording。Commit `fix(spec): finalize L6 background lifecycle wording per mlab observation`
8. mlab 結果 capture 進 PR description（截圖 / 文字描述都可）

**Failure path**：若觀察結果跟 spec §2.6 兩種預期都不符（罕見第三種行為），停下來 surface 給 user 重評估，不直接進 PR。

---

## 6. PR + Review

### P6-T1 — Push + 開 PR

**目標**：把 worktree branch push 上 origin，開 PR，包含 spec + plan + 6 commits + mlab verify 描述。

**步驟**：
1. 從 worktree 內 `git push -u origin worktree-lights-bash-sniff-delegating`
2. `gh pr create --title "[lights] cc Bash sniff delegating flag for codex visibility (#821)" --body "$(...)"`：body 含 issue ref、spec freeze 結論、各 phase commit 摘要、mlab verify capture、AC checklist
3. Mark P6-T1 done

### P6-T2 — Round 1 codex standard review

**目標**：跨模型第二意見，spec coherence + impl 對齊 + invariant 保護。

**步驟**：
1. `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" review --background --base main`
2. 等 1-3 min 輪詢 result
3. 若 0 finding → 進 P6-T3
4. 若有 finding → 用 spec §10「PR review 問題彙整 + 收斂」流程：表格化（信心 / 關聯 / 複雜度），優先處理 high-conf + high-rel + low-cplx；當下不修開 issue 追蹤。
5. 修完 commit `fix: address codex round-1 review`，重派 round 1 直到 0 blocker

### P6-T3 — Round 2 codex 三平行 adversarial review（**always run**，對齊 repo 兩輪 review 流程）

**目標**：spec §10 兩輪 review 的第二輪，三視角找 bug。

**步驟**：
1. `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" adversarial-review --background --base main "focus: 1) 攻擊方 — token detection 繞過 / Pre-Post 配對 race / SubagentStop 順序問題 / B2 slice unbounded growth; 2) 防守方 — IsProxy invariant 是否真不破壞 / L2 turn-aware 是否真不衝突 / canonicalizeDescendantsAfterUpsert 是否仍正確 read IsProxy only; 3) 體質 — delegation_extractor.go SRP / frame_ops.go 是否過大 / handler.go wiring 是否該抽 helper"`
2. 等 5-10 min 輪詢
3. 收斂 finding 同 P6-T2 流程
4. 採納完所有可修項，issue 追蹤 deferred

### P6-T4 — Squash merge + bump PR

**目標**：CLAUDE.md 流程 — 不直推 main，PR merge 後獨立 bump PR。

**步驟**：
1. `gh pr merge --squash --delete-branch <PR-number>`
2. 主 session ExitWorktree（remove）
3. 新 worktree `chore-bump-alpha-NNN`，`git reset --hard origin/main`，更 VERSION + package.json + spa/package.json + CHANGELOG.md
4. `gh pr create --title "chore: bump version to 1.0.0-alpha.NNN"`
5. self-review approve + squash merge

### P6-T5 — 清理 + 更新 memory

**目標**：清掉 worktree，記住 ship 結果。

**步驟**：
1. `git worktree list` 確認沒殘留
2. 更新 issue #821 標 closed by PR #X
3. 更新 `kickoff_codex_broker_and_lights_governance.md` §6 加註：「✅ shipped at alpha.NNN」
4. 在 MEMORY.md kickoff 段加新條目（或附在 governance kickoff 之下） — issue #821 結案

---

## 7. 風險與兜底

| 風險 | 兜底 |
|---|---|
| `mutateSubagentsWithRetry` 在 race 條件下的 retry edge case | 跑 `-count=10 -race` 多輪；若 panic 有 trace 可追 |
| Background Bash 真行為 (a) flicker only → user 看不到完整 visibility | spec L6 已預先承認；mlab verify 只更新 wording，不改 code |
| codex plugin script renaming (script 改名 codex-companion.mjs → codex-runner.mjs) | spec L1 已知；token 偵測仍可改一行；不需大改 |
| `mutateSubagentsWithRetry` race 描述太強的 `markDelegatingRef`（PreToolUse 在 SubagentStart 之前到達） | spec L7 / regression #12 已預先承認；no-op 行為 by design |
| PostToolUseFailure raw payload schema 跟 PostToolUse 不完全相同 | extractor 走容錯解析（同 PathHint pattern）；fixture 在 P3-T1 integration test 覆蓋 |
| SubagentRef slice 無上限增長（PostToolUse 不來） | SubagentStop 自動清整個 ref；極端情況下加防禦性 cap (~32) → 暫不加，等實測 |

---

## 8. Plan freeze checklist

- [x] 範圍對齊 spec
- [x] 鎖序與不變式具體可遵循
- [x] 4 個 phase + 1 整合 + 1 PR phase 拆分清楚
- [x] 每 task 有目標 / 改動 / TDD 步驟 / commit message
- [x] 涵蓋 spec 全 11 AC + 全 15 regression scenario + 全 7 known limitation
- [x] 估計時間 + LOC 對齊 spec §8
- [x] Codex plan-review round 1 0 blocker（v2 採納 2 blocker + 4 minor 全收斂）
- [x] Codex plan-review round 2 0 blocker（v3 採納 1 high + 1 minor 全收斂；implementation design ready per round-2 verdict）
