# Purdex — PRODUCT.md

> 本文件定錨 Purdex 的產品定位、核心概念、設計法則與長期方向。
> 所有 IA / 視覺 / 互動決策應以本文件為依據；本文件未涵蓋之事，回到本文件討論增補。
> Register: **product**（design serves the product）

---

## 1. Vision

Purdex 是 dev/ops 工作區的**人與 agent 協作平台**。

- 使用者在 workspace 內與多個 terminal agent 並行協作；
- Agent 模式為 **terminal / stream(wrap) / agent(popup 對話)** 三種；
- 任意 agent 皆可被賦予 **center 角色**，協助跨 agent 的雜項管理與指揮（長期方向）。

Purdex 不是 IDE、不是 terminal app、不是 dashboard — 是上述三者交集處的 **agent 工作站**。

---

## 2. Users

- **主要對象**：Power-user developer、SRE、ops engineer
- **使用情境**：同時在多 workspace、多 host、多 task 並行作業
- **使用習慣**：願意把雜項委託 agent、但保留指揮權與可見度
- **使用環境**：本地 / 跨機（Tailnet）；桌面為主，移動端為輔（長期）

不是的對象：
- 非開發者 / 終端使用者 / 消費級 user
- 不熟 terminal / tmux / agent 概念的初學者

---

## 3. Core Concepts（嚴格詞彙表）

> 所有 UI 文案、文件、設定 key 都必須沿用本表詞彙；不得在不同地方混用同義詞。

### 3.1 Workspace

**廣義工作容器** — 任何 task 領域都算：軟體開發、寫作、學習、運維、研究、生活雜項。

- ❌ 不等於 project（避免窄化定位）
- ❌ 不等於 scene / 場景（不是 layout snapshot）
- ✅ 是「一個容器有自己的內容範圍」（agent、tab、settings、modules）

**並行性**：多個 workspace 可同時 alive；不強制 single active viewport。

### 3.2 Tab

Workspace 內的工作單位。

- 預設 **1 tab : 1 content**
- 可選 split 為 **1 tab : N pane**（power user 進階）
- Tab 是主操作單位 — 鍵盤快捷鍵與 chrome 設計圍繞 tab 展開

### 3.3 Pane

Tab 內可選分割。

- `Tab → PaneLayout tree → Pane → PaneContent`
- 預設關閉（不暗示「應該 split」）
- 進階使用者主動觸發

### 3.4 PaneContent

Pane 渲染的具體內容；discriminated union。

現有 kinds：`new-tab`、`tmux-session`、`dashboard`、`history`、`settings`、`browser`、`hosts`、`memory-monitor`、`editor`、`image-preview`、`pdf-preview`。

新增（與本文件配套）：`agent-popup`（對話模式，見 3.5）。

### 3.5 Agent

執行單元；具備兩個正交維度：

| 維度 | 值 | 說明 |
|---|---|---|
| **Mode** | `terminal` | 直接 tmux pty 操作 |
|  | `stream(wrap)` | 觀察 wrap 的執行流（如 Claude Code `-p` stream-json） |
|  | `agent(popup)` | 對話式互動（人 ↔ agent 主動對話） |
| **Role** | `worker` | 預設角色：執行使用者下達的工作 |
|  | `center`（長期方向） | 升級角色：多了跨 agent 訪問能力 + 角色定義；控制走 MCP / message inject |

**Mode × Role 正交** — UI 不為 Role 新建獨立 surface；Role 差異透過 agent 卡片角標 + 設定面板擴展欄位呈現。

### 3.6 Module

提供 Pane（≤1）+ Views（≥0）的功能擴展單位。

- 由 `module-registry.ts` 統一管理
- Module ID 與 PaneContent kind 可不同
- 範例：`session`、`cc`、`stream`、`agent`、`dev`、`files`、`editor`

### 3.7 Host

Daemon 部署位置；infrastructure 資源層級。

- 每台 daemon 只管自己主機上的資源
- Host 跨 workspace 共用（不屬於任一 workspace）
- Host 管理是獨立 PaneContent kind，不在 Settings 內

### 3.8 Layout

4-Region 配置：

- `primary-sidebar`（左外，全高）
- `primary-panel`（左內，工作區級）
- `secondary-panel`（右內）
- `secondary-sidebar`（右外）

每個 region 三種模式：`pinned` / `default` / `collapsed`。

---

## 4. Design Laws（設計憲法）

### Law 1 — 願景軸線是方向感與校驗器

> 願景不是 roadmap、不是 backlog — 是當下決策的方向感與校驗器。

**雙重檢驗**（硬性條件，每個 PR / 設計決策都要過）：

- (a) **是否解決當下問題？**
- (b) **是否在長期方向上能自然延伸？**

兩者都要 yes。任何當下決策若會**堵死**長期方向（見 §7），需重新討論。

### Law 2 — 不過度為未來建造

預留可能性 **≠** 提前建造 surface。

- ✅ 為 Center Agent role 在 agent metadata schema 留 capability / role 欄位
- ❌ 為 Center Agent 提前建 Voice Indicator / Mode Switcher / Activity Log surface
- ✅ 為未來 hands-off 模式在 Layout primitive 留 density mode 屬性
- ❌ 提前設計 dual visual language

### Law 3 — Workspace 是中性容器

文案、UI、設定 key、文件中**禁止**將 workspace 窄化為：
- ❌ project / 專案
- ❌ repo / 程式碼庫
- ❌ folder / 工作資料夾

✅ 統一稱「workspace / 工作區」。

### Law 4 — Progressive Complexity

預設路徑覆蓋 **80% 使用**，不強求理解進階概念。

| 層 | 路徑 | 受眾 |
|---|---|---|
| 1 | Workspace → Tab → 1 個 agent → 用 | 80% 使用者 |
| 2 | 多 workspace 並開 → 跨 workspace 跳轉 | 中階 |
| 3 | Tab split → 多 pane 並列 → layout 命名 | Power user |

進階能力存在但不顯示在預設 chrome；不暗示「應該 split / 應該開多 workspace」。

### Law 5 — Mode × Role 正交

Agent 的 Mode（呈現方式）與 Role（能力範圍）獨立演化。

- UI 不為 Role 新建獨立 surface
- Mode 決定 PaneContent 渲染
- Role 決定 agent 角標 + 設定欄位

### Law 6 — 一套視覺語言

Purdex 維持**一套**視覺語言；hands-off / mobile / wearable 等變體是這套語言的**降密度衍生**，不是另一套設計系統。

- ❌ Dual visual language（hands-on 桌面 vs hands-off CarPlay）
- ✅ Layout primitive 支援 density 屬性（預設 normal，預留 compact / hands-off）

### Law 7 — Surface 邊界明確

設定 UI 的歸位法則（避免「全域 settings 大頁」這種反面範例）：

| Surface | 內容 | 觸發 |
|---|---|---|
| **App Window**（獨立 window） | 跨所有 workspace 的偏好（鍵盤、theme、自動更新、telemetry、locale） | Cmd+, |
| **Workspace Library** | 所有 workspace 的 metadata 管理（卡片列表 + 新建 / 匯入 / 刪除） | App menu |
| **Workspace Sheet**（從 workspace 喚出） | per-workspace 設定（名稱、icon、顏色、預設 agent mode、啟用 modules、sync rules） | workspace tab ⋯ → Settings |
| **Pane Inspector**（pane focus 時側邊） | per-pane / per-agent 設定（agent mode、命令、env、homepath） | pane focus |
| **Host 管理** | 跨 workspace 的 host 資源（連線狀態、批次動作） | App menu / 獨立頁面 |

### Law 8 — Anti-References（不抄）

| 來源 | 不抄 | 原因 |
|---|---|---|
| Warp | Cloud Agents / Warp Drive 雲端 | self-host / Tailscale 路線 |
| Warp | AI 全域強制介入 | Purdex 的 AI 是「協作」不是「主導」 |
| Arc | 非常規 sidebar tab | dev tool 要可預測 |
| VS Code | 無限 settings 樹 | Module 系統可控制邊界 |
| Tabby | YAML 為主介面 | GUI 為主，YAML 只當 import/export |
| Linear | 重度依賴鍵盤 | 純滑鼠使用者也要好用 |
| 消費級工具 | 過度動畫、彈性曲線、emoji-heavy | Power user 要的是密度與克制 |

---

## 5. Reference Anchors（對標總圖）

### 現階段對標（直接借鑑）

| 領域 | 第一參考 | 第二參考 | 借什麼 |
|---|---|---|---|
| **設定視覺** | Linear Settings | macOS Sonoma System Settings | SettingsRow 統一格式、sticky header、克制色彩 |
| **Tab + Pane** | iTerm2 | WezTerm + VS Code editor splits | 預設 1:1、Cmd+D split、tab strip 簡潔 |
| **Multi-host** | Termius | Tailscale Admin + Tabby Profiles | host group + status badge + 批次動作 |
| **Stream block UI** | Warp Block UI | Linear comments | 可摺疊 block、tool/duration/token header |
| **命令面板** | Raycast | Linear Cmd+K + VS Code Cmd+P | 全域命令入口、取代多個現有入口 |
| **Agent popup 對話** | Cursor Composer | ChatGPT Desktop（風格） | 對話 surface 但不離開 workspace 上下文 |
| **Empty/Error/Loading** | Linear | Vercel | primitive 一致化 |
| **Status bar** | VS Code | Cursor AI status | host / connection / Lights / sync 整合 |

### 長期方向對標（方向感，不照抄）

| 方向 | 參考 | 借什麼 |
|---|---|---|
| Center Agent / 多 agent 編排 | Cursor Background Agents、Devin Multiplayer、Anthropic Computer Use | role upgrade 模型、commander view、自主行為審查 |
| Voice / Hands-off | ChatGPT Advanced Voice、Granola、Apple CarPlay | ambient listen、低延遲對話、降密度視覺 |
| Mission Control 隱喻 | Bloomberg Terminal、NASA MCC、Tesla Autopilot UI | 多 panel 密度、layout snapshot、信賴度視覺化 |
| 跨設備延伸 | Apple Continuity、Tesla 主螢幕 + 手機 | client-agnostic 資料模型 |

---

## 6. Non-Goals（明確不做，現階段）

- ❌ 不做雲端 SaaS（self-host / Tailscale 路線）
- ❌ 不做消費級 / 對非開發者
- ❌ 不和 VS Code 競爭編輯器深度（editor module 是「session context 內 quick edit」尺度）
- ❌ 不做拋棄視覺的純語音介面（Humane Pin 教訓）
- ❌ 不做 Vision Pro 級沉浸體驗
- ❌ 不做完全自主 AI（使用者必須能審查、中斷、接管）

---

## 7. Long-term Direction（願景軸線）

> 這些是 Purdex 的演化方向；現在不實作，但每個當下決策都要能往這方向自然延伸。
> 任何 PR / 設計決策若會堵死下列任一方向，需重新討論。

### 7.1 Center Agent role

**願景**：任何 agent 可被升級為 center role；多了跨 agent 訪問能力 + 角色定義；控制其他 agent 走 MCP / message inject（daemon 層）；SPA 端僅是「角標 + 設定多項」。

**SPA 端銜接點**：
- agent 卡片角標（區分 worker / center）
- agent 設定面板可擴展欄位（capability、訪問權限、角色 prompt）

**現在的預留方式**：
- agent metadata schema 為 `capability` / `role` 留位（不必立即用）
- 設定 IA 4 surface 中的 Pane Inspector 已支援動態欄位（不需重構即可加 role 設定）

### 7.2 Voice / Hands-off Engagement

**願景**：與 Center role 配套；使用者可語音指揮 center、center 操作其他 agent；hands-off 場景（開車、走路）下視覺降級為狀態板。

**SPA 端銜接點**：
- 一套視覺語言的「降密度變體」（不是全新設計系統）
- 既有 status bar / Lights / subagent dots 即可作為 hands-off 狀態源

**現在的預留方式**：
- Layout primitive 支援 density 屬性（預設 `normal`，預留 `compact` / `hands-off`）
- 不立即建 Voice Indicator / Mode Switcher / Authorization Panel 等專屬 surface

### 7.3 跨設備延伸（mobile / wearable companion）

**願景**：桌面是主、其他是次；mobile companion 提供語音對話與狀態接收；wearable 僅作為通知層。

**SPA 端銜接點**：
- workspace / agent 是 client-agnostic data model
- daemon API 不假設 client 是 desktop SPA

**現在的預留方式**：
- 所有 client state 走 daemon API（已落地）
- 不在 SPA 內 hardcode 「desktop」假設（避免 viewport / 鍵盤 / 滑鼠專屬邏輯）

### 7.4 願景的角色

**校驗器，非 roadmap**：
- 不在 milestone / sprint 內排期
- 但每個現在的 PR / 設計決策都要過 Law 1 雙重檢驗
- 任何決策若堵死上述方向，需重新討論

---

## 8. 文件維護規則

- 本文件是 Purdex 設計決策的**單一真相源**
- 修改本文件需明確 PR、列出影響的 IA / 視覺 / 互動決策
- 詞彙表（§3）變更需同步檢查 codebase 與 UI 文案
- Design Laws（§4）變更需 review 既有設計是否仍合規
- Long-term Direction（§7）的「銜接點」與「預留方式」必須在新增方向時一併寫清楚
