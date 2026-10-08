---
name: pdx-lease
description: 主機資源租約。要跑重指令前使用（完整測試 vitest run／go test ./...、build、全專案 lint），或看到 pdx lease 的等待訊息時。說明哪些指令算重、怎麼用 pdx lease run 排隊、等待時不要做什麼，以及為什麼不要在子 shell 裡直接 pdx lease acquire。
---

# pdx-lease — 主機資源租約

這台主機的 CPU 與記憶體是共用的：好幾個 session 同時跑重指令會把主機壓滿。重指令先跟 daemon 申請「租約」，主機有空就立刻放行，忙就排隊。這是建議不是封鎖：排太久會放行，daemon 連不上也會直接執行。

## 哪些指令算重（要包 `pdx lease run`）

| kind | 指令 | 權重 |
|---|---|---|
| `test-full` | `vitest run`（不帶檔案或 `-t`）、`go test ./...`、`make test` | 35 |
| `build` | `pnpm run build`、`tsc -b`、`vite build`、`electron:build`、`electron-vite build` | 35 |
| `test-pkg` | `go test -race <單一套件>` | 15 |
| `lint-full` | `go vet ./...`、`eslint .`、`pnpm run lint` | 10 |

只跑受影響檔案的測試（有檔名、`-run`、`-t`）不算，不用包。完整 vitest 請自己帶 `--maxWorkers=3`。

## 怎麼用

- `pdx lease run --kind <kind> -- <指令…>`，例如 `pdx lease run --kind test-full -- pnpm exec vitest run --maxWorkers=3`。不在上表的重指令用 `--weight <1-200>` 自己估一個佔幾成（100＝整台機器）。
- `pdx lease run` 會等到有空才執行你的指令，指令結束（含被中斷）自動歸還；它的結束碼就是你指令的結束碼。
- 預設最多等 5 分鐘，到時超量放行並記一筆。`--wait 2m` 可以縮短（最長 9 分 50 秒）。
- `pdx lease ls` 看現在誰佔著、誰在排隊、最近有沒有超量。

## 等待時

- **等待就是主機忙，不是卡住。** 不要重試、不要殺掉它、不要另外再開一個同樣的指令。
- **不要用 `run_in_background` 繞過排隊。** 背景執行的重指令不受租約管（v1 只靠量測擋住後面的申請），而且會讓你看不到它的結果。
- 看到 `pdx lease: 等了 N 秒主機資源` 只是告知，指令照常執行。

## 不要在子 shell 裡直接 acquire

`pdx lease acquire` 是給 Purdex mod 與腳本用的底層指令：租約綁在 `acquire` 的**父行程**上。在 `$(…)`、管線或 `( … )` 子 shell 裡呼叫，父行程馬上結束，租約會被判定「holder 已不在」而收回，你的重指令實際上沒有被管到。**agent 一律用 `pdx lease run`**；真的要用 `acquire`，必須自己給 `--holder-pid`（一個會活到指令結束的行程），結束後 `pdx lease release <id>`。
