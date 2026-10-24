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

host config `relay` 列加一個布林欄位 **`member_auto`**，**預設 true**（沒寫過的主機、沒帶這個欄位的列，行為與現在相同）。**這個欄位只在值為 false 時出現在線上與存檔裡**（§3）。

`member_auto = false` 時，**這台主機上的 member**（daemon 依自己的角色紀錄判斷），每條程式路徑看執行它的那台主機的開關：
1. **A 關**：hello 回應不帶 `idle_intercept`（等於 0）。mod 已支援「0＝daemon 不決定，照常壓縮」（`register.js` hello 處理約 :262），不必改 mod。
   - 已經從先前的 hello 聽到 1 的 mod 仍會擋一次並回報 `--intercepted`：daemon 回 `route: "silent"`、不發通知、不動 intercept 的 claim。mod 照現有邏輯（`interceptedLater`，約 :1649）把角色暫時當成 none、重送 hello，拿到 role member＋沒有 `idle_intercept`，之後的閒置壓縮照常跑。已被擋掉的那一次不補。
   - **已知窗口（接受）**：mod 把角色當 none 到 hello 回來之間（通常一秒內），若剛好有 turn 結束且用量 ≥ 門檻，mod 會走一般 session 的 `begin --self`。daemon 用自己的角色紀錄判斷，member 的非手動 begin 一律回 409 `member_relay_is_leads`（`relay_begin.go` 約 :118），mod 收到後把角色改回 member（約 :797），不會接力、也不消耗 ask 階梯。這段期間送進來的 prompt 最多被 begin 的等待擋住 `BEGIN_HOLD_MS`。40% 壓縮下用量到不了 70% 門檻，實際上碰不到。
2. **B 關**：`relay compacted`（非 intercepted）的記帳全部照舊：刪 fresh half、`rosterChanged`、撤銷 open ask、`askAnswered`、`markIdleCompacted`、`DisarmNoticeForce`。閘門放在 **`DisarmNoticeForce` 之後、組通知文字之前**；關閉時回 `{}`（`noticed: false`），不發通知給 lead。
3. **C 關**：`POST /api/relay/ask` 對 **daemon 確認是 member 的 session** 回 **409 `relay_unsupported`**，detail「member 自動接力已在主機設定關閉」。不建列、不通知 lead、無人值守也不自動接受。
   - 順序：遠端 member（`ActiveRemoteMemberBySession` 找到列）→ 在 `askRemote` 轉送之前擋；本機 → 先用 `ActiveMemberInLiveTeam` 確認是 active member 才擋。**不是 member 的 session 照舊**走 `CreateRelayAsk` 回 `not_member`，錯誤優先序不變。
   - 用既有錯誤碼是因為 mod 對它的處理正是我們要的（`lastAskPct` 記下，+10 點之前不再問；到 80%、90% 會再問一次、照樣被拒），新錯誤碼就得改 mod、兩台重跑 `pdx setup`。
4. **D 關**：`noticeUsage` 照常跑 arm 的分支（用量低於門檻 → `ArmNotice`），**只跳過**「超過門檻 → 檢查閒置 → disarm → 送」那一段。這樣關閉期間 arm 狀態仍跟著用量走，重新打開後下一輪就能依當下狀態判斷。
5. **E 關**：`noticeIdle` 照常維護閒置期（`idleStart`／`endIdlePeriod`／`pruneIdle`），**只跳過** `claimIdleTold` 與送出那一段。重新打開後，已閒置超過門檻的 member 下一輪照規則通知一次。
6. **已經開著的 ask**（關閉前建立的，含遠端 member 在 lead 主機上的鏡像）：
   - 不補送通知（`retryAskNotices` 關閉時直接 return）。
   - 無人值守不自動接受（`acceptOpenAsksUnattended` 關閉時回 0；它同時涵蓋 tick 與無人值守 sweep 兩個呼叫端）。
   - final ask 過期不轉 unanswered、不暫停 member（`markFinalAsks` 關閉時直接 return）。實作者要確認 `ExpireRelayAsks` 對 final ask 的處理：若 final ask 不會被它過期，關閉期間就維持 open，重新打開後照原規則處理；不新增 schema 或撤銷原因。
   - 一般過期（`ExpireRelayAsks`，5 分鐘）、member 離隊撤銷、lead 手動 `pdx relay` 接受 ask：照舊。
   - 已經在跑的 relay op：照原本流程結束，不追溯取消。

不受影響：
- lead 手動 `pdx relay _<ref>`（`handleRelayCreate`）、使用者在 member 打的 `/relay`（`req.Manual`）。這是人主動要求的，與 `self_solo`／`self_lead` 不管手動 `/relay` 的原則一致。
- 一般 session 與 lead 的自我接力（仍由 `self_solo`／`self_lead` 管）。

**讀不到開關時（store 錯誤、列無效）**：`member_auto` 視為 **false**。與 self 開關的 fail closed 同方向：讀不到就不做自動的事；這裡「不做」＝不攔截、不通知，壓縮照常，不會卡住任何 session。錯誤寫 daemon log（同一種錯誤不要每個 tick 都刷一行，節流或只在狀態變化時寫）。

**開關何時生效**：daemon 每次判斷時讀 store（`RelaySwitches()` 本來就每次讀，與 self 開關相同），不快取。mod 端只在下一次 hello 才知道 A 的新值；關掉時靠 §2-1 的 silent 讓 mod 立刻重問；打開時 mod 要等下一次 hello（session start、`/clear`、hello 重試、member recheck），可以接受。

## 3. 相容性與部署

舊版 App（現行 SPA）的 relay 解析器遇到不認識的 key，會把整列判為無法讀取、改用 `{self_solo:false, self_lead:false}`；之後在舊 App 按任何一個開關，會用這個物件整列 PUT，**把 `prompt_write`／`prompt_fix`／`prompt_seed` 一起洗掉**（mlab 現在就有自訂的 `prompt_write`）。所以線上格式要讓舊 App 在預設值下看不到新欄位：

- **daemon**：`RelaySwitches` 序列化時 `member_auto` **只在 false 時寫出**（存檔與 GET 的 `items` 都是；never-written 的預設 GET 維持 `{"self_solo":true,"self_lead":true}` 不變）。讀取時缺少＝true。
- **SPA**：送 PUT 時 `member_auto` **只在 false 時放進 body**；true 就省略。這樣新 App 對舊 daemon 的 self 開關與 prompt 存檔照常成功；只有在舊 daemon 上把 member 開關切到關，才會被 400（unknown relay field），錯誤會顯示在接力區。
- 結果：新 daemon＋舊 App、舊 daemon＋新 App，在 `member_auto` 維持預設時都相容，可以照平常的順序部署。
- **寫入 `member_auto=false` 的前提**：兩台 daemon 都已部署，**而且使用者的 Mac App 都已更新到同版**。寫之前要先問使用者確認 App 已更新；不然舊 App 一碰接力區的開關就會洗掉自訂 prompt。
- 不改 mod → 部署只要重啟 daemon，不需要 `pdx setup`。
- 寫入方式：沿用 10-24 關 self 開關的腳本（先 GET 整列，只改這個欄位，帶 `baseRevision` PUT），mlab 與 air26 各一次。token 用 HDR 檔傳給 curl，不放 argv。

## 4. 介面文字（SPA 主機頁「接力」區）

第三列原本是說明文字「member 自我接力／member 的接力一律由 lead 安排」，改成開關：
- 標籤：**member 自動接力提醒**（en：Automatic relay reminders for members）
- 開關下方小字：**到門檻或閒置時通知 lead、擋下 member 的閒置壓縮請 lead 決定。關閉後 member 由 Claude Code 自行壓縮，lead 仍可手動 pdx relay。**
  （en：Tells the lead at the threshold or when idle, and holds a member's idle compaction for the lead to decide. Off: members compact on their own; the lead can still run pdx relay.）
- `hosts.relay.member`、`hosts.relay.member_note` 兩個舊 key 由新文字取代（沿用 key 名或換新 key 皆可，兩個語系都要有，舊 key 沒人用就刪）。
- 無法讀取的列（fail closed）：三個開關都顯示關。

## 5. Plan

每個 task 獨立 commit；TDD（先寫失敗測試）。

### T1 hostconfig：欄位、解析、序列化（`internal/module/hostconfig/relay.go`）
- `RelaySwitches` 加 `MemberAuto bool`；`DefaultRelaySwitches` 的 `MemberAuto` 為 true。
- 序列化：`RelaySwitches` 實作 `MarshalJSON`，`member_auto` 只在 `MemberAuto == false` 時輸出（其餘欄位的輸出與現在逐位元組相同，含 `spawn_resource_check`、prompt 的 omitempty）。`relaySwitchesJSON` 不變。
- `relayFields` 接受 `member_auto`（錯誤訊息裡的欄位清單同步）；`relaySwitchesOf` 讀它（布林；`null`／非布林拒絕；缺少＝true），錯誤訊息同步。
- `readRelay` 與 `RelaySwitches()` 的 fail closed 回 `RelaySwitches{}`，`MemberAuto` 為 false，符合 §2。
- 測試（`relay_test.go`、`read_test.go`）：
  - PUT：缺少 → 讀回 true、存檔裡沒有 `member_auto`；`false` → 存檔有 `"member_auto":false`、讀回 false；`true` 明寫 → 存檔省略；`null`／字串 → 400；拼錯的 key 仍 400。
  - 只帶 self 開關的 PUT 會把 false 變回 true（記錄現行「整列取代」語意，SPA 因此必須整列送）。
  - GET：never-written 的 `items` 與現在逐位元組相同；`member_auto:false` 的列 GET 帶 false；壞列 GET 三個都 false。
  - `Module.RelaySwitches()`：存檔裡 `member_auto` 為 null／字串的列回 error（正式閘門走的是這條，不是 GET）。
  - 現有 prompt 欄位在帶 `member_auto:false` 的 PUT 前後保留。

### T2 team 模組：閘門（`internal/module/team/`）
- 新 helper `func (m *Module) memberAutoOn() bool`：讀 `m.switches.RelaySwitches()`；錯誤 → 回 false 並寫 log（節流，見 §2）；`m.switches == nil` → true。放在 `relay_handler.go` 的 `selfRelayState` 旁邊。
- 閘門（位置照 §2 逐條）：
  1. `handleRelayHello`：`IdleIntercept` 只在 `memberAutoOn()` 時帶 `team.IdleInterceptMembers`。
  2. `handleRelayCompacted` intercepted 分支：關閉 → 回 `{Noticed:false, Route: InterceptRouteSilent}`，不呼叫 `interceptedIdle`。
  3. `handleRelayCompacted` 一般分支：`DisarmNoticeForce` 之後、組通知之前檢查；關閉 → 回 `{}`。
  4. `handleRelayAsk`：遠端 member 在 `askRemote` 之前；本機在 `ActiveMemberInLiveTeam` 確認是 active member 之後、`CreateRelayAsk` 之前。關閉 → 409 `team.ErrRelayUnsupported`。
  5. `noticeUsage`：arm 分支照跑，超過門檻那段 `continue`。
  6. `noticeIdle`：閒置期維護照跑，`claimIdleTold` 之前 `continue`。
  7. `retryAskNotices`、`markFinalAsks`：開頭關閉即 return；`acceptOpenAsksUnattended`：開頭關閉回 0。
- 測試（`fakeSwitches` 的預設改用新的 `hostconfig.DefaultRelaySwitches`；**既有測試裡直接寫 `hostconfig.RelaySwitches{...}` 字面值的，現在 `MemberAuto` 會是 false**，逐一檢查：測 self 開關的補上 `MemberAuto: true`，不要讓既有測試的意圖被新閘門改掉）：
  - hello：開 → `idle_intercept: 1`；關 → 欄位缺席；`RelaySwitches()` 回錯 → 缺席。
  - intercepted：關 → route silent、lead 沒收到訊息、claim 沒動（之後打開再回報同一 period 仍會通知一次）。
  - compacted auto／idle：關 → open ask 被撤銷、fresh half 被刪、idle 標記與 70% disarm 都做了（逐項斷言）、lead 沒收到通知。
  - ask：關 → 本機 member 409 relay_unsupported、沒有 ask 列、無人值守開著也沒接受；遠端 member 也 409 且沒有轉送；**非 member 的 session 仍回 not_member**。
  - noticeUsage：關閉期間用量降到門檻下 → 仍會 arm；關閉時超過門檻不送；打開後下一輪送一次（off→on）。
  - noticeIdle：關 → 閒置超過門檻不送；打開後下一輪送一次。
  - 已開著的 ask：關 → 不補送通知、無人值守 tick 與 sweep 都不接受、final ask 過期不暫停 member；一般過期照舊。
  - 手動：關時 lead 的 `POST /api/team/relays` 照常建 op；member 自己的 `/relay`（manual begin）照常。
- 註解：`relay.go`、`relay_handler.go` 等處「a member has no switch (U13)」的說法改成指向本 spec。

### T3 SPA：開關與文字（`spa/src/`）
- `lib/host-config-api.ts` 的 `RelaySwitches` 型別加 `member_auto: boolean`（必填，解析後一定有值）；`stores/useHostConfigStore.ts` 的 `DEFAULT_RELAY_SWITCHES` 加 `member_auto: true`。
- `lib/host-config-parse.ts`：`RELAY_SWITCHES` 加 `member_auto`；缺少＝true；非布林 → 整列無法讀取；fail closed 的 `off()` 三個都 false。
- PUT body：relay 的序列化在送出前**省略值為 true 的 `member_auto`**（§3）；放在送 relay PUT 的那一層（`saveRelay` 或 api 函式），讓 toggle、prompt 存檔、衝突後重寫都經過它。
- `components/hosts/RelaySection.tsx`：第三列改成 `ToggleSwitch`（`testId="relay-member-auto"`），`toggle` 的欄位型別加 `member_auto`；小字照 §4；`locked` 規則同其他兩個。
- 兩個語系檔改字串（§4），舊 key 沒人用就刪。
- 測試：
  - 既有直接構造 `RelaySwitches` 的 fixture（`RelaySection.test.tsx`、`RelaySection.tab-switch.test.tsx`、`useHostConfigStore.test.ts` 等）補 `member_auto`，讓 tsc 在這個 commit 就過。
  - parser：缺少＝true、false 保留、非布林 → 無法讀取、fail closed 三個 false。
  - PUT body：`member_auto:true` 時 body 不含這個 key；false 時含 `"member_auto":false`。
  - 保留：`member_auto:false` 經 self 開關 toggle、prompt 存檔、409 衝突後重試，送出的 body 仍是 false。
  - RelaySection：顯示開關、點擊送出翻轉後的值、locked 時不送；新標籤與小字（兩個語系）逐字斷言。

### T4 mod 迴歸測試（只加測試，不改 `register.js`）
在 `cmd/pdx/plugin/purdex/hooks/member.test.ts`（或 `relay.test.ts`，依既有 harness）加：
- member 收到 `route: "silent"` 後重送 hello，hello 回 `role: "member"` 但沒有 `idle_intercept` → 下一次閒置壓縮不被攔（`session.compact` 回 next 的結果，不是 `{skip}`）。
- 那段期間的 `begin --self` 被回 409 `member_relay_is_leads` → 角色回到 member、`lastAskPct` 沒被改。
- member 的 ask 收到 409 `relay_unsupported` → 同一輪與 +10 點內不再問；到 +10 點再問一次。
若既有測試已覆蓋其中某條，引用測試名稱即可，不重複寫。

### 測試分層（照 CLAUDE.md）
- 開發中：只跑改到的檔／套件：`pdx test go-pkg ./internal/module/hostconfig/`、`pdx test go-pkg ./internal/module/team/`、`pdx test spa-file <檔>`、mod 測試用 `pdx test mod-hooks`。
- PR 前：`pdx test go-changed`（改到的兩個套件）、`pdx test lint`、`pdx test spa-related <改到的 SPA 檔>`、`pdx test tsc`（會被送到 mserv）、`pdx test mod-hooks`。不改鎖或 goroutine，不跑 `-race`。
- 完整套件留給發版。

## 6. 不做
- 不拆 relay 程式、不改 mod 程式、不改 `pdx-team` skill。
- 不追溯取消已在跑的 relay op；不新增 schema、撤銷原因或錯誤碼。
- SPA 對 relay 列未知 key 的容錯（例如既有的 `spawn_resource_check` SPA 也不認得，一旦打開舊 App 接力區就會判成無法讀取）另開 issue。

## 7. plan review 回應（codex `task-mvma8080-aljxbg`）
| # | 判定 | 處理 |
|---|---|---|
| 1 critical：silent 後角色暫為 none 可能走自我接力 | 不成立 | daemon 依自己的角色紀錄回 `member_relay_is_leads`（`relay_begin.go` 約 :118），mod 改回 member（`register.js` 約 :797）；寫進 §2-1 的已知窗口，T4 加測試 |
| 2、3：混版部署、舊 App 洗掉 prompt | 成立 | §3 改成 `member_auto` 只在 false 時出現在線上；寫 false 前先確認 App 已更新 |
| 4：noticeUsage 整輪跳過會卡住 arm | 成立 | §2-4 改成只跳過送出段 |
| 5：compacted 閘門位置 | 成立 | §2-2／T2-3 指定在 `DisarmNoticeForce` 之後，逐項斷言 |
| 6：ask 閘門改變非 member 的錯誤 | 成立 | §2-3 改成先確認是 member |
| 7：已開著的 ask 的 sweep 路徑 | 成立 | 新增 §2-6、T2-7 |
| 8：SPA fixture 與整列保存 | 成立 | T3 測試清單補齊 |
| 9：`RelaySwitches()` 路徑沒測 | 成立 | T1 補 |
| 10：mod 組合行為沒測 | 成立 | 新增 T4 |
| 11：文字沒有逐字斷言 | 成立 | T3 補 |
