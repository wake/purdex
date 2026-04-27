# Lights Rebuild — Phase 4a-2 Plan v1 (PR-4a-2 codex / opencode ProbeProfile)

## v1.x 演進

| 版本 | Commit | 日期 | 主要變更 |
|---|---|---|---|
| v1 | (TBD) | 2026-04-28 | 初版 |

## 0. 來龍去脈

依 `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md` 落實 PR-4a-2：

- **Slice 5**：codex Provider 實作 `ProbeProfileProvider`（推薦 `{TopLines: 10, IdleStableTicks: 3}`）
- **Slice 6**：opencode Provider 實作 `ProbeProfileProvider`（同推薦值）
- **Slice 7**（新加 — orchestrator integration test）：`probe_orchestrator_test.go` 補 OR-codex / OR-opencode case，驗證 orchestrator 對 codex / opencode 取到自訂 profile（非 default fallback）

承接：PR-4a-1 ship at alpha.234（PR #670），probe primitive + orchestrator + cc profile 已就位。

## 1. Scope

### 1.1 In scope

- `internal/agent/codex/probe_profile.go`（新檔）+ `_test.go`（CDX1）
- `internal/agent/opencode/probe_profile.go`（新檔）+ `_test.go`（OCD1）
- `internal/module/agent/probe_orchestrator_test.go` 加兩 case：OR-codex（fakeAgentProvider 換真 codex.NewProvider）+ OR-opencode（fakeAgentProvider 換真 opencode.NewProvider）

### 1.2 Out of scope

- `shouldWatchActivity` per-agent 化（per spec §3）
- ProbeProfile schema 擴展（per spec §3）
- default profile / orchestrator / probe primitive 改動（per spec §3）
- opencode hook / template / catalog 改動（已在 PR-4a-0 對齊）

## 2. Commit 順序（TDD red → green，每 commit 獨立）

### Commit 1 — `feat(agent/codex): implement ProbeProfileProvider with TopLines profile`

**TDD 流程**：

1. **紅燈**：先建 `internal/agent/codex/probe_profile_test.go`：
   ```go
   // CDX1 — characterization test pinning codex's ProbeProfile values.
   // codex CLI is an append-only TUI: top hash signals new turns; bottom
   // contains spinner + elapsed timer (variable). TopLines=10 captures
   // the recent turn cluster while keeping captures cheap. IdleStableTicks=3
   // matches the watch-loop default (BB-stable).
   //
   // If a future codex UI revision invalidates these tunings, prefer
   // RE-SAMPLING (per spec §2.2.3) and updating values + this test
   // together. See spec docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.2.
   func TestCodexProvider_ProbeProfile(t *testing.T) { ... }
   ```
   `go test ./internal/agent/codex/... -run TestCodexProvider_ProbeProfile` → **fail**（method 不存在）

2. **綠燈**：建 `internal/agent/codex/probe_profile.go`：
   ```go
   package codex

   import "github.com/wake/purdex/internal/agent"

   // ProbeProfile returns the watch parameters for the codex agent. codex
   // CLI is an append-only TUI: top hash signals new turns; bottom contains
   // spinner + elapsed timer (variable). TopLines=10 captures the recent
   // turn cluster while keeping captures cheap. IdleStableTicks=3 matches
   // the watch-loop default.
   //
   // If a future codex UI revision invalidates these tunings, RE-SAMPLE
   // per spec §2.2.3 (docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md)
   // and update values + CDX1 test together. Touching these values
   // intentionally surfaces the change for review.
   func (p *Provider) ProbeProfile() agent.ProbeProfile {
       return agent.ProbeProfile{
           TopLines:        10,
           IdleStableTicks: 3,
       }
   }
   ```
   `go test ./internal/agent/codex/... -count=1` → **pass**

3. **採樣 confirm（spec G7）**：implementer 在實作後對真實 codex session 跑：
   ```bash
   tmux capture-pane -t <codex-session>:0 -p -S -10 -E -1   # 上方 10 行
   ```
   三狀態各跑一次（idle / running with output / spinner-only）— 結果寫進 PR description 的 Sampling Evidence 段（per spec §5 G7）。

4. **Commit msg**：
   ```
   feat(agent/codex): implement ProbeProfileProvider with TopLines profile

   codex CLI is append-only TUI: top hash signals new turns; bottom is
   spinner + elapsed timer (variable). TopLines=10 captures recent turn
   cluster while keeping captures cheap. IdleStableTicks=3 matches the
   watch-loop default.

   See docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.2.
   ```

### Commit 2 — `feat(agent/opencode): implement ProbeProfileProvider with TopLines profile`

**TDD 流程**：

1. **紅燈**：建 `internal/agent/opencode/probe_profile_test.go`（OCD1，結構同 CDX1，profile 同值）→ fail

2. **綠燈**：建 `internal/agent/opencode/probe_profile.go`（同型，註解強調 opencode TUI 結構與 codex 同型）→ pass

3. **採樣 confirm**：對真實 opencode session 跑同樣三狀態採樣，結果寫進 PR description

4. **Commit msg**：
   ```
   feat(agent/opencode): implement ProbeProfileProvider with TopLines profile

   opencode TUI is structurally similar to codex CLI: append-only top,
   bottom prompt + spinner. TopLines=10 + IdleStableTicks=3 mirror codex
   profile values; per-agent rationale documented in spec §2.3.

   See docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.3.
   ```

### Commit 3 — `test(module/agent): cover orchestrator with real codex/opencode providers`

**目的**：驗證 orchestrator 對真實 codex / opencode provider type-assert `ProbeProfileProvider` 成功（不退到 default fallback）。

**TDD 流程**：

1. **紅燈/綠燈一體**：在 `internal/module/agent/probe_orchestrator_test.go` 加兩 case（不需先紅後綠 — 是 integration assertion，新加直接綠才有意義；若紅就是 Commit 1/2 沒過）：

   ```go
   // OR-codex — orchestrator picks up codex.Provider.ProbeProfile() (TopLines)
   // not the default profile (BottomLines).
   func TestProbeOrchestrator_StartWatch_CodexProvider(t *testing.T) {
       harness := newTestHarness(t)
       harness.registry.Register(codex.NewProvider())
       harness.module.manageActivityWatch("S1", "codex", agentpkg.StatusRunning)

       // Assert: prober.WatchOptions has TopLines=10, BottomLines=0
       got := harness.fakeProber.LastWatchOptions("S1:")
       if got.TopLines != 10 || got.BottomLines != 0 {
           t.Fatalf("expected TopLines=10 BottomLines=0, got %+v", got)
       }
   }

   // OR-opencode — orchestrator picks up opencode.Provider.ProbeProfile()
   func TestProbeOrchestrator_StartWatch_OpenCodeProvider(t *testing.T) { ... }
   ```

2. **跑**：`go test ./internal/module/agent/... -run "TestProbeOrchestrator_StartWatch_CodexProvider|TestProbeOrchestrator_StartWatch_OpenCodeProvider" -count=1` → pass

3. **不重複既有覆蓋**：OR1（fake provider with profile）+ OR2（fake provider without profile）已驗 type-assert 機制；OR-codex / OR-opencode 是「真實 provider 對接」regression 而非 mechanism re-test。

4. **Commit msg**：
   ```
   test(module/agent): cover orchestrator with real codex/opencode providers

   OR1/OR2 cover the type-assert mechanism via fakes; OR-codex / OR-opencode
   add regression coverage that real codex.Provider / opencode.Provider
   actually implement ProbeProfileProvider and produce the expected
   TopLines profile (catches future accidental method removal).
   ```

## 3. 測試矩陣

| Test ID | 檔案 | 覆蓋 |
|---|---|---|
| CDX1 `TestCodexProvider_ProbeProfile` | `internal/agent/codex/probe_profile_test.go` | codex profile values pinning（characterization） |
| OCD1 `TestOpenCodeProvider_ProbeProfile` | `internal/agent/opencode/probe_profile_test.go` | opencode profile values pinning（characterization） |
| OR-codex `TestProbeOrchestrator_StartWatch_CodexProvider` | `internal/module/agent/probe_orchestrator_test.go` | orchestrator 真實 codex provider type-assert + WatchOptions forward |
| OR-opencode `TestProbeOrchestrator_StartWatch_OpenCodeProvider` | `internal/module/agent/probe_orchestrator_test.go` | orchestrator 真實 opencode provider type-assert + WatchOptions forward |

**淨增**：4 tests / 0 既有 test 動到（cc CC1 / OR1 / OR2 等全部不變）。

## 4. Implementer 採樣 confirm 流程（落實 spec G7）

每 commit 在實作 `ProbeProfile()` method 後、commit 前：

1. **準備真實 session**：
   - codex：開一個 codex CLI session 在某 tmux pane（target 例：`%41`）
   - opencode：開一個 opencode session 在某 tmux pane

2. **三狀態採樣**（每狀態跑一次）：
   ```bash
   # State A: idle（agent 沒在處理，user 也沒輸入）
   tmux capture-pane -t <target> -p -S 0 -E 9            # 頂部 10 行
   tmux capture-pane -t <target> -p -S 0 -E 9 | md5      # hash

   # State B: running with active output（spinner 持續轉、輸出滾動）
   # （相隔 1.5s 跑兩次，比較頂部 10 行 hash 是否變動）
   tmux capture-pane -t <target> -p -S 0 -E 9 | md5
   sleep 1.5
   tmux capture-pane -t <target> -p -S 0 -E 9 | md5

   # State C: spinner-only（agent 在等模型回應，僅 spinner 動）
   tmux capture-pane -t <target> -p -S 0 -E 9 | md5
   sleep 1.5
   tmux capture-pane -t <target> -p -S 0 -E 9 | md5
   ```

3. **判讀**：
   - State A：兩次 hash 必須**相同**（idle stable）
   - State B：兩次 hash 必須**不同**（new content scrolls top）
   - State C：兩次 hash 必須**相同**（spinner 在底部不影響頂部）— 此即 TopLines vs BottomLines 的關鍵差異
   - 頂部 10 行內容檢查：**不應**含 elapsed timer / spinner 字元

4. **PR description 留痕**：把每次 capture 輸出的前幾行（敏感內容遮蔽）+ hash + 判讀結論貼進 PR description 的 Sampling Evidence 段（per spec §5 G7）。

5. **若任一條件不過**：
   - State A 失敗 → top 區域不穩定（可能 codex 在頂部畫了某個動態元件）→ 改用較大 IdleStableTicks 或改 capture mode；spec §2.2 / §2.3 同步修
   - State B 失敗 → top 區域不會被 new turn 變動 → TopLines hash 永遠 stable → idle 判定誤報 → 應改用 BottomLines 或調整 N
   - State C 失敗 → 頂部含動態字元 → 改 BottomLines 反而不行（同樣動態）→ 增大 N 跳過動態區，或改全 pane + 加 IdleStableTicks
   - 任一情況 → spec 修值或改設計後重採樣，不硬上

## 5. Final Verification

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-phase-4a-2/

# Go
go test ./internal/agent/cc/... -count=1                              # 確認 cc 沒受影響
go test ./internal/agent/codex/... -count=1                           # CDX1 綠
go test ./internal/agent/opencode/... -count=1                        # OCD1 綠
go test ./internal/module/agent/... -count=1                          # OR-codex / OR-opencode + 既有 OR1-OR10 全綠
go test ./... -count=1                                                # 全 repo 全綠

# SPA（未動 SPA，但仍跑驗證）
pnpm --prefix spa run lint
pnpm --prefix spa run build
pnpm --prefix spa exec vitest run

# Sampling evidence（per G7）
# Implementer 跑 §4 三狀態採樣 × 兩家 agent，貼進 PR description
```

全綠 + 採樣記錄齊全 → 開 PR。

## 6. PR description 必含

| 段落 | 內容 |
|---|---|
| Summary | 1-2 句說明「為 codex / opencode 加自訂 ProbeProfileProvider，profile = TopLines:10 + IdleStableTicks:3」 |
| Spec link | `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md` |
| Plan link | `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-plan.md` |
| Commit list | C1 codex / C2 opencode / C3 orchestrator regression — 每 commit 一行說明 |
| Sampling Evidence — codex | 三狀態採樣輸出（前 5 行 + hash） + 判讀結論（State A/B/C 各 PASS）|
| Sampling Evidence — opencode | 同上 |
| Worktree origin | `git status -s` clean check 留痕（per CLAUDE.md feedback_concurrent_session_safety） |
| Test summary | 4 new tests（CDX1 / OCD1 / OR-codex / OR-opencode），全 repo go test + SPA lint/build/vitest 全綠 |
| Out of scope | spec §3 anchor 提示，避免 reviewer 期待此 PR 做 readiness 整合 / shouldWatchActivity per-agent 化 |

## 7. Risk

| Risk | Mitigation |
|---|---|
| 採樣失敗導致 spec/plan 反覆修 | 每家 agent 獨立採樣 + 獨立 commit；codex 失敗不影響 opencode；spec §2.2 / §2.3 各 anchor 可獨立修 |
| 主 repo 並發 session（feedback_concurrent_session_safety） | 進 worktree 前 `git status -s` clean + push 前 `git pull --rebase origin main` |
| Codex sandbox 無網路（feedback_codex_sandbox_no_install） | 主 Claude 手動跑 SPA lint / build / vitest，不依賴 codex review |
| OR-codex / OR-opencode test 引入 codex / opencode package 形成循環依賴 | `internal/module/agent` import `internal/agent/codex` + `internal/agent/opencode` 已在 module.go 各 callsite 用過（registry 註冊）；test import 同型，無新循環 — 若 import cycle 出現則改在 `cmd/pdx` 寫 e2e 確認 |
| codex / opencode 真實 session 取得困難（dev 環境沒裝） | 採樣可在 mlab（已有 codex / opencode CLI）做；implementer 若無環境 → 標 PR 為 draft 等實際採樣完才開 ready-for-review |

## 8. Ship gate（指向 spec §5）

依 spec §5 八個 gate 逐項驗。本 plan 不重複內容；ship 前對照 spec §5 checklist 全綠。

## 9. LOC 預估

| Commit | 估 LoC | 估 tests | 備註 |
|---|---|---|---|
| 1: codex probe_profile.go + test | ~25 (impl) + ~20 (test) | 1 (CDX1) | mirrors cc/probe_profile.go shape |
| 2: opencode probe_profile.go + test | ~25 (impl) + ~20 (test) | 1 (OCD1) | mirrors codex |
| 3: orchestrator integration tests | ~60 (test) | 2 (OR-codex / OR-opencode) | re-uses existing newTestHarness + fakeProber |

**總計**：~150 LoC + 4 tests。**屬小型 PR**（per spec scope 縮減）。

## 10. 結束條件

**Ship**：

- spec §5 G1-G8 全綠
- PR merged
- 對應 main bump PR ship（VERSION 進到 alpha.235）

**Memory 更新**：

- `kickoff_lights_rebuild.md`：標 PR-4a-2 完成 + 觸發詞清掉「PR-4a-2」分支；Phase 4a 全完工 → 下個觸發改為「啟動 Phase 4b」
- `project_progress.md`：alpha.235

**下一階段**：Phase 4b（`ProbeIntentProvider` + readiness 整合）按 spec §8.2 接續。

## 11. 文獻

- Spec: `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md`
- 前置 Phase 4a 系列：
  - `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` v1.3（PR-4a-0 已 ship）
  - `docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md` v2.1（PR-4a-1 已 ship）
- 既有實作參考：`internal/agent/cc/probe_profile.go`（CC1 模板）+ `internal/module/agent/probe_orchestrator_test.go`（OR1/OR2 fake provider 模式）
- 相關 PR：#664（PR-4a-0）/ #670（PR-4a-1）
