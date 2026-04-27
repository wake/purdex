# Lights Rebuild — Phase 4a-2 Spec v1 (PR-4a-2 codex / opencode ProbeProfile)

## v1.x 演進

| 版本 | Commit | 日期 | 主要變更 |
|---|---|---|---|
| v1 | (TBD) | 2026-04-28 | 初版 |

## 0. 來龍去脈

PR-4a-1 v2.1 已 ship at alpha.234（PR #670），落地內容包含：

- `agent.ProbeProfile` struct + `ProbeProfileProvider` optional interface
- `probeOrchestrator` 解析 provider profile（fallback 到 `defaultProbeProfile = {BottomLines: 10, IdleStableTicks: 3}`）
- cc 實作 `ProbeProfile() = {TopLines: 12, IdleStableTicks: 3}`
- legacy `ActivitySignal` enum / `StartWatch` API / `activityLoop` / `onActivityDetected` 全清（compile 必需）

PR-4a-1 plan v2.1 §1.2 已縮減 PR-4a-2 範圍：

> codex / opencode wiring 走 default profile 已在 PR-4a-1 完成；只有「**自訂 profile values**」延到 PR-4a-2。

本 spec 落實該縮減：PR-4a-2 = **codex `ProbeProfile()`** + **opencode `ProbeProfile()`** 兩個自訂 profile，不含其他結構性改動。

## 1. Scope

### 1.1 In scope

**Slice 5 — codex `ProbeProfile()`**

- `internal/agent/codex/probe_profile.go`（新檔）— codex.Provider 實作 `ProbeProfile()`
- `internal/agent/codex/probe_profile_test.go`（新檔）— characterization test 釘 codex profile values，含 rationale 註解

**Slice 6 — opencode `ProbeProfile()`**

- `internal/agent/opencode/probe_profile.go`（新檔）— opencode.Provider 實作 `ProbeProfile()`
- `internal/agent/opencode/probe_profile_test.go`（新檔）— characterization test 釘 opencode profile values，含 rationale 註解

### 1.2 Out of scope（明列分流 — 不在本 PR）

- **`shouldWatchActivity` per-agent 化**：當前 `internal/module/agent/module.go:494` 的 `shouldWatchActivity(status)` 是 status-based policy（Waiting / Running / Idle → watch；其他 → not watch）。三家 agent 在這維度邏輯一致；無觀察到差異化需求。**本 PR 不做**（per kickoff 對齊 A2）。若未來出現 per-agent 差異需求（例如 opencode 在某 status 不需 watch），開新 PR 處理。
- **Slice 8 清舊（onActivityDetected / activityLoop / ActivitySignal enum）**：plan v1.3 §7.2 列為「PR-4a-2」是過時 wording；實際在 PR-4a-1 v2.1 §1.1（compile 必需）已全清。本 PR 無遺留可清。
- **Default profile 改變**：`defaultProbeProfile = {BottomLines: 10, IdleStableTicks: 3}` 維持不動。本 PR 是「為 codex / opencode 加 provider override」，不動 fallback。
- **新增 ProbeProfile 欄位**：`{TopLines, BottomLines, IdleStableTicks}` 三欄足以表達 codex / opencode 需求；本 PR 不擴 schema。
- **probe primitive / orchestrator 改動**：probe layer 與 orchestrator wiring 全部沿用 PR-4a-1 v2.0 dumb-probe 設計，不動。
- **Plugin template / hooks / catalog 改動**：opencode 1.14.23 hook surface 已在 PR-4a-0 對齊；本 PR 不動 hook / template / catalog 任何檔案。

## 2. 設計

### 2.1 設計原則

1. **Provider-owned tuning**：每家 agent 自己最了解 TUI 結構，profile 值由各 provider 宣告；orchestrator 只負責 lookup + forward，不強制統一。
2. **Default fallback 保留**：未實作 `ProbeProfileProvider` 的（未來新）agent 仍享 PR-4a-1 G5 parity（`BottomLines: 10`），不被本 PR 影響。
3. **Characterization test = pinning**：每家 profile 值用 characterization test 釘住；改值要明確改測試，避免靜默 drift（同 cc 的 CC1 模式）。
4. **Rationale 內嵌註解**：每個 profile field 的選擇理由寫在 source 註解 + test 註解（雙寫），未來改值的 reviewer 知道 why。

### 2.2 Codex `ProbeProfile()`

#### 2.2.1 推薦值

```go
func (p *Provider) ProbeProfile() agent.ProbeProfile {
    return agent.ProbeProfile{
        TopLines:        10,
        IdleStableTicks: 3,
    }
}
```

#### 2.2.2 Rationale

**Codex CLI TUI 結構觀察**：

- **底部**：prompt 輸入區 + spinner（含 elapsed timer，每秒變動）+ 模型 / token 統計列
- **頂部 + 中段**：對話歷史（assistant 訊息、工具呼叫輸出、user prompt）— append-only 渲染
- **新 turn 進入時**：頂部會 scroll up，top hash 隨之變動 → 適合作 activity signal
- **idle 時**：頂部維持上一個 turn 的尾巴，hash 穩定 → 適合作 idle signal

**為何不用 `BottomLines`**：

- 底部 spinner / elapsed timer 在 running / waiting 狀態持續變動，但這是「畫面動但 agent 沒進度」的場景；BottomLines hash 對此會誤報 ScreenChanged，破壞 idle 判定。
- 同一理由 PR-4a-1 為 cc 選 `TopLines` 而非 `BottomLines`（cc 的 ●● header + task description 是頂部固定區）。

**為何不用 `BottomLines: 10` default profile**：

- 走 default 在 PR-4a-1 是 G5 parity 過渡用（避免一次改太大），本 PR 是 PR-4a-2 既定終點。
- 實證：PR-4a-1 plan §2.4.1 cc 已驗證 TopLines 對 spinner / 彩虹底部位置雜訊的過濾效果優於 BottomLines。

**為何 `TopLines: 10` 而非 cc 的 `12`**：

- cc TopLines=12 是為了 cover ●● header + task description block（cc-specific UI 結構）。
- codex 沒有等價的 fixed-top 區塊；10 行對應 default profile 的 line count，作 baseline；若實作後採樣顯示 10 行不足以涵蓋 turn boundary，再 tune up（spec 容許 +/- 5 範圍微調）。

**為何 `IdleStableTicks: 3`**：

- 對齊 watch-loop default（v2.0 dumb probe 設計）；codex 無觀察到需要更長的穩定窗口。
- 3 ticks × 500ms = 1.5s 穩定即判 idle，與 cc 一致。

#### 2.2.3 Implementer 採樣 confirm（TBD gate）

實作時 implementer 須對真實 codex session 做一次 `tmux capture-pane` 採樣（idle / running / spinner 三狀態），驗證：

1. TopLines=10 在 idle 狀態 hash 穩定（連續 3 ticks 一致）
2. TopLines=10 在 new turn 進入時 hash 變動（fire ScreenChanged）
3. 頂部 10 行內容**不含** elapsed timer 等持續變動字元

若任一條件不滿足 → 回 spec 修推薦值或調整 capture mode（例如改採 BottomLines + 較大 IdleStableTicks），並更新 §2.2 + 對應 test。

### 2.3 OpenCode `ProbeProfile()`

#### 2.3.1 推薦值

```go
func (p *Provider) ProbeProfile() agent.ProbeProfile {
    return agent.ProbeProfile{
        TopLines:        10,
        IdleStableTicks: 3,
    }
}
```

#### 2.3.2 Rationale

**OpenCode TUI 結構觀察**：

- 與 codex CLI 結構同型：底部 prompt + spinner / status，上方對話歷史 append-only。
- `internal/agent/opencode/events.go` 的 `tui.prompt.append` 是「TUI 有 prompt channel」的弱證據（catalog 主要描述 hook surface，不是渲染結構）。
- `chat.message` / `permission.asked` / `question.asked` 等實際 plugin 訂閱對話流事件可在 `internal/agent/opencode/plugin_template.go` switch 與 `internal/agent/opencode/testdata/opencode-1.14.23-events.json` catalog 中追蹤；上述事件每次 fire 對應對話流新訊息 → 頂部 scroll，與 codex 行為一致。
- TUI 渲染結構與行為的最終驗證仍以 §2.3.3 implementer 實機採樣為準（spec 推論不取代真實 pane 觀察）。

**為何 profile 與 codex 同值**：

- 兩家 TUI 結構同型；無觀察到需差異化的場景。
- 避免「為差異而差異」的人為 tuning（per §3 bloat 警覺）。

**為何不用 `BottomLines`**：

- 同 §2.2.2：底部 spinner / status 變動會破壞 idle 判定。

#### 2.3.3 Implementer 採樣 confirm（TBD gate）

實作時 implementer 須對真實 opencode session 做一次 `tmux capture-pane` 採樣，驗證同 §2.2.3 三條件。

若採樣顯示 codex / opencode 結構差異需要不同 profile 值（例如 opencode 對話 turn 較長需要 IdleStableTicks=4），更新 §2.3 並文件化「不同值的依據」。**不接受**「為對齊而對齊」回去同值；profile 是 per-agent 政策，差異有依據即可保留。

### 2.4 Default profile 維持不動

`defaultProbeProfile = {BottomLines: 10, IdleStableTicks: 3}` 不動。Rationale：

- 是「未實作 `ProbeProfileProvider` 的新 agent」的 fallback，本 PR 不動。
- 未來若所有現存 agent 都自訂 profile，可考慮調整 default 或加 deprecation warning，但**不在本 PR 範圍**。

### 2.5 Probe orchestrator 自動接管

本 PR 不需動 `internal/module/agent/probe_orchestrator.go`：

```go
profile := defaultProbeProfile
if provider, ok := o.parent.registry.Get(agentType); ok {
    if pp, ok := provider.(agentpkg.ProbeProfileProvider); ok {
        profile = pp.ProbeProfile()
    }
}
```

orchestrator type-assertion 自動 pick up codex / opencode 新加的 `ProbeProfile()` method。**整合測試**透過既有 `probe_orchestrator_test.go` / `probe_orchestrator_integration_test.go` 既有 fake provider 機制覆蓋 — 本 PR 加新 fake provider case（OR-codex / OR-opencode）驗證 orchestrator 對 codex / opencode 真的取到 TopLines profile（非 fallback default）。

## 3. 不做的事（明列 boundary + bloat guard）

本節同時作為本 spec 的 **bloat guardrail**：對「擴 schema / 新增欄位 / 引入混合 capture mode / 加 per-agent metric」這類擴張提案，本節即為 anchor — 提案者須在此節新增一行說明為何該擴張不在範圍。

| 項目 | 為何不做 |
|---|---|
| **`shouldWatchActivity` per-agent 化** | 三家在 status 維度邏輯一致；無 per-agent 差異需求被觀察到（per kickoff A2）。證據：`internal/module/agent/module.go:494` 的 status-only switch；三家 `internal/agent/{cc,codex,opencode}/events.go` 的 `EmitsStatus` set；`internal/agent/drift_test.go` 守住 Status 對齊 |
| **新 ProbeProfile 欄位**（如 `BottomLines + TopLines` 同時、`SkipChars` 等） | 三欄（TopLines / BottomLines / IdleStableTicks）已足；不為假設場景擴 schema；採樣失敗只在既有三欄內調整 |
| **混合 capture mode**（同時 hash top + bottom） | TopLines 與 BottomLines 在 `WatchOptions` 設計即為互斥（PR-4a-1 §2.1.2）；若需要 full-pane fallback 應使用 `{TopLines: 0, BottomLines: 0}`（per PR-4a-1 §2.1.2 — 兩欄同 0 才退到 full pane），而非新增混合模式或單欄走 full-pane |
| **Default profile 改用 TopLines** | default 是「未實作 ProbeProfileProvider 的 agent fallback」；本 PR 範圍只到三家現存 agent 全自訂後再考慮 |
| **probe primitive / orchestrator 改動** | PR-4a-1 v2.0 dumb-probe 架構未發現問題 |
| **opencode hook / template / catalog 改動** | 已在 PR-4a-0 對齊；本 PR 不動 |
| **加 metrics / dev log（codex / opencode 專屬）** | PR-4a-1 已加 `purdex_probe_*` expvar + `PDX_DEV_MODE=1` log，三家共用，不需 per-agent |
| **整合 readiness（cc CheckReadiness 等）** | 是 spec §8.2 Phase 4b 範圍 |

## 4. Risk

| Risk | Mitigation |
|---|---|
| **TopLines=10 對 codex / opencode 採樣後不適用** | §2.2.3 / §2.3.3 implementer 採樣 confirm gate；若採樣不過，spec 同步修推薦值，不硬上 |
| **codex / opencode 結構未來變動**（upstream UI 改版）| Characterization test 只防止 Purdex profile 常數 silent drift（值改了強制改測試 + reviewer 看到 rationale）；upstream UI 改版**不會** trigger test fail。upstream drift 由 §2.2.3 / §2.3.3 實機採樣與後續 e2e / observability（probe metrics 異常、user 回報誤判）發現，需顯式重採樣才能確認 |
| **三家 profile 值難以維護一致**（drift） | 每家 profile 由各 provider 自宣告 + characterization test 釘；無「中央同步」需求（per §2.1 設計原則 #1 Provider-owned tuning；對齊 lights-rebuild-spec §2.4.1 分散判準） |
| **新人不知道 profile 改動需 PR review** | source 註解 + test 註解雙寫 rationale + 引用本 spec 路徑（同 cc CC1 模式） |
| **主 repo 並發 session**（feedback_concurrent_session_safety）| 進 worktree 前 `git status -s` clean check + 留痕 PR description（沿用 PR-4a-0 / PR-4a-1 流程）|

## 5. Ship gate

| Gate | 條件 |
|---|---|
| G1 codex Provider 實作 ProbeProfileProvider | `internal/agent/codex/probe_profile.go` 存在；test 覆蓋 |
| G2 opencode Provider 實作 ProbeProfileProvider | `internal/agent/opencode/probe_profile.go` 存在；test 覆蓋 |
| G3 orchestrator 整合測試覆蓋 | `probe_orchestrator_test.go` 加 fake codex / opencode provider case，驗證 orchestrator 取到自訂 profile（非 default fallback）|
| G4 cc profile 不受影響 | 既有 `cc/probe_profile_test.go` 全綠 |
| G5 default profile 不受影響 | 既有 orchestrator default-profile fallback test 全綠 |
| G6 全 repo `go test ./...` 全綠 | TDD 紅燈→綠燈→commit；包含現有 metrics tests（`internal/agent/metrics_test.go`）與 dev log gating tests（`internal/module/agent/probe_orchestrator_test.go` 內 PDX_DEV_MODE 相關 case）— 本 PR 不加新 metrics / dev log，所以既有 test 通過即代表三家共用機制未受影響 |
| G7 implementer 採樣 confirm — **PR description 留痕** | §2.2.3 / §2.3.3 三條件每家都驗證；**PR description 必含**：(a) 採樣命令（含 tmux session target）、(b) 三狀態（idle / running / spinner）各自 `tmux capture-pane` 輸出（前 N 行）、(c) hash 是否穩定 / 變動的判讀、(d) 頂部 N 行是否含 elapsed timer / spinner 字元的觀察。若三條件之一不過 → spec 修值或改 capture mode 後再上 |
| G8 SPA lint / build / vitest 不受影響 | 本 PR 不動 SPA，但仍跑一次驗證 |

## 6. 後續

- **下個 PR**：本 PR ship 後，Phase 4a 全部完成。Phase 4b（`ProbeIntentProvider` + readiness 整合）按 spec §8.2 接續。
- **memory 更新**：`kickoff_lights_rebuild.md` 觸發詞 / 階段更新；`project_progress.md` 更新到 alpha.235（bump PR ship 後）。

## 7. 文獻

- Lights rebuild spec: `docs/specs/2026-04-23-lights-rebuild-spec.md`（§8.1 Phase 4a 原方向；§8.2 Phase 4b 後續）
- Phase 4a plan v1.3: `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md`（§7.2 PR-4a-2 大綱起源）
- Phase 4a-1 plan v2.1: `docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md`（§1.2 範圍縮減關鍵 — 本 spec 落實依據）
- 既有實作參考: `internal/agent/cc/probe_profile.go`（cc CC1 — 同型 characterization test 模板）
- 前置 PR: #670（PR-4a-1 ship）/ #664（PR-4a-0 ship）
