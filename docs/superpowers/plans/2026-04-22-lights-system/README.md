# Lights System — Plans

Spec：[`../../specs/2026-04-22-lights-system-design.md`](../../specs/2026-04-22-lights-system-design.md)（v3 封版，PR #548 @ alpha.199）

7 phase / 17 PR 的實作計畫。每個 phase 獨立檔，含 1 段說明 + ≤5 主架構。每主架構列出對應 spec section 與 TDD checklist，實作細節由實作時決定。

## 檔案索引

| Phase | 標題 | PR 數 | 檔案 |
|---|---|---|---|
| 0 | 輕清理 | 1 | [phase-0.md](phase-0.md) |
| 1 | Schema + 雙寫過渡 | 2 | [phase-1.md](phase-1.md) |
| 2 | Arbitrator 切換 | 3 | [phase-2.md](phase-2.md) |
| 3 | 抽象層重構 | 2 | [phase-3.md](phase-3.md) |
| 4 | 三 agent 對齊 | 3 | [phase-4.md](phase-4.md) |
| 5 | 硬編拆除 + SPA 對齊 | 4 | [phase-5.md](phase-5.md) |
| 6 | Trace viewer | 2 | [phase-6.md](phase-6.md) |

## 核心哲學（v3 釘死）

> 「沒有反應」比「錯誤反應」更好。

- Role unknown → drop + trace only，不建 unknown actor
- Status unknown → 不改 actor.status，只進 trace
- Generation 只有 `hook.SessionStart` 可推進
- Reconcile 只觀察不 apply

## 實作順序

預設串行：Phase 0 → 1 → 2 → 3 → 4 → 5 → 6。

Parallel 機會：
- Phase 4 內 PR-4a（Codex）+ PR-4b（OpenCode）可 parallel（獨立檔）
- Phase 6 PR-6a（API）可從 Phase 1 合後就並行，不必等 5

## 已完成

- PR #486 `feat/agent-watch-alive` close + 分支刪（spec kickoff session 於 2026-04-22 完成）
