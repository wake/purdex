---
name: pdx-gate
description: 主機負載控制（pdx gate）。要跑重指令前使用（完整測試 vitest run／go test ./...、build、全專案 lint、phpunit／pest），或看到「waiting for a heavy slot」的排隊訊息、Purdex mod 把你的 Bash 改成背景執行的說明、pdx gate 的結束碼時。說明哪些指令會被排隊、為什麼不能用 & 繞過、怎麼看 pdx gate status、腳本裡怎麼用 pdx gate -- <script>。
---

# pdx-gate — 主機負載控制

這台主機的 CPU 與記憶體是共用的，好幾個 session 同時跑重指令會把主機壓滿。重指令因此一次只讓有限個跑（預設**重指令 1 個、輕指令 3 個**，實際數字以 `pdx gate status` 顯示的生效設定為準），其他的排隊；主機記憶體吃緊時，新的重指令也會先等。**排隊就是主機忙，不是卡住**：它輪到時會自己開始，結束時你會被通知。

## 你不必做什麼

一般的 `go test`、`pnpm …`、`npx vitest`、`tsc`、`composer …` 這類指令，主機的 shim 會自動把它們交給 `pdx gate`：能立刻跑就立刻跑，要等就在原地排隊並印出誰佔著、等多久、前面有幾個。**你不必自己包 `pdx gate`**。gate 還會替指令降低優先權（nice）、限制 `go test -p`／vitest workers 的數量，並提醒 `go test -count=1` 會關掉測試快取（發版用的 release profile 與 `--fg` 不管這條）；加上去的限制會寫在輸出裡（`pdx gate: limits added: …`）。

Purdex mod 另外替你處理兩件事（只對 Bash）：

- 直接用路徑呼叫的 `node_modules/.bin/vitest`、`node_modules/.bin/tsc`、`vendor/bin/phpunit`、`vendor/bin/pest`（shim 看不到）會被加上 `pdx gate --auto --`，並在結果的 context 說明實際執行的指令。
- 前景的 Bash 呼叫如果 gate 現在會讓它排隊，mod 會把**整個**呼叫改成背景執行，並說明誰佔著、佔多久、排第幾。看到這段說明時：**不要重跑、不要自己加 `&`、不要假設它做完了**；它輪到時自己開始，整段跑完時你會收到通知與輸出。能立刻開始的呼叫維持前景。

## 排隊時

- **不要用 `&`、`run_in_background` 或 `nohup` 繞過排隊**，也不要改用別的方式跑同一件事。繞過的指令不在 gate 底下，會在忙的時候把主機壓垮，之後看到的就是全部人一起變慢。
- 不要中斷、不要重試、不要另外再開一個同樣的指令。先做不需要這個結果的事。
- 一次要跑好幾個重指令時，**一個接一個**跑，不要同時開。

## 看狀態

```
pdx gate status            # 誰佔著名額（指令、專案、多久）、誰在排隊、目前生效的設定與來源
pdx gate status --json     # 同樣的內容給程式讀
pdx gate would-wait --auto -- <指令…>   # 這個指令現在會不會排隊（不拿名額）
```

`gate 已不在，樹還活著` 的名額是 gate 被殺掉、但它啟動的行程還在跑：看「拿著鎖的行程」那一行，結束那些行程名額就會空出來。

## 腳本裡怎麼用

- 整段序列要佔一個名額：`pdx gate -- ./script.sh`（裡面的重指令認得外層名額，不會再排一次）。**不要只包腳本裡的某一步**。
- 旗標都放在 `--` 之前：`pdx gate --class light -- <指令…>`；`--` 之後整段都是你的指令。`--fg` 保留原本優先權。
- 結束碼是你的指令的結束碼（被 signal 殺掉是 128+n）；gate 自己拒絕執行（gate 目錄壞掉、磁碟空間低於下限）是 **75**，它一定在 stderr 留下以 `pdx gate:` 開頭的一行說明原因；等待中被你中斷是 128+n。
