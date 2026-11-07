# 壓縮控制 spec

2026-11-07。長時 session 改版的第三個 change。前兩個是 `2026-11-01-session-dialogue-log-spec.md`（對話檔、回合摘要、recap，alpha.777）與 `2026-11-07-compact-inject-recap-spec.md`（壓縮時注入 recap，alpha.781）。

## 1. 為什麼要做

現在兩台主機的 `~/.claude/settings.json` 都設 `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=70`：每個 session 都在 context 用到 70% 時壓縮，回合跑到一半也照壓；閒置約 54 分鐘的壓縮也全部開著。討論型的 session 因此常在談到一半時被壓縮，而這些都只有全域設定，沒辦法逐個 session 調整。

781 已經讓每次壓縮後都補上 recap，自動接力也在 11-07 關了。這個 change 把「什麼時候壓縮」交給 Purdex 決定，並讓每個 session 可以各自設定。

## 2. 定案（使用者 11-01）

- 壓縮改由 Purdex mod 觸發。Claude Code 原生的壓縮（約 97%）只當安全網：mod 只能比它早，不能比它晚。只涵蓋有 Purdex mod 的 Claude Code。
- 介面可以開關、調門檻。
- 壓縮模式是「主機預設＋逐 session 覆寫」：
  - 討論：手動壓縮，閒置壓縮關。
  - 獨立開發：自動，70%。
  - team：自動，70%，加上 lead 管理。
  - 每個 session 都可以調門檻、自動壓縮開關、閒置壓縮開關。

## 3. 範圍

**要做**：
1. 設定：主機預設（獨立 session、team 各一組）與逐 session 覆寫，由 daemon 存並算出生效值。
2. mod：回合結束時用量達門檻就壓縮；依設定擋下閒置壓縮。
3. 修 781 的注入：壓縮後使用者的提示可能被模型當成注入的舊紀錄。注入文字加結尾，壓縮期間送出的提示等注入補上才送（§4.3、§6.5）。
4. App：主機頁的「壓縮」區；個人面板與 team 面板上的 session 設定。
5. lead：`pdx compact`，看與改 member 的設定、叫 member 壓縮。
6. 部署：兩台拿掉 `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`，讓安全網回到原生約 97%。

**不做**：
- Codex、沒有 Purdex mod 的 session、`claude -p`。
- 改壓縮摘要的指示（`instructions`）。
- 退役自動接力的程式（D3）。
- 回合中途由 mod 壓縮：mod API 不允許（§4.2）。
- session 自己改自己的設定：沒有 slash command，agent 也沒有指令可用。
- App 的「立刻壓縮」按鈕：在終端機打 `/compact` 即可。

**做出來卻算失敗的情況**：
- 壓縮打斷使用者：回答沒顯示完就壓、使用者打的字不見、使用者的提示被模型當成注入的舊紀錄。
- 同一段 context 反覆被壓縮。
- 設成「討論」的 session 被自動壓縮（安全網除外）。
- daemon 不在、設定讀不到時，壓縮比今天更糟。這時應該退回 Claude Code 原生的行為。

## 4. 實測事實（11-07，Claude Code 2.1.319）

### 4.1 回合中的自動壓縮（mlab 主對話 transcript，10-29 起 9 天）

87 次壓縮：手動 12 次、閒置 47 次、回合中自動 28 次。沒有一次自動壓縮剛好落在回合結束。

mod 只能在回合結束時壓縮，所以這 28 次會延到回合結束。依壓縮後同一回合還跑了多少請求估算：
- 這些回合之後共有 857 次請求，中位數 10 次，最多 197 次。
- 多讀的快取約 4.3 億 token，約占同期快取讀取（88 億）的 4.9%。
- 沒有一次會撐到 97%，最高約 83%（1M 視窗的 session 從 69% 再長 14.8 萬 token）。

只算 mlab，沒含 air26 與 subagent，是粗估。腳本：`docs/specs/assets/2026-11-07-compact-control/compact-midturn.py`。

### 4.2 `$.session.compact`（探針 1）

三輪探針的紀錄都在 `docs/specs/assets/2026-11-07-compact-control/probes.md`。

- 在 `turn.complete` hook 裡直接 await，或用 `$.clock.after(0)` 讓 hook 先回傳，兩種都能壓縮，回答都先完整顯示。壓縮花 5–17 秒，這還沒算 781 等 recap 的時間（最多 20 秒）。
- 壓縮期間打字：被排隊，壓縮完約 0.17 秒開始那個回合，不會丟掉，也不會打斷壓縮。
- 使用者在模型生成最後回答時打字：那句排隊的話變成下一個回合，比壓縮先開始，`$.session.compact` 被拒，訊息是 `a turn is running (<turnId>); the conversation compacts between turns`。打字落在工具執行中的話，會在同一個回合裡被消化，之後壓縮照常。
- `$.prompt.submit` 在壓縮期間送出：等到壓縮完才開始，不會被拒。
- 中斷的回合，`turn.complete` 的 `reason` 是 `aborted`；正常回答是 `answer`。
- 有背景工作（Bash `run_in_background`）時照樣能壓縮；壓縮中完成的通知不會中斷壓縮，也不會遺失。
- 壓縮剛完成時，`usage().context` 只有 `window`，沒有 `tokens` 與 `percent`；下一個回合才有。下一個回合讀到的 tokens（約 6 萬）比 `tokensAfter`（約 1.5 萬）大很多。
- hook 回 `{ skip }` 時，`$.session.compact` 回傳 `{ skip }`，畫面沒有任何提示。

### 4.3 781 的注入會讓模型誤認使用者的話（探針 1–3）

兩個問題疊在一起：

1. **順序**：壓縮期間排隊的提示，在壓縮完約 0.2 秒就開始；781 的注入要在壓縮 hook 回傳 1 秒後才補。transcript 因此變成「摘要 → 使用者提示 → 注入」。
2. **沒有結尾**：Claude Code 把連續的 user 訊息併成一則送給模型（推論），而注入文字只有開頭標記、沒有結尾。模型分不出注入在哪裡結束、使用者的話從哪裡開始。

探針 2 把順序修好之後，Haiku 仍然有兩次拒絕照做，理由是「這句在注入紀錄之後，不是您直接輸入的」；壓縮後過 6 秒才送、完全沒排隊的提示也被拒。探針 3 再加上結尾後，兩種寫法的四個情境都照做，模型也說得出那句是使用者打的。不過每個情境只跑了一次。

真實 session 目前只有一例（本 session 11-07 14:56，Opus）：使用者的下一句緊接在注入文字後面，那次 Opus 判斷正確。

改成回合結束時由 mod 壓縮之後，使用者剛讀完回答、正要打字，排隊的情況會變常見；閒置壓縮後使用者回來打的第一句，今天就會碰到第 2 個問題。所以這部分要最先做（§13 Phase 1）。

結尾的兩種寫法，探針 3 結果相同。採用 B（Purdex 自己的結束標記），不採用 A（借用 Claude Code 的 `<system-reminder>` 標籤）。B 被追問時，模型直接說「這句在結束標記之後」；A 則要去讀 jsonl 才回答。

## 5. 設定

### 5.1 三個值

| 值 | 意思 | 討論 | 開發 |
|---|---|---|---|
| `auto` 自動壓縮 | 回合正常結束、用量達門檻時，由 mod 壓縮 | 關 | 開 |
| `threshold` 門檻 | context 用量的百分比，跟 App 上 context 圈圈是同一個數，整數 1–100 | 不變 | 70 |
| `idle` 閒置壓縮 | Claude Code 閒置約 54 分鐘、快取快過期前的壓縮 | 關 | 開 |

模式不另外存。App 依三個值顯示「討論」「開發」，對不上兩者就顯示「自訂」。

門檻高於原生安全網（約 97%）時，mod 永遠不會先觸發，等於交給原生壓縮。不另設上限。

### 5.2 主機預設

host config 新增一列 `compaction`：

```json
{ "solo": { "auto": true, "threshold": 70, "idle": true },
  "team": { "auto": true, "threshold": 70, "idle": true } }
```

- `solo` 給不是 lead 也不是 member 的 session，`team` 給 lead 與 member。角色在讀的當下從 team.db 判斷。
- 沒寫過的主機，兩組都是開發（自動、70、閒置開），跟今天的行為一樣。
- 另開一列，不併進 `relay` 列：舊 App 遇到 `relay` 列裡不認得的欄位會整列判壞（#3970）。

### 5.3 逐 session 覆寫

team.db 新表 `session_compaction`：`session_id` 為主鍵，`auto`、`threshold`、`idle` 都可以是 NULL，NULL 表示跟主機預設；另有 `updated_at`。三個值都清成 NULL 時刪掉那一列。

- 接力後的新 session 沿用前任的覆寫：寫 `session_lineage` 時一起複製。
- 手動 `/clear` 是新對話，不沿用。

### 5.4 生效值與「不介入」

生效值 = 覆寫 ?? 主機預設[角色]。

下面兩種情況，壓縮控制整個不介入（`managed: false`），跟今天一樣交給接力與原生壓縮：
- 這個 session 的自我接力是開的：主機的 `self_solo`／`self_lead` 開，且沒有 `/relay off`。
- member，且主機的 `member_auto` 開。

自動接力 11-07 已經兩台全關，所以上線後所有 session 都會被管理。

### 5.5 誰能改

- 使用者：App（§8）。
- lead：自己 team 的 member（§9）。
- agent 不能改自己的設定。

## 6. mod

### 6.1 讀設定

mod 每 15 秒、以及 session 換 id 時，已經會讀 `GET /mod/v1/team`。回應加一個欄位：

```json
"compaction": { "managed": true, "auto": true, "threshold": 70, "idle": true }
```

讀不到就保留上一次讀到的值；從來沒讀到過，就不介入。舊 daemon 沒有這個欄位，等於不介入。

### 6.2 觸發

`turn.complete`（主對話，不是 subagent）時，下面全部成立才壓縮：
- `reason` 是 `answer`。中斷、拒答、API 錯誤結束的回合都不壓。
- `managed` 與 `auto` 為真。
- mod 的接力狀態是閒置：沒有接力在進行、沒有待處理的 member 接力控制訊息、沒有被暫停。
- `usage().context.percent` 有值且 ≥ 門檻。
- 防止重壓：每次壓縮（不論誰觸發）之後，第一個讀到的 tokens 記為「底」；之後 tokens 要比底多 20,000 才會再觸發。mod 重新載入後沒有底，第一次不受這條限制。

成立時用 `$.clock.after(0)` 呼叫 `$.session.compact()`，不在 hook 裡等，讓 `turn.complete` 的其他步驟照常跑完。
- 被拒、訊息是回合正在跑（§4.2）：什麼都不記，下個回合結束再判斷。
- 其他失敗，或被某個 hook 回 `{ skip }`：把底設成當下的 tokens，要再長 20,000 才重試。

### 6.3 閒置壓縮

mod 現在用「`trigger` 是 `auto` 且沒有主回合在跑」辨認閒置壓縮。`managed` 為真且 `idle` 為假時：
- 閒置壓縮回 `{ skip }`。
- `precompute`（引擎預先算摘要，結果留給下一次壓縮）也回 `{ skip }`，避免白花一次摘要請求。

member 的既有攔截（`member_auto` 開時交給 lead 決定）照舊排在前面。`member_auto` 開時本來就不介入（§5.4），兩者不會同時生效。

回合中的自動壓縮（安全網）永遠不擋：擋了會逼近硬上限，連手動 `/compact` 都可能失敗。

### 6.4 回報與紀錄

- 回報 daemon 已壓縮（`pdx relay compacted`）時，`plugin` 一律報成 `manual`。今天 `plugin` 完全不回報，daemon 不會清掉過期的接力半檔。daemon 把它當人為壓縮：不通知 lead。
- 781 的 `compact-context` 請求加上 `trigger`，daemon 的 `compact context` log 印出來。transcript 的分界列把 mod 觸發的壓縮寫成 `manual`，之後要分開統計來源只能靠這行。

### 6.5 注入的結尾，與壓縮期間送出的提示

§4.3 的修正，所有觸發方式都適用，不只 mod 觸發的壓縮。

**結尾**：daemon 組的注入文字（781 spec §6）最後加一行：

```
</injected by Purdex: the record ends here; anything below this line is not part of it>
```

開頭兩行不變。這行由 daemon 加，mod 照舊只負責補上。

**等注入**：從壓縮開始（向 daemon 要注入文字時）到注入補上或確定不補為止，`prompt.submit` 先等，最多 60 秒。
- 「確定不補」：daemon 回 204 或逾時、壓縮被 skip 或失敗、session 換了 id。
- 等的期間用本機的 `/bin/sleep 5` 撐住 hook 的 10 秒預算，做法同 `register.js` 的 `sleepUntil`。
- 按 Escape 就放行。
- 實測 `prompt.submit` 在壓縮結束時才觸發，所以排隊的提示多等約 1–1.6 秒。

## 7. daemon

- `GET /api/hostconfig/compaction`、`PUT /api/hostconfig/compaction`：照其他 host config 列，用 revision 防衝突。
- `GET /api/team/compaction/{session_id}`：

  ```json
  { "role": "none", "override": { "threshold": 50 },
    "effective": { "managed": true, "auto": true, "threshold": 50, "idle": true } }
  ```

  `managed` 為假時多一個 `reason`：`self_relay` 或 `member_auto`。
- `PUT /api/team/compaction/{session_id}`：body `{ "auto": bool|null, "threshold": int|null, "idle": bool|null }`，整份取代覆寫，回應同 GET。後寫的蓋掉先寫的：這是一個 session 的三個值，App 與 lead 同時改的機會很小。
- capability `team.compaction.v1`。
- `/mod/v1/team` 回應加 `compaction`（§6.1）。

## 8. App

需要 capability，舊 daemon 不顯示。

**主機 › 壓縮**（新區，排在「接力」旁）：兩列，「獨立 session」與「team」，每列有自動壓縮開關、門檻、閒置壓縮開關。下方一行說明：「關掉自動壓縮時，context 快滿（約 97%）仍會由 Claude Code 自動壓縮。」

**個人面板**的設定區加一行「壓縮」：

```
壓縮   [討論] [開發]  門檻 [70]%  閒置壓縮 (開)   跟主機預設
```

- 按「討論」寫入 自動關、閒置關；按「開發」寫入 自動開、70、閒置開。值對不上兩者時兩個都不亮，前面標「自訂」。
- 自動關時門檻變灰。
- 有覆寫時才顯示「跟主機預設」，按了清掉覆寫。
- `managed` 為假時整行換成一句：「自我接力開著，由接力處理」或「member 由 lead 的接力設定處理」。
- 面板打開時讀一次，自己改完再讀一次；lead 用 `pdx compact` 改的，下次打開面板才看得到。

**team 面板**：設定區加同樣的一行，對象是目前分頁的那個 session（lead 或 member）。

## 9. lead：`pdx compact`

- `pdx compact <ref>`：印出 member 的生效值與覆寫。
- `pdx compact <ref> [--auto on|off] [--threshold N] [--idle on|off] [--reset]`：改 member 的覆寫；`--reset` 清掉。
- `pdx compact <ref> --now`：叫 member 壓縮。member 沒在跑回合就馬上壓；正在跑就在這個回合結束時壓。走 App 送提示用的同一條佇列，新增工作種類 `compact`。
- 只能對自己 team 的 member：不是的回 `not_your_member`（exit 13），另一台主機的 member 回 `remote_unsupported`（exit 13）。
- `pdx team` 加一欄 COMPACT：`自動 70%`、`手動`、`接力`（不介入時）。
- 更新 pdx-team skill 的說明。

## 10. 相容與部署

- daemon、mod（兩台都要 `pdx setup`）、Mac App 一起發。
- 新 mod 配舊 daemon：讀不到 `compaction`，不介入。新 daemon 配舊 mod：多出的欄位沒人讀。
- **部署時拿掉兩台 `~/.claude/settings.json` 的 `env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`**（先備份），讓安全網回到原生約 97%。這一步部署前再問使用者一次。
- env 只在 Claude Code 啟動時讀。已經在跑的 session 會繼續在 70% 由原生壓縮，回合中也照壓；設成「討論」的 session 要重開才會生效。

## 11. 驗收

真機，Haiku，預設 tmux server 上名字固定的拋棄式 session（隔離 `-L` 的 session daemon 不追蹤，見 781 驗收）。

1. 把該 session 的門檻調到很低（例如 10%），跑幾個回合：回合結束後由 mod 壓縮、有注入、模型引得出 recap 的 headline；daemon log 的 trigger 是 `plugin`。
2. 注入之後的第一句使用者提示，三種情況**各跑 3 次**：壓縮期間打字（mod 觸發）、壓縮期間打字（手動 `/compact`）、壓縮結束 10 秒後才送。每次都要：transcript 順序是「分界 → 注入 → 使用者提示」、注入有結尾那一行、模型照做、追問時說是使用者打的。9 次裡有任何一次拒絕或誤認，就不算通過。
3. 切成「討論」：用量過門檻也不壓縮。
4. 防重壓：門檻設得比壓縮後的用量還低，不會每個回合都壓。
5. `pdx compact <ref> --now` 對一個閒置的 member，以及一個正在跑回合的 member。
6. 閒置壓縮擋下這項等不了 54 分鐘：靠單元測試，加上部署後的比對（§12）。

## 12. 部署後比對

原本排定 11-10 的 3 天比對照做，另外加：
- 依 `compact context` log 的 trigger 分開算 mod 觸發與原生觸發。
- 設成閒置關的 session，transcript 裡不應該再出現 `Compacted while idle`。

## 13. Phase 與 PR

| Phase | 內容 |
|---|---|
| 1 | 修 781（§6.5）：daemon 的注入結尾＋mod 的等注入。單獨可發版 |
| 2 | daemon：`compaction` 列、`session_compaction` 表與接力沿用、API、`/mod/v1/team` 欄位、capability、compact-context 的 trigger |
| 3 | mod：§6.1–6.4 |
| 4 | App：主機頁與兩個面板 |
| 5 | lead：`pdx compact`、佇列的 `compact` 工作、`pdx team` 欄位、skill |

每張 PR ≤ 800 行 diff 或 ≤ 20 檔，超過再拆。

Phase 1 修的是已上線的問題，建議先單獨 bump、部署（daemon＋兩台 `pdx setup`，不用打包 App），只驗 §11 第 2 項。Phase 2–5 再一起 bump、一起部署。

## 14. 做法的取捨

- **逐 session 用不同的 env 門檻**：env 只在啟動時讀，改不了跑著的 session，也做不到「討論」的閒置關。不採用。
- **維持全域 70%，只加閒置開關**：做不到「討論＝手動」，回合中也照樣被打斷。不採用。
- **mod 在回合結束時壓縮，原生 97% 當安全網**（採用）：符合定案。代價是回合中的 70% 不再生效，依 §4.1 的估算，快取讀取約多 5%，近 9 天沒有回合會撐到 97%。

## 15. 留給實作者

- 「底」與重試的狀態放在 mod 的記憶體，重新載入就重來。
- App 的行怎麼排版照現有設定區的樣式；文字以本 spec 為準。
- `pdx compact` 的輸出格式照 `pdx team`。
