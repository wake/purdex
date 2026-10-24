# member 自動接力開關（spec＋plan）

2026-10-24。一張 PR：daemon（hostconfig＋team 模組）＋SPA 主機頁。不改 mod、不改 wire 格式、不動 schema。

## 1. 背景

使用者 10-24 決定：
- Claude Code 的自動壓縮改在用量 40% 觸發（`~/.claude/settings.json` 的 `env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE = "40"`，mlab 與 air26 已改）。
- 關閉 relay。一般 session 與 lead 的自我接力本來就有主機開關 `self_solo`／`self_lead`，兩台都已關。

member 這一側沒有開關（規格 U13：member 的接力由 lead 安排）。daemon 與 mod 會自動替 member 做五件事，其中兩件在 40% 壓縮下會變成常態雜訊或阻擋：

| # | 自動行為 | 位置 | 40% 壓縮下 |
|---|---|---|---|
| A | mod 攔截 member 的閒置壓縮（回 `{skip}`），daemon 發卡片請 lead 決定接力／交接關閉／關閉 | hello 的 `idle_intercept`；`compacted.go` `--intercepted`；`notice_idle.go` `interceptedIdle` | **一直發生**：Claude Code 對閒置約 54 分鐘、context ≥ 20 萬 token 的 session 做閒置壓縮，1M 視窗的 member 在 20 萬～39 萬 token 之間每次閒置都被擋 |
| B | member 每次自動壓縮或閒置壓縮後通知 lead（`CompactedNoticeFmt`「lead 未在 70% 時接力」、`CompactedIdleNoticeFmt`） | `compacted.go` | **每次壓縮都發** |
| C | member 到 70% 時 mod 問 lead（`pdx relay ask`）；無人值守時 daemon 自己接受並接力 | `relay_ask.go`、`relay_ask_unattended.go`、`relay_ask_remote.go` | 到不了 70%，但開關要名副其實 |
| D | 70% 閒置通知（舊 mod 的後備） | `notice_usage.go` `noticeUsage` | 到不了 |
| E | 閒置 40 分鐘且 ≥40% 的通知 | `notice_idle.go` `noticeIdle` | 少見邊角 |

## 2. 決定

host config `relay` 列加一個布林欄位 **`member_auto`**，**預設 true**（沒寫過的主機、沒帶這個欄位的列，行為與現在相同）。

`member_auto = false` 時，**這台主機上的 member**（daemon 依自己的角色紀錄判斷）：
1. **A 關**：hello 回應不帶 `idle_intercept`（等於 0）。mod 已支援「0＝daemon 不決定，照常壓縮」，不必改 mod。
   - 已經從先前的 hello 聽到 1 的 mod 仍會擋一次並回報 `--intercepted`：daemon 回 `route: "silent"`、不發通知。mod 照現有邏輯把角色當成 none、重送 hello，拿到 0，之後的閒置壓縮照常跑。已被擋掉的那一次不補。
2. **B 關**：`relay compacted`（非 intercepted）的記帳照舊（刪 fresh half、撤銷 open ask、`markIdleCompacted`、disarm 70% 通知），只是**不發**通知給 lead；回應 `noticed: false`。
3. **C 關**：`POST /api/relay/ask` 在建 ask 列之前回 **409 `relay_unsupported`**，detail「member 自動接力已在主機設定關閉」。不建列、不通知 lead、無人值守也不自動接受。遠端 member（`askRemote`）同樣在轉送之前擋。用既有錯誤碼是因為 mod 對它的處理正是我們要的（`lastAskPct` 記下，+10 點之前不再問），新錯誤碼就得改 mod、兩台重跑 `pdx setup`。
4. **D、E 關**：`noticeUsage`、`noticeIdle` 這一輪整個跳過（不 arm、不 disarm、不送）。閒置期的計時資料照樣維護也可以，但最簡單是整輪跳過；開關重新打開後，下一輪依當下狀態重新判斷。

不受影響：
- lead 手動 `pdx relay _<ref>`（`handleRelayCreate`）、使用者在 member 打的 `/relay`（`req.Manual`）。這是人主動要求的，與 `self_solo`／`self_lead` 不管手動 `/relay` 的原則一致。
- 一般 session 與 lead 的自我接力（仍由 `self_solo`／`self_lead` 管）。
- 已經開著的 ask、已經在跑的 relay op：照原本流程結束，不追溯取消。

**讀不到開關時（store 錯誤、列無效）**：`member_auto` 視為 **false**。與 self 開關的 fail closed 同方向：讀不到就不做自動的事；這裡「不做」＝不攔截、不通知，壓縮照常，不會卡住任何 session。錯誤寫 daemon log。

**開關何時生效**：daemon 每次判斷時讀 store（`RelaySwitches()` 本來就每次讀，與 self 開關相同），不快取。mod 端只在下一次 hello 才知道 A 的新值；關掉時靠 §2-1 的 silent 讓 mod 立刻重問；打開時 mod 要等下一次 hello（session start、`/clear`、hello 重試、member recheck），可以接受。

## 3. 相容性與部署

- 舊版 App 的 SPA 解析 relay 列遇到不認識的 key 會整列判為無法讀取（顯示兩個開關都關、出現問題提示），從舊 App 按開關會把 `member_auto` 洗回預設 true。新版 daemon 一律把 `member_auto` 寫進列裡。所以：
  - **兩台 daemon 與 Mac App 同一版出**；`member_auto=false` 等兩台 daemon 都部署後再寫。
  - 新 App 對舊 daemon PUT 會被 400（unknown relay field）；兩台同版部署即可避免。
- 不改 mod → 部署不需要 `pdx setup`（只重啟 daemon）。
- 部署後在 mlab、air26 各寫一次 `member_auto=false`（同 10-24 關 self 開關的腳本，保留既有 prompt 欄位）。

## 4. 介面文字（SPA 主機頁「接力」區）

第三列原本是說明文字「member 自我接力／member 的接力一律由 lead 安排」，改成開關：
- 標籤：**member 自動接力提醒**（en：Automatic relay reminders for members）
- 開關下方小字：**到門檻或閒置時通知 lead、擋下 member 的閒置壓縮請 lead 決定。關閉後 member 由 Claude Code 自行壓縮，lead 仍可手動 pdx relay。**
  （en：Tells the lead at the threshold or when idle, and holds a member's idle compaction for the lead to decide. Off: members compact on their own; the lead can still run pdx relay.）
- `hosts.relay.member`、`hosts.relay.member_note` 兩個舊 key 由新文字取代（沿用 key 名或換新 key 皆可，兩個語系都要有，舊 key 沒人用就刪）。
- 無法讀取的列（fail closed）：三個開關都顯示關。

## 5. Plan

每個 task 獨立 commit；TDD（先寫失敗測試）。

### T1 hostconfig：欄位與解析（`internal/module/hostconfig/relay.go`）
- `RelaySwitches` 加 `MemberAuto bool \`json:"member_auto"\``；`DefaultRelaySwitches`、`relaySwitchesJSON` 加 `member_auto: true`。
- `relayFields` 接受 `member_auto`（錯誤訊息的欄位清單同步）；`relaySwitchesOf` 讀它（布林、null 拒絕、缺少＝true），錯誤訊息同步。
- `readRelay` 的 fail closed 回 `RelaySwitches{}`，`MemberAuto` 自然是 false，符合 §2。
- 測試（`relay_test.go`）：缺少＝true；`false` 存得進去、讀得回來；`null`／非布林 → 400；拼錯的 key 仍 400；never-written GET 帶 `member_auto: true`；壞列 GET 三個都 false；PUT 只帶 self 開關時 `member_auto` 回到 true（記錄現行「整列取代」語意）。

### T2 team 模組：五個閘門（`internal/module/team/`）
- 新 helper `func (m *Module) memberAutoOn() bool`：讀 `m.switches.RelaySwitches()`；錯誤 → log 一次並回 false；`m.switches == nil`（測試或未註冊）→ true（維持現有測試的預設行為；正式環境一定有註冊）。放在 `relay_handler.go` 的 `selfRelayState` 旁邊。
- 閘門：
  1. `handleRelayHello`：`IdleIntercept` 只在 `memberAutoOn()` 時帶 `team.IdleInterceptMembers`，否則 0。
  2. `handleRelayCompacted` intercepted 分支：`!memberAutoOn()` → 直接回 `{Noticed:false, Route: silent}`，不呼叫 `interceptedIdle`。
  3. `handleRelayCompacted` 一般分支：記帳照舊，送通知前 `!memberAutoOn()` → 回 `{}`（noticed false）。
  4. `handleRelayAsk`：解析與驗證之後、`ActiveRemoteMemberBySession` 之前，`!memberAutoOn()` → 409 `team.ErrRelayUnsupported`，detail 用 §2-3 的文字。
  5. `noticeUsage`、`noticeIdle`：開頭 `!memberAutoOn()` → return。
- 測試（沿用 `fakeSwitches`，加 `MemberAuto` 欄位與錯誤注入）：
  - hello：開 → `idle_intercept: 1`；關 → 欄位缺席；讀取錯誤 → 缺席。
  - intercepted：關 → route silent、lead 收不到訊息、`interceptedIdle` 的 claim 沒動。
  - compacted auto／idle：關 → open ask 仍被撤銷、lead 收不到通知。
  - ask：關 → 409 relay_unsupported、沒有 ask 列、無人值守開著也不接受；遠端 member 也擋。
  - noticeUsage／noticeIdle：關 → 超過門檻且閒置的 member 不收到通知；開回來 → 下一輪照常。
  - 手動：關時 lead 的 `POST /api/team/relays` 照常建 op（防止誤擋）。
- 註解：`relay.go` 的「a member has no switch (U13)」等說法改成指向本 spec。

### T3 SPA：開關與文字（`spa/src/`）
- `lib/host-config-api.ts` 的 `RelaySwitches` 型別加 `member_auto: boolean`；`stores/useHostConfigStore.ts` 的 `DEFAULT_RELAY_SWITCHES` 加 `member_auto: true`。
- `lib/host-config-parse.ts`：`RELAY_SWITCHES` 加 `member_auto`；預設 true；fail closed 的 `off()` 三個都 false。
- `components/hosts/RelaySection.tsx`：第三列改成 `ToggleSwitch`（`testId="relay-member-auto"`），`toggle` 的欄位型別加 `member_auto`，小字照 §4；`locked` 規則同其他兩個。
- 兩個語系檔加／改字串（§4）。
- 測試：parser（缺少＝true、false 保留、非布林→無法讀取、fail closed 三個 false）；RelaySection（顯示開關、點擊送出 `{...current, member_auto: !current.member_auto}`、locked 時不送）；若有語系 key 對齊測試要過。

### 測試分層（照 CLAUDE.md）
- 開發中：只跑改到的檔／套件：`pdx test go-pkg ./internal/module/hostconfig/`、`pdx test go-pkg ./internal/module/team/`、`pdx test spa-file <檔>`。
- PR 前：`pdx test go-changed`（改到的兩個套件）、`pdx test lint`、`pdx test spa-related <改到的 SPA 檔>`、`pdx test tsc`（會被送到 mserv）。不改鎖或 goroutine，不跑 `-race`。
- 完整套件留給發版。

## 6. 不做
- 不拆 relay 程式、不改 mod、不改 `pdx-team` skill。
- 不追溯取消已開著的 ask／op。
- SPA 對 relay 列未知 key 的容錯（例如既有的 `spawn_resource_check` SPA 也不認得）另開 issue。
