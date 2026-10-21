---
name: pdx-gate
description: pdx gate 現在是直通（WWD-9 S0）：不排隊、不取名額、不降優先權。看到 pdx gate、shim、「waiting for a heavy slot」、結束碼 75，或要判斷重指令會不會被主機擋時使用。重工作的管控改由 wwd 的 pdx test。
---

# pdx-gate — 現在是直通

`pdx gate` 以前替重指令排隊、限制並行、降低優先權。**從 WWD-9 S0 起它什麼都不擋**：

- `pdx gate [--class …] [--auto] -- <指令…>`、shim 的 `--tool`、mod 加在前面的 `pdx gate --auto --` 都是**立刻直接執行**指令（exec），結束碼與輸出就是指令自己的。不排隊、不取名額、不加 `nice`、不限制 `go test -p`／vitest workers。
- `--class`、`--auto`、`--fg`、`--wait-max`、`--class-source`、`--assist-*`、`--config` 仍可寫（舊腳本不會壞），**沒有作用**。不會再有結束碼 **75**，也不會再看到「waiting for a heavy slot」。
- `pdx gate would-wait …` 一律回「不會等」（JSON 形狀不變：`class` 依指令分類、`ahead` 為 0）。Purdex mod 因此不再把你的 Bash 呼叫改成背景執行。
- `pdx gate status` 等唯讀／管理子命令暫時還在，看到的是舊閘門的設定與名額，已**不代表**主機現在的負載控制。

## 現在誰管重工作

主機的負載控制改由 **wwd** 負責：重的測試與建置請用 `pdx test`（repo 的 `.wwd/tasks.toml` 宣告的 task，或 `pdx test --adhoc -- <指令…>`），它會依主機的狀態排隊或在本機快速通道跑；細節見 `pdx-test` skill。直接跑的 `go test ./...`、`pnpm …`、`tsc` 不再被擋，**自己要克制**：

- 一次一個重指令，不要同時開好幾個。
- 重的全套測試與建置交給 `pdx test`，不要在前景直接跑。
- 主機忙不忙，看 `pdx wwd` 與守門員的警報，不要看 `pdx gate status`。
