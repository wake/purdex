# Peer 信箱整合：虛擬地址、執行體收訊、指揮台顯示（spec）

> 2026-10-08，統籌＝purdex-54（`mlab/_vioqlc`）。Nexen 側對口＝nexen-74（`mlab/_10qxao`）。
> 前置：Purdex 已升 Nexen v0.20.0（#1874，alpha.588，peer 信箱尚未開）。

## 1. 使用者定案（2026-10-08，不翻案）

| # | 決定 |
|---|---|
| U1 | `pdx msg send` 對執行體**走 Nexen 新端點**（統一投遞：pdx 決定最後一跳，依目標分流寫入方式） |
| U2 | **誰都可以喚醒任何執行體**，不設額度 |
| U3 | **多則訊息不合併**，一則一則送（每則一個 turn） |
| U4 | `[nex.peer]` **預設全開**；設定頁可關 |
| U5 | **虛擬地址**：地址的名字＝`<基底>-<後綴>`，後綴取 ref 的前兩碼（ref `_k3m9qz` → `-k3`）；基底＝這個對話第一次被看到時的名字（例：`purdex-54` → `purdex-54-k3`）。之後 CLI 名字怎麼跳（每次喚醒換名、`/rename`），所有地方一律顯示這一組：`pdx peers`、`pdx msg whoami`、指揮台、它寄出訊息的寄件者、回信指示 |
| U6 | 虛擬地址**套全部對話**（純終端機也是），統一規則；最後一跳由 pdx 決定（執行體 → Nexen 信箱，終端機 → CLI socket） |
| U7 | 用詞：worker 退場，底層＝**執行體 execution**、介面＝**指揮台 deck**（見介面整合定案） |
| U8 | peer 訊息在指揮台上**比照終端機、明顯看得出是 peer 訊息**；喚醒那一段收成像工具呼叫的一列（符號＋文字），點開才看細節 |
| U9 | 喚醒採**兩段式**的方向：短 prompt 喚醒（例「有 peer 訊息即將送達，請回 Y」）→ 再把 peer 訊息從 inbox 打進去；**否決的是「把 peer 訊息當 prompt 送進去」**。先驗證能否正確喚醒＋送達（§8） |

## 2. 現況（已查證）

- **Nexen v0.20.0 契約**：`POST /v1/executions/{id}/peer-messages`，body `{from_name, reply_to?, from_mode?, msg_id?, text}`，不需要 lease，回 `{turn_id, delivery: delivered|queued}`；同 `msg_id` 重送冪等（`duplicate:true`、`turn_state`，`stalled` 要換新 msg_id）；`from_name`／`reply_to`／`msg_id` 必須是一個 shell word（`^[A-Za-z0-9._:/@_][A-Za-z0-9._:/@_-]{0,199}$`，reply_to／msg_id 上限 128）；錯誤 `403 peer_messages_disabled`、`429 peer_queue_full`、`400 peer_message_invalid`＋`/messages` 的碼。事件 `peer_message`（取代該 turn 的 `execution.message_accepted`），站台級只留 `turn_id`／`msg_id`／`template_version`。`capabilities.peer_message` 缺席＝不送。in-process 入口 `(*execution.Service).PeerMessage`。
- **v0.20.0 的喚醒方式與 U9 不同**：它把 peer 原文放進喚醒 prompt（transcript 那行＝3 個 text block，無 `origin`／`isMeta`）。這正是 U9 否決的形式，§8 處理。
- **CLI 名字每次啟動都換、ref 不變**：Nexen probe §4（`addr-97` → `addr-58`，同 ref）；ref＝`RefID(sessionId)`，`--resume` 不變（`internal/peers/ref.go`）。CC 預設名＝`<cwd basename>-<2 hex>`（peer-address-v2 spec §1）。
- **Purdex 沒有執行體 ↔ peer 地址的對應**：`internal/peers`、`internal/module/peers` 沒有任何 execution／nex 引用；睡著的執行體沒有行程、沒有 registry 檔，`pdx peers` 不列；醒著時以一列 `row_kind:"entry"`、每輪換名的 cc 列出現。SPA `types.ts` 的 `peer_address` 註明「沒有 daemon 會送」。
- **地址組成**：`applyIdentity`（`internal/peers/record.go`）＝`alias/<registry name>`（routable 時）否則 `alias/_<ref>`；Resolve tier 1 比對 `Agent.PeerName`（`address.go`）；遠端列由寄件端用 `remoteAddress`（`internal/module/peers/module.go`）從對方的 `Agent.PeerName`／`Ref` 重建。
- **接力 lineage**：team module 提供 `PreviousRefs`（session id → 前任 ref 清單），Resolve 的 lineage tier 讓舊 ref 繼續有效（lead-team-relay spec §8.4，U3「定址一律用 ref、由 Purdex 讓 ref 繼續有效」）。
- **registry 名稱已有一張表**：`conversation_names`（session id → 最近一次看到的 registry 名，worker-test-tab spec §4.1），在 peer inventory 週期寫入；用途是標題退路，不參與定址。
- **inventory 是即時建的**：`localEnvelope` 每次 `GET /api/peers`／送訊時建，沒有背景刷新。
- **設定**：`[nex]` 由程式碼組 `nexconfig.Config`（`internal/module/nex/build_config.go`）；`PUT /api/config` **整段取代** `[nex]`，SPA `NexConfigForm.trimForSubmit` 只送 7 個鍵 ⇒ 新欄位不一起送就會被清掉。
- **SPA**：reducer 對未知 kind 直接把 payload 塞進 `messages`（`event-reducer.ts`），`peer_message` 不會開 turn；即時畫面的使用者那行來自 reducer 合成的泡泡（CC 不回放頂層 prompt）；站台級訂閱是白名單 `SITE_STREAM_KINDS`；`PreludeNote` 已有 `peer_message` 來源的樣式；設計文件 §14 提案 `UserMessage.source:"peer"`、設計稿 `ublock peer`（「來自 X · 時間」、左邊色線）。

## 3. 虛擬地址（Peer Address v5）

### 3.1 形式

- 名字＝`<基底>-<後綴>`。後綴＝ref 去掉底線後的**前兩碼**（`_k3m9qz` → `k3`）。基底取自 §3.2，先檢查字元形狀（`^[a-z0-9][a-z0-9-]*$`），**再**從尾端截短使整個名字 ≤ 64 字元（截短後去掉結尾的 `-`），最後才驗證整個名字。
- 結果必須通過 `RoutableName`（`^[a-z0-9][a-z0-9-]{1,63}$`、不是剛好 6 碼 base36）；含 `-` 的名字不可能是 6 碼 base36，所以只要基底合格就合格。
- 基底取不到合格值時**不配名**，地址維持 `alias/_<ref>`（與今天 unroutable 名字的行為一致）。
- 顯示：`<host>/<虛擬名> [<ref>]`，例如 `mlab/purdex-54-k3 [k3m9qz]`。

### 3.2 配發（每個對話一次，之後不變）

- 新表（meta DB，`CREATE TABLE IF NOT EXISTS`，Alpha 不做 migration）：

```sql
CREATE TABLE IF NOT EXISTS peer_names (
  session_id  TEXT PRIMARY KEY,  -- lowercase UUID
  ref         TEXT NOT NULL,     -- RefID(session_id)，給接力繼承用 ref 反查
  name        TEXT NOT NULL,     -- 虛擬名，RoutableName
  source      TEXT NOT NULL,     -- lineage | registry | conversation_name | dir
  assigned_at INTEGER NOT NULL   -- Unix ms
);
CREATE INDEX IF NOT EXISTS peer_names_ref ON peer_names(ref);
```

- **配發時機**＝這個 session id 第一次被 daemon 看到：inventory 建列時（終端機的 live entry、執行體列）、送訊解析時。`INSERT … ON CONFLICT DO NOTHING`，已有就不動 ⇒ `/rename`、CLI 每次喚醒換名都不影響。
- **基底來源順序**：(1) 該 session 目前 live registry entry 的 routable 名；(2) `conversation_names` 記過的名字；(3) 執行體專用：工作目錄 basename 正規化（小寫、非 `[a-z0-9-]` 換成 `-`、去頭尾 `-`）。都沒有 ⇒ 不配名（§3.1）。
- **接力繼承**：一個 session 的 `PreviousRefs` 非空時，沿 lineage（新到舊）找最近一個已配名的前任，**沿用同一個名字**（後綴不重算，因此後綴與新 ref 可能不同，這是預期的）。手動 `/clear`（沒有 lineage）＝新對話，重新配發。
- **lineage 晚到時升級一次**：接力後的新 session 可能先出現在 registry、lineage 稍後才寫入，這段時間會先用其他來源配到名字。之後一旦看到 lineage 且前任有名字，就把這個 session 的名字**改成前任的名字一次**（`source` 改為 `lineage`）；`source = lineage` 的名字永遠不再改。這是「之後不變」的唯一例外。**晚到是常態而非罕見**：接力時新 session 通常先出現在 registry、lineage 之後才寫入，所以多數接力都會經過一次這個升級；升級不改 `assigned_at`（它決定 `ByRefs` 對共用 ref 的排序）。
- **同名**：兩個對話剛好配到同一個名字時不另外處理，照現有規則（同名→ `ambiguous` 409，附候選，請對方用 `[ref]` 或 `_ref`）。
- **失敗語意**：`conversation_names` 讀失敗或 `AdoptLineage` 失敗 ⇒ 該列本輪**無名**（地址退回 ref 形式），不發放未持久化的名字，下一輪再試。
- **有界**：inventory 以外的呼叫端（self verbs 如 whoami、`OriginResolver.originOf`）命名時用請求 ctx（originOf 無請求 ctx，用 `namerTimeout`＝1s 上限），且不在 `titleMu` 內做，避免名稱表慢拖住整個 titles 路徑。
- **刪除**：不刪（與 `conversation_names` 同生命期）。

### 3.3 定址

- Resolve tier 1（bare name）、合併式 `"<name> [<ref>]"` 的 name 核對：改比對**虛擬名**。registry 名**不再路由**。
- `_ref`、lineage、`tmux:<name>`、`cc:` 拒絕等其他 tier 不變。
- **遷移提示**：bare name 沒命中、但等於某個 live entry 的 registry 名時，`peer_not_found` 的 detail 附「你要找的可能是 `<alias>/<虛擬名>`」。
- **遠端列**：`PeerRecord` 新增 `name`（虛擬名，omitempty）。新舊版**不由 row 有沒有 `name` 推斷**，而由 envelope 的 `address_version`（≥ 5 ＝ v5 規則）明說；沒帶 `address_version` 的對方是舊版，退回現行規則（`Agent.PeerName`），混版本照常可送。

### 3.4 顯示與寄件者

- `address` 欄位改用虛擬名 ⇒ `pdx peers`、`pdx msg whoami`、SPA 的 peers 清單、送出成功訊息 `sent … → <address>` 自動跟著換。
- 直接拿 registry 名當地址顯示或複製的地方要一起改：SPA 狀態列的地址與複製（`StatusBar.tsx`，資料來自 `usePeerStore.ts`）、改名彈窗（`RenamePopover.tsx`）、`cmd/pdx` 的 whoami 輸出。team／relay／lead 通知的地址經 `OriginResolver`，跟著 §3.2 一起變。
- 送給終端機的 frame，`from-name`＝寄件者的虛擬地址（本來就取 origin 的 address）。
- 專案 `CLAUDE.md` 的「Peer addresses」一節同步改寫（`<name>` 改為虛擬名、registry 名不再路由）。使用者全域 `~/.claude/CLAUDE.md` 也有一句「地址會隨改名變動」，由使用者決定是否改，Purdex 不動。

## 4. 執行體收訊（本機）

### 4.1 執行體列

- 對每個**未結束、未封存**、有 session id（`SessionID`，尚未回報時用 `ResumeSessionID`）的執行體，inventory 多一列：`row_kind:"execution"`、`ref`＝`RefID(session id)`、`address`＝`alias/<虛擬名>`（§3）、`execution_id`、狀態（idle／running）、標題。
- **一個 session id 只有一列**，優先序：tmux session 列 ＞ 執行體列 ＞ entry 列。
  - headless 的 live entry（執行體這一輪的行程）併進執行體列，不另列；它的 pid 掛到執行體列上。
  - 同一個 session id 若出現在 tmux session 列（對話已被拿回終端機、執行體正在退出），以終端機為準，不列執行體列。
- **清單要完整**：Nexen 的 `List` 是分頁的，要走完所有頁（沿用 `conversations_http.go` 的完整遍歷與重複 cursor 防護）。中途失敗 ⇒ 這一輪不列任何執行體列，並標記 `executions_unavailable`；此時地址沒命中回 `not_ready`（503），不回 `peer_not_found`（比照 `lineage_unavailable`）。
- `deliverable`：本機 Nexen 的 `capabilities.peer_message` 存在 ⇒ true；否則 false，reason `mailbox_disabled`。
- nex module 透過 service registry 提供這份清單與投遞入口；peers module 在請求時**選擇性**取用（nex 沒開就沒有執行體列，peers 照常運作），不加硬依賴。

### 4.2 最後一跳

- Resolve 命中執行體列 ⇒ 呼叫 Nexen `PeerMessage`（in-process），**不寫 socket、不先 interrupt**：

| Nexen 欄位 | 來源 |
|---|---|
| `from_name` | 寄件者的虛擬地址（`mlab/purdex-54-k3`；沒配名時 `mlab/_k3m9qz`） |
| `reply_to` | 同上 |
| `from_mode` | 寄件端宣告：`bypass` → `bypass`，其他 → `prompting` |
| `msg_id` | pdx 這則的 msg_id（UUID，符合 shell word） |
| `text` | 原文 |

- 結果對應：`delivered` → `delivered`；`queued` → **新的結果值 `queued`**（CLI 顯示 `sent … → <address> (queued, …)`）。
- 錯誤對應（in-process 呼叫拿到的是 Nexen 的 error 值，依 `errors.Is` 判斷；detail 的代碼是 Purdex 自己定的固定字串，名稱比照 Nexen HTTP 碼）：

| Nexen 錯誤 | HTTP | pdx 碼 | detail |
|---|---|---|---|
| `ErrPeerDisabled` | 409 | `not_deliverable` | `peer_messages_disabled`：這台主機沒開 peer 信箱 |
| `ErrPeerQueueFull` | 429 | **新碼 `queue_full`** | 排隊已滿 |
| `ErrPeerInvalid`、`store.ErrInvalidMessageText` | 400 | `bad_request` | `peer_message_invalid`／`message_text_invalid` |
| 執行體已結束／已封存／不存在（列出後才變的競態） | 409 | `target_gone` | `execution_terminal`／`execution_archived`／`execution_not_found` |
| credential 解析失敗 | 502 | **新碼 `mailbox_error`** | `credential_unavailable` |
| `ErrTurnFailedToLaunch`／`ErrTurnStalled`（turn 已建立） | 502 | `mailbox_error` | `turn_failed_to_launch`／`turn_stalled`，附 turn id |
| 其他 | 502 | `mailbox_error` | `internal` |

- 稽核：照現行 `peer_messages` 寫 `out` 列與結果；Nexen 回的 turn id（成功或上表最後兩列）寫進既有的 `native_msg_id` 欄。
- **自己送自己**：寄件 origin 的 session id 等於目標執行體的 session id ⇒ 400 `self_target`。
- 不設額度（U2）；每則一個 turn、不合併（U3，Nexen 預設行為）；Nexen 的 `max_pending` 是唯一上限。

## 5. 跨主機

- 寄件端照現行流程抓對方 `GET /api/peers`，對方的執行體列一起進解析（地址用 §3.3 的 `name`）。
- `DeliverRequest.To` 加 `execution_id`。收件 daemon 的 `/api/peers/deliver` 命中執行體列 ⇒ 走 §4.2 的最後一跳；`from_name`／`reply_to`＝收件端對寄件主機的 alias＋寄件者虛擬名（`<alias>/<name>`），`from_mode` 照現行規則夾成 `prompting`。
- 對方是舊版（沒有執行體列）⇒ 解析不到，回 `peer_not_found`，不另做相容。

## 6. `[nex.peer]` 設定

```toml
[nex.peer]
  enabled      = true   # U4：預設開
  max_pending  = 0      # 0 ＝ Nexen 預設（32）
  wake_template = ""    # "" ＝ Nexen 預設
  reply_line    = ""    # "" ＝ Nexen 預設
```

- `NexConfig` 加 `Peer` 子結構；`DefaultNexConfig` 的 `Peer.Enabled = true`；`Equal`、status 的 effective 一起補。
- **驗證**：`max_pending` 不可為負；樣板用 Nexen 自己的驗證（`PeerConfig.Build`）**在載入與 `PUT` 時就擋**，不能等到下次啟動 Assemble 才失敗（壞樣板會讓 nex module 起不來）。錯誤訊息照慣例以 `nex.peer.<key>:` 開頭。
- `buildOptions` 映射到 `cfg.Peer`。改了照現行規則顯示「需要重啟」。
- **設定頁**（主機 → Nex）：加「peer 信箱」開關與「排隊上限」；兩個樣板不在 UI 編輯，但 **PUT 時原樣帶回**，不會被清掉。`NexConfigForm` 的鍵集合測試同步更新。

## 7. 指揮台顯示

- reducer：`peer_message` **開一個 turn**（同 `message_accepted` 的 turn 起點、`turnLive`、`turn_id`），但**不動**這個 pane 自己的 `pendingLocal`／`sendLocked`（那不是使用者的送出）；不把 raw payload 塞進 `messages`，改放一則合成的 peer 訊息（`from_name`、`text`、`msg_id`、`at`）。
- 顯示：新的 **peer 訊息元件**，比照設計稿 `ublock peer`：左邊色線、標「來自 `<from_name>` · 時間」、內文照 agent prose 畫。這個元件之後也給終端機底層的原生 peer 訊息用（U1 開工時接上），兩種底層長一樣。
- 喚醒那一段：一列像工具呼叫的「由 peer 訊息喚醒」（不用單獨的「喚醒」：接力功能已用掉這個詞），預設收合，點開看實際送給模型的喚醒內容。**資料形狀依 §8 的結果決定**，所以這部分排在最後一個 PR。
- 站台級：`SITE_STREAM_KINDS` 加 `peer_message`（只讓清單在 peer 喚醒時刷新；站台級事件內容已剝除，**不**進 pane 的 reducer、不開 turn）。
- `NexCapabilities` 型別加 `peer_message`。
- 測試資料：從 nexen PR #161 複製真實樣本（`event-peer-message.scoped.json`、`.sitewide.sse`、`transcript-user-line.jsonl`）進 `spa/src/lib/nex/__fixtures__/`，reducer 與畫面測試用真樣本。

## 8. 喚醒方式（U9）— 依賴 Nexen 驗證

- 已請 nexen-74 在 7802 測試實例驗證兩段式：短 prompt 喚醒 → 把 peer 訊息用 pdx frame 寫進 CLI socket，時機（a）socket 一出現、喚醒那輪 result 之前（b）result 一出來；from-mode bypass／prompting；觀察是否被處理、行程是否等第二輪跑完才退出、Nexen 怎麼記帳第二個 result、背景 subagent 是否受影響。
- 已知限制條件（實測）：Nexen 在 result 時關 stdin，行程 0.3–0.5 s 內退出；回合中送進 socket 的訊息在 stdin 開著時會排成下一輪，stdin 關閉下未驗證；bypass 收件端收到 `prompting` 會被扣最多 5 分鐘。
- **建議做法**：兩段式做在 Nexen 內部，**pdx 的契約不變**（照樣 `POST …/peer-messages`）；Nexen 自己掌握行程生命週期，負責「短喚醒 → 等 socket → 寫 frame → 記帳第二輪」，喚醒 prompt 仍參數化。如此 §3–§6 不受驗證結果影響，只有 §7 的喚醒列資料形狀要跟著定。
- **驗證結果（nexen-74，2026-10-08，7802、claude 2.1.292、bypass 執行體，各 3 次；原始資料 nexen `docs/research/2026-10-08-two-stage-wake-matrix.jsonl`）**：

| 時機 | from-mode | 結果 |
|---|---|---|
| (a) socket 一出現就寫（喚醒那輪還在跑） | bypass | **3/3 成功**：第二個 result 帶 `origin.kind=peer`、transcript 是 CLI 原生格式（`isMeta`＋origin）、模型照回信指示用 `pdx msg send`；行程等第二輪跑完才退出；背景子代理不受影響 |
| (a) | prompting | 0/3：被 CLI 扣住，約 2 秒後行程退出，訊息消失，寄件端仍以為送達 |
| (b) 喚醒那輪 result 之後才寫 | 兩者 | 0/3：socket 已消失，或寫入成功但從未被處理 |

  - 時序：送出喚醒 prompt 後 0.7–2.4 秒 socket 就緒；result 後 0.5–0.8 秒行程退出。
  - 結論：**只有「(a)＋bypass」可用**。限制：必須在喚醒那輪 result 前寫入（競態，模型越快窗口越小）；frame 必須標 bypass（等於由 Nexen／pdx 決定放行，CLI 不驗證）；**沒有投遞回執**，只能等第二個 result 的 `origin.msg_id` 確認，逾時要當失敗並 fallback；Nexen 的記帳要改（現況第二個 result 併進喚醒那一輪、沒有 `peer_message` 事件）。
  - nexen-74 建議：兩段式做成 config 開關，**保留 v0.20.0 的內嵌路徑當 fallback**。
- **待使用者決定**是否採用（含上述限制）。不採用時維持 v0.20.0 的單輪形式（畫面上照樣把 Nexen 包裝收進「由 peer 訊息喚醒」那一列，peer 原文用 peer 元件畫）。

## 9. 不做

- 多則合併喚醒（U3）、喚醒額度（U2）。
- 指揮台的終端機底層（U1 分期）——本 spec 只做元件，終端機底層接上時沿用。
- Codex（另一條通訊線）。
- prelude 裡 Nexen 包裝過的 peer 行（交接前對話裡出現的機率低，沿用現有 prelude 規則）。
- 改使用者全域 `~/.claude/CLAUDE.md`。

## 10. 測試重點

- 虛擬名：後綴＝ref 前兩碼；基底三種來源順序；截短到 64；不合格基底不配名；配發一次後 `/rename`、換名都不變；接力繼承；手動 `/clear` 新配；同名 → ambiguous；registry 名不再路由＋遷移提示；遠端舊版退回。
- 執行體列：睡著／醒著都只有一列；併列；未開信箱 → `mailbox_disabled`；nex 沒開時 peers 照常。
- 最後一跳：欄位對應表逐欄；`delivered`／`queued`；四類錯誤對應；自己送自己；不呼叫 interrupt（測試要能證明沒呼叫）；稽核列。
- 跨主機：執行體列經 `name` 解析；收件端走信箱；from/reply 用收件端 alias。
- 設定：預設開；壞樣板在 PUT 被擋；PUT 原樣帶回樣板；`Equal` 觸發需要重啟。
- SPA：真樣本驅動；peer turn 開 turn 但不清使用者的送出狀態；不再塞 raw payload；元件長相；站台級白名單；切 tab 再切回（Tab-hosted 檢查清單）。
- mutation test 是交付項目：每個 PR 至少對核心判斷各做一組，證明測試會紅。

## 11. 切 PR（一個 PR ≤ 800 行 diff 或 ≤ 20 檔）

| PR | 內容 | 依賴 |
|---|---|---|
| P1 | `[nex.peer]` 設定（daemon＋設定頁） | — |
| P2 | SPA：`peer_message` 開 turn、peer 訊息元件、站台級白名單、型別、真樣本 | — |
| P3 | 虛擬地址 v5：`peer_names` 表、配發、定址、遠端 `name`、遷移提示、`CLAUDE.md` | — |
| P4 | 執行體列＋本機最後一跳（含錯誤對應、稽核） | P1、P3 |
| P5 | 跨主機送執行體 | P4 |
| P6 | 「由 peer 訊息喚醒」收合列 | P2、§8 結果 |

- P1、P2、P3 互不依賴，可平行。第一次部署在 P4 之後（端到端可用）；P5、P6 各自部署。
- P3 若超過門檻，拆成 P3a（表＋配發＋顯示）與 P3b（定址＋遷移提示＋遠端）。
