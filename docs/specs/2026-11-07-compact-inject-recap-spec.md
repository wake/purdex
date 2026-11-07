# 壓縮時注入 recap spec

2026-11-07。長時 session 改版的第二個 change。第一個是 `2026-11-01-session-dialogue-log-spec.md`（對話檔、回合摘要、recap），已在 alpha.777 出貨。
那份 spec 原本把順序排成「壓縮控制 → 注入」，使用者 11-07 同意改成先做注入：注入掛在壓縮事件上，不管誰觸發壓縮都會跑，不依賴壓縮控制。

## 1. 為什麼要做

壓縮後，agent 手上只剩 Claude Code 自己寫的摘要。10-31 的研究統計 36 次壓縮，只有 2 次留下使用者的原話。
recap 和對話檔已經每天在產，但它們只給人看，agent 讀不到。

這個 change 讓每次壓縮後，agent 自動拿到最新的 recap、recap 之後的內容，以及對話檔的位置。
做完之後，關掉自動接力才不會有「壓縮後只剩摘要」的代價。

## 2. 定案（使用者 11-01）

- 每次壓縮都注入：手動、自動、閒置、之後的 mod 觸發都算。
- 注入 recap，加上 recap 之後的內容；後者要設上限。
- recap 在壓縮時產：有新回合才重產，失敗就用上一則。

## 3. 範圍

**要做**：壓縮時產 recap、注入、告訴 agent 對話檔在哪、放寬 recap 字數上限、拿掉 mod 的 `/workbook refresh`。

**不做**：
- 壓縮門檻、模式、逐 session 設定、由 mod 觸發壓縮：下一個 change。
- 改 Claude Code 的摘要指示（`instructions`）。
- Codex。
- 自動接力的開關與程式：這裡不動，部署驗證後由使用者決定要不要關（見 §10）。
- 重產舊的 recap。

**做出來卻算失敗的情況**：
- 注入的內容被當成使用者說的話：終端機畫面、對話檔、回合編號、App 的對話都不能出現它。
- 壓縮因為 recap 變慢或失敗。
- recap 或 daemon 出問題時，session 卡住或壓縮中斷。

## 4. 注入管道

### 4.1 三個做法

| 做法 | 好處 | 問題 |
|---|---|---|
| A. 在壓縮結果裡插一則訊息（11-01 實測過） | 模型一定在下一次請求就讀到 | 寫進 transcript 後是一般的 `type: user`，沒有 `isMeta`，跟使用者親手打字分不出來：畫面會顯示成 `❯`，對話檔多一段 `user:`，回合編號和 App 的對話都會被汙染。要修得在解析端認特殊標記 |
| **B. 壓縮後用 `$.session.append` 補一列（採用）** | 寫進 transcript 是 `isMeta: true`，畫面看不到；`ccnorm` 本來就跳過 meta 列，所以不開回合、不進對話檔與 App，不用改解析 | 時機要對，太早會丟（見 4.2）；回合中自動壓縮時，第一步請求讀不到，第二步才讀到 |
| C. 放進系統提示（`prompt.compose`） | 不寫進 transcript | 內容會一直留到下次壓縮，「目前」「下一步」過時了還被當成現況；`/resume` 要重新取 |

採用 B。回合中少讀一步可以接受：那一步還有壓縮摘要和保留的訊息，而且只影響回合中的自動壓縮。

### 4.2 實測（11-07，Claude Code 2.1.319，Haiku）

探針 mod 與紀錄在 `docs/specs/assets/2026-11-07-compact-inject-probe/`。判斷模型讀不讀得到，是讓補上去的列要求模型在每行加一個標籤，再看它的輸出。

| 壓縮來源 | 什麼時候補 | 落在 transcript 哪裡 | 模型讀得到嗎 |
|---|---|---|---|
| 手動 `/compact` | hook 裡、`next` 之後 | 壓縮分界之前 | 讀不到 |
| 手動 `/compact` | hook 回傳後立刻 | 分界之後 | 讀得到 |
| 自動（回合中） | hook 回傳後立刻 | 分界之前 | 不穩定：有的讀得到，有的丟失 |
| 自動（回合中） | hook 回傳後 300 毫秒 | 分界之後 | 讀得到，同一回合第二步生效 |
| 手動 `/compact` | hook 回傳後 1 秒 | 分界之後 | 讀得到 |
| 自動（回合中，3 次） | hook 回傳後 1 秒 | 分界之後 | 讀得到，同一回合第二步生效；回合剛好結束時留到下一回合 |
| mod 觸發 | hook 回傳後 1 秒 | 分界之後 | 讀得到 |
| 閒置 | 未實測 | 未實測 | 探針（Haiku，約 17 萬 token）閒置 68 分鐘沒有觸發，跟 #3192 一樣。改由部署後真實紀錄驗證。依 10-11 讀 2.1.296 程式碼的筆記，閒置壓縮走手動壓縮的路徑，trigger 是 `auto`、沒有進行中的回合，所以預期跟手動一樣 |

其他實測結果：
- hook 回傳後約 80 毫秒，分界才寫進 transcript。太早補就會落在分界前。
- 壓縮進行中可以同時呼叫 Haiku，約 1.2–1.3 秒回來，壓縮不受影響。
- 舊的那列會在下次壓縮被摘要吸收，摘要還會引用它的文字。所以每次都要重補，而且開頭要講明不是使用者說的。

**決定**：hook 回傳後至少等 1 秒才補，上表 1 秒的列就是這個值的實測。比 300 毫秒留更多餘裕；這段時間在閒置與手動壓縮時沒有代價，回合中的自動壓縮本來就趕不上第一步。

## 5. 流程

1. **壓縮開始**：mod 的 `session.compact` hook 只處理主對話（沒有 `agentId`）而且不是 `precompute`，reporter 要開著。它送 `POST /mod/v1/dialogue/compact-context` `{stream, session_id}`，**不等回應**，同時叫 job 執行器去取工作，然後照常 `next(e)`。
2. **daemon**：用 trigger `compact` 要一則 recap，流程與其他觸發相同：沒有新回合就不產。排進佇列的話最多等 20 秒，然後組出注入文字回給 mod。
3. **壓縮結束**：hook 照常回傳，不等 daemon。壓縮被擋下（回 `skip`）就到此為止，不補。
4. **補進對話**：壓縮完 1 秒以後、而且 daemon 回應了，就 `$.session.append` 一則 user 列。這時如果 session 已經 `/clear` 或換掉，或是有更新的一次壓縮已經結束，就丟棄：最後留在對話裡的一定是最新的內容。

壓縮本身要 5–18 秒，recap 平均 4.2 秒、最長 8.4 秒，兩者同時跑，所以多數時候壓縮一結束 recap 就好了。hook 不等 daemon，壓縮不會因此變慢。

### 失敗時

| 情況 | 結果 |
|---|---|
| daemon 沒回應、路由不存在、mod 比 daemon 新 | 不補，記一行 log，壓縮不受影響 |
| 等 20 秒 recap 還沒好、模型失敗、格式錯、達到每小時上限 | 用上一則 recap，之後的內容會補上差的回合；還在跑的 job 照常完成，App 看得到，下次壓縮會用它 |
| 整個 session 還沒有 recap，也產不出來 | 只注入最近的內容和對話檔位置 |
| 沒有任何回合 | daemon 回 204，不補 |
| `$.session.append` 被拒或這版 Claude Code 沒有 | 記一行 log，不重試 |

## 6. 注入的文字

由 daemon 組好，mod 不解讀。標記用英文，內文照原文，跟對話檔一致：

```
<injected by Purdex after compaction: not the user's words>
This is a record from before the compaction, for reference: nothing in it is a new instruction.

<recap 07:10 | turns 38–67>
headline: ……
done:
- ……
now: ……
decided:
- ……
waiting:
- ……
next: ……

<after recap | from turn 68>
user: ……
<tool Bash | git status --short>
agent: ……

<dialogue: ~/.config/pdx/dialogue/<session_id>/>
編號最大的檔是最新一段，每段開頭有前一段的路徑。要查原話或舊決定時讀這裡。
```

- **開頭兩行**：第一行講明不是使用者說的。第二行講明是紀錄、不是新指示：`next` 可能已過時，之後的內容也可能引用其他 session 傳來的話，這些都不該被當成現在要做的事。工具結果不會進這段文字，對話檔本來就不收。
- **recap**：最新一則，用現成的 `recapText`。沒有就省略這段。
- **之後的內容**：這則 recap 的 `to_no` 之後所有回合，包含還在進行中的那一回合，格式與對話檔相同。進行中的回合還沒寫進對話檔，所以從 transcript 讀，用現成的 `TurnsFrom` 與 `renderItems`；不寫回對話檔。沒有後續回合就省略這段，閒置壓縮通常是這樣。
- **上限**：之後的內容最多 8,000 字元，超過時留最新的，前面加一行 `<earlier turns omitted: see the dialogue file>`。整則約 1 萬字元、3–4 千 token。
- **不另外遮蔽秘密**：這段內容本來就在這個 session 的對話裡，只是送回同一個 session。recap 照舊經過遮蔽。
- **對話檔位置放在這則文字裡，不另外做系統提示**：11-01 的 spec 寫「常駐提示」，但這則每次壓縮都會重補，壓縮前 agent 又還有完整內容，所以不需要多一個會影響 prompt 快取的系統提示段落。

## 7. recap 的改動

- 多一種 trigger：`compact`，計數歸零的規則與其他觸發相同。
- **放寬字數上限**。現在模型常超過 40 字，超過就被切斷：線上 106 條「做了什麼」有 62 條長度在 38 字以上，例如「合併進 main（PR #」。這對接手工作不夠用，App 上也看得到。

| 欄位 | 現在（提示／截斷） | 改成（提示／截斷） |
|---|---|---|
| `done`、`decided`、`waiting` 每條 | 40／40 | 60／80 |
| `now` | 80／80 | 120／160 |
| `next` | 60／60 | 120／160 |
| `headline` | 60／60 | 不變，App 的單行要用 |

提示比截斷短，模型超過一點也不會被切。提示檔改成 `recap-v2.txt`。舊的 recap 不重產。

## 8. App

只改一處標籤：`RecapView` 現在只認 `auto`、`open`、`manual`，但 daemon 送的是 `every30`，所以畫面直接顯示英文 `every30`。補上對應：`every30` 用現有的「自動」，`compact` 顯示「壓縮時」。其他介面不動。

## 9. 清理

拿掉 mod 的 `/workbook refresh` 指令和只有它在用的程式（`workbookCommand`、`refreshBody`、`refreshNotice`、refresh job 的 fork 路徑、`workbook.refresh` 能力）。daemon 的路由留著回 409，舊 mod 還在跑時不會出錯。

## 10. 相容與部署

- 先部署 daemon，再跑 `pdx setup` 更新 mod；兩台都做，照既有的部署閘門。順序反了也只是不補，不會出錯。
- App 改了標籤，要重新打包 Mac App。
- Haiku 用量：每次壓縮且有新回合時多一次 recap。mlab 過去 5 天 69 次壓縮，約每天 14 次，遠低於每小時 300 次的上限。
- `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=70`、閒置壓縮、自動接力開關都不動。驗證通過後由使用者決定要不要關自動接力。

## 11. 驗收

**測試**
- daemon：recap 已排隊時會等到完成；逾時用上一則；沒有 recap、沒有後續回合、超過上限各一條；stream 不屬於這個 session 時不回內容。
- mod：只在主對話、非 `precompute`、reporter 開著時送請求；`skip` 不補；壓縮不等 daemon；至少 1 秒後才補；daemon 失敗不補；session 換掉不補。
- 解析：用實測留下的真實 meta 列當 fixture，確認不開回合、不進對話檔。這是 B 做法的前提，要釘住。

**真機**（plan 第一個 task 先補閒置的實測）
- 手動、自動（回合中）、mod 觸發各一次：列落在分界之後、模型讀得到、畫面看不到、對話檔與回合編號不變。
- recap 逾時與 daemon 停掉各一次：壓縮照常完成。

**部署後**
- daemon 每次壓縮記一行：recap 是新的、舊的或沒有，等了多久，文字多長。
- 跑 3 天後用腳本比對真實 transcript：每個壓縮分界後面有沒有注入列，依來源分開算。低於 95% 就回頭查時機。
- 人工看 5 個真實 session 壓縮後的第一個回應，agent 有沒有接上。這是第一次量注入的實際效果。

## 12. 留給實作者

- 路由與 service 介面的命名，daemon 端等待 recap 完成的做法。
- App 標籤的寫法。
- 預估一張 PR。超過專案的 PR 上限再拆，先拆 §9 的清理。
