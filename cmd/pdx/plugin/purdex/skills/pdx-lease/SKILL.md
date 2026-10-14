---
name: pdx-lease
description: 主機資源租約。要跑重指令前使用（完整測試 vitest run／go test ./...、build、全專案 lint），或看到 pdx lease 的排隊訊息、「輪到你了」的通知、最後一個指令的預告時。說明哪些指令算重、mod 會自動處理前景與背景的重 Bash、被排隊時怎麼辦、腳本裡怎麼用 pdx lease run，以及為什麼不要在子 shell 裡直接 pdx lease acquire。
---

# pdx-lease — 主機資源租約

這台主機的 CPU 與記憶體是共用的：好幾個 session 同時跑重指令會把主機壓滿。重指令先跟 daemon 申請，主機有空就立刻放行，忙就排隊。**你的 session 永遠不會卡在等待上**：排不到會立刻回覆你、讓你先做別的事，輪到時 mod 會通知你。daemon 連不上時指令照常執行。

## Purdex mod 已經替你處理前景與背景的重 Bash

在 Claude Code 裡直接下的**前景** Bash 重指令（下表那些），Purdex mod 會自動替你向 daemon 申請、結束後歸還，**你不必自己包 `pdx lease run`**。完整 vitest 沒帶 `--maxWorkers` 時，mod 會替你加上 `--maxWorkers=3`，並在 tool 的 context 說明實際執行的指令。已經包了 `pdx lease run` 的指令，mod 不管。

- **有空**：立刻執行，跟沒有租約一樣。
- **忙**：指令**沒有執行**，你立刻得到「主機資源忙，這個指令還沒執行：排在第 N 位。輪到你時我會通知你」（或「等你自己的上一個指令跑完」）。先做不需要這個指令的事；收到「輪到你了」的通知後，**照原樣重跑同一個指令**，會立刻執行。不要改成背景執行或加 `&` 繞過排隊。
- **延續**：你的指令跑完後，位置會再保留 3 分鐘；這段時間內接著下的重指令直接執行，不用再排。`pdx lease done` 可以提早放掉位置（可選）。
- **有人在排隊時**：一個位置最多連續 3 個指令或 30 分鐘。放掉之前一定先預告，預告會出現在指令結果裡：
  - 「有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。」→ 下一個指令照跑，跑完位置就放掉，之後要跑重指令得重新排。
  - 「因為執行時間過長，這次指令執行完畢後會釋出執行排序，請自行安排執行方式與內容。」→ 這個指令跑完位置就放掉；把還沒做的重指令合併或改排。
- **跑很久的指令**：前景指令超過 Claude Code 的時限（預設 2 分鐘）會被移到背景；租約會留到它真的結束才放掉，結束時的預告會出現在你的下一個 Bash 結果裡。
- **位置空著超過 3 分鐘**，位置會放掉；之後要跑請重新下指令。

背景執行（`run_in_background`）的重指令，mod 會包成 `pdx lease run --ask …`：主機忙時它在背景等，輪到時你會收到問題。要執行就 `pdx lease confirm <id>`，不要就 `pdx lease cancel <id>`；3 分鐘沒回應會取消。mod 在 context 註明「沒有納入資源排隊」的背景指令，照今天的方式放行。

主機模式是 `advise` 時，mod 只記錄「如果是 lease 模式會不會排隊」，一律立刻放行。

## 哪些指令算重

| kind | 指令 | 權重 |
|---|---|---|
| `test-full` | `vitest run`（不帶檔案或 `-t`）、`go test ./...`、`make test` | 35 |
| `build` | `pnpm run build`、`tsc -b`、`vite build`、`electron:build`、`electron-vite build` | 35 |
| `test-pkg` | `go test -race <單一套件>` | 15 |
| `lint-full` | `go vet ./...`、`eslint .`、`pnpm run lint` | 10 |

只跑受影響檔案的測試（有檔名、`-run`、`-t`）不算，不用包。完整 vitest 請自己帶 `--maxWorkers=3`。

**完整 vitest 的名額規定照舊**：直到 lead 宣布主機切到 `lease` 模式（mod 真的會排隊）為止，完整 vitest 仍然只在 merge 前跑、而且要先向統籌（lead）取得名額；之後 lead 會更新 brief，名額規定才退役。`advise` 期間 mod 只記錄不擋，租約不取代這條規定，只是讓漏掉的人不會把主機壓垮。

## 腳本裡怎麼用（mod 管不到的地方）

- 格式是 `pdx lease run --kind <kind> -- <指令…>`。腳本整支包起來：`pdx lease run --kind <kind> -- ./script.sh`，例如 `pdx lease run --kind test-full -- sh -c 'cd spa && npx vitest run --maxWorkers=3'`。不在上表的重指令用 `--weight <1-200>` 自己估一個佔幾成（100＝整台機器）。**不要只包腳本裡的某一步**：被排隊時前面的步驟已經執行，`pdx lease run` 會在 stderr 提醒你這支腳本做到一半被排隊，之後請整支重跑。
- **`pdx lease run` 自己的旗標（`--kind`、`--weight`、`--queue`、`--ask`、`--wait`、`--client-id`、`--config`）都要放在 `--` 之前**；`--` 之後整段都是你的指令，連旗標都會原樣交給它。對：`pdx lease run --kind build --ask -- pnpm run build`；錯：`pdx lease run --kind build -- pnpm run build --ask`（`--ask` 會被交給 pnpm）。
- 不帶 `--queue`／`--ask`／`--wait` 時，由 daemon 依你是否在互動的 session 底下決定：在互動 session 底下就排隊（結束碼 **15**、指令沒有執行），否則（launchd、cron、`claude -p`）照舊等到有空。`--wait` 要的話可以明講，它後面的時間值會被忽略。
- `pdx lease run` 指令跑起來之後，它的結束碼就是你指令的結束碼（被 signal 殺掉是 128+n）；另外有四個也可能是 `pdx lease run` 自己的：**12**＝等待中被你中斷（指令沒執行）、**15**＝排隊中或被取消（指令沒執行）、**126**＝指令無法執行、**127**＝找不到指令。這幾個數字你的指令自己也可能回，所以要看 stderr：`pdx lease run` 自己的結束一定在 stderr 留下以 `pdx lease:` 開頭的一行；沒有那一行，就是你的指令的結果。
- 指令結束後 `pdx lease run` 會把最後一個指令的預告印在 stderr（`pdx lease: …`）。
- `pdx lease ls` 看整個佇列：誰佔著、誰在排、最近有沒有超量。`confirm`／`cancel`／`done` 只能動你自己的請求與位置。

## 等待時

- **排隊就是主機忙，不是卡住。** 不要重試、不要殺掉、不要另外再開一個同樣的指令；等通知。
- **不要用 `&` 或 `run_in_background` 繞過排隊。**
- 看到 `pdx lease: 等了 N 秒主機資源` 只是告知，指令照常執行。

## 不要在子 shell 裡直接 acquire

`pdx lease acquire` 是給 Purdex mod 與腳本用的底層指令：租約綁在 `acquire` 的**父行程**上。在 `$(…)`、管線或 `( … )` 子 shell 裡呼叫，父行程馬上結束，租約會被判定「holder 已不在」而收回，你的重指令實際上沒有被管到。**agent 一律用 `pdx lease run`**；真的要用 `acquire`，必須自己給 `--holder-pid`（一個會活到指令結束的行程），結束後 `pdx lease release <id>`。
