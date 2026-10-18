---
name: pdx-test
description: 用 pdx test 跑 repo 宣告的測試／建置工作（.wwd/tasks.toml）。repo 有 .wwd/tasks.toml 而你要跑測試、建置、lint 時使用；要跑單一測試檔、想知道有哪些 task、沒有對應的 task、pdx test 回了 3／4／5／6／124／130 之類的結束碼、要看或取消一個工作（pdx jobs）、或不確定該用 pdx test 還是 pdx gate 時使用。
---

# pdx-test — 跑 repo 宣告的 task

repo 的根目錄若有 `.wwd/tasks.toml`，這個 repo 的測試與建置**宣告成 task**，一律用 `pdx test` 跑，不要自己拼 `go test …`、`npx vitest …`。同一個 task 可能被 wwd 排到別台比較閒的主機跑；宣告裡寫好了要怎麼跑、要哪些鎖、逾時多久，你不必猜。

## 什麼時候用哪個

| 情況 | 做法 |
|---|---|
| 想知道有哪些 task | `pdx test --list`（`--json` 給程式讀） |
| 跑宣告的 task | `pdx test <task> [參數…]`，例如 `pdx test spa-test src/foo.test.ts` |
| 只跑單一測試檔 | 一樣 `pdx test <task> <檔案>`：單檔的 task 走**本機快車道**，直接在你的工作樹跑，不進佇列、不離開本機；它會記錄成工作，並佔閘門的一個 light 名額（名額或鎖被占住時在本機等，等到 task 的 `queue_timeout` 為止）。wwd 沒在跑時它仍會在本機跑，只是不記錄 |
| 沒有對應的 task | `pdx test --adhoc -- <指令> [參數…]`：只在本機、當成 heavy 跑、**不經 shell**（`&&`、管線、`$VAR` 都不會被解讀；要多步就分開跑，或請人把它加進 tasks.toml） |
| repo 沒有 `.wwd/tasks.toml` | 這個 skill 不適用，照 `pdx-gate` |

task 的參數會依宣告檢查：檔案必須存在、套件必須是套件樣式；參數以 `-` 開頭時前面加 `--`。`--class dev|merge|release` 只能**降低**優先權，不能高過 task 自己宣告的；`--json` 把報告印成一個 JSON 物件。

## 等待

- 前景等待太久時，Purdex mod 會把這次呼叫轉到背景；跑完時 Claude Code 會通知你。**不要重跑、不要自己加 `&`、`nohup`，也不要改用真實執行檔的路徑繞過**，繞過的測試不在名額管制之下，主機忙的時候會拖垮所有人。
- `--detach`：送出後只印工作 id，不等。快車道（本機單檔）與 `--adhoc` 不能 detach，會以 exit 4 拒絕。
- **Ctrl-C 的意義看工作在哪**：對排隊中的工作，它只結束「等待」，工作照樣進行；對本機快車道的執行，它會中止那次執行（沒有人等的快車道沒有自己的生命），記為 cancelled，結束碼 130。
- 要真的取消一個已送出的工作：`pdx jobs cancel <id>`（加 `--wait` 等它確實結束）。

## 結束碼

結束碼就是工作自己的結束碼，加上 wwd 自己的幾個：

| 碼 | 意思 | 建議 |
|---|---|---|
| 0 | 成功 | |
| 1 | 失敗，但工作沒有自己的結束碼（基礎設施、lost、OOM）；工作自己回 3–6 或 124 以上時也會被報成 1（報告裡的 `exit` 保留真實數字） | 看 `pdx jobs show <id>` 與 `pdx jobs log <id>`；OOM／lost 通常重跑一次即可，連兩次再回報 |
| 3 | wwd 連不上（要進佇列的 task 才會；本機快車道的 task 在這情況會照跑、不記錄） | 本機的 wwd 沒在跑：`pdx wwd status` 看；**不要**改用手拼的指令把測試跑掉，先回報或請使用者啟動 wwd |
| 4 | manifest、task 名稱或參數錯誤（快車道的 task 與 `--adhoc` 拒絕 `--detach`、`--adhoc` 拒絕 `--class`，也是這個碼） | 讀錯誤訊息，`pdx test --list` 核對 task 與參數；這是呼叫端的錯，不要重試 |
| 5 | 沒有任何主機能接這個工作（標籤對不上） | 重試沒用；回報哪個 task 缺哪些標籤 |
| 6 | 在佇列裡等過上限仍沒開始（沒有任何東西跑過） | 主機忙；稍後再送，或 `pdx wwd pool` 看各台狀況；本機快車道的鎖衝突等到上限也是這個碼 |
| 124 | 命令跑超過時間 | 不是測試壞了；看 log 是不是卡住，真的很慢才回報 |
| 130 | 三種情況：工作已取消（被取代、被 `pdx jobs cancel`）；本機快車道的執行被中斷；**排隊工作的前景等待被中斷，但工作本身還在跑** | **先 `pdx jobs show <id>` 看終態再決定**：還在 queued／running 就等它或看 log，不要重送（會重複佔用名額）；確定已 cancelled 且確實還需要結果才重送 |

## 看工作

```
pdx jobs                       # 這個 session 的工作，新的在前（預設 20 筆；--all 看整台主機、--state、--limit）
pdx jobs show <id>             # 狀態、在哪台跑、摘要
pdx jobs log <id>              # 存下來的 log（工作結束前是 404；控制字元顯示成 \xNN，--raw 原樣輸出）
pdx jobs cancel <id> [--wait]  # 取消
pdx wwd [status|pool]          # 本機 wwd 與各主機的狀態
```

每個送出的工作結束時（排隊中的任何終態；快車道只在 lost 時）Purdex 會通知你一次，通知裡有工作 id。

## 與 pdx-gate 的關係

`pdx gate`（見 `pdx-gate` skill）到 WWD-9 才拆，這段期間**兩者並存**，各管各的：

- **repo 有 tasks.toml、做的是它宣告的事**（測試、建置、lint）：用 `pdx test`。wwd 決定在哪跑、排不排隊。
- **沒有 tasks.toml、或不是測試的重指令**（臨時的編譯、掃描）：照舊，指令交給 shim／`pdx gate`；若只是一條單純的指令又想留紀錄，可以用 `pdx test --adhoc`。
- 不要把 `pdx test` 包在 `pdx gate -- …` 裡：本機執行的 `pdx test` 自己已經拿著閘門的名額，外面再包一層只會雙重排隊。
- 排隊時的規矩一樣：看到 mod 說明「已轉背景」就等通知，不重跑、不繞過。
