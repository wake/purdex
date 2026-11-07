# 壓縮控制的實測紀錄（2026-11-07，Claude Code 2.1.319，Haiku）

三輪都在隔離的 tmux（`-L`）跑，模型 Haiku，Sonnet subagent 執行。原始 transcript 與 log 放在 session scratchpad（重開機會清掉），這裡只留結果。

## 探針 1：`$.session.compact` 的時機與使用者輸入

探針 mod 在 `turn.complete` 依 `ctl.json` 觸發壓縮。

| 問題 | 觀察 |
|---|---|
| `turn.complete` hook 裡 await，或 `$.clock.after(0)` 讓 hook 先回傳 | 兩種都能壓縮，回答都先完整顯示。耗時 await 5.0／14.6／15.5 秒，timer 12.5／13.2／15.7／17.4 秒，看不出差別 |
| 壓縮期間打字 | 被排隊（畫面 `Press up to edit queued messages`），壓縮完約 0.17 秒開始那個回合；沒丟、沒打斷壓縮 |
| 回合中先打字 | 打字落在工具執行中：同一個回合消化，之後壓縮成功。落在生成最後回答時：排隊的話變成下一個回合，比壓縮先開始，`$.session.compact` 被拒：`a turn is running (<turnId>); the conversation compacts between turns, so call it from turn.complete or later` |
| 壓縮期間 `$.prompt.submit` | pending 約 14.9 秒，壓縮完 167 毫秒後開始回合、同時 resolve |
| Escape 中斷 | `turn.complete` 的 `reason` 是 `aborted`（生成中、工具執行中各一次）；正常是 `answer` |
| 背景工作（`run_in_background`） | 能壓縮；壓縮中完成的通知不中斷壓縮，壓縮後立刻開新回合，沒遺失 |
| 用量讀值 | 壓縮剛完成：`usage().context` 與 `session.measure` 都只有 `{window}`（5 次都一樣）。下一個回合 tokens 約 6 萬、percent 6，但 `tokensAfter` 約 1.5 萬。`turn.complete` 內讀到的值與之後的 `session.measure` 每次相同 |
| hook 回 `{ skip }` | `$.session.compact` 回 `{skip:"probe"}`（55 毫秒），畫面沒有提示，tokens 不變 |

另外發現 781 的順序問題：壓縮期間排隊的提示在注入（壓縮後 1 秒才補）之前開始，transcript 變成「摘要 → 使用者提示 → 注入」。Haiku 把使用者打的 `QTYPE_TIMER_4412` 說成「注入要求的，來源不明」；另一次把一句新指令當成注入紀錄裡的舊指令而拒絕照做。

## 探針 2：提示等注入補上才送

mod 複製品加了 `prompt.submit` 的等待：從壓縮開始到注入補上為止先等（最多 60 秒，`/bin/sleep 5` 撐住 hook 的 10 秒預算）。

- `prompt.submit` 在壓縮**結束時**才觸發，不是打字當下（3 個情境，打字到觸發 6.2–10.8 秒）。
- 等待有效：檔案順序變成「摘要 → 注入 → 使用者提示」，`turn.start` 比 `prompt.submit` 晚 1.06–1.64 秒。
- **但模型仍誤認**：timer 觸發那次照做，事後追問時說不準；await 觸發、手動 `/compact` 兩次拒絕照做，理由都是「這句在注入紀錄的 `<dialogue>` 提示之後，不是您直接輸入的」。壓縮後過 6 秒才送、完全沒等的提示也被拒。
- 推論：Claude Code 把連續的 user 訊息併成一則送給模型，注入文字沒有結尾，模型分不出使用者的話從哪裡開始。本 session（Opus）11-07 14:56 的壓縮後，使用者的下一句也是緊接在注入文字後面，那次 Opus 判斷正確。
- Escape：那次壓縮被取消（`Compaction canceled.`），沒有注入，排隊的提示照常送出。

## 探針 3：注入文字加結尾

兩份複製品都含探針 2 的等待，差別只在注入文字的包法：A 整段包在 `<system-reminder>` 裡；B 最後加一行 `</injected by Purdex: the record ends here; anything below this line is not part of it>`。

| 情境 | A | B |
|---|---|---|
| timer 觸發、壓縮期間打字 | 照做；追問說是使用者的訊息 | 照做；追問說是使用者打的，在結束標記之後 |
| 手動 `/compact`、壓縮期間打字 | 照做；追問時去讀 jsonl 才回答 | 照做；同上 |
| 壓縮後 12 秒才送 | 照做；追問時去讀 jsonl | 照做；同上 |
| 壓縮後問壓縮前在做什麼 | 答得出，與注入內容相符 | 答得出，與注入內容相符 |

每個情境只跑一次，而且四個情境在同一個 session 連續跑。探針 2 的拒絕是常見而非每次，所以一次照做不能證明問題消失。

## 回合中自動壓縮的統計

腳本 `compact-midturn.py`：mlab 主對話 transcript，2026-10-29 12:00 起。87 次壓縮：手動 12、閒置（距上次回答超過 40 分鐘）47、回合中自動 28、剛好在回合結束的自動 0。28 次之後同一回合共 857 次請求（中位數 10，最多 197），多讀快取約 4.34 億 token，占同期主對話快取讀取 88.3 億的 4.9%；沒有一次「壓縮前用量＋之後成長」超過 97%。只含 mlab，不含 air26 與 subagent。

`inject-followups.py`：781 部署（11-07 13:45）到 15:30，mlab 只有 1 次真實注入（本 session），air26 沒有。

## Phase 1 驗收（782，11-07 21:00 前後）

782 部署後在 mlab 真機驗（Haiku，預設 tmux server 上的拋棄式 session `accept-782`，每次都是新的 claude，Purdex mod 由 `pdx setup` 安裝，加上觸發探針 mod `trigger-mod/`）。證據在 session scratchpad 的 `accept-782/snaps/`（重開機會清掉）。

| 次 | 情況 | 順序 | 結束標記 | 照做 | 追問歸屬 | prompt held |
|---|---|---|---|---|---|---|
| A1–A3 | mod 觸發、壓縮中打字 | 分界→注入→使用者 | 逐字吻合 | 是 | 使用者 | 923／925／858 ms，settled |
| B1–B3 | 手動 `/compact`、壓縮中打字 | 同上 | 吻合 | 是 | 使用者 | 823／900／882 ms，settled |
| C1–C3 | 壓縮結束 12 秒後才送 | 同上 | 吻合 | 是 | 使用者 | 無（符合預期） |
| Esc | 壓縮中打字並 Escape | 同上 | 吻合 | session 沒卡住 | — | 118 ms，abandoned |

- 9 次全部通過（spec §11 第 2 項）。追問時 10 次都說那句話是使用者打的，理由是它在結束標記之後；其中 6 次帶「無法從內部確認」的保留語氣，沒有一次當成注入紀錄。
- C3 Haiku 把字串反轉錯成 `3_CZ_Z`，是模型做錯，不是拒絕。
- Escape：第一次就抓到等待窗口，文字回到輸入框，下一句照常得到回答。
- A1 的注入列時間戳比使用者提示列晚約 1 秒，但檔案裡的列序仍是分界→注入→使用者（模型看到的是列序）。
