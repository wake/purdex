# Claude Code Channels vs Stream 模式研究

> 日期：2026-03-21
> 目的：比較 CC 四種運作模式，評估 Channel 是否可取代 tmux-box 現有的 `-p stream-json` 架構

---

## 1. 四種運作模式概覽

| 模式 | 本質 | Session 模型 | 認證 |
|------|------|-------------|------|
| **互動式** | Terminal TUI | 持續 | claude.ai |
| **`-p` stream-json** | 無頭雙向 JSON 管線 | 持續（stdin/stdout） | claude.ai 或 API key |
| **Agent SDK** | Python/TS 程式庫 | 程式控制 | API key only |
| **Channels** | MCP server push 進互動 session | 推送進現有 session | claude.ai only |

---

## 2. Release Channels（發佈頻道，不同概念）

Claude Code 有兩條發佈頻道，與 Channels 功能無關：

| 頻道 | 說明 | 切換方式 |
|------|------|---------|
| **`latest`**（預設） | 即時發佈，可能一天多版 | `/config` 或 `"autoUpdatesChannel": "latest"` |
| **`stable`** | 延遲約 1～3 週，跳過有迴歸的版本 | `/config` 或 `"autoUpdatesChannel": "stable"` |

npm dist-tags（截至 2026-03-21）：
- `latest` / `next` → 2.1.80
- `stable` → 2.1.62（落後約 18 版）

安裝時指定：`curl -fsSL https://claude.ai/install.sh | bash -s stable`

---

## 3. Claude Code Channels（通訊頻道功能）

### 3.1 架構

Channel 本質是一個宣告 `claude/channel` experimental capability 的 MCP server：

```
[外部平台] --polling--> [Channel MCP Server (本地 subprocess)] --stdio--> [Claude Code]
```

- 不建立新 session，**推送事件進已存在的 running session**
- 需 v2.1.80+，額外需要 Bun runtime
- 目前支援：Telegram、Discord、fakechat（本機 demo）

啟動方式：

```bash
claude --channels plugin:telegram@claude-plugins-official
```

### 3.2 MCP 協議細節

Server 宣告 channel capability：

```typescript
const mcp = new Server(
  { name: 'telegram', version: '1.0.0' },
  {
    capabilities: {
      experimental: { 'claude/channel': {} },
      tools: {},
    },
    instructions: '...',
  },
)
```

推送 notification 格式：

```typescript
await mcp.notification({
  method: 'notifications/claude/channel',
  params: {
    content: 'message text',
    meta: { chat_id: '12345', user: 'wake', ts: '...' },
  },
})
```

Claude 收到時呈現為：

```xml
<channel source="telegram" chat_id="12345" user="wake">message text</channel>
```

### 3.3 安全模型

- **Sender Allowlist**：未列入的人訊息靜默丟棄
- **Pairing Flow**：6 字元 hex code，1 小時有效，上限 3 pending
- **Anti-Prompt-Injection**：system prompt 禁止 Claude 因 channel 訊息修改 access.json
- **Outbound Gate**：reply 只能操作 allowlist 裡的 chat
- **State File Protection**：阻止 Claude 洩漏 `.env`、`access.json`
- **Enterprise**：Team/Enterprise 預設停用，需管理員啟用 `channelsEnabled`

### 3.4 Plugin 系統

```
plugin-name/
├── .claude-plugin/
│   └── plugin.json      # metadata
├── .mcp.json             # MCP server 設定
├── commands/             # Slash commands
├── agents/               # Agent 定義
├── skills/               # Skill 定義
├── hooks/                # Event handlers
└── settings.json
```

安裝：`/plugin install telegram@claude-plugins-official`

社群 plugin：Feishu（飛書）、Slack、Google Chat、QQ、Codex bridge 等。

---

## 4. `-p` 模式功能完整性（修正）

**`-p` 模式的能力遠比預期完整**，缺的基本上只有 TUI 互動介面：

| 功能 | `-p` stream-json | 互動模式 | Channel |
|------|:---:|:---:|:---:|
| Skills（model 自動觸發） | ✅ | ✅ | ✅ |
| Skills（`/skill-name` 觸發） | ✅ 作為 prompt | ✅ | ✅ |
| Custom commands | ✅ 作為 prompt | ✅ | ✅ |
| `/compact`、`/clear` | ✅ | ✅ | ✅ |
| MCP servers | ✅ | ✅ | ✅ |
| Subagents | ✅ | ✅ | ❌ |
| Hooks | ✅ | ✅ | ✅ |
| CLAUDE.md | ✅（需 `--setting-sources`） | ✅ 自動 | ✅ 自動 |
| 多輪對話 | ✅ stdin stream-json | ✅ | ✅ |
| Token-level 事件流 | ✅ | ❌（TUI 渲染） | ❌ |
| `/diff`、`/rewind`、`/memory` | ❌ | ✅ | ✅ |
| `/config`、`/theme` | ❌ | ✅ | ✅ |
| 結構化輸出（JSON Schema） | ✅ | ❌ | ❌ |

### `-p` 模式專屬 flags

| Flag | 用途 |
|------|------|
| `--output-format stream-json` | NDJSON 事件流輸出 |
| `--input-format stream-json` | 多輪 JSON 訊息輸入 |
| `--include-partial-messages` | 串流部分訊息（逐 token） |
| `--max-turns` | 限制 agentic turns |
| `--max-budget-usd` | 限制 API 花費 |
| `--json-schema` | 結構化輸出 Schema |
| `--replay-user-messages` | 回覆 stdin user messages |
| `--no-session-persistence` | 不保存 session |

### Skills 已知限制

- 大量 skills（40+）+ MCP servers 時，model 自動觸發率下降（context competition）
- Skills 的 `allowed-tools` frontmatter 在 SDK/`-p` 模式中無效
- 參考：GitHub issue `anthropics/claude-code#34648`

---

## 5. 模式比較：抽象架構

三者都是「外部 Client ↔ Bridge ↔ Claude Code」的 bridge pattern：

```
Stream:  SPA ←→ Go daemon (NDJSON) ←→ claude -p stream-json
Channel: SPA ←→ 自建 MCP plugin   ←→ claude --channels
SDK:     SPA ←→ Python/TS 程式碼   ←→ Agent SDK query()
```

### 關鍵取捨

| 維度 | `-p` stream-json | Channel plugin | Agent SDK |
|------|---|---|---|
| **資料粒度** | Token-level 事件 | 結果級文字 | Token-level（async iterator） |
| **CC 功能** | 完整（除 TUI） | 完整（含 TUI） | 完整 + hooks + subagents |
| **程式化控制** | ✅ stdin/stdout | ❌ 需透過 MCP | ✅ 原生 API |
| **額外依賴** | 無（CLI） | Bun + MCP plugin | Python/TS runtime |
| **認證** | claude.ai 或 API key | claude.ai only | API key only |
| **Session 管理** | 自行處理（`--resume`） | 天然持續 | 程式控制 |

---

## 6. 對 tmux-box 的結論

### 現有架構（`-p stream-json`）的優勢

tmux-box 使用 `claude -p --verbose --input-format stream-json --output-format stream-json`，這個架構：

1. **已經是持續連線**：stdin/stdout 管線不需每次重啟 process
2. **Token-level 事件流**：SPA 可即時渲染 tool call、streaming text
3. **Skills 和 commands 都能用**：作為 prompt 送入即可
4. **不需額外依賴**：不需 Bun、不需建 MCP plugin

### Channel 不適合取代 Stream 的原因

1. **缺少結構化事件流**：SPA 的 tool call 可視化、逐字渲染都依賴 NDJSON 事件
2. **需額外建構 MCP plugin**：增加複雜度但不增加能力
3. **認證受限**：Channel 只支援 claude.ai，不支援 API key
4. **資訊粒度不足**：只拿到最終結果，中間過程不可見

### Channel 的互補價值

Channel 適合作為 tmux-box 的**額外輸入管道**——在 Terminal 模式中掛載 `--channels`，讓使用者從手機 Telegram 推指令進 session。但不適合取代 Stream 模式作為 SPA 的資料來源。

---

## 參考來源

- [Push events into a running session with channels](https://code.claude.com/docs/en/channels)
- [Channels reference](https://code.claude.com/docs/en/channels-reference)
- [Run Claude Code programmatically](https://code.claude.com/docs/en/headless)
- [Agent SDK overview](https://platform.claude.com/docs/en/agent-sdk/overview)
- [Agent SDK: Slash Commands](https://platform.claude.com/docs/en/agent-sdk/slash-commands)
- [Agent SDK: Skills](https://platform.claude.com/docs/en/agent-sdk/skills)
- [Create plugins](https://code.claude.com/docs/en/plugins)
- [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official)
- [GitHub issue #34648 — Skills in -p mode](https://github.com/anthropics/claude-code/issues/34648)
- [VentureBeat: Claude Code Channels](https://venturebeat.com/orchestration/anthropic-just-shipped-an-openclaw-killer-called-claude-code-channels)
