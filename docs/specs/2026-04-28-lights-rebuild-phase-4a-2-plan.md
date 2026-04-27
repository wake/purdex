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

合併原 C3（orchestrator regression test）進 C1 / C2 — OR-codex / OR-opencode 在 codex / opencode 尚未實作 `ProbeProfile()` 時，orchestrator type-assert 失敗會走 default `{BottomLines:10}`，正好作為 C1 / C2 紅燈的另一條斷言（codex review R1 plan finding #1）。

### Commit 1 — `feat(agent/codex): implement ProbeProfileProvider with TopLines profile`

**改動 4 處**：
- 新檔 `internal/agent/codex/probe_profile.go`
- 新檔 `internal/agent/codex/probe_profile_test.go`（CDX1）
- 既檔 `internal/module/agent/probe_orchestrator_test.go` +OR-codex test
- 既檔 `internal/module/agent/probe_orchestrator_test.go` 加小 helper（見 §3.1，視 OR-codex 是否需 helper 簡化而定）

**TDD 流程**：

1. **紅燈 — 兩個 test 一起紅**：
   - 建 `internal/agent/codex/probe_profile_test.go`（CDX1）：
     ```go
     // CDX1 — characterization test pinning codex's ProbeProfile values.
     // See spec docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.2.
     func TestCodexProvider_ProbeProfile(t *testing.T) {
         p := codex.NewProvider()
         got := p.ProbeProfile()
         want := agent.ProbeProfile{TopLines: 10, IdleStableTicks: 3}
         if got != want {
             t.Fatalf("ProbeProfile() = %+v, want %+v", got, want)
         }
     }
     ```
     `go test ./internal/agent/codex/... -run TestCodexProvider_ProbeProfile` → **fail**（`p.ProbeProfile undefined`）

   - 在 `internal/module/agent/probe_orchestrator_test.go` 加 OR-codex（沿用既有 `newTestModule` + `recordingProber` + `rec.watchOpts["sess:"]` 模式，per OR1 line 758-781）：
     ```go
     // OR-codex — orchestrator picks up real codex.Provider's TopLines profile
     // (not the default BottomLines fallback). Regression for accidental
     // ProbeProfile() removal on codex.Provider.
     func TestOrchestrator_RealCodexProviderUsesTopLinesProfile(t *testing.T) {
         m := newTestModule(t)
         rec := newRecordingProber()
         m.probeOrch.watcher = rec
         m.registry.Register(codex.NewProvider())

         m.probeOrch.startWatch("sess", "codex")

         rec.mu.Lock()
         defer rec.mu.Unlock()
         got, ok := rec.watchOpts["sess:"]
         if !ok {
             t.Fatalf("expected Watch on target %q, got %v", "sess:", rec.watchOpts)
         }
         want := probe.WatchOptions{TopLines: 10, BottomLines: 0, IdleStableTicks: 3}
         if got != want {
             t.Fatalf("WatchOptions = %+v, want %+v (codex TopLines profile)", got, want)
         }
     }
     ```
     在 codex.Provider 還沒實作 `ProbeProfile()` 之前，orchestrator type-assert 失敗 → fallback 到 `defaultProbeProfile = {BottomLines: 10, IdleStableTicks: 3}` → 與 want 不符 → **fail**。

   - 在 `internal/module/agent/probe_orchestrator_test.go` 同檔加 import：`"github.com/wake/purdex/internal/agent/codex"`（若尚未 import）

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
   `go test ./internal/agent/codex/... ./internal/module/agent/... -count=1` → **pass**（CDX1 + OR-codex 都綠）

3. **採樣 confirm（spec G7）**：implementer 在實作後對真實 codex session 跑採樣（命令見 §4），三狀態結果寫進 PR description 的 Sampling Evidence — codex 段。

4. **Commit msg**：
   ```
   feat(agent/codex): implement ProbeProfileProvider with TopLines profile

   codex CLI is append-only TUI: top hash signals new turns; bottom is
   spinner + elapsed timer (variable). TopLines=10 captures recent turn
   cluster while keeping captures cheap. IdleStableTicks=3 matches the
   watch-loop default.

   Tests: CDX1 (characterization) + OR-codex (orchestrator regression
   that real codex.Provider really implements ProbeProfileProvider).

   See docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.2.
   ```

### Commit 2 — `feat(agent/opencode): implement ProbeProfileProvider with TopLines profile`

**改動 3-4 處**：
- 新檔 `internal/agent/opencode/probe_profile.go`
- 新檔 `internal/agent/opencode/probe_profile_test.go`（OCD1）
- 既檔 `internal/module/agent/probe_orchestrator_test.go` +OR-opencode test（同 OR-codex 結構）

**TDD 流程**（與 Commit 1 同型）：

1. **紅燈 — 兩個 test 一起紅**：
   - 建 OCD1：與 CDX1 同型，change provider 為 `opencode.NewProvider()`，want 同值（`{TopLines: 10, IdleStableTicks: 3}`）
   - 加 OR-opencode：與 OR-codex 同型，target `"sess2:"`，agentType `"opencode"`，import `"github.com/wake/purdex/internal/agent/opencode"`
   - 兩者都 fail（opencode.Provider 沒 `ProbeProfile()` → type-assert 失敗 → default fallback 不符 want）

2. **綠燈**：建 `internal/agent/opencode/probe_profile.go`（同 codex 結構，註解強調「opencode TUI 與 codex 同型」+ 引用 spec §2.3）→ pass

3. **採樣 confirm**：對真實 opencode session 跑同樣三狀態採樣，寫進 PR description

4. **Commit msg**：
   ```
   feat(agent/opencode): implement ProbeProfileProvider with TopLines profile

   opencode TUI is structurally similar to codex CLI: append-only top,
   bottom prompt + spinner. TopLines=10 + IdleStableTicks=3 mirror codex
   profile values; per-agent rationale documented in spec §2.3.

   Tests: OCD1 (characterization) + OR-opencode (orchestrator regression).

   See docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md §2.3.
   ```

## 3. 測試矩陣

| Test ID | 檔案 | 覆蓋 |
|---|---|---|
| CDX1 `TestCodexProvider_ProbeProfile` | `internal/agent/codex/probe_profile_test.go` | codex profile values pinning（characterization） |
| OCD1 `TestOpenCodeProvider_ProbeProfile` | `internal/agent/opencode/probe_profile_test.go` | opencode profile values pinning（characterization） |
| OR-codex `TestOrchestrator_RealCodexProviderUsesTopLinesProfile` | `internal/module/agent/probe_orchestrator_test.go` | orchestrator 真實 codex provider type-assert + WatchOptions forward |
| OR-opencode `TestOrchestrator_RealOpenCodeProviderUsesTopLinesProfile` | `internal/module/agent/probe_orchestrator_test.go` | orchestrator 真實 opencode provider type-assert + WatchOptions forward |

**淨增**：4 tests / 0 既有 test 動到（cc CC1 / OR1 / OR2 等全部不變）。

### 3.1 既有 test infrastructure 沿用

OR-codex / OR-opencode 直接沿用既有 `internal/module/agent/probe_orchestrator_test.go` 既有設施（per OR1 line 758-781）：

- `newTestModule(t)` — module test fixture（fakes_test.go）
- `newRecordingProber()` — recording fake prober；直接讀 `rec.watchOpts[target]` 取 last WatchOptions（**不需新加 helper**）
- `m.registry.Register(...)` — provider registry registration

**不新增 helper**（如 `LastWatchOptions`），保持 PR diff 最小化。若 implementer 跑時發現直讀 map 太冗長導致 OR-codex / OR-opencode body 過大，可選擇加同型 helper（必須在 commit 內 docu rationale）。

## 4. Implementer 採樣 confirm 流程（落實 spec G7）

每 commit 在實作 `ProbeProfile()` method 後、commit 前：

1. **準備真實 session**：
   - codex：開一個 codex CLI session 在某 tmux pane（target 例：`%41`）
   - opencode：開一個 opencode session 在某 tmux pane

2. **三狀態採樣**（每狀態 3 次 capture，間隔 0.5s — 對齊 spec IdleStableTicks=3 約定）：

   採樣命令統一使用與 production 一致的 `CapturePaneTopLines` 等價命令（per `internal/tmux/executor.go:344` → `CapturePaneRange(target, 0, n-1)` → `tmux capture-pane -e -p -t <target> -S 0 -E 9`）。**`-e` 必須加** — production 保留 ANSI escape 以區分 spinner color 動畫（per executor.go:332-336 註解）；移除 `-e` 會讓採樣跟 production 行為不同步。

   ```bash
   TARGET=<codex-or-opencode-tmux-target>     # e.g. %41 or session:window.pane
   CAPTURE() { tmux capture-pane -e -p -t "$TARGET" -S 0 -E 9; }
   HASH() { CAPTURE | md5; }

   # State A: idle（agent 沒在處理，user 也沒輸入）
   echo "=== State A capture ==="; CAPTURE
   echo "=== State A hashes (3 ticks @ 0.5s) ==="
   HASH; sleep 0.5; HASH; sleep 0.5; HASH

   # State B: running with active output（spinner 持續轉、輸出滾動）
   echo "=== State B capture ==="; CAPTURE
   echo "=== State B hashes (3 ticks @ 0.5s) ==="
   HASH; sleep 0.5; HASH; sleep 0.5; HASH

   # State C: spinner-only（agent 在等模型回應，僅 spinner 動，無新輸出 scroll 到頂部）
   echo "=== State C capture ==="; CAPTURE
   echo "=== State C hashes (3 ticks @ 0.5s) ==="
   HASH; sleep 0.5; HASH; sleep 0.5; HASH
   ```

3. **判讀**（3 次 hash 為一組）：
   - **State A — idle stable**：3 次 hash 全部**相同** → 對應 watch-loop 連續 3 ticks 一致即 fire ScreenStable，PASS
   - **State B — top scrolls on new turn**：3 次 hash 至少**有一次變動** → 對應 watch-loop fire ScreenChanged，PASS
   - **State C — spinner doesn't reach top**：3 次 hash 全部**相同** → 證明底部 spinner 不污染頂部 10 行；TopLines vs BottomLines 的關鍵差異，PASS
   - 頂部 10 行**內容檢查**（State A capture）：**不應**含 elapsed timer（如 `(12s elapsed)` / `[1.5s]`）、braille spinner（`⠋⠙⠹...`）、旋轉系列（`/─\|`）字元

4. **PR description 留痕**（per spec §5 G7）：每家 agent 段落含：
   - 採樣命令（含 TARGET 值；session 內容若敏感可遮蔽用戶輸入但保留 hash）
   - State A / B / C 各自的 capture 輸出前 5 行（ANSI 可保留為 raw escape）
   - State A / B / C 各自 3 次 hash 列表
   - 三狀態 PASS / FAIL 判讀
   - 「頂部 10 行不含動態字元」的 yes/no 觀察

5. **若任一條件不過**：
   - State A 失敗（idle 3 hash 不全同）→ top 區域不穩定（可能 codex 在頂部畫了某個動態元件）→ 改用較大 IdleStableTicks 或改 capture mode；spec §2.2 / §2.3 同步修
   - State B 失敗（new turn 3 hash 全同）→ top 區域不會被 new turn 變動 → TopLines hash 永遠 stable → idle 判定誤報 → 應改用 BottomLines 或調整 N
   - State C 失敗（spinner-only 3 hash 不全同）→ 頂部含動態字元 → 改 BottomLines 反而不行（同樣動態）→ 增大 N 跳過動態區，或改全 pane（`{TopLines: 0, BottomLines: 0}`）+ 加 IdleStableTicks
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
| Commit list | C1 codex（含 CDX1 + OR-codex）/ C2 opencode（含 OCD1 + OR-opencode）— 每 commit 一行說明 |
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
| 1: codex probe_profile.go + CDX1 + OR-codex | ~25 (impl) + ~20 (CDX1) + ~30 (OR-codex) | 2 (CDX1 + OR-codex) | mirrors cc/probe_profile.go shape；OR-codex 沿用 OR1 line 758-781 形狀，直讀 `rec.watchOpts["sess:"]`，不新加 helper |
| 2: opencode probe_profile.go + OCD1 + OR-opencode | ~25 (impl) + ~20 (OCD1) + ~30 (OR-opencode) | 2 (OCD1 + OR-opencode) | mirrors codex |

**總計**：~150 LoC + 4 tests。**屬小型 PR**（per spec scope 縮減）。

## 10. 結束條件

**Ship**：

- spec §5 G1-G8 全綠
- PR merged
- 對應 main bump PR ship

**Bump PR 步驟**（per CLAUDE.md「VERSION 為 SOT，bump 時須同步 package.json + spa/package.json」+ `feedback_bump_base_origin_not_local`）：

1. 進新 worktree（branch 名 `worktree-bump-alpha-235`），先 `git fetch origin main && git reset --hard origin/main`（避免 local main 並發 session commit）
2. 改 `VERSION`：`1.0.0-alpha.234` → `1.0.0-alpha.235`
3. 改 `package.json` `version` field 同步
4. 改 `spa/package.json` `version` field 同步
5. 加 `CHANGELOG.md` alpha.235 條目（含 PR-4a-2 PR 號 + 一行說明 codex/opencode ProbeProfile）
6. Commit message: `chore: bump version to 1.0.0-alpha.235 (#<PR>)`
7. 開 PR、merge
8. 退 worktree

**Memory 更新**（bump merge 後）：

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
