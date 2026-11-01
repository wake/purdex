# 對話檔、回合摘要與 recap（取代工作簿）spec

2026-11-01。長時 session 改版的第一個 change，後面還有兩個：壓縮控制、壓縮時注入。
本 change 只負責**記錄與產生**：對話檔、回合摘要、recap、推播句，並停用工作簿。壓縮時產 recap 與注入給 agent 屬第三個 change。

## 1. 定案（使用者 11-01）

- 停用工作簿。在它原本的紀錄點（每回合結束）改做三件事：
  1. 把這一回合的對話接到**對話檔**；
  2. 用 Haiku 產一則**回合摘要**（`turn-summary`），同一次呼叫產推播用的一句；
  3. 每累積 30 則回合摘要，產一則較長的 **recap**，涵蓋這 30 回合。
- 對話檔**按壓縮次數拆段**。壓縮不換 session id，但不讓單一檔案一直變大。
- 工具只留一行摘要，不附結果。
- 回合摘要只寫這一回合，不累積；recap 只讀最近 30 則回合摘要，不讀上一則 recap。
- 檔案標記一律英文；摘要與 recap 的內文寫繁體中文；使用者和 agent 的原話照原文保留。

## 2. 對話檔

### 2.1 位置與拆段

- 路徑是 `<data_dir>/dialogue/<session_id>/001.md`、`002.md`……，放在 daemon 的資料夾，不進 repo。
- session 第一次被記錄時開 `001.md`，從 transcript 開頭補寫，之前的壓縮分段照分界拆開。歷史回合只寫內容，不補產回合摘要。
- transcript 每出現一次壓縮分界（自動、手動、閒置都算），就關閉目前這一段、開下一號：分界之前的內容留在舊段，之後的進新段。
- 接力、`/clear`、新開的 session 會有新的 session id，所以開新資料夾。
- 段號由 daemon 自己計數，不用 transcript 的回合序號（那個會在 transcript 重讀時漂移）。

### 2.2 格式

```
<prev: ~/.config/pdx/dialogue/<前一個 session_id>/003.md>
<source: ~/.claude/projects/-Users-wake-Workspace-wake-purdex/<session_id>.jsonl>
<carried recap 05:44 | turns 31–60>
……（上一段最後一則 recap 原文）

user: ……
<tool Bash | git status --short>
<tool Edit | internal/module/wwd/confirm.go>
<tool AskUserQuestion | recap 要不要進推播？>
<answer | 要>
<subagent done | Fix review findings>
agent: ……

<turn-summary 37 · 06:41 | P2b′-4a 確認撤銷 API>
四個 API 審查通過，都只能操作自己發起的請求。確認遇到位置不存在時誤回 500，應為 409；小修後直接 merge。

<recap 07:10 | turns 38–67>
headline：……
done:
- ……
now: ……
decided:
- ……
waiting:
- ……
next: ……

<compacted | auto>
<next: ~/.config/pdx/dialogue/<session_id>/002.md>
```

| 標記 | 何時寫 | 內容 |
|---|---|---|
| `<prev: …>` | 每段第一行 | 前一段的路徑。第一段寫接力前那個 session 的最後一段；沒有前任寫 `<prev: none>` |
| `<source: …>` | 每段第二行 | 原始 transcript 路徑 |
| `<carried recap …>` | 第二段起 | 上一段最後一則 recap 的原文，讓每段能單獨讀懂；上一段沒有 recap 就省略 |
| `user:` | 你打的訊息，以及排隊送出的訊息 | 全文；附圖時另起一行 `<image ×N>` |
| `peer(<名字>):` | 其他 session 傳來的訊息 | 全文 |
| `scheduled:` | 排程送來的訊息 | 全文 |
| `<slash | …>`、`<shell | …>` | 你打的 slash 指令、`!` 指令 | 一行 |
| `<tool <工具名> | …>` | 每次工具呼叫 | convmodel 現成的一行摘要，不附結果 |
| `<answer | …>` | 你回答 AskUserQuestion | 選項或你打的文字（這是你的話，要留） |
| `<subagent done | …>` | 子代理完成的通知 | 通知裡的摘要，一行 |
| `agent:` | agent 的每段文字 | 全文；不收思考內容 |
| `<turn-summary N · HH:MM | 標題>` | 回合摘要寫好時 | N 是回合編號，摘要在下一行 |
| `<recap HH:MM | turns a–b>` | recap 寫好時 | 結構化全文（見 §4） |
| `<compacted | auto 或 manual>` | 壓縮分界 | 一行，接著寫 `<next: …>` 收尾 |
| `<next: …>` | 每段最後一行 | 下一段的路徑 |

- 不收：子代理內部的對話、Claude Code 的壓縮摘要、system reminder、hook 輸出。
- **回合編號**：每個 session 從 1 開始，換段不歸零。recap 的 `turns a–b` 對應的就是回合摘要上的編號。

### 2.3 寫入時機與冪等

- 每回合結束時把這一回合的內容接到檔尾；回合摘要寫好後再接在後面。摘要帶著回合編號，所以就算下一回合的內容先寫進去，也對得起來。
- 每個 session 記錄寫到哪：回合 id、回合內的位置、段號、回合計數。重複的回合結束事件不會重寫。
- 對不上時整個 session 的對話檔從頭重建：內容從 transcript 重抽，回合摘要與 recap 從資料庫依回合重新放回去，回合編號沿用。對不上指：上次寫到的那一回合在 transcript 裡找不到（transcript 被改寫或換檔），或段檔比記錄的短、不見了。段檔比記錄的長（寫了檔、還沒記錄就當機）則截回記錄的位置再接著寫。
- 只有 daemon 寫這些檔案。

### 2.4 涵蓋範圍與保留

- 涵蓋所有有 Purdex mod 的互動 Claude Code session，包括 lead、member、solo。Codex 不做（daemon 目前沒有 Codex transcript 的正規化）。
- 第一版不自動刪除。11-01 用真實資料估計：討論型每段約 1–3 萬字元，開發型約 8–11 萬字元。

## 3. 回合摘要

- 取代工作簿的 turn job：觸發、佇列、線上形狀都沿用（job kind `turn`、能力字串 `workbook.v2`），已部署的 mod 不用改。
- **輸入**：
  - 這一回合你的訊息、agent 的文字、工具摘要，上限和遮蔽秘密的處理沿用現行；
  - 前 3 則回合摘要的標題，讓同一件事的名稱前後一致。
- **輸出**：`{skip, title, push, summary}`。
  - 上限：`title` 16 字、`push` 40 字、`summary` 150 字。
  - 超過上限就在字詞邊界截斷，不再另派重寫的 job。
- **skip**：沒有實質內容的回合不產回合摘要，但對話檔的內容照寫。判斷規則沿用現行。
- **推播**：沿用 `workbook.push-lines` 介面和 `push_wait_s` 設定，`title` 接在推播標題後、`push` 當推播正文。推播模組不改。
- 每寫一則回合摘要，這個 session 的「距上次 recap」計數加 1。

## 4. recap

- **輸入**：最近 30 則回合摘要（標題、內文、回合編號、時間）。不讀上一則 recap，也不重讀對話。
- **輸出**：
  - `headline`：一行，60 字以內；
  - `done`：這段做了什麼，3–6 條；
  - `now`：目前；
  - `decided`：已定；
  - `waiting`：等你；
  - `next`：下一步。
  - 全部約 1000 字以內。
- **觸發**：沒有新的回合摘要就不產；任何一種觸發產完，計數都歸零。recap 在 session 自己的程序裡產生，session 已結束時不產，App 顯示最後一則。
  1. 距上次 recap 滿 30 則回合摘要；
  2. 你在 App 打開這個 session 的 recap 時，有新的回合摘要；
  3. 手動（App 上的按鈕）；
  4. 壓縮時：第三個 change 才做。
- 只靠第 1 種觸發時，各則 recap 剛好首尾相接（1–30、31–60……）。其他觸發會跟前一則部分重疊，不影響使用。
- 寫進目前這一段對話檔，同時存進資料庫給 App 讀。
- 排在同一個對話佇列裡，位置在 turn job 之後，所以不影響推播等待。

## 5. 停用工作簿

- **拿掉**：
  - 工作簿的 entry、狀態、待辦，以及重寫與 refresh 兩種 job；
  - 團隊日誌（journal）整套；
  - 對應的 API 路由、`/api/info` 的 `workbook.v1` 與 `journal.*`、裝置 token 白名單；
  - App 的工作簿與日誌元件。
- **保留**：
  - Haiku 派工通道：路由、租約、`workbook.v2` 能力字串；
  - `workbook.push-lines` 介面和 `push_wait_s` 設定。
- **舊資料不刪**：`workbook.db` 的舊表和 `<data_dir>/journal/` 原地保留，只是不再讀寫。
- mod 的 `/workbook refresh` 指令要改 mod 才能拿掉，留到第二個 change（它本來就要改 mod）一起拿掉。在那之前，daemon 對 refresh 請求回 409「已停用」，mod 會顯示「目前沒有可執行重整的 session」。

## 6. App

- 原本的工作簿入口（標題列按鈕、團隊面板的列）改成 recap：
  - 列表上一行：最新 recap 的 headline，加上「截至 HH:MM」；
  - 點開是 recap 歷史，新的在上面。每則可以展開看全文，再往下展開是那個範圍的回合摘要；
  - 「重新產生」按鈕，就是手動觸發。
- 日誌卡片、確認帶、任務卡、路線圖卡拿掉。
- 新 API：
  - `GET /api/dialogue/{session_id}/recaps`
  - `GET /api/dialogue/{session_id}/turn-summaries?from=&to=`
  - `POST /api/dialogue/{session_id}/recap`（手動或打開時更新）
  - WS 事件 `dialogue.recap`
- 介面細節先做原型給你看，確認後才實作。

## 7. 這個 change 不做

- 壓縮時產 recap、注入給 agent、讓 agent 知道對話檔在哪的常駐提示：第三個 change。
- 壓縮門檻、模式、逐 session 設定：第二個 change。
- Codex、跨 session 的每日 digest、對話檔自動清理。

## 8. 相容與部署

- 已部署的 mod 不用改，也不用重跑 `pdx setup`：job 的線上形狀和能力字串都沒變。
- 舊 App 連新 daemon 時，工作簿 API 已經不存在，舊 App 的工作簿面板會是空的。App 和 daemon 照慣例一起部署。
- Haiku 用量：每回合一次（與現在相同），加上約每 30 回合一次 recap，多約 3%。工作簿原本給 lead 和 solo 的 journal 呼叫不再有，總量反而可能下降。

## 9. PR 切分

1. **daemon：對話檔**。新的寫入器、拆段、冪等、重建。
2. **daemon：回合摘要與 recap**。換掉 turn job 的 prompt 與輸出、recap 的 job 與觸發、新 API，並拿掉工作簿與日誌的後端。
3. **App：recap 檢視**。原型確認後，取代工作簿與日誌的介面。

依序做。

## 10. 已確認（使用者 11-01）

1. 團隊日誌（journal）跟著工作簿一起拿掉。
2. App 上 recap 的入口取代原本的工作簿按鈕和團隊面板的列。介面細節在 PR 3 先給你看原型。
