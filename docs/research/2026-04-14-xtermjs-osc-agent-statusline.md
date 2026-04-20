# xterm.js OSC / Agent 偵測 / Status Line 分離研究

> 研究日期：2026-04-14
> 目的：評估 xterm.js v6 API 能否實作 OSC title 動態更新、Agent 偵測、Status Line 擷取，及對 Purdex 的可行性
> 起源：Sherly terminal 支援 OSC 0/2 動態 tab title + agent 偵測 + status line 串接

---

## 1. OSC Escape Sequence 基礎

### 格式

| 序列 | 格式 | 效果 |
|------|------|------|
| OSC 0 | `\x1b]0;{title}\x07` | 同時設 icon name + window title |
| OSC 1 | `\x1b]1;{title}\x07` | 僅 icon name |
| OSC 2 | `\x1b]2;{title}\x07` | 僅 window title |

ST（String Terminator）有兩種：BEL (`0x07`) 和 ESC `\` (`0x1b\x5c`)。BEL 較常用。

### 各 Agent 的 OSC 行為

| Agent | OSC Title | 內容 |
|-------|-----------|------|
| Claude Code | ✅ OSC 0 | `Claude Code` 或含 `-n` session name |
| Codex CLI | ✅ OSC 0 | 含安全清理（去控制字元、截斷 240 字元） |
| Aider / Copilot CLI | ❌ | 無公開文件顯示送 OSC title |

### 其他常見 OSC 序列

| OSC | 用途 | 誰在用 |
|-----|------|--------|
| OSC 7 | 回報 working directory | zsh / bash integration |
| OSC 8 | 超連結 `\x1b]8;;url\x07text\x1b]8;;\x07` | Claude Code（PR link 等） |
| OSC 52 | 剪貼簿操作 | 多數現代 terminal |
| OSC 133 | Semantic prompt zones | Shell integration（Claude Code 有 issue 討論） |
| OSC 1337 | iTerm2 專屬（user var / 圖片 / badge） | iTerm2 |

---

## 2. Agent Status Line 渲染機制

### Claude Code

- 修改版 **Ink**（React for CLI）+ Bun 單一 binary
- 用 **DECSTBM**（`CSI Ps;Ps r`）設定捲動區域，底部區域不捲動 → status line
- **不用** alternate screen buffer
- DECSTBM 啟用受環境偵測（檢查 `TMUX`、`ZELLIJ`、`TERM_PROGRAM`）
- 自建 screen buffer + cell-based diff 渲染，非標準 Ink

### Codex CLI

- **Rust** 實作，Ratatui + crossterm
- 用 **alternate screen buffer**（`EnterAlternateScreen`）
- Status line 是 ratatui widget，渲染在 `bottom_pane`
- OSC 0 設 terminal title

### 關鍵差異

| | Claude Code | Codex CLI |
|---|---|---|
| Status Line 機制 | DECSTBM scroll region | Alternate screen + widget |
| 偵測信號 | `CSI Ps;Ps r` | `CSI ?1049h` |
| Buffer 模式 | Normal buffer | Alternate buffer |

---

## 3. xterm.js v6 API 深度分析

### 3.1 Parser Handler 系統

5 種 handler 註冊方法，堆疊 LIFO，handler 回傳 `true` 消化序列、`false` 放行給下一個（含內建）。

```typescript
// CSI — 例如 DECSTBM (CSI r)、SGR (CSI m)
parser.registerCsiHandler(
  id: IFunctionIdentifier,
  callback: (params: (number | number[])[]) => boolean | Promise<boolean>
): IDisposable

// OSC — 例如 OSC 0 (title)、OSC 52 (clipboard)
parser.registerOscHandler(
  ident: number,  // 直接用數字，不是 IFunctionIdentifier
  callback: (data: string) => boolean | Promise<boolean>
): IDisposable

// DCS — Device Control String（payload 上限 10MB）
parser.registerDcsHandler(id, callback): IDisposable

// ESC — 簡單 escape sequence（無參數）
parser.registerEscHandler(id, handler): IDisposable

// APC — Application Program Command（較少文件，存在於 .d.ts 但官方文件未列）
parser.registerApcHandler(ident, callback): IDisposable
```

`IFunctionIdentifier`:
```typescript
{ prefix?: string, intermediates?: string, final: string }
// prefix: \x3c-\x3f（CSI/DCS 用）
// intermediates: \x20-\x2f
// final: \x40-\x7e（CSI/DCS）或 \x30-\x7e（ESC）
```

**非同步 handler**：可回傳 `Promise<boolean>`，parser 暫停直到 resolve（超過 5 秒警告）。

### 3.2 Buffer API（唯讀）

```typescript
terminal.buffer.active   // IBuffer — 目前活躍 buffer
terminal.buffer.normal   // IBuffer — normal buffer
terminal.buffer.alternate // IBuffer — alternate buffer

// IBuffer
buffer.type        // 'normal' | 'alternate'
buffer.cursorX/Y   // cursor 位置
buffer.viewportY   // viewport 頂部行號
buffer.baseY       // scroll 到底時 viewport 頂部行號
buffer.length      // 總行數
buffer.getLine(y)  // IBufferLine | undefined
buffer.getNullCell() // 建立可重用 cell（效能）

// IBufferLine
line.isWrapped     // 是否是上一行的 wrap
line.length        // 欄數
line.getCell(x, cell?) // IBufferCell（傳入 cell 可重用避免 GC）
line.translateToString(trimRight?, startCol?, endCol?) // 轉文字

// IBufferCell — 逐 cell 完整資訊
cell.getChars()    // 字元
cell.getWidth()    // 0=combining, 1=normal, 2=wide(CJK)
cell.getFgColor() / getBgColor()  // 色值
cell.isFgRGB() / isFgPalette() / isFgDefault()  // 色彩模式
cell.isBold() / isItalic() / isDim() / isUnderline() / isStrikethrough() / isOverline()
```

**不能寫入 buffer** — 唯一途徑是 `terminal.write()` 送 escape sequence。

### 3.3 事件系統

| 事件 | Payload | 用途 |
|------|---------|------|
| `onTitleChange` | `string` | OSC 0/2 title 變更 |
| `onWriteParsed` | `void` | **v6 新增** — write 資料 parse 完成，讀 buffer 最佳時機 |
| `onRender` | `{start, end}` | 哪些行被重繪 |
| `onLineFeed` | `void` | 換行 |
| `onCursorMove` | `void` | cursor 移動 |
| `onResize` | `{cols, rows}` | terminal 尺寸變更 |
| `onBell` | `void` | BEL 字元 |
| `onData` | `string` | 使用者輸入 |
| `onKey` | `{key, domEvent}` | 按鍵（含 DOM event） |
| `onScroll` | `number` | scroll 位置 |
| `onSelectionChange` | `void` | 選取範圍變更 |
| `onBinary` | `string` | 非 UTF-8 二進位資料 |
| `buffer.onBufferChange` | `IBuffer` | normal ↔ alternate 切換 |

### 3.4 Decoration API

```typescript
const marker = terminal.registerMarker(cursorYOffset?)
// marker.line — 追蹤的行號，scrollback 截斷時自動調整
// alternate buffer 時 markers 永遠回傳空陣列

const deco = terminal.registerDecoration({
  marker,               // 必填
  anchor: 'left',       // 'left' | 'right'
  x: 0, width: 80, height: 2,  // cell 單位
  layer: 'top',         // 'bottom' | 'top'（top 蓋在 selection 上方）
  backgroundColor: '#1a1a2e',
  foregroundColor: '#ffffff',
})

deco?.onRender((el: HTMLElement) => {
  // 完全自訂 DOM — innerHTML、CSS style 等
  // 可跨多行多欄、視覺疊加在 terminal 內容上
})
```

**限制**：decoration 是 DOM overlay，不能真正「隱藏」底下的 row，只能蓋住。

### 3.5 Modes（唯讀）

`terminal.modes` 暴露的 DEC 模式：

- `applicationCursorKeysMode` / `applicationKeypadMode`
- `bracketedPasteMode` / `insertMode`
- `mouseTrackingMode` ('none'|'x10'|'vt200'|'drag'|'any')
- `originMode` / `wraparoundMode` / `reverseWraparoundMode`
- `sendFocusMode` / `synchronizedOutputMode`（DEC 2026，v6 新增）

**未暴露**：DECSTBM scroll margin 值 — 內部有 `_bufferService.buffer.scrollTop/scrollBottom` 但是 private。

### 3.6 其他有用 API

```typescript
// 自訂按鍵處理
terminal.attachCustomKeyEventHandler(handler: (e: KeyboardEvent) => boolean)
// 回傳 false 阻止 xterm.js 處理

// 自訂滾輪處理
terminal.attachCustomWheelEventHandler(handler: (e: WheelEvent) => boolean)

// 程式化輸入（觸發 onData，如同使用者打字）
terminal.input(data: string, wasUserInput?: boolean)

// Link provider（自訂連結偵測）
terminal.registerLinkProvider(provider: ILinkProvider)

// Character joiner（ligature 渲染用）
terminal.registerCharacterJoiner(handler)
```

### 3.7 官方 Addon 清單（v6）

| Addon | 功能 |
|-------|------|
| `@xterm/addon-attach` | WebSocket 連線 |
| `@xterm/addon-clipboard` | OSC 52 剪貼簿 |
| `@xterm/addon-fit` | 自動 resize |
| `@xterm/addon-image` | SIXEL / iTerm2 inline 圖片 |
| `@xterm/addon-ligatures` | 字型連字 |
| `@xterm/addon-progress` | ConEmu 進度序列 |
| `@xterm/addon-search` | Buffer 搜尋 + highlight |
| `@xterm/addon-serialize` | Buffer → VT 序列 / HTML |
| `@xterm/addon-unicode-graphemes` | Grapheme cluster（實驗性） |
| `@xterm/addon-unicode11` | Unicode 11 字寬 |
| `@xterm/addon-web-fonts` | Web font 載入 |
| `@xterm/addon-web-links` | URL 偵測 |
| `@xterm/addon-webgl` | WebGL2 渲染 |

另有 `@xterm/headless` — Node.js 無 DOM headless terminal。

---

## 4. Status Line 分離方案

### 核心策略

```
PTY → xterm.js parser
          ↓ registerCsiHandler({ final: 'r' })
     偵測 DECSTBM → scrollRegion = { top, bottom }
          ↓ onWriteParsed
     讀取 buffer row [bottom..rows] 的 cell 內容
          ↓
     送到 App Status Bar（React component）
          ↓ registerDecoration / CSS clip / resize
     隱藏 xterm.js 中的 status line 行
```

### 隱藏策略比較

| 方案 | 做法 | 優點 | 缺點 |
|------|------|------|------|
| Decoration 覆蓋 | `layer:'top'` + 同背景色 DOM 蓋住 | 零副作用 | 蓋住但 cell 仍在，select 會選到 |
| CSS clip | `overflow:hidden` + 減少容器高度 | 真正隱藏 | 需精確計算 cell height |
| resize 欺騙 | xterm rows 設為 scrollRegion.bottom，PTY 維持原始 rows | 最乾淨 | 需 daemon 配合（雙 rows 管理） |

### resize 欺騙方案細節

最乾淨的方案是讓 xterm.js 和 PTY 用不同的 rows：

1. 偵測到 DECSTBM `CSI 1;22 r`（24 行 terminal，22 行 scroll region）
2. `terminal.resize(cols, 22)` — xterm.js 只渲染 22 行
3. PTY 仍維持 24 行 — Claude Code 繼續正常寫入 row 23-24
4. row 23-24 的資料透過 parser handler 攔截，送到 App Status Bar
5. 使用者看到：terminal 22 行 + App 原生 status bar（含 Claude Code 狀態）

**問題**：xterm.js resize 會送 `CSI 8;22;80 t` 回 PTY，可能觸發 app 重新計算 layout。需要攔截 resize 回報或在 daemon 層處理。

---

## 5. Agent 偵測方案

### 前端偵測（OSC title pattern matching）

```typescript
terminal.onTitleChange((title) => {
  if (/claude\s*code/i.test(title)) agent = 'claude-code';
  else if (/codex/i.test(title))    agent = 'codex';
  else                               agent = 'unknown';
});
```

### 前端偵測（buffer 模式）

```typescript
terminal.buffer.onBufferChange((buf) => {
  // Codex 進入 alternate screen
  if (buf.type === 'alternate') screenMode = 'alternate';
});

// DECSTBM → Claude Code
terminal.parser.registerCsiHandler({ final: 'r' }, (params) => {
  if ((params[1] || terminal.rows) < terminal.rows) {
    statusLineMode = 'decstbm'; // 可能是 Claude Code
  }
  return false;
});
```

### 後端偵測（tmux pane process）

```bash
tmux display-message -p -t {pane} '#{pane_current_command}'
# → claude / codex / aider / vim / zsh
```

可整合到 Purdex Probe Chain 的 Activity probe。

---

## 6. 可行性評估

| 功能 | 難度 | 改動範圍 | xterm.js API |
|------|------|---------|-------------|
| OSC title → Tab 標題 | 🟢 低 | 前端 | `onTitleChange` |
| Agent 偵測（title pattern） | 🟢 低 | 前端 | `onTitleChange` |
| Agent 偵測（tmux process） | 🟢 低 | Daemon probe | tmux CLI |
| Alternate screen 偵測 | 🟢 低 | 前端 | `buffer.onBufferChange` |
| DECSTBM scroll region 追蹤 | 🟢 低 | 前端 addon | `registerCsiHandler` |
| Status line 內容擷取 | 🟡 中 | 前端 addon | `onWriteParsed` + Buffer Cell API |
| Status line 分離顯示 | 🟡 中 | 前端 + 可能 daemon | Decoration / CSS / resize |
| 自訂 OSC 結構化資料 | 🔴 需 agent 配合 | 前端 addon | `registerOscHandler` |

---

## 7. 相關資源

- [xterm.js Parser Hooks Guide](https://xtermjs.org/docs/guides/hooks/)
- [xterm.js Terminal API](https://xtermjs.org/docs/api/terminal/classes/terminal/)
- [xterm.js v6.0.0 Release](https://github.com/xtermjs/xterm.js/releases/tag/6.0.0)
- [Claude Code terminal title issue #18326](https://github.com/anthropics/claude-code/issues/18326)
- [Claude Code OSC 133 issue #32635](https://github.com/anthropics/claude-code/issues/32635)
- [xterm.js DECSLRM issue #4285](https://github.com/xtermjs/xterm.js/issues/4285)
- [xterm.js libghostty exploration #5686](https://github.com/xtermjs/xterm.js/issues/5686)
- [ghostty-web — Ghostty VT parser as WASM](https://github.com/coder/ghostty-web)
