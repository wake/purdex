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
| 只跑單一測試檔 | 一樣 `pdx test <task> <檔案>`：宣告 `local = true` 的 task（`pdx test --list` 的 `local` 欄；目前是 `spa-file`、`go-pkg`）走**本機快車道**，直接在你的工作樹跑（未 commit 的改動也跑得到），**永遠不進佇列、不離開本機**；它會記錄成工作，並佔閘門的一個 light 名額。名額或 lock 被別的工作占住時留在本機等：每 2 秒再問一次、印一行說明，等到 task 的 `queue_timeout` 為止，逾時結束碼 6。wwd 沒在跑時它仍會在本機跑，只是不記錄 |
| 沒有對應的 task | `pdx test --adhoc -- <指令> [參數…]`：只在本機、在你當下的工作目錄、佔一個 heavy 名額跑、**不經 shell**，輸出即時串到 stdout，記錄成 undeclared（wwd 沒在跑時照跑、不記錄；`&&`、管線、`$VAR` 都不會被解讀；要多步就分開跑，或請人把它加進 tasks.toml）；不排隊，所以 `--detach`、`--class` 都會被拒（結束碼 4） |
| repo 沒有 `.wwd/tasks.toml` | 這個 skill 不適用；重指令不會被擋，自己克制（見 `pdx-gate`：閘門已是直通） |

task 的參數會依宣告檢查：檔案必須存在、套件必須是套件樣式；參數以 `-` 開頭時前面加 `--`。`--class dev|merge|release` 只能**降低**優先權，不能高過 task 自己宣告的；`--json` 把報告印成一個 JSON 物件。

## 等待

- 前景等待太久時，Purdex mod 會把這次呼叫轉到背景；跑完時通常會有通知（見〈看工作〉末段，通知是盡力而為，終態以 `pdx jobs show <id>` 為準）。**不要重跑、不要自己加 `&`、`nohup`，也不要改用真實執行檔的路徑繞過**，繞過的測試不在名額管制之下，主機忙的時候會拖垮所有人。
- `--detach`：送出後不等，印出工作 id（加 `--json` 時是含 `id` 欄位的 JSON；若結果命中快取，不會排隊，直接印終態報告，所以程式要讀 id 請用 `--json` 取 `id`）。只有進佇列的 task 能用；`local` task 的快車道與 `--adhoc` 都不進佇列，會以 exit 4 拒絕（要排進佇列請改用 `go-changed`、`spa-related` 這類沒有 `local` 的 task）。
- **Ctrl-C 的意義看工作在哪**：對排隊中的工作，它只結束「等待」，工作照樣進行；對本機快車道的執行，它會中止那次執行（沒有人等的快車道沒有自己的生命），記為 cancelled，結束碼 130。
- 要真的取消一個已送出的工作：`pdx jobs cancel <id>`（加 `--wait` 等它確實結束）。

## 結束碼

結束碼就是工作自己的結束碼，加上 wwd 自己的幾個：

| 碼 | 意思 | 建議 |
|---|---|---|
| 0 | 成功 | |
| 1 | 失敗，但工作沒有自己的結束碼（基礎設施、lost、OOM）；工作自己回 3–6 或 124 以上時也會被報成 1（報告裡的 `exit` 保留真實數字） | 看 `pdx jobs show <id>` 與 `pdx jobs log <id>` 查原因與主機狀態，再決定要不要重送 |
| 3 | wwd 連不上（要進佇列的 task 才會；本機快車道的 task 與 `--adhoc` 在這情況會照跑、不記錄） | 本機的 wwd 沒在跑：`pdx wwd status` 看；**不要**改用手拼的指令把測試跑掉，先回報或請使用者啟動 wwd |
| 4 | manifest、task 名稱或參數錯誤（快車道的 task 與 `--adhoc` 拒絕 `--detach`、`--adhoc` 拒絕 `--class`，也是這個碼） | 讀錯誤訊息，`pdx test --list` 核對 task 與參數；這是呼叫端的錯，不要重試 |
| 5 | 沒有任何主機能接這個工作：送件當下就判，pool 裡沒有主機持有這個 task 的全部標籤（連不上的主機以最後已知標籤計）；訊息列出缺的標籤與每台缺哪幾個，工作不會進佇列 | 重試沒用；回報哪個 task 缺哪些標籤。另：有主機持有標籤但現在都不能領（worker 關、連不上、讓位中）不是 5，照常排隊，stderr 印一行提示列出那些主機，可能要等 |
| 6 | 在佇列裡等過上限仍沒開始（沒有任何東西跑過） | 主機忙；稍後再送，或 `pdx wwd pool` 看各台狀況；本機快車道的鎖衝突等到上限也是這個碼 |
| 124 | 命令跑超過時間 | task 宣告的時限到了；看 `pdx jobs log <id>`（快車道看報告印的 log 路徑）判斷是卡住還是單純很慢，再決定回報或重送 |
| 130 | 三種情況：工作已取消（被取代、被 `pdx jobs cancel`）；本機快車道的執行被中斷；**排隊工作的前景等待被中斷，但工作本身還在跑** | **先 `pdx jobs show <id>` 看終態再決定**：還在 queued／running 就等它或看 log，不要重送（會重複佔用名額）；確定已 cancelled 且確實還需要結果才重送 |

## 看工作

```
pdx jobs                       # 這個 session 的工作，新的在前（預設 20 筆；--all 看整台主機、--state、--limit）
pdx jobs show <id>             # 狀態、在哪台跑、摘要
pdx jobs log <id>              # 存下來的 log（工作結束前是 404；控制字元顯示成 \xNN，--raw 原樣輸出）
pdx jobs cancel <id> [--wait]  # 取消
pdx wwd [status|pool]          # 本機 wwd 與各主機的狀態
```

佇列工作結束時（任何終態，包括取消、逾時未開始、命中快取），Purdex 會盡力通知送件人的那個對話，通知裡有工作 id；送件人取自環境變數 `PDX_SESSION_ID`、`CLAUDE_CODE_SESSION_ID`、`CLAUDE_SESSION_ID`（依序，取第一個格式有效的；都沒有就不通知）。通知是盡力而為：同一個工作罕見時可能重複，連續送不到（最多 6 次）會放棄，所以沒收到時用 `pdx jobs show <id>` 查終態，不要一直等。快車道與 `--adhoc` 的結果由 `pdx test` 自己印出，**不另外通知**，只有 lost（呼叫端被殺、沒人回報）才會通知。

## 與 pdx-gate 的關係

`pdx gate`（見 `pdx-gate` skill）**已經是直通**（WWD-9 S0）：不排隊、不取名額、不提供任何互斥。舊閘門與 wwd 不再並存，重指令的管控只剩 wwd：

- **repo 有 tasks.toml、做的是它宣告的事**（測試、建置、lint）：用 `pdx test`。wwd 決定在哪跑、排不排隊。
- **沒有 tasks.toml、或不是測試的重指令**（臨時的編譯、掃描）：不會被擋，**自己克制**（一次一個）；若只是一條單純的指令又想留紀錄，可以用 `pdx test --adhoc`。
- 直接呼叫 `pdx test` **不必也不要**再包 `pdx gate`：外層 `pdx gate` 不提供整段 script 的名額或互斥，裡面的 `pdx test` 不會沿用它。需要限制並行的 script，把重的步驟**各自交給 `pdx test`**，由 wwd 管。
- 排隊時的規矩一樣：看到 mod 說明「已轉背景」就等通知（久未收到時用 `pdx jobs show <id>` 查終態），不重跑、不繞過。
