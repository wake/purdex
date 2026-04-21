# Phase 0 — 輕清理

> Spec 對照：§10 PR 表 PR-0；§6.4
> 依賴：—
> PR：PR-0（極小）

清理舊實作遺跡，為新架構鋪路。watchAlive 分支（PR #486）已於 spec kickoff session close + 分支刪除，剩下把 Codex 的 `HasReadiness` capability bit 先落成 placeholder（預設 false，真實邏輯留 Phase 4）。獨立 PR 是為了讓後續每個 phase 都從乾淨的 capability matrix 出發，避免 Phase 3 抽象層落地時還要改既有 agent 的 capability 宣告。

## 主架構

### 1. Codex `HasReadiness` capability bit placeholder（§6.4）

- [ ] 測試：`registry.Get("codex").Descriptor().Capabilities.HasReadiness == false`
- [ ] 測試：spec 加 capability 後不破壞既有 codex session 的 probe 行為（readiness 仍是 stub，回 `running`）
- [ ] Descriptor / capability bit 宣告位先加（struct field 或 const flag，擇一即可，Phase 3 可能統一）

## 驗收

- [ ] VERSION + CHANGELOG bump（獨立 bump PR）
- [ ] PR #486 on GitHub 顯示 closed（已完成）
