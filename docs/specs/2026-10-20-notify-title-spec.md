# agent 通知標題：「{人名} — {任務編組}」— spec

> 2026-10-20 lead 1f。使用者交辦並逐條確認（20:2x，§5 兩題 10-21 確認）。B 線 88 提供任務編組的資料事實並審過 N-3 分工；plan 已與本 spec 一起送 codex 審（task-mvgogcnd-s6scng，處理對照在 plan 文末）。

## 1. 規則（使用者定案）

agent 通知（任務完成、異常中斷、需要授權、等待輸入）的標題：

| 情況 | 標題 | 例 |
|---|---|---|
| 有人名、有任務編組 | `{人名} — {任務編組}` | `Mei — Purdex A team` |
| 有人名、沒有任務編組 | `{人名} — {主機}：{tmux 名}` | `Mei — mlab：aigora4` |
| 沒有人名 | 現行標題 `{主機}：{tmux 名}` | `mlab：aigora4` |

- 分隔是前後有空格的破折號 ` — `（U+2014）。工作簿那一輪的主題照現行規則接在最後：`Mei — Purdex A team・修好 G-3`；標題超長時先截主題，再截任務編組，人名不截。
- **人名**＝該 session 的 persona 顯示名，與 App 團隊面板相同（persona 清單的 `name`，例如 `Mei`，首字大寫照清單）。所有 session 都可能有 persona（虛擬名的尾段），不限 team。
- **任務編組**＝與 App 側欄判斷分頁屬於哪一組相同的順序（task groups spec §5、`placement.ts` 的 `groupOfTab`），先符合的算：
  1. 有 team 座位 → 該 team 的顯示名，與 App 的 `panelName` 完全相同：`team_name`，空的才用 lead 的 label（`labelOf`：title → 地址最後一段 → ref；roster 先用 live origin、離線才用 request 存的值，`internal/module/team/roster.go`）；
  2. session 的 (主機, root session) 是某個自訂編組的成員 → 該編組名；
  3. 分頁的 `tab.group` 指向一個存在的 team（分頁歸在 team 底下但沒有座位）或自訂編組 → 該 team 的顯示名（同第 1 層的 `panelName`）或該編組名；
  4. 都不是 → 沒有任務編組。
  workspace（工作區）不參與。使用者補充：任務編組／team 與 workspace 在結構上應是相仿的層級（給 B 線 task-groups-as-workspaces 的方向參考，不影響這份規則）。
- 內文不變（Claude 最後一句話、需要授權的工具等）。點通知的行為不變。
- **只套在 tmux 的 agent session**。worker（`exec-<id>`）維持現行命名（worker 標題／execution id），不套這條規則。
- **同一套**：daemon 算好人名與任務編組，手機推播與 Mac App 的通知都用 daemon 帶出的值、照同一份格式規則組標題（§3.4）。Mac 只在 daemon 沒帶（舊 daemon）時退回現行的 `notificationName`。
- Mac 的第 3 列也改成與手機相同的 `{主機}：{tmux 名}`（現行 Mac 只顯示 tmux 名或 persona 名，沒有主機）；`{主機}` 用該裝置自己替那台主機取的名稱，手機推播亦同（現行行為）。

## 2. 資料來源與限制（2026-10-20 查證）

| 層 | 資料在哪 | daemon 能否自己算 |
|---|---|---|
| 人名 | 虛擬名尾段（`peers` 的 `CodeOf(sessionID)`）＋ hostconfig `personas` 的 code→name | 能；peers 模組目前只有 code 集合，要補 code→name |
| team | team 模組（`Store.ModTeamRead`：lead 讀 `teams`；遠端 member 另走 `ActiveRemoteMemberBySession`） | 能；`ModRead` 目前只有 `TeamLabel`，要補 `team_name` 與 lead 顯示名 |
| 自訂編組 | Profile Sync `settings` 區 `purdex-task-groups.{order, groups}`，成員 `{wire host id, root session id}`；**只在 SOT 主機的 profiles.db** | 只有 SOT 主機上的 daemon 讀得到；其他台沒有副本 |
| tab.group | Profile Sync `tabs` 區；本機工作台（沒 attach 或 slave 世界）**完全不上傳** | 不能（要解析 tabs 區、對應分頁與 session；本機工作台根本看不到） |

推播由「session 所在主機」的 daemon 送出，所以第 2、3 層 daemon 無法單獨算對。

## 3. 設計

### 3.1 daemon 自己算的部分（人名、team）

- **算的位置**：送給 App 的 `NormalizedEvent` 在 notify hub 之前就已廣播（`hookemitter.go` 的 `emitNormalizedToCode`），App 訂閱時的 `agent.snapshot` 又走另一條 `buildNormalized`；所以 `notify_label` 要在這兩條路徑**共用的投影層**填，推播（`onNotify`）也從同一個 labeler 取，三者同源。labeler 只做**記憶體內查詢**（不在 `hookEmitter.mu` 裡碰 SQLite）：它內部有一張 sessionID → {persona code, team 顯示名, 讀到的時間} 的記憶體表，`Label()` 只讀這張表；查不到或資料超過 30 秒，就排一次背景重讀（單一 goroutine、同一 session 去重、不持鎖做 I/O，讀 `NameService.CodeOf` 與 team 模組的 `NotifyGroupOf`），這次呼叫照當下的表回答。所以人名與 team 名**最多晚 30 秒**反映改名、換 team（不在 peers／team 的各寫入點掛回呼，避免漏掉一個寫入點就悄悄過期）；快取還沒暖時事件不帶 `notify_label`，標題退回現行格式。表裡只存 persona code，顯示名在 `Label()` 裡用 `PoolDisplayName`（記憶體）即時查，persona 改名馬上生效。回寫的編組查記憶體表（寫入時 write-through）。
- 名字：`NameService.CodeOf(ctx, sessionID) (string, bool)`（讀 SQLite，只在背景重讀裡呼叫，且無法回報讀取失敗：失敗視同沒有 persona，下次重讀再修正）→ `PoolDisplayName(code)`（peers 的 atomic registry 已由 `SetPool` 維護，含 retired persona；查不到為空）。
- team：team 模組的 `NotifyGroupOf(sessionID)`（背景重讀時呼叫；不動 mod socket 用的 `ModRead`，那個答案有結構相等的測試且在 mod 的熱路徑上）：lead、本機 member（`team_members` join `teams`）、遠端 member（`remote_members` 已有 `team_name`、`lead_title`、`lead_address`、`lead_ref`，`team.appearance` 會更新）→ `team_name`，空則 lead 的 label（同 §1 第 1 層；本機 lead 先用 registry 的 live origin、離線才用 request 存的值）。讀取錯誤時保留上一次的值。
- root：`team.LineageRootResolver.RootSessionOf(sessionID)`（以 `team.LineageRootKey` 註冊；不在 lineage 表的 session 回傳自己）；讀取錯誤時 group 留空。

### 3.2 App 回寫的部分（自訂編組、tab.group）

- 新路由 `PUT /api/notify/group-labels`（每台主機的 daemon 各收自己那台的），本文 `{device, labels: [{root_session_id, group}]}`；`device` 是 App 的 client identity（localStorage 鍵 `purdex-client-identity`）；identity 換了就視為新裝置，舊的那份靠 30 天清理收掉。
- **歸屬**：放在**常駐、不依 APNs 設定**的地方（不能放 push 模組：它只有設定 `apns_dir` 才掛載、APNs key 載入成功才開 `push.db`），因為 Mac 通知也要用。新的小模組（例如 `internal/module/notify`，自己的 `notify.db`）或 agent 模組，由 plan 定。
- App 寫給主機 H 的內容（88 2026-10-20）：
  - (a) 自訂編組 store 裡 host＝H 的**每一個成員** → 該編組名（跟有沒有開分頁無關；store 已同步，各裝置算出來相同）；
  - (b) H 上帶 `tab.group`、而且 (a) 沒蓋到的 agent 分頁 → `tab.group` 指向自訂編組時寫該編組名；指向 team 時寫該 team 的顯示名（`team_name`，空的用 lead 的 label，同 §1 第 1 層）。分頁的 session 用 `sessionOfTab`（`spa/src/lib/task-groups/session-actions.ts`），需要這個分頁的 root。
  - 第 1 層 team 不寫，由 daemon 自己算。
- daemon 依 (device, root) 存在本機小表，整份取代該 device 的那一份（**單一 transaction**，讀者不會看到空集合或一半）；「最後寫入」用遞增的序號（不是毫秒時間戳，同一毫秒也有全序）。某個 device 30 天沒有再寫入，整份刪掉（換機後舊裝置的值不會永遠留著）。上限每個 device 2000 筆。
- App 何時寫：只在**變動時**（編組新增、改名、刪除、成員變動、分頁的 group 變動，debounce）與**連上主機時**；不做定時重寫，標題在兩台裝置之間跳動只會發生在真的有人改了東西的時候（只有第 3 層可能因本機工作台不同而不同）。
- root 的來源（88）：只有 (b) 需要 root；(a) 直接用 store 裡成員的 root。App 目前只在任務編組檢視或個人面板顯示時讀 root（`session-roots-lifecycle.ts` 的 `setRootsInterest`），依 asker 分開的 keyed interests 已由 #3763（TG-5b-1）進 main。N-3 加第三個 asker `notify`：只對宣告能力的主機、帶 `tab.group` 的 session 讀；各 asker 的設定與卸載互不覆蓋；root 快取照舊保留。
- App 關著時 daemon 照樣用最後一次寫入的值。權限：admin／App token 可寫；配對手機的裝置 token 受 `deviceAllowed` 的預設拒絕清單管，這條路由**不加進**清單（回 403）。字串照通知的正規化規則（去控制字元、截長）。能力 `notify.group_labels.v1` 要明確加進 `info_handler.go` 的 `capabilityList`。

### 3.3 合成

daemon 的 `notify_label.group` ＝ team（3.1）優先；沒有才用 App 回寫（3.2）；都沒有為空。標題依 §1 組出；手機推播的 `alert.title` 與 `NormalizedEvent.notify_label` 同源。

### 3.4 格式規則只有一份定義；Mac App

- daemon 在 `NormalizedEvent` 帶 `notify_label {name, group, tmux}`（三個字串，空＝沒有），手機推播與 Mac 都由這三個值加上「該裝置對這台主機的名稱」與「工作簿主題」組標題。
- 組法（§1 的三列、` — `、`：`、`・`、截斷順序：先截主題、再截任務編組、人名不截）在 Go（`internal/push`）與 TS（SPA 的 notification-content）各有一份實作，**兩邊的測試讀同一份案例檔**（例如 `internal/push/testdata/notify-title-cases.json`，SPA 的測試以相對路徑讀同一個檔），任何一邊改了規則而另一邊沒跟上，測試就紅。（88 建議 daemon 直接帶完整字串；但標題裡的 `{主機}` 是各裝置自己取的名稱，daemon 組不出所有裝置通用的字串，所以改用「同一份案例檔鎖兩份實作」。）
- Mac：`useAgentNotifications.ts` 的 `notificationName` 改成：事件帶了 `notify_label` → 依規則組，`{主機}` 用本裝置的 `hostLookOf`／`hostLabel`；沒帶（舊 daemon）→ 現行做法。worker 照舊。手機：照現行在逐一收件裝置組 payload 時代入該裝置的 `d.HostLabel`，不改成 daemon 的全域主機名。

## 4. 分期與分工

| 期 | 內容 | 線 |
|---|---|---|
| N-1 | daemon：人名（code→name）、team 名（`ModRead` 補 `team_name` 與 lead 名、遠端 member）、`notify_label`、手機標題依 §1、共用案例檔；Mac 端不動 | A |
| N-2 | daemon：`PUT /api/notify/group-labels`、本機小表、合成；能力 `notify.group_labels.v1` | A |
| N-3 | App：依 §3.2 的 (a)(b) 算第 2、3 層並回寫（只在主機宣告能力時）、加 `notify` asker 讀 root；Mac 通知讀 `notify_label`、照共用案例檔組標題；在 N-2 之後 | B |

N-1 單獨出版就有效果（team 的 session 立即變成 `Mei — Purdex A team`，其他照 §1 的「沒有任務編組」列）。

## 5. 已確認（使用者 2026-10-21）

1. Mac 通知的第 3 列（沒有人名）改成與手機相同的 `{主機}：{tmux 名}`（§1 末條）。
2. 自訂編組與 `tab.group` 的正本仍是 profile（Profile Sync 的 settings／tabs 區）；§3.2 回寫給各台 daemon 的只是算好的編組名，供 daemon 組推播標題用，不另存一份到 profile。

另：手機推播的標題由 daemon 組（APNs `alert.title`）。iOS App 若之後另有自己顯示的本機通知，照同一套規則（§3.4 的案例檔），不在 N-1～N-3 範圍。
