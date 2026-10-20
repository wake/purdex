# agent 通知標題 — plan

> spec：`docs/specs/2026-10-20-notify-title-spec.md`。lead 1f。一期一張 PR（≤800 行／20 檔）。N-1、N-2 是 A 線（Sonnet member，TDD，每個 task 一個 commit）；N-3 是 B 線（88 排，**在 N-2 merge 之後**；它需要的 keyed interests #3763 與 #3764 都已在 main）。
> 測試分層照 CLAUDE.md：開發中只跑改到的套件；PR 前 `pdx test go-changed <套件>`＋`pdx test lint`（SPA：`vitest related`＋`tsc -b`），job 的 snapshot tree＝PR head 的 tree。codex R1→R2（攻擊→critic），sol、effort low。
> 修訂 1：照 codex plan 審查（task-mvgogcnd-s6scng）13 條修正，對照表在文末。
> 修訂 2：照 88 對 N-3 的五點（依賴、`sessionOfTab`、`tab.group` 指向 team、device 鍵、(a) 不需 root）修正。

## N-1：daemon 算人名與 team，送出 notify_label，手機標題改格式

1. **共用案例檔**：`internal/push/testdata/notify-title-cases.json`，每筆 `{name, group, tmux, host_label, thing, want}`，涵蓋 spec §1 三列、工作簿主題、各種超長（主題先截、再截任務編組、人名不截）、正規化（控制字元、bidi、換行）。Go 測試讀它；N-3 的 TS 測試以相對路徑讀同一個檔（`spa/` 到 `internal/` 的相對路徑在 vitest 可讀，N-1 先放一個最小的 TS 讀檔測試確認可行，否則改放到兩邊都讀得到的位置並在 PR 說明寫明）。
2. **格式函式**：`internal/push` 新增 `NotifyTitle(label NotifyLabel, hostLabel, thing string) string`；`AgentContent` 的標題改用它（`AgentInput` 加 `Label NotifyLabel`；空的就是現行行為 `{host}：{tmux}`）。`hostLabel` 照現行在逐一收件裝置時代入 `d.HostLabel`（`agent_trigger.go` 的 per-device Make）。測試：同一 daemon 對兩個裝置主機名不同、各自正確；APNs payload 只有 `aps.alert.title` 改變、`purdex` 那一段的形狀不變。worker（execution）不經過這條。
3. **labeler（記憶體內）**：一個 `NotifyLabeler`（放在 agent 模組或新的小套件，由實作者定，但要能被 agent 的事件投影層與 push 共用，經 registry 取得）：`Label(sessionID, sessionName) NotifyLabel`，只做記憶體查詢：
   - 人名：`NameService.CodeOf(ctx, sessionID) (string, bool)` → `peers.PoolDisplayName(code)`（atomic registry，含 retired persona）；
   - team：team 模組提供一個快取（team 事件、appearance、成員變動時更新）：session → `{team_name, lead_label}`，`lead_label` 照 SPA `labelOf`（title → 地址最後一段 → ref）與 roster 的「先 live origin、離線用 request 存的值」（`internal/module/team/roster.go:193–225`）；本機 member（`team_members` join `teams`）與遠端 member（`remote_members` 的 `team_name`、`lead_title`、`lead_address`、`lead_ref`）分開查；`ModRead` 的 member 分支要拆成 local／remote 兩支。
   - 測試：有 persona／沒有／retired persona／name row 讀不到／persona 改名後；lead、本機 member、遠端 member、team_name 空時取 lead label、遠端改名後讀值；查詢不在 emitter 的鎖裡做 I/O（以注入的慢查詢證明事件廣播不被擋）。
4. **事件投影**：`agentpkg.NormalizedEvent`（`internal/agent/status.go`）加 `notify_label {name, group, tmux}`（omitempty），在 live 廣播（`hookemitter.go` 的 `emitNormalizedToCode`）與訂閱 snapshot（agent 模組的 `buildNormalized`）**共用的投影層**填；push 的 `onNotify` 也呼叫同一個 labeler，不另算。測試：live 與 snapshot 兩條路徑都帶出同樣的 label；worker 的 synthetic event 不帶。
5. **成本**：labeler 每個事件只做記憶體查詢；PR 說明附單次耗時。

## N-2：回寫路由、儲存與合成

1. **歸屬**：常駐、不依 APNs 的位置（新模組 `internal/module/notify` 與 `notify.db`，或 agent 模組，實作者先給 lead 兩三行方案）。**不放 push 模組**（只有 `apns_dir` 才掛載）。
2. **資料表**：`group_labels(seq INTEGER PRIMARY KEY AUTOINCREMENT, device TEXT, root TEXT, grp TEXT, written_at INTEGER, UNIQUE(device, root))`；整份取代該 device 在**單一 transaction** 內完成；查詢同一 root 取 `seq` 最大者。若放在既有 DB，要有遷移與「部署前的舊 DB 開得起來、舊資料完整、第二次開啟冪等」的測試；若是新 DB，測開檔與冪等。
3. **記憶體表**：寫入時 write-through 到記憶體表，labeler 只讀記憶體表。
4. **路由**：`PUT /api/notify/group-labels` 本文 `{device, labels:[{root_session_id, group}]}`；上限每 device 2000 筆；字串正規化。權限：admin／App token 可寫；**不加進** `cmd/pdx/device_scope.go` 的 `deviceAllowed` 清單（配對手機的裝置 token 回 403）。能力 `notify.group_labels.v1` 明確加進 `internal/core/info_handler.go` 的 `capabilityList`。
5. **清理**：每天一次刪掉 30 天沒寫入的 device 的全部列。
6. **合成**：labeler 的 group＝team 優先；否則以 `team.LineageRootResolver.RootSessionOf(sessionID)`（經 `team.LineageRootKey` 從 registry 取）得 root，查記憶體表；root 讀取錯誤時 group 空。
7. **測試**：整份取代（transaction，讀者不會看到一半）、兩個 device 同 root 依 seq 取最後寫入（含同一毫秒）、30 天清理、上限、正規化、team 優先、own-root／relay-root／root 讀取錯誤、admin 與 App token 成功、裝置 token 403、`/api/info` 宣告能力。

## N-3（B 線，88 排；在 N-2 之後）

1. **投影**：App 依 spec §3.2 的 (a)(b) 算出每台主機的 labels：(a) 自訂編組用本機 host id 對應、`order` 第一個 owner 勝出，直接用 store 成員的 root，不需要讀 root；(b) 分頁的 session 用 `sessionOfTab`（`lib/task-groups/session-actions.ts`），不另寫 tree-order；`tab.group` 只在 team／自訂編組確實存在時有效；`tab.group` 指向 team（分頁歸在 team 底下但沒座位）時寫該 team 的顯示名（`team_name`，空的用 lead label，與 `panelName` 相同）；root 還不知道就延後；改名、刪除、移除成員後以整份 payload 清掉 daemon 舊值。`device` 用 `purdex-client-identity`。
2. **寫入**：只對宣告 `notify.group_labels.v1` 的主機寫；變動時 debounce、連上主機時重送；投影沒變不重寫。
3. **root**：只有 (b) 需要；`session-roots-lifecycle` 加 asker `notify`（依 #3763 的 keyed interests），只對宣告能力的主機、帶 `tab.group` 的 session 讀；sidebar／personal／notify 同時啟用、各自更新與卸載互不影響。
4. **Mac 通知**：`useAgentNotifications.ts` 事件帶 `notify_label` → 依規則組，`{主機}` 用本裝置的 `hostLookOf`／`hostLabel`；沒帶 → 現行。TS 格式函式讀 N-1 的同一份案例檔。worker 照舊。
5. **測試**：上述投影規則逐條（含 `tab.group` 指向 team 而沒座位、team_name 空時用 lead label）、capability 閘、連線重送、debounce、投影不變不重寫、新舊 daemon 的 fallback、worker 不受影響。

## 完成後

- N-1 出版即可見效（team 的 session）；N-2 與 N-3 都上線後，自訂編組與 tab.group 的 session 才會顯示編組名。
- 部署照現行順序（每台先 Purdex daemon、再 wwd；本功能只動 Purdex daemon 與 SPA）。

## codex plan 審查（task-mvgogcnd-s6scng）處理對照

| # | 嚴重度 | 內容 | 處理 |
|---|---|---|---|
| 1 | critical | 事件先廣播、snapshot 另一條路，push 合成無法回寫；不能在 emitter 鎖內查 SQLite | N-1 #3、#4：記憶體內 labeler，在 live 與 snapshot 共用的投影層填，push 共用 |
| 2 | critical | 放 push 模組會因 APNs 未設定而整個沒有 | spec §3.2、N-2 #1：常駐、不依 APNs 的位置 |
| 3 | important | 未命名 team 的 fallback 是 lead label（labelOf），不是 persona | spec §1、N-1 #3 |
| 4 | important | ModRead member 要拆 local／remote；遠端已有 team_name | N-1 #3 |
| 5 | important | CodeOf 簽名；已有 PoolDisplayName；漏測 retired／讀取失敗／改名 | N-1 #3 |
| 6 | important | root 有 LineageRootResolver | spec §3.1、N-2 #6 |
| 7 | critical | main 的 roots lifecycle 沒有 asker 概念 | keyed interests 已由 #3763 進 main（88 修訂 2）；N-3 排在 N-2 之後，測多 asker 互不覆蓋 |
| 8 | important | 最後寫入要全序；整份取代要 transaction | N-2 #2：seq 自增、單一 transaction |
| 9 | important | 既有 DB 的遷移測試 | N-2 #2 |
| 10 | important | capabilityList 是寫死的；裝置 token 的 allow-list | N-2 #4 |
| 11 | important | N-3 沒有投影與回寫的測試 | N-3 #1、#5 |
| 12 | important | 每裝置的主機名（Mac 用 hostLookOf、手機用 d.HostLabel） | spec §3.4、N-1 #2、N-3 #4 |
| 13 | minor | 相容性測試層級不對 | N-1 #2（APNs payload 形狀）、#4（兩條路徑、worker）、N-3 #5（新舊 daemon） |
