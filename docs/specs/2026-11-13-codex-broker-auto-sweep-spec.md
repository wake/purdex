# Spec — codex broker 自動清理（#668 收尾）

日期：2026-11-13。前作：`2026-05-01-codex-broker-governance-spec.md`（P1 盤點 #792、P2 判定＋殺＋手動 API #813 已出貨）。

## 1. 為什麼

2026-11-13 air26 被 99 個殘留的 codex broker（連同子行程共約 1170 個、swap 15 GB）擠到記憶體壓力 2，wwd 判成 warning、
重型名額歸零，4 件只有 air26 能跑的 aigora-v2 測試卡了 75 分鐘。P2 的判定在 dry-run 下正確判出這些 broker 已無人使用
（`kill: true`），但沒有東西自動去跑它，而且：

1. **沒有觸發**：P3（定時掃描）沒做，只能手動 `POST /api/codex/brokers/sweep`。
2. **全部被當成 foreign**：kill 規則要求 launch registry 有登記，但寫入端（原訂 PR-G）從沒做，registry 永遠空，
   未過濾的 apply 一律跳過 foreign → 就算接上定時器也一個都不殺。
3. P2 已經算出的 E1（工作目錄確認消失）從未被 sweep 採用。

## 2. 做什麼

### 2.1 自動清理迴圈

`Module.Start` 起一個背景 goroutine：啟動後立刻跑一次，之後每 **10 分鐘**跑一次；`Module.Stop` 取消並等它結束。
每次跑＝「未過濾、apply 模式」的 sweep，但走 **auto 規則**（2.2），單次預算 **3 分鐘**。

- 與手動 sweep 共用同一把 `globalApplyMu`（寫鎖），互相排隊；拿不到鎖就等到預算用完、這輪放棄。
- `ApplyDisabled` 非空（quarantine.json 損毀的降級模式）時整輪跳過，不掃描。
- 有殺、有錯、有隔離時才寫 log（一行摘要＋每個 key 的 reason）；沒事不寫。每個 kill 照 P2 的 KillSequence 寫 audit dump。

### 2.2 auto 規則（只多一條）

在 sweep 現有的 kill 判斷表加一列，只對 auto 生效：

| 條件 | 結果 | audit reason |
|---|---|---|
| auto ∧ `decision.Kill` ∧ foreign ∧ `decision.OverrideE1` | 殺 | `workspace_gone` |

其餘列不變：

- `decision.Kill` 為 false 一律不殺（存活保護優先：有進行中的 thread／queued 或新鮮 running job、30 分鐘內有可讀結果、
  閒置未滿 30 分鐘、工作目錄在而且有 pane／session／lease）。
- 手動未過濾 apply 照舊不殺 foreign（即使 E1 成立）。
- 手動帶 brokerKey 照舊可覆寫 foreign。

E1 的定義沿用 P2：工作目錄 `os.Stat` 明確回 ENOENT，且同一 broker 兩次觀察相隔 ≥ 60 秒（daemon 生命週期內的
`E1Tracker`；重啟後重新計）。暫時性錯誤（ESTALE／EIO／EACCES／逾時）不算。所以一個 worktree 被刪後，它的 broker
最快在第二輪（約 10 分鐘後）、且閒置滿 30 分鐘時才會被清。

### 2.3 預算用完就停

sweep 迴圈在處理每個 broker 前檢查 ctx：已過期就停止（剩下的不評估、不殺），在 `Errors` 記一筆
`sweep deadline reached: N brokers not evaluated`。手動與 auto 都適用。理由：過期的 ctx 會讓 predicate A 的 RPC 立刻失敗，
只剩 state.json 當依據，保護變弱。

## 3. 不做什麼

- 不寫 launch registry、不做 ExitWorktree hook、WS 推播、metrics、盤點快取、設定鍵、SPA 看板（原 P3 其餘與 P4）。
- 不清「broker 已死但 socket 目錄還在」的殘留（`/var/folders/.../cxc-*`），不佔記憶體。
- 工作目錄還在、但開它的 session 已結束的 broker 不清（每個目錄最多一份，不會無限累積）。

## 4. 風險

- **外接碟沒掛**：`~/Workspace` 卸載時所有 worktree 都會 ENOENT。兩次觀察後，閒置滿 30 分鐘、沒有新鮮結果的 broker 會被清。
  後果只是下次用 codex 要重新暖機；結果檔在 `~/.claude/plugins/data`（內建碟），不受影響。可接受。
- **誤殺進行中的 job**：受 `decision.Kill` 的存活保護擋；KillSequence Step 0 會重驗 (pid, lstart, cmdline)，不符就轉 E2 隔離。

## 5. 驗收

- 單元測試覆蓋 2.2 每一列（auto 殺／不殺三種、手動未過濾仍不殺）、2.1 降級跳過、迴圈啟動即跑與 Stop 會等、2.3 過期即停。
- 部署後在 mlab 與 air26：`GET /api/codex/brokers` 的 broker 數量在開關 worktree 一週後不再單調上升；daemon log 出現
  `workspace_gone` 的自動清理紀錄。
