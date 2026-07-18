# 三套系統架構定位討論(MM / Ploom / Purdex)+ 執行模型 + MM bridge

> 討論脈絡快照,2026-07-18。從「Ploom↔Purdex 派工整合」延伸到「三套系統的職責切分 + agent 執行模型 + MM 即時對話 bridge」。尚未進 spec,brainstorming 收斂中。
> 前置脈絡見同目錄 `../pages/index.html`(P0-P5 派工流程 + 業界 7 產品研究 + Flycoder 實例 + deeplink 三段式落點)。

---

## 0. 使用者情境(硬約束)

- **單人自用**,目前無其他使用者(MM 團隊含自己共 2 人)。
- 主力工作習慣:目前直接在 **Purdex** 開 Claude Code session 幹活,但**感覺有極限/瓶頸**。
- 三套系統並存,其中 **MM 與 Ploom 的職責邊界**是使用者最大的困惑點。

---

## 1. 三套系統

| 系統 | 定位 | 現況 |
|---|---|---|
| **Mattermost (MM)** | 2 人團隊人際 + **助理型角色 bots 住的地方** | 現成聊天工具;bots = 使用者的 **hermes / ark-bridge**(導演路由 + 委派擬人角色)。**MM 已確認是「常駐角色/助理型」,不是 coding 任務 agent** |
| **Ploom** | **issue 管理**(+ 預計 wiki + 檔案同步);非同步派工、狀態驅動、自動 trigger/定時掃描 issue 自發派工、資訊沉澱進 issue、改狀態 | 自建 Go+React;S1-S5 已 merge(auth/org/專案導入/issue+board+kanban/view+分享/外部整合);v1 Web UI 缺口未補;S6 Purdex 對接 PARKED |
| **Purdex** | **terminal tool**(執行);tmux session 遠端管理,Terminal / Stream(`claude -p` 串流) / JSONL 三模式,多 host,Editor,workspace snapshot | Go daemon + React SPA + Electron tray app;使用者目前主力 |

---

## 2. 關鍵洞察演進(依討論順序)

### 2.1 Flycoder / Custie 實例(台灣開發者蔣定宇公開分享)
- 把 Slack 改造成 AI 工作台半年,結論「**方向對了,房子錯了**」:Slack 為「人跟人聊天」蓋,AI 進來「只能睡沙發」(沒有 activity steps / 喊停 / skill / 多 agent / subagent 這些一等概念)。於是「乾脆從頭蓋一間」= Flyspace/Flycoder。
- Flycoder 完整形態(截圖):**channel = 看板;每個 thread = 一張卡片 = 一張工單 = 一個 agent 工作空間**。status 欄位(Backlog/To Do/In Progress/In Review/Done)。「**討論串本來就是工單**」「每個討論串裡本來就住著一個 AI agent,每張工單可以直接叫它開工」。
- 洩露計價:一張卡「這次任務超過了 **$5 的單次花費上限**被中止」→ 走 **API token 計價**(有成本上限),不是訂閱。
- 意義:Flycoder 是「即時對話 + issue/看板 + 執行」揉成**一個 monolith**;正好驗證了 Ploom 的核心賭注(issue-first、統一、不聚合)成立。

### 2.2 業界研究(7 產品,已上網查證)
Claude Code in Slack / Devin / Cursor bg agents / Copilot / Codex / Factory / Amp。共識:thread(≙issue)=控制面+通知面(摘要/狀態badge/按鈕放 thread,plan/diff/transcript 放執行端用 deeplink 導流);deeplink=穩定 handle 非一次性連結;狀態用輕量 badge;**blocked-on-human 是公認缺口**;issue:session 別寫死 1:1;痛點=prompt injection/長 thread 漂移/挑錯 repo/通知疲勞。

### 2.3 執行模型:`-p` vs Agent SDK vs Managed Agents(已上網查證官方)
- **官方(code.claude.com/docs/en/agent-sdk):Agent SDK 與 Claude Code CLI 是「same capabilities, different interface」**。SDK 底層就是 Claude Code 引擎。「其他語言,run the CLI programmatically with `-p` and `--output-format json`」。
- **`claude -p --output-format stream-json --verbose`** 吐結構化事件(每個 tool_use/tool_result 一條 JSON;`--include-partial-messages` 給 token delta)。**這就是 activity steps 的來源。** `--input-format stream-json` 開**雙向串流**,協定 capability 有 `interrupt_receipt_v1` → **headless 原生支援中斷(喊停)**。subagent 訊息帶 `parent_tool_use_id` 可追蹤歸屬。
- **修正先前誤判**:CLI 模式的 activity **不需要反解 tmux**;`-p stream-json`(Purdex 的 Stream 模式)本來就吐結構化事件。反解 tmux 只有 Terminal 模式(互動 TUI)才需要。
- **對 Purdex 關鍵**:Agent SDK 只有 Python/TS,**Purdex daemon 是 Go → 沒有「接 SDK」的直接選項**,官方明說其他語言用 `-p`。所以走 `-p stream-json` **不是妥協,是 Go 生態下的官方正解**,能力(activity/interrupt/subagent)不輸 SDK。SDK 相對 `-p` 只多「程式化封裝」(query()/hooks/AgentDefinition/session resume),不是能力差異。

### 2.4 計價(已上網查證官方 support.claude.com)
- Agent SDK / `claude -p` / 第三方 app **個人自用可走 Claude 訂閱**認證。原訂 2026-06-15 把 programmatic 用量改成獨立月度 credit pool(Max 5x=$100/Max 20x=$200,用完流 API rates),**但此變動已宣布暫停**,目前仍吃訂閱 usage limits。
- 設 `ANTHROPIC_API_KEY` 覆蓋訂閱,走標準 API 計價。
- **硬限制**:OAuth 訂閱憑證是**個人使用**授權;**多用戶/對外服務不可用訂閱憑證**(違條款+rate limit 秒爆),官方要求用 API key。
- 使用者是**單人自用** → 走訂閱完全合規。**Purdex 現況(tmux 跑 `claude -p`)天生走使用者機器的訂閱,零 API token 成本** —— 這是相對 Flycoder(走 API token)的成本優勢。

### 2.5 tmux 的定位重估
- Purdex 用 tmux 的價值拆解:**(a) 進程存活監管**(daemon 掛了 agent 還在)、**(b) 人 attach 逃生艙**(卡住時跳進終端手動接管)、**(c) context 續接**(跑到一半斷了能接)。
- 其中 **(c) 靠 Claude Code `--resume <session_id>` 就有,不需要 tmux**;**(a) daemon 可自己做進程監管或用薄 tmux 只當進程容器**;**(b) 是 tmux 唯一不可替代的**,但走 Ploom/MM-first(卡住時在對話裡救 agent 而非跳終端)後需求也淡。
- 結論:**tmux 從「一等主體」降為「可選的進程容器 / 逃生艙」**;大部分情況 `-p` runner 就夠。

---

## 3. 架構收斂

### 3.1 切分軸修正:不是「即時 vs 非同步」,是「常駐角色 vs 任務執行」
使用者原本用「即時(MM) vs 非同步(Ploom)」區分兩者,導致難切。換軸後清晰:MM 的 bots 是**常駐角色/助理**(找「誰」聊),Ploom 的 agent 是**任務執行工人**(把「什麼事」交出去、做完回報)。是兩種**不同物種**的 agent,不搶位置。

### 3.2 三層定位
```
人際/通知層:   Mattermost —— 2 人人際 + 常駐角色 bots(hermes)。不承載 coding 任務 agent。
工作組織/協作層:Ploom —— 任務 agent 的 issue 派工 + 狀態/kanban + wiki + 檔案。即時&非同步都在這。
執行層:        Purdex / `-p` runner —— 任務 agent 實際跑的地方,被 Ploom 調用。
```
- 使用者瓶頸診斷:直接在 Purdex 開 session 幹活會撞牆,因為**跳過了 Ploom(任務化/組織/沉澱那層)** —— session 瞬時、做完就散、無 backlog/狀態/非同步。補上 Ploom 即解。

### 3.3 單向動線(避免「聊完抄進 Jira」的荒謬)
```
MM 跟角色 bot 聊 → 聊出一件要做的事
   └─ 一鍵開成 Ploom issue(單向,對話 context 帶過去)
Ploom issue → 派工 → Purdex `-p` runner 執行 → 回報狀態進 issue
```

### 3.4 Purdex 執行端定位岔路(待使用者最終拍板)
- **(甲) 薄 `-p` runner**:daemon spawn `claude -p stream-json` + 長連線回報/受控,協作介面全交上層。tmux 降為可選逃生艙。Purdex 大幅簡化。**使用者傾向此。**
- **(乙) 保留 tmux + 完善 Stream UI**:Purdex 自己也是完整 agent 控制台,上層用 deeplink 導流。有協作 UI 重疊稅,但能獨立自用 + attach 逃生艙。
- 註:若走(甲),原本在討論的 **deeplink(跳回 Purdex App 看)價值下降** —— activity 已即時 stream 到上層,deeplink 退成「逃生艙入口」。

---

## 4. MM bridge 構想(最新,本次討論重點)

**需求**:在三層模式下,想「直接跟 Ploom 某個 issue 的執行 agent/subagent 即時對話」,把它**拉進 MM thread**。MM 當即時窗口,agent 的家仍在 Ploom/Purdex(不讓 MM 養 coding agent、不讓 Ploom 自建即時對話 UI,各取所長)。

**使用者提的兩方案**:
1. **固定 bot 角色**:一個 MM bot,在不同 thread 接線不同 Ploom issue agent。
2. **Proxy 代理**:中間一個 proxy,MM ↔ Ploom issue agent 兩邊傳訊,不需要真正的 bot instance。

**分析(把兩案看穿)**:兩案的 bridge 核心相同(訂閱兩邊+雙向轉發+維護 thread↔issue 映射),真正差別只有兩件事:
- **(a) MM 側呈現身分**:方案1=**Bot account**(所有 agent 以一個 bot 名義說話,thread 區分);方案2=**Incoming webhook**(per-message 覆寫 username/頭像,每個 issue agent 以自己名字出現,無常駐 bot user)。「不需要真 bot instance」→ webhook 做得到。
- **(b) 路由狀態放哪**:proxy 收 MM 訊息要知道轉給哪個 issue agent。映射狀態**存在 Ploom**(issue 加 `mm_thread_id` 綁定欄位)最乾淨 → **bridge 可完全無狀態**,靠讀 Ploom 綁定路由。

**現成地基**:使用者的 **hermes / ark-bridge** 本來就是「MM 裡橋接對話到後端 agent + 導演路由/擬人角色」。把 Ploom issue agent 當成 ark-bridge 可接線的一種新 agent 來源,方案1 幾乎就是 hermes 的導演路由再套一次。**可能不用從零造 bridge。**

**與執行層咬合**:
```
MM thread(人即時打字)
   ↕  bridge(無狀態,路由狀態存 Ploom issue.mm_thread_id)
Ploom issue ── 派工 ──▶ Purdex `-p` runner
                         └─ stream-json events ──▶ bridge ──▶ 貼回 MM thread
   人在 MM 打的話 ──▶ bridge ──▶ 注入 -p 的 stdin(--input-format stream-json)
```
即時對話走 `-p` = 走使用者訂閱,**零 API token 成本**。

**目前傾向**:無狀態 bridge + 路由狀態存 Ploom(issue.mm_thread_id) + MM 用 webhook 動態身分(方案2 精神);固定 bot 保留給**控制入口**(你 @ 它說「把 issue #42 接進這 thread」,它建綁定),agent 本人訊息走 webhook。兩者混用。

**待釘的關鍵設計問題**:
- **接線是一次性還是常駐?** 拉 issue agent 進 MM thread 後,是「即時對話完就斷、agent 回 Ploom 繼續非同步」,還是「thread 從此常駐綁定 issue,隨時回來接著聊」?這決定 bridge 要不要處理斷線重連、agent session 要不要一直活著(還是靠 `--resume` 重新接)。

---

## 5. 給 Codex 的問題(輔助思考)

1. **三層切分(常駐角色 MM / 任務 agent Ploom / 執行 Purdex)合理嗎?** 有沒有更好的定位框架、或我沒看到的重疊/盲點?尤其「常駐角色 vs 任務執行」這個切分軸,會不會在某些場景崩掉(例如一個任務 agent 需要長期常駐,或一個角色 bot 需要執行任務)?

2. **Purdex 走薄 `-p` runner + tmux 降為逃生艙,這個簡化對嗎?** 風險在哪?(進程監管、斷線、多 host、workspace snapshot 這些現有能力會不會被犧牲?)`--resume` 當 context 續接方案夠穩健嗎?

3. **MM bridge 設計(無狀態 bridge + 路由狀態存 Ploom + webhook 動態身分 + 固定控制 bot)有盲點嗎?** 尤其:
   - 「接線一次性 vs 常駐」該怎麼定最穩健?各自的取捨?
   - 斷線重連、agent session 生命週期、多個 MM thread 同時接不同 issue agent 的並發,怎麼處理?
   - 訊息路由的 race / 錯接 / 安全(MM 訊息注入 agent stdin = prompt injection 面?)
   - 把 hermes/ark-bridge 當地基是好主意,還是會被它既有的角色路由假設綁住?

4. **YAGNI / 過度工程檢查**:這整套(三系統 + bridge + `-p` runner + Ploom 派工)對一個**單人自用**的場景,是不是過度?有沒有更簡單的路達到同樣目的?(例如:是不是根本不需要 MM bridge,直接在 Ploom 裡做即時對話就好?或根本不需要 Ploom,Purdex + 一個 issue 清單就夠?)請直言。

5. **框架性錯誤**:有沒有我從裡面看不到的方向性錯誤或隱藏假設?

---

## 6. Codex 第二輪諮詢回應(2026-07-18,job task-mrq62auh-1q0sen)

> 主 session 幾乎全盤認同。以下為 codex 逐題結論。

**總結**:三層產品定位可成立,但少的不是再多一層,是更硬的「控制權模型」。不把 `issue/dispatch/execution/session` 分開,MM bridge 會把三層重新糊回一團。「MM 不承載 coding agent」太絕對——它不該持有執行權,但一旦能即時對話,它就已經是控制面的一部分。

1. **三層可用,但「常駐角色 vs 任務執行」單一軸不夠,壓力一來就崩。** 真正決定穩不穩的是另一條軸:**誰有 runtime 控制權**。會崩場景:① 長期常駐 steward agent(有人格也派工)② 一 issue 對多 execution/多 subagent/多 resume ③ 人同時從 MM 與 Ploom 回 blocked-on-human。→ 三層 UI 留,**資料模型一定要拆 `issue/dispatch/execution/session`**(session=runtime stdin/stdout 生命週期,execution=一次可追蹤工作單元,別混)。

2. **薄 `-p` runner 對,但 tmux 別因 `--resume` 判死刑。** `--resume` 只解模型對話續接,不解整個 terminal process tree(背景 job/watch mode/互動 editor/半跑 pipeline)。tmux 剩的價值＝保住「非 Claude 子進程 runtime 現場」。現況程式已 session-centric(stream bridge 用 `session_code` 當主鍵、單 relay 多 subscriber,`handler.go:20`)適合薄 runner,但**外部契約不能再暴露 `session_code` 當穩定識別**。定調:預設薄 runner + tmux 為 feature-flagged escape hatch。

3. **MM bridge 盲點:你叫它「無狀態」但一定有狀態。** `thread_id→issue_id` 可存 Ploom,但互動控制還需 **interactive lease**(誰有 stdin 寫入權)。預設模型:**thread 綁 issue 常駐、互動接線短租(可過期/顯式 detach)**,別讓整條 thread 永久綁 live session。並發硬規則:一 execution 同時一 interactive controller、多 observer 唯讀(⚠️ 現況 stream subscribe 多 subscriber 且每個都能寫回 relay stdin,`handler.go:92`＝**現成 race**)。斷線靠 state machine(`detached/attaching/attached/stale/terminated`),每則 MM→agent 訊息帶 `execution_id+lease_id+seq` 做 idempotency。**prompt injection 不能用單人自用當藉口**:MM thread 最髒(混人話/貼文/quote/連結 preview/bot 回覆),別 raw forward 整串,控制命令(stop/resume/attach)走控制通道。hermes/ark-bridge 只重用 transport/auth/webhook plumbing,**別重用其導演式角色路由腦**,bridge 要 dumb adapter(不選 agent/不改寫意圖/不加人格)。

4. **YAGNI:對單人自用現在就是過度工程,第一刀砍 MM bridge。** 核心問題「Purdex session 太瞬時、缺 work organization」是 **Ploom 解,不是 MM bridge 解**。砍法:先只保留 `Ploom + Purdex`,MM 只做建 issue + 收摘要通知,不做 agent 拉進 thread。更狠:Ploom 簡化成最小 issue/dispatch UI,wiki/檔案同步/MM bridge 全延後——單人最值錢的是「把 work item 與 execution 對上」,不是把對話面做滿。**不建議先砍 Purdex**:本機訂閱執行、terminal 接管、workspace snapshot 是稀缺能力(成本與主權來源)。

5. **五個框架錯誤/隱藏假設**:① 把「產品分層」誤當「資料/控制分層」② 假設單人=低並發(錯:execution/subagent/resume/watcher 並發一樣高)③ 假設 stable handle 可晚點抽象(現況多處仍 `sessionCode/hostId` 思維,`electron/main.ts:65` 通知 payload 也是,外部契約不先抽 `execution_id` 之後難翻)④ 假設 bridge 是中性傳輸層(錯:MM thread 進 agent 上下文語義被改寫,bridge 本身就是 prompt boundary)⑤ 假設零 API 成本=便宜(付的是 rate limit/訂閱政策波動/多執行體並發不可預測)。

**Codex 拍板順序**:先釘死 `issue/dispatch/execution/session` 模型 → 做薄 `-p` runner → 最後才決定要不要 MM bridge。現在先做 bridge 風險最高。

---

## 7. 當前停頓點(重啟後接續)

停在**等使用者拍板兩點**:
1. 接受「MM bridge 先延後、先做 Ploom + Purdex 核心,MM 暫時只做建 issue + 收通知」?
2. 接受把「先釘死 `issue/dispatch/execution/session` 資料模型」當下一個實質工作?

主 session 傾向兩點都接受(codex 兩輪 + YAGNI 一致)。定了才開工,不重跑兔子洞。
