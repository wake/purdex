# Lights Rebuild — Fix-Spec (整體架構修正)

- **Date**: 2026-04-28
- **Worktree**: `lights-phase-4a-2`（branch `worktree-lights-phase-4a-2`）
- **Status**: 整體架構修正計劃 — 不取代 `2026-04-23-lights-rebuild-spec.md`，而是修正 Phase 4a 跑偏並重新規劃 Phase 4b/5 範圍
- **Replaces**: `2026-04-28-lights-rebuild-phase-4a-2-spec.md` + `2026-04-28-lights-rebuild-phase-4a-2-plan.md`（PR #676 跑偏產物）

---

## 0. 來龍去脈：為什麼需要 fix-spec

Phase 0/1/2/3/3.5 全 ship 至 alpha.234。Phase 4a 系列發生**架構性跑偏**：

| PR | 預期 (per spec §8.1) | 實際做了 |
|---|---|---|
| PR-4a-0 | OpenCode 1.14.23 hooks completion | ✅ 對 — alpha.233 ship |
| PR-4a-1 | Activity probe 內部強化（彩虹字 / spinner） | 字元偵測砍掉改 Top-N hash + **加了 ProbeProfileProvider generic framework + cc TopLines profile + default profile fallback + always-on policy** ← **越界做了 Phase 4b 的事，且方向錯** |
| PR-4a-2 (#676) | codex / opencode 接同 framework | 為 codex / opencode 加 generic profile（framework 本身是錯方向，再延伸只會擴大錯誤）→ codex review R3 抓到「TopLines 在 spinner-only 階段誤判 Idle」P1 → 框架定型錯誤 |

**根因**：PR-4a-1 把 spec §8.1（probe 內部強化）誤擴展為 spec §8.2（per-agent ProbeIntentProvider）的初步框架，且**抽錯方向** — 抽成「per-agent generic single profile」而不是「per-agent + per-缺口 ad-hoc ProbeIntent」。

**修正方向**：

1. 撤回 PR-4a-1 ship 的 generic framework 部分
2. 保留 PR-4a-1 ship 的 shared utilities（probe primitive / tmux capture / orchestrator 機制）
3. 重做 Phase 4b（spec §8.2 既有設計）— 這次正確：per-agent + per-缺口 ProbeIntent
4. 順手清掉一個累積已久的可讀性債：catalog naming 沒區分 PurdexName vs UpstreamKey

---

## 1. 架構基準圖（fix 後）

```
HOOK LAYER (per-agent installer + handler entry)
  cc:       ~/.claude/settings.json   key=UpstreamKey "SessionStart"
                                      command arg = PurdexName "PdxSessionStart"
  codex:    ~/.codex/hooks.json       key=UpstreamKey "SessionStart"
                                      command arg = PurdexName "PdxSessionStart"
  opencode: plugin Bus listener       case label=UpstreamKey "session.created"
                                      plugin emit() arg = PurdexName "PdxSessionStart"
                          ↓
        Daemon handler entry 收到永遠是 (agent_type, PurdexName)
                          ↓
─────────── 對齊 audit ───────────→ 列出 hook → status → 燈號 缺口
                          ↓
                  決定每家 agent 哪些缺口要用 probe 補
                          ↓
              (only those gaps) → ad-hoc probe code in agent module
                                  using PROBE LAYER shared utilities
                                  via per-agent ProbeIntentProvider
                          ↓
                Status broadcast → SPA 燈號（不在本 fix scope）
                          ↓
        Observable via TraceStore + PDX_DEV_MODE log
        Inspectable via Dev Inspector UI (Phase 5)
```

**核心原則**：

- **Probe 不是 always-on**：是 hook coverage **缺口導向** 的補位工具
- **缺口 per-agent specific**：實際運行觀察決定，不憑空假設
- **補位方式 ad-hoc**：寫在 agent module 內，不抽 generic framework
- **抽象在 input/output 邊界**：catalog naming 分離 PurdexName / UpstreamKey；daemon 內部一律 PurdexName
- **觀察優先**：runtime observability (TraceStore + dev log) 比 ship-time sampling 更可靠

---

## 2. 工作項拆解 (W1–W7)

| Work | 對應決議 | 預估規模 | 對應 spec phase |
|---|---|---|---|
| **W1 — Hook → status → 燈號 對齊 audit**（純 docs） | (a) | 小（純 audit doc） | Phase 4b 前置 audit (spec §8.2) |
| **W2 — Catalog naming separation**（PurdexName + UpstreamKey） | 4 | 中（catalog + installer + tests + reinstall hooks.json） | 不在原 spec phase 內，新增 |
| **W3 — Framework 撤回**（ProbeProfileProvider + ProbeProfile + default profile + always-on policy） | 2 | 中（撤 code + 改 manageActivityWatch policy） | Phase 4b 範圍（撤回 PR-4a-1 越界部分） |
| **W4 — Observability 補完**（TraceStore step + PDX_DEV_MODE log 全路徑覆蓋；handler / DeriveStatus / probe / broadcast 各層） | 5 | 中 | Phase 4b 範圍 (d) |
| **W5 — 修 W1 audit 出來的燈號 bug** | (b) | 視 audit 結果 | Phase 4b follow-up |
| **W6 — per-agent ad-hoc probe 補缺口**（為 W1 audit 找出的缺口寫 ProbeIntent / ad-hoc detector） | (c) | 視 audit 結果 | Phase 4b 主體 (spec §8.2 ProbeIntentProvider) |
| **W7 — Dev Inspector UI**（消費 W4 補完的 observability + Coverage endpoint） | 5 | 中（新 endpoint + SPA 4 視圖） | Phase 5 (spec §9) |

**保留 unchanged**：

- PR-4a-1 ship 的 shared utilities：`probe.Watch` / `WatchOptions` / `tmux.CapturePaneTopLines` / `CapturePaneRange` / `LooksLikeShellPrompt` / orchestrator `graceWindow` / `Error guard` / `stale-callback guard` / `transition gate` / `recordHookAt`
- PR-4a-1 ship 的 `purdex_probe_*` expvar counters（4 個；跟 (d) 正交，不擴不撤）
- PR-4a-1 ship 的 PDX_DEV_MODE log 既有 5 條（W4 是補完不是重做）
- Phase 0–3.5 全部 ship 內容

---

## 3. 撤回清單（W3 範圍）

**從 main 撤回**（PR-4a-1 ship 的 generic framework 部分）：

| 項目 | 檔案 | 撤回後行為 |
|---|---|---|
| `ProbeProfileProvider` interface | `internal/agent/provider.go` | 移除；orchestrator 不再 type-assert |
| `ProbeProfile` struct | `internal/agent/provider.go` | 移除 |
| cc `ProbeProfile()` impl | `internal/agent/cc/probe_profile.go` (+ test) | 整檔刪 |
| `defaultProbeProfile` | `internal/module/agent/probe_orchestrator.go` | 移除（後面也不需要 default — probe 不 always-on） |
| `manageActivityWatch` 「status ∈ {Waiting/Running/Idle} 啟動 probe」always-on policy | `internal/module/agent/module.go:469-501` | 改為 per-agent gating，預設不啟動；W6 為各 agent 寫的 ProbeIntent 才啟動 |
| 既有 OR1 / OR2 / FX4 等 generic profile fake test | `internal/module/agent/probe_orchestrator_test.go` | 撤；改測 ProbeIntent 行為 |

**保留**：

- `probe.Watch(target, opts, cb)` + `WatchOptions{TopLines, BottomLines, IdleStableTicks}` — 這是 shared utility，W6 ad-hoc detector 還會用
- orchestrator 的 graceWindow / Error guard / stale-callback guard / transition gate — shared 機制
- `recordHookAt` — hook → probe 銜接機制

---

## 4. PR 拆分提議

```
PR-1 [Step A] Close PR #676
  - 不縮、不 squash，直接 close
  - 跑偏的 spec/plan/sampling artifact 不 merge
  - 留 reference link 在 fix-spec
  - 即時可做，無相依

PR-2 [W1] Hook → status → 燈號 對齊 audit doc
  - 純 docs，無 code
  - 產出 docs/specs/2026-04-XX-hook-status-audit.md
  - 涵蓋三家 × 5 status × hook 觸發點 × 實際燈號 + Subagent + Proxy + Error 路徑
  - 列出實際燈號 bug 清單作為 W5 工作池
  - 列出實際 probe 缺口清單作為 W6 工作池
  - 依賴 PR-1 merge

PR-3 [W2] Catalog naming separation (PurdexName / UpstreamKey)
  - HookEventSpec 加 PurdexName + UpstreamKey 欄位
  - 三家 events.go catalog 改名（PurdexName 用 "Pdx" 前綴或自定義 namespace，視 PR-3 dev spec 決定）
  - cc/codex installer 改：hooks file key=UpstreamKey, command arg=PurdexName
  - opencode plugin_template 改：emit 時用 PurdexName
  - handler entry 收到永遠是 PurdexName
  - 既有 ~/.codex/hooks.json / ~/.claude/settings.json 需 reinstall（alpha 階段可破壞）
  - 跟 PR-2 平行可做（不依賴 audit 結果）

PR-4 [W3 + W4] Framework 撤回 + Observability 補完
  - 撤 §3 列的 framework code
  - manageActivityWatch policy 改 per-agent gating（三家先全 disable probe，等 W6 為各 agent 加 ProbeIntent 才啟動）
  - 補 dev log 覆蓋整條 hook → DeriveStatus → handler → projection → broadcast 路徑
  - 補 TraceStore step 在每層轉換點寫一筆（per spec §2.3 SOT 設計）
  - 依賴 PR-2 audit 結果決定 dev log 哪些路徑 priority 最高
  - 依賴 PR-3 完成（撤回時 catalog 已是 PurdexName 形態）

PR-5+ [W5 + W6] 修 audit 找出的燈號 bug + 為缺口寫 per-agent ad-hoc probe
  - per-agent / per-bug / per-缺口 拆 multiple small PR
  - 每個 ad-hoc probe 用 ProbeIntentProvider interface（spec §8.2 形態，但 lazy 設計 — 等真寫第一個才 finalize interface shape）
  - readiness 整合（cc CheckReadiness）也屬於這層 — spec §8.2 既定要求

PR-N [W7] Dev Inspector UI (Phase 5)
  - 新 backend endpoint /api/agent/monitor/coverage
  - SPA 4 視圖：Chain / Detail / Projection / Coverage Matrix
  - 主要消費 PR-4 補完的 observability + Coverage 既有結構
  - 依賴 PR-4 ship + PR-5+ 大致 stabilize
```

---

## 5. 對應原 spec phase mapping

| 原 spec phase | 在 fix-spec 中對應 | 狀態 |
|---|---|---|
| Phase 0–3.5 | 不變，已 ship | ✅ |
| Phase 4a (spec §8.1) | PR-4a-0 (alpha.233) ✅ ship；PR-4a-1 (alpha.234) ship 但有越界部分待 W3 撤回；PR-4a-2 (#676) close 不 ship | ⚠️ 部分撤回 |
| Phase 4b (spec §8.2) | W1 (audit) + W3 (撤回 generic framework) + W4 (observability) + W6 (ProbeIntent + readiness 整合) | 重新規劃 |
| Phase 5 (spec §9) | W7 | 不變，仍待 4b 完成 |
| W2 (catalog naming) | 新增，不在原 spec | 同期可讀性整理 |

---

## 6. Risk & Mitigation

| Risk | Mitigation |
|---|---|
| PR-4a-1 ship 在 alpha.234，撤回 framework 可能影響並發開發 | W3 撤回前先 W1 audit + W2 naming，三 PR 並行；W3 撤回時 main 上 framework 才被 active 使用兩週 (alpha.234 → 撤回時 alpha.~240)，影響 surface 小 |
| reinstall hooks.json 破壞所有現有 user 環境 | alpha 階段允許破壞性升級（per `feedback_no_alpha_migration`）；PR-3 ship 後 user 跑一次 `pdx install --reinstall` 即可 |
| W1 audit 規模超預期（三家 × 5 status × N 路徑） | audit 不求完整，求**已知 bug + 已知缺口** 的 evidence；未發現的留 follow-up |
| W6 ProbeIntent interface 設計過早 framework 化 | lazy 設計 — 等寫第一個 per-agent ProbeIntent 時才 finalize interface shape，避免再次 over-framework |
| Phase 5 Inspector 等不到 4b 收斂 | W7 可選擇性提早 — backend Coverage endpoint 可以在 PR-4 一併補；SPA UI 等 PR-5+ 大致 stabilize |
| 主 repo 並發 session（feedback_concurrent_session_safety） | 每個子 PR 進 worktree 前 `git status -s` clean check + push 前 `git pull --rebase origin main` |

---

## 7. 遵循的設計原則 (per lights-rebuild-spec §2.4 Architecture Guardrails)

- **§2.4.1 中央 vs 分散判準**：W3 撤的 ProbeProfileProvider 是「runtime 跑過中央物件」= 嚴重膨脹徵兆 → 撤；W6 ProbeIntentProvider 是「per-agent 自宣告 + 共用 plumbing」= 不膨脹 → 寫
- **§2.4.5 Bloat 警覺詞**：W6 ProbeIntent 設計時警覺「Central FSM / Event catalog / Transition registry」三詞；任一冒出停手反思
- **`feedback_skeleton_convergence`**：W2/W3/W4/W6 設計時警覺五大 bloat 徵兆（把 working code 變 data / parallel registry / 統一抽象 / refactor working code / config flag）

---

## 8. 結束條件（fix-spec 完成）

當 PR-1 至 PR-N 全部 ship 後：

- Lights 子系統完全對齊 spec §8.1 (Phase 4a)/§8.2 (Phase 4b)/§9 (Phase 5) 設計
- 三家 agent 的 hook → status → 燈號 路徑全 audit + 已知 bug 已修
- per-agent ProbeIntent 為實際缺口提供 ad-hoc 補位
- TraceStore + PDX_DEV_MODE log 全路徑可觀察
- Dev Inspector UI 提供視覺化檢視
- catalog naming 完全分離 PurdexName / UpstreamKey

**memory 更新**：

- `kickoff_lights_rebuild.md`：Phase 4a 標完成 with caveats（caveats 由 fix-spec 後續處理）；觸發詞改為按 W1-W7 順序執行
- `project_progress.md`：對應 alpha 版本

---

## 9. 文獻

- 原 spec: `docs/specs/2026-04-23-lights-rebuild-spec.md`（不取代，本 fix-spec 是其修正延伸）
- Phase 4a plans:
  - `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` (PR-4a-0 ship)
  - `docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md` (PR-4a-1 ship, framework 部分待撤)
- Phase 4a-2 廢棄 plans（PR #676 跑偏產物，本 fix-spec 取代）：
  - `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md`
  - `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-plan.md`
  - `docs/specs/2026-04-28-lights-rebuild-phase-4a-2-sampling.md`
- 相關 PR: #664 (4a-0) / #670 (4a-1) / #676 (4a-2 close)
- 相關 issue: 待 W1 audit 後開（W5/W6 工作池）

---

## 10. 後續產出

每個 W 進入實作前，產出對應 dev spec：

- `docs/specs/2026-04-XX-hook-status-audit-spec.md` (W1)
- `docs/specs/2026-04-XX-catalog-naming-separation-spec.md` (W2)
- `docs/specs/2026-04-XX-probe-framework-revert-spec.md` (W3)
- `docs/specs/2026-04-XX-observability-completion-spec.md` (W4)
- `docs/specs/2026-04-XX-light-bug-fix-spec.md` (W5，per-bug 或 batch)
- `docs/specs/2026-04-XX-probe-intent-impl-spec.md` (W6，per-agent 或 batch)
- `docs/specs/2026-04-XX-dev-inspector-ui-spec.md` (W7)

每份 dev spec 後跟對應 plan + 實作 + 兩輪 codex review + ship + bump，沿用 CLAUDE.md 流程。
