# Vibe Kanban 競品與參考工具盤點（2026-07 現況）

> 研究目的：為 Ploom（控制/協作平面，issue SOT）+ Purdex（主機執行平面）雙平面架構的 build-vs-buy 決策提供對照。重點放在「還沒看過的新工具」與「控制面/執行面分離」「issue/PM 平面 ↔ agent 派工橋接」兩個主題。已知工具（VK/CAO/Baton/opencode/Conductor/Crystal/Claude Squad/Sculptor/Paneflow/Symphony/Emdash/Bernstein/Composio AO/Munder Difflin）簡列更新現況，不深挖。

---

## 一、總表

| 名稱 | URL | 開源/License | 技術棧 | 定位 | Provider中立 | Headless/MCP | 自架 | 多Host | 訂閱合規 | 現況 |
|---|---|---|---|---|---|---|---|---|---|---|
| **Vibe Kanban** | github.com/BloopAI/vibe-kanban | 開源 Apache-2.0（2026-04 公司收攤後轉交社群） | Rust + TS + Postgres | Monolith（board+執行同機） | 是（Claude/Codex/Cursor等） | 有 API | 是 | 否 | 包官方 CLI | 棄養轉社群維護，無商業支援 |
| **CAO** (awslabs/cli-agent-orchestrator) | github.com/awslabs/cli-agent-orchestrator | 開源 | Python | Monolith 偏 orchestrator | 是（Claude/Kiro/Codex/Copilot CLI/Cursor CLI 等 9+） | MCP（memory_store 等） | 是 | 未知 | 包官方 CLI | **活躍**，2.2 版剛加入跨 session 持久記憶（2026-06） |
| **Baton** | github.com/mraza007/baton | 開源 | Python | 執行面（輪詢 GitHub Issues 當任務佇列） | 目前偏 Claude Code | 無獨立 API，靠 GH Issues 輪詢 | 是 | 否 | 包官方 CLI | 活躍小專案，定位單純（無視覺化看板） |
| **opencode** | github.com/anomalyco/opencode（原 sst/opencode） | MIT | Go + TUI | 執行面（agent harness 本身） | 是（OpenAI/Claude/Gemini/Bedrock 等） | 有 | 是 | 否 | 自帶 harness（非包官方 CLI） | 極活躍，150K+ stars，6.5M MAU（2026 中） |
| **Conductor** | conductor.build | **閉源** | Mac native app | Monolith | 是（Claude Code/Codex/Cursor） | 未知 | 否（雲端/桌面應用） | 否 | 包官方 CLI/訂閱 | YC 支持，$22M A 輪，商業活躍 |
| **Crystal** | github.com/stravu/crystal | 開源 | TS/Electron | Monolith | 是 | 未知 | 是 | 否 | 包官方 CLI | 活躍 |
| **Claude Squad** | github.com/smtg-ai/claude-squad | 開源 | Go/tmux | Monolith（TUI） | 是（Claude/Codex/OpenCode/Aider） | 無 | 是 | 否 | 包官方 CLI | 活躍，tmux+worktree 架構與 Purdex 高度同構 |
| **Sculptor** (Imbue) | github.com/imbue-ai/sculptor | 開源 | 未詳（容器化） | Monolith | 是（用你自己的 Claude Code 訂閱） | 未知 | 是（資料不離開本機） | 否 | 包官方 CLI | 公司產品，beta 免費，活躍 |
| **Paneflow** | github.com/arthjean/paneflow, paneflow.dev | 開源 | Rust + GPUI (Zed) | Monolith（真實終端機 pane） | 是 | 唯讀 MCP | 是 | 否 | 包官方 CLI | 活躍，原生 macOS/Linux/Windows |
| **OpenAI Symphony** | openai.com/index/open-source-codex-orchestration-symphony | 開源 Apache-2.0 | **Elixir** | **控制面(Linear)／執行面(Codex sandbox) 明確分離** | 偏 Codex（spec 開放） | 輪詢 Linear API，非 MCP | 是 | 未知 | 包官方 Codex CLI | 2026-04-27 發布，OpenAI 內部 500% PR 增長案例，社群關注度高 |
| **Emdash** | github.com/generalaction/emdash | 開源 | 未詳 | Monolith | 是 | 未知 | 是 | 否 | 包官方 CLI | 活躍 |
| **Bernstein** | bernstein.run / github.com/sipyourdrink-ltd/bernstein | 開源 | Python | 執行面（Goal→Planner→Task Graph→Agents→Janitor→merge） | 是（Claude/Codex/Gemini +44 個） | HMAC 稽核鏈、無 LLM token 花在協調上 | 是（含 air-gap 部署） | 未知 | 包官方 CLI | 主打「合規稽核級」orchestrator，2026 仍在迭代 |
| **Composio AO** | github.com/ComposioHQ/agent-orchestrator（原 mnemom/composio-ao） | 開源 | TypeScript（4萬行/17外掛/3288測試） | Monolith 全自動化 | 是 | 有 dashboard | 是 | 未知 | 包官方 CLI | 活躍，強調自我改進（記錄成效/回顧） |
| **Munder Difflin** | munderdiffl.in | 開源（本機執行） | 未詳 | Monolith（辦公室視覺化隱喻） | 是（包任何 CLI agent） | 未知 | 是 | 否 | 包官方 CLI | 小眾但有趣，GOD orchestrator routing 多 agent |
| **Nimbalyst** | github.com/nimbalyst/nimbalyst | 開源 MIT | 桌面 App（跨平台）+ 手機端 | Monolith | 是（Claude Code/Codex/OpenCode） | 未知 | 是 | 未知 | 包官方 CLI | 新興但活躍，主打視覺編輯（markdown/mockup/diagram）+ 手機 companion |
| **Parallel Code** | github.com/johannesjo/parallel-code, parallelcode.app | 開源 | 桌面 App | Monolith | 是 | 未知 | 是 | 否 | 包官方 CLI | 活躍，VK 的原生桌面替代 |
| **Gastown** | github.com/steveyegge/gastown | 開源 | Go | **控制面自建於 git（bead 追蹤）／執行面 tmux+worktree** | 是（Claude/Copilot/Codex/Gemini） | 無傳統 API，走 git-backed 資料 | 是 | **是（DoltHub 跨 Gas Town 聯邦協作）** | 包官方 CLI | Steve Yegge 第四次嘗試，2026-04 到 v1.0，2026-06 到 v1.2.1，社群熱度高 |
| **Hephaestus** | github.com/agentlas-ai/Hephaestus | 開源 Apache-2.0 | 未詳 | 控制面偏重（meta-agent builder + A2A Hub 路由） | 是 | A2A/MCP | 是 | 未知 | 包官方 CLI | 新專案，「Agent OS」定位 |
| **kodo** | github.com/ikamensh/kodo | 開源 | 未詳 | 執行面（work cycle + 獨立驗證） | 是（Claude/Codex/Gemini CLI） | 未知 | 是 | 未知 | 包官方 CLI | 新興小專案 |
| **Devin Desktop / Windsurf** (Cognition) | devin.ai, docs.devin.ai | 閉源 | 未詳 | **控制面(Agent Command Center)／執行面(cloud+local agents) 分離** | 偏 Devin，但支援 **ACP** 協定接其他 agent | 有雲端 API + ACP | 否（SaaS，可連本機） | 是（本機+雲端混合） | 訂閱制商業服務 | 2026-06 Devin Desktop 上線，支援 Devin 派生/管理其他 Devin（agent scheduling） |
| **Factory Droid** | github.com/factory-ai/factory, factory.ai | 閉源（CLI 開放但非 OSS license） | 未詳 | Monolith 偏執行面 | 是（Anthropic/OpenAI/Google/開源權重模型可中途換） | **droid exec 為官方 headless 模式** | 部分（BYOK 可自架模型端） | 未知 | 包官方 CLI（自身也是 harness） | 活躍商業產品，Terminal-Bench #1，2026 加入 Mission 多 agent 模式 |
| **OpenHands** (All Hands AI) | github.com/All-Hands-AI/OpenHands | MIT（Enterprise 另計費） | Python | **企業版有獨立 Agent Control Plane** | 是 | 有 API/SDK | 是（Helm chart 可自架 K8s） | 是（企業版可跨多 agent 同時跑） | 部分包官方 CLI，部分自建 sandbox runtime | 76K+ stars，1.7.0（2026-05），從 OpenDevin 演進而來，活躍 |
| **Paseo** | github.com/getpaseo/paseo, paseo.sh | 開源 **AGPL-3.0** | 未詳（daemon + web/desktop/mobile client） | **執行面清楚獨立**（daemon 常駐，多 client 連線） | **是**（Claude Code/Codex/Copilot/OpenCode/Pi/Cursor/Gemini CLI/Amp 等 30+） | Websocket API | **是**（daemon 跑在自己機器） | 架構上支援跨裝置存取（未明確標榜跨主機 tailnet，但 daemon+client 分離設計相同） | 包官方 CLI（用你自己裝的 CLI+憑證） | 新興但架構完整，含原生 iOS/Android app |
| **Overstory** | github.com/jayminwest/overstory | 開源 | 未詳 | 執行面（pluggable AgentRuntime，ov serve 提供 web UI） | 是（11 種 runtime：Claude Code/Pi/Gemini CLI/Aider/Goose/Amp 等） | headless 預設（`-p --output-format stream-json`），NDJSON | 是 | 未知 | 包官方 CLI | 新專案，介面設計（ClaudeRuntime.parseEvents）與 Purdex 的 stream-json 處理路數相近 |
| **swarm-protocol** | github.com/phuryn/swarm-protocol | 開源 | Node.js + TS + PostgreSQL（raw SQL）+ `@modelcontextprotocol/sdk` | **純控制面（無 UI，無 board）**，執行面仍是各自的 Claude Code | 中立（協定層，不綁定特定 CLI） | **本質就是 MCP server** | 是 | 是（設計上就是給多人各自本機 agent 共享狀態） | 不涉入 agent 執行，天然相容任何包官方 CLI 的用法 | 新專案（2026），定位精準：跨 session／跨人協調層，不做 Jira/看板 |
| **Centaur** (Paradigm) | github.com/paradigmxyz/centaur, centaur.run | 開源 MIT | **FastAPI 控制面 + Kubernetes 沙盒執行面** | 未強調中立，偏自訂 agent+工具外掛 | **有**（auto-generated REST endpoints per tool plugin，durable workflow engine） | 是（自架 k3s 即可，不需完整 K8s） | **是** | 是（每個對話各自獨立沙盒，可分散） | 非包官方 CLI，是自建 agent runtime | 2026-05 由 Paradigm（加密貨幣創投）開源，架構文件完整 |
| **GitHub Copilot coding agent** | github.com/features/copilot/agents | 閉源 | GitHub Actions sandbox | **控制面=GitHub Issue（assignee）／執行面=Actions 沙盒，官方原生分離** | 否（僅 Copilot） | 有（Actions 觸發） | 否（GitHub 代管） | 否 | 訂閱制（Copilot Pro+/Business/Enterprise） | 2026 已 GA，指派 Issue 給 Copilot 即自動跑 |
| **Cursor Background Agents / Automations** | cursor.com/docs/integrations/slack | 閉源 | 雲端 VM | **控制面=Slack/Linear/GitHub/PagerDuty/webhook 觸發／執行面=雲端隔離 VM** | 否（僅 Cursor agent） | 有（webhook 觸發 API） | 否 | 否 | 訂閱制 | 2026-03 Automations 上線，多來源觸發＋排程，商業活躍 |
| **Mattermost Agents Plugin** | github.com/mattermost/mattermost-plugin-agents | 開源（Mattermost 生態） | Go 外掛 + `bridgeclient` | 控制面＝MM thread／執行面＝外掛呼叫的 LLM/agent（官方 bridge） | 是（支援多 LLM） | 有官方 bridgeclient API | 是（隨 MM 自架） | 是（MM 本身多 team/多 server） | 視所接 agent 而定 | 官方持續維護，2026-06 剛修 thread context 保留問題；**是 MM↔agent 橋接最直接的官方參考** |
| **ACP (Agent Client Protocol)** | agentclientprotocol.com（Zed 主導） | 開放協定 | 協定規格（JSON-RPC 類） | 協定層，串接「任意 agent ↔ 任意 editor/控制面」 | 是（設計初衷） | 是（協定本身即是介面） | N/A | N/A | N/A | 2025-08 由 Zed 推出，2026 已被 JetBrains/Google/GitHub/Cognition(Devin) 採用，逐漸變成事實標準 |

---

## 二、各工具短評

### A. 已知工具（簡列，僅更新現況）

- **Vibe Kanban**：Bloop 於 2026-04-10 收攤，交棒社群，Apache-2.0，可自架但無商業支援。[Nimbalyst blog](https://nimbalyst.com/blog/best-vibe-kanban-alternatives-2026/)
- **CAO (awslabs)**：仍活躍，2.2 版新增跨 session 記憶（`memory_store`/`memory_recall`），支援 9+ CLI provider。[GitHub](https://github.com/awslabs/cli-agent-orchestrator)
- **Baton**：定位單純——把 GitHub Issues 當任務佇列，輪詢 + worktree + Claude Code + `agent-browser` 視覺驗收，無視覺化看板。[GitHub](https://github.com/mraza007/baton)
- **opencode**：原 SST 團隊作品，現屬 anomalyco，MIT，150K+ stars/6.5M MAU，是目前最主流的開源終端 agent harness之一。[GitHub](https://github.com/anomalyco/opencode)
- **Conductor**：**閉源**，YC 背書 + $22M A輪，Mac 原生 app，商業路線清楚（非 build 參考對象，是 buy 對象）。[conductor.build](https://www.conductor.build/)
- **Crystal**：開源 TS/Electron，多平行 session + worktree，穩定活躍。[GitHub](https://github.com/stravu/crystal)
- **Claude Squad**：tmux + git worktree 的 TUI，架構與 Purdex（tmux session 管理）高度同構，可作為「純 TUI 端」對照組。[GitHub](https://github.com/smtg-ai/claude-squad)
- **Sculptor (Imbue)**：容器化 pairing 模式（一鍵把 agent 工作拉回本地 IDE），資料不離開本機，公司背書但目前免費。[imbue.com](https://imbue.com/sculptor/)
- **Paneflow**：Rust + Zed 的 GPUI，強調「真實終端 pane、非純 chat 抽象」，唯讀 MCP，跨三平台原生。[GitHub](https://github.com/arthjean/paneflow)
- **Symphony (OpenAI)**：見下方「控制面/執行面分離代表」專節，最重要的參考案例之一。
- **Emdash**：開源，多 agent 並行協作，細節資訊有限。
- **Bernstein**：Python 確定性排程（無 LLM token 花在協調），HMAC 稽核鏈 + air-gap 部署，主打合規/稽核場景。[bernstein.run](https://bernstein.run/)
- **Composio AO**：TS 全自動化，agent 自己修 CI、回覆 review、管理 PR 生命週期，含自我改進迴圈。[GitHub](https://github.com/ComposioHQ/agent-orchestrator)
- **Munder Difflin**：本機 agent hive，長期記憶 + mailbox + 2D 辦公室視覺化 + GOD orchestrator 路由，小眾實驗性質。[munderdiffl.in](https://munderdiffl.in/)

### B. 新發現 — Agent 編排看板 / Orchestrator

- **Nimbalyst**（MIT）：VK 最直接的視覺化替代品，桌面+手機 companion，主打「視覺編輯 agent 產出的 markdown/mockup/diagram」，非純看板。[GitHub](https://github.com/nimbalyst/nimbalyst)
- **Parallel Code**（開源）：原生桌面 app 版的 VK，強調免瀏覽器。[parallelcode.app](https://parallelcode.app/compare/parallel-code-vs-vibe-kanban/)
- **Gastown**（Steve Yegge，開源 Go）：前 Google/Amazon 工程師的第四次 orchestrator 嘗試。最大特色是**控制面完全自建於 git**——工作狀態存成 git-backed「bead」（issue 般的最小單位），並透過 **DoltHub 做跨 Gas Town 的聯邦式協作**（不同機器/團隊的 rig 可以互相張貼、認領工作、累積可攜式信譽）。2026-04 到 v1.0，6 月到 v1.2.1，社群熱度高。[GitHub](https://github.com/steveyegge/gastown) / [Ry Walker 研究](https://rywalker.com/research/gastown)
- **Hephaestus**（Apache-2.0）：自稱「Agent OS」，含 meta-agent builder 與 A2A Hub 路由，控制面色彩較重。[GitHub](https://github.com/agentlas-ai/Hephaestus)
- **kodo**：多 agent work cycle + 獨立驗證步驟的輕量 orchestrator。[GitHub](https://github.com/ikamensh/kodo)
- **Devin Desktop（原 Windsurf, Cognition）**：2026-06 上線，把 IDE 變成「Agent Command Center」，**Devin 現在可以派生/協調其他 Devin**（agent scheduling），並支援 **ACP** 協定接其他家 agent 進來。跟 Linear/Slack 有原生整合可直接從 issue/訊息啟動 session。[The Agent Report](https://the-agent-report.com/2026/06/cognition-devin-desktop-agent-orchestration/)
- **Factory Droid**：商業 CLI + `droid exec`（官方 headless 模式）+ Mission 模式（plan/delegate/validate 三角色多 agent），Terminal-Bench 排名第一。[docs.factory.ai](https://docs.factory.ai/cli/droid-exec/overview)
- **OpenHands**：76K+ stars，MIT 核心，企業版有**獨立的 Agent Control Plane** 可同時協調多個 OpenHands agent，是「開源核心 + 企業控制面加值」的典型商業模式。[GitHub](https://github.com/All-Hands-AI/OpenHands)

### C. 新發現 — 執行/Dispatch 平面工具（可被外部驅動的 agent runner）

這一類是跟 **Purdex 定位最接近**的同型競品，值得重點參考：

- **Paseo**（AGPL-3.0）：**架構與 Purdex 幾乎同構**——一個常駐 daemon 跑在你自己的機器上，用你安裝好的 CLI（Claude Code/Codex/Copilot/OpenCode/Pi/Cursor/Gemini CLI/Amp 等 30+）與既有憑證去啟動 local process，然後桌面/web/手機/CLI 多種 client 透過 websocket 連進來操作。等於是「先把執行面做成獨立服務，再疊多種前端」的路線，跟 Purdex（daemon + Electron/SPA 多前端、走 Tailscale 多 host）思路一致，差別是 Paseo 目前重心在多 provider 而非多 host。[paseo.sh](https://paseo.sh/) / [GitHub](https://github.com/getpaseo/paseo)
- **Overstory**：`AgentRuntime` 介面把 11 種 runtime（Claude Code/Pi/Gemini CLI/Aider/Goose/Amp…）統一抽象，預設走 headless `-p --output-format stream-json`，NDJSON 事件經 `ClaudeRuntime.parseEvents` 解析後餵給 `ov serve` 的 web UI ——**這條 stream-json 解析管線的設計跟 Purdex 的 Stream 模式幾乎是同一套做法**，可以直接拿來對照實作細節。[GitHub](https://github.com/jayminwest/overstory)
- **Droid Exec**（Factory）：官方原生 headless 執行模式，做 CI/腳本整合，是「vendor 自己出的 headless API」範例。[docs.factory.ai](https://docs.factory.ai/cli/droid-exec/overview)
- **GitHub Copilot coding agent**：Issue 指派 → GitHub Actions 沙盒執行 → 開 PR，全程雲端代管，是大廠「issue 驅動 headless 執行」的天花板範例（但完全綁 GitHub/Copilot）。[GitHub Docs](https://docs.github.com/copilot/concepts/agents/coding-agent/about-coding-agent)
- **Cursor Background Agents / Automations**：Automations 可被 Slack/Linear/GitHub/PagerDuty/任意 webhook 觸發，執行面是雲端隔離 VM，2026-03 上線。展示「控制面觸發來源可以很雜，執行面統一收斂成一種雲端沙盒」的模式。[agentmarketcap.ai](https://agentmarketcap.ai/blog/2026/04/05/cursor-april-2026-agent-mode-overhaul-background-agents-ide-convergence)

### D. 新發現 — Issue/PM 平面 → Agent 派工整合（控制面/執行面分離代表，核心研究目標）

這是使用者最關心的類別：

- **OpenAI Symphony**（Apache-2.0，Elixir）：**目前最純粹、最值得參考的「控制面/執行面分離」實作**。把 Linear board 直接當控制面——每個開著的 issue 對應一個專屬 Codex agent workspace，30 秒輪詢一次 Linear，每個 issue 建一個隔離 sandbox，agent 持續跑到完成（崩潰/卡住會自動重啟），人類只在 issue/PR 層級審查，不用盯著中間過程。OpenAI 內部團隊回報三週內 PR 產出量成長 500%。核心啟示：**issue tracker 不需要感知 agent 內部狀態，只要能被輪詢+能承載結果（PR）**，控制面與執行面就能徹底解耦。[OpenAI 官方](https://openai.com/index/open-source-codex-orchestration-symphony/) / [InfoWorld](https://www.infoworld.com/article/4164173/openais-symphony-spec-pushes-coding-agents-from-prompts-to-orchestration.html)
- **Centaur (Paradigm)**（MIT，FastAPI + Kubernetes）：**架構圖上最直接對照使用者「MM bridge」構想的專案**。Slack 是控制面入口（@mention → thread 內回報進度與結果），FastAPI 服務本身是控制面，負責管理 agent session 生命週期（spawn→message→execute）、自動為每個工具外掛產生 REST endpoint、跑 durable workflow engine、把執行事件串流回 client；每個對話各自在獨立 Kubernetes（或輕量 k3s）沙盒執行，執行面與控制面用清楚的 API 邊界分開。2026-05 由 Paradigm（加密貨幣創投的內部工具）開源，架構文件完整，是「Slack thread ↔ agent」橋接的最佳現成範例。[Paradigm 開源公告](https://www.paradigm.xyz/2026/05/open-sourcing-centaur-multiplayer-self-hosted-secure-agents) / [centaur.run/architecture](https://centaur.run/architecture)
- **Gastown**：跟 Symphony/Centaur 路線不同——**不借用外部 issue tracker，控制面自己用 git 存**（bead + TOML formula/molecule 工作流模板），並透過 DoltHub 做跨機（甚至跨組織）的工作聯邦交換。這代表另一種取捨：如果不想依賴 Linear/Jira 之類 SaaS，也可以把「issue SOT」直接建在 git 裡。跟 Ploom（自建 issue SOT，但走 Go+SQLite+多人協作 UI）方向相反但互補，值得對比兩種「控制面自建 vs 借用外部工具」的優劣。[GitHub](https://github.com/steveyegge/gastown)
- **swarm-protocol**（Node+TS+Postgres+MCP）：**最輕量的控制面實作**——沒有 UI、沒有看板、沒有 Jira，就是一個暴露成 MCP server 的狀態同步層，讓多個人（各自跑自己的 Claude Code）能互相看到誰在動什麼檔案、認領工作、偵測衝突、交接任務（10-15 分鐘心跳偵測 stale claim）。如果 Ploom 的 issue 深度功能顯得太重，這是「控制面極簡化到只剩狀態同步」的參考下限。[GitHub](https://github.com/phuryn/swarm-protocol)
- **GitHub Copilot coding agent** / **Cursor Automations** / **Devin + Linear/Slack**：三家大廠都收斂到同一個模式——「熟悉的協作介面（Issue/Slack/Linear）當控制面觸發點，雲端沙盒當執行面」，差異只在觸發來源廣度（Cursor 支援 Slack/Linear/GitHub/PagerDuty/webhook，Copilot 只認 GitHub Issue，Devin 兩者都做）。這組合起來代表「buy」路線的產品天花板。
- **Mattermost Agents Plugin（官方）**：Mattermost 官方自己維護的 `mattermost-plugin-agents`，內含 `bridgeclient`，就是「MM thread ↔ LLM/agent」的官方橋接實作。2026-06 剛修過「thread context 在 channel tool follow-up 時遺失」的 bug，說明這條路線官方仍在持續打磨，可作為使用者自己的 MM bridge 構想（kickoff_ploom_purdex_dispatch_integration）的直接技術參考（尤其是 thread↔session 對應、串流回報進度的實作細節）。[GitHub](https://github.com/mattermost/mattermost-plugin-agents)

### E. 協定層（跨工具的架構參考）

- **ACP (Agent Client Protocol)**：Zed 於 2025-08 推出，定位「LSP for AI coding agents」——讓任意相容 agent 接上任意相容 editor/控制面。2026 已被 JetBrains、Google、GitHub 採用，Cognition（Devin Desktop）也剛跟進，正在變成事實標準。如果 Purdex 未來想開放給第三方 agent（不只 claude/codex）接入，ACP 是比自訂協定更值得對齊的方向。[agentclientprotocol.com](https://agentclientprotocol.com/)
- **AIP (Agent Interaction Protocol，研究提案)**：學界/業界提出的「事件驅動控制面」協定構想，試圖解決 A2A/ACP（IBM）這類短生命週期、無狀態互動假設下的協調脆弱問題。目前仍偏概念/研究階段，非成熟落地產品，僅供架構思路參考。

---

## 三、結論：最值得參考的 5 個 + 為什麼

1. **OpenAI Symphony** —— 目前業界「控制面/執行面分離」最乾淨、最有實測數據（500% PR 成長）的範例。核心可抄的設計：**issue tracker 只需要被輪詢 + 能收 PR 結果，agent 執行細節完全不用讓控制面知道**。這跟 Ploom(issue SOT) / Purdex(執行面) 的分工完全同構，值得直接借鏡它的「輪詢頻率、per-issue workspace、崩潰自動重啟」三個實作細節。

2. **Centaur (Paradigm)** —— 跟使用者自己正在構思的「MM bridge」在架構圖層級幾乎一模一樣：Slack thread 當控制面入口，FastAPI 服務管理 agent session 生命週期並把工具外掛自動轉成 REST endpoint，K8s/k3s 沙盒當執行面，訊息串流回 thread。MIT 開源、架構文件公開完整，是目前找到「聊天串 ↔ agent 派工」最值得逐段對照抄的現成實作。

3. **Paseo** —— 在「執行面工具」這一類裡，跟 Purdex 的定位最貼近：常駐 daemon（非雲端 SaaS）+ 用使用者自己裝的官方 CLI/憑證（訂閱合規）+ 多種前端（桌面/web/手機/CLI）透過 websocket 連進同一個 daemon。目前差異是它主打多 provider 廣度、Purdex 主打多 host（tailnet）廣度——兩者互補，可以互相檢視對方沒做的維度（Paseo 沒做多主機聯網，Purdex 沒做手機端）。

4. **Overstory** —— headless stream-json 解析管線（`ClaudeRuntime.parseEvents` → NDJSON → web UI）跟 Purdex 的 Stream 模式做法幾乎一致，是少數能直接拿來對照「我們的 parseEvents 是否有漏掉的 edge case」的開源實作，優先度高但工作量小（純技術細節參考，非架構決策）。

5. **Gastown** —— 代表跟 Ploom 完全相反的取捨：控制面不借用任何外部 SaaS（Linear/Jira/Slack），而是把 issue（bead）直接存進 git，再用 DoltHub 做跨機/跨團隊聯邦協作。即使最終不採用這個方向，它清楚劃出了「自建協作平面」光譜的另一端，有助於在 build-vs-buy 討論時把 Ploom 的定位（自建 Go+SQLite+UI 的完整協作平面）放到座標系裡驗證："我們比 Gastown 重多少、換來了什麼（權限/view/wiki/分享）"。

**次要但值得記錄**：`swarm-protocol`（控制面可以輕到只剩狀態同步層，若 Ploom 顯得太重時的下限參照）、Mattermost 官方 `mattermost-plugin-agents`（MM bridge 的官方技術先例）、ACP 協定（若 Purdex 未來要對外開放非 claude/codex 的 agent 介接，應優先考慮對齊此協定而非自訂）。
