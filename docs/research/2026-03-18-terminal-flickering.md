# CC 終端閃爍問題研究：DEC 2026 Synchronized Output 與 tmux-box 的處理方案

> 研究日期：2026-03-18
> 目的：分析 Claude Code 終端閃爍根因，評估在 tmux-box 架構中的解決方案

---

## 1. 問題根因

Claude Code 使用 React + Ink 的 TUI 架構，閃爍源自多個層面：

### 1.1 Ink 的全量重繪

Ink 在每次 React state 變更時執行完整的 tree traversal 和全螢幕重繪，即使只有一個 component 更新。Anthropic 在 v2.0.10 (2025-10) 用自製 differential renderer 取代 Ink 的渲染器，將閃爍降至約 1/3 的 session。

### 1.2 不用 Alternate Screen

Claude Code 刻意使用 scrollback（非 alternate screen）以保留歷史，這迫使每次更新都可能是全螢幕重繪。

### 1.3 tmux 下的 Scroll Event 風暴

在 tmux/screen 內執行時，串流輸出產生每秒 4,000-6,700 次 scroll event，造成嚴重 UI jitter（[#9935](https://github.com/anthropics/claude-code/issues/9935)）。

### 1.4 ANSI Escape Sequence 衝突

多個元素同時渲染（串流回應、狀態列、spinner 動畫），各自寫入不同的 cursor 定位 escape sequence，文字寫到錯誤位置。

### 1.5 GC 壓力

大量 JSX allocation 造成 V8 GC 壓力，VM lock-up 期間掉幀。在 16ms 幀預算中，scene graph → ANSI 約需 5ms。

---

## 2. DEC Mode 2026 (Synchronized Output) 技術規格

**規格來源**：[christianparpart/d8a62cc1ab659194337d73e399004036](https://gist.github.com/christianparpart/d8a62cc1ab659194337d73e399004036)

### 2.1 協定

- `\x1b[?2026h` — BSU (Begin Synchronized Update)：通知終端暫緩渲染
- `\x1b[?2026l` — ESU (End Synchronized Update)：通知終端原子性刷新所有緩衝內容
- `\x1b[?2026$p` — DECRQM 查詢：詢問終端是否支援

### 2.2 各專案 Safety Timeout

| 專案 | Timeout | 備註 |
|------|---------|------|
| tmux | 1 秒 | 超時後清除 MODE_SYNC，觸發 PANE_REDRAW |
| xterm.js | 5 秒 | 可透過 `synchronizedOutputTimeout` 設定 |
| Windows Terminal | 100ms | 類似 V-Sync 機制 |
| claude-chill | 50ms | sync block delay |

---

## 3. tmux 對 DEC 2026 的處理行為

### 3.1 PR #4744（Anthropic 的 Chris Lloyd 提交）

| 項目 | 內容 |
|------|------|
| PR | [tmux/tmux#4744](https://github.com/tmux/tmux/pull/4744) |
| 合併 commit | [`1c7e164c`](https://github.com/tmux/tmux/commit/1c7e164c) |
| 合併日期 | 2025-12-17 |
| 所屬版本 | **僅 tmux master**。3.6a (2025-12-05) 不包含，預計 3.7 |

### 3.2 兩層獨立的 Sync 機制

tmux 在 inbound 和 outbound 方向各有一套 sync，彼此正交：

#### Inbound（應用程式 → tmux）

- `input.c`：解析 `case 2026` → 呼叫 `screen_write_start_sync(wp)`
- 設置 pane 的 `MODE_SYNC` 旗標，啟動 1 秒 evtimer
- `screen_write_collect_flush()` 和 `screen_write_cell()`：`MODE_SYNC` 期間跳過 tty 輸出
- 收到 `\x1b[?2026l` → 清除 `MODE_SYNC`，設 `PANE_REDRAW` 觸發重繪
- **DEC 2026 序列被完全消費，不透傳到外部終端**

#### Outbound（tmux → 外部終端）

- `tty_sync_start()` / `tty_sync_end()`：tmux 執行自身畫面重繪時使用
- 透過 `terminal-features` 的 `sync` 能力控制
- 前提：外部終端的 terminfo 宣告支援 `Sync`

### 3.3 tmux-box 中的實際資料流

```
Claude Code 發送 \x1b[?2026h...\x1b[?2026l
  ↓ (inbound)
tmux 消費 → 設 MODE_SYNC → 暫停 pane 輸出 → ESU 後觸發 PANE_REDRAW
  ↓ (outbound)
tmux 重繪 → 自己產生 \x1b[?2026h...重繪資料...\x1b[?2026l
  ↓
Go daemon 的 PTY 收到 tmux 產生的 DEC 2026 markers
  ↓
Batcher(16ms/64KB) → WebSocket → xterm.js
```

**結論：Go daemon 的 PTY 端能看到 DEC 2026 markers，但來源是 tmux 的 outgoing sync，而非 Claude Code 的直接透傳。**

### 3.4 版本限制

- **tmux 3.6a**（本機版本）：**不包含 PR #4744**，tmux 完全忽略 Claude Code 的 DEC 2026
- 需升級至 tmux master 或等 3.7 release 才能啟用 inbound sync consume + outgoing sync re-emit

---

## 4. xterm.js 的 DEC 2026 支援

| 項目 | 內容 |
|------|------|
| Issue | [#3375](https://github.com/xtermjs/xterm.js/issues/3375)（2021 年開始追蹤） |
| PR | [#5453](https://github.com/xtermjs/xterm.js/pull/5453)（Chris Lloyd 實作） |
| 合併版本 | **xterm.js 6.0.0**（2025-12-22） |

實作行為：
- BSU (`\x1b[?2026h`) → 延遲渲染
- ESU (`\x1b[?2026l`) → 原子性刷新，只重新渲染變更的行
- DECRQM 查詢支援
- 預設 5 秒 safety timeout（可配置）

---

## 5. claude-chill：PTY Proxy 參考實作

**Repo**：[davidbeesley/claude-chill](https://github.com/davidbeesley/claude-chill)
**語言**：Rust
**架構**：PTY man-in-the-middle proxy

### 5.1 核心技術

```
使用者終端 ←→ claude-chill ←→ Claude Code PTY
                  ├─ DEC 2026 sync block 攔截
                  ├─ VT100 模擬器（虛擬螢幕 buffer）
                  └─ 差異渲染（只送出變化的 cells）
```

### 5.2 Sync Block 偵測

使用 `memchr::memmem::Finder`（Boyer-Moore-Horspool）在 byte stream 中搜尋 sync markers：

```rust
// 常數
const SYNC_START: &[u8] = b"\x1b[?2026h";
const SYNC_END: &[u8] = b"\x1b[?2026l";
const SYNC_BUFFER_CAPACITY: usize = 1_048_576; // 1MB

// 狀態機
if self.in_sync_block {
    if let Some(idx) = self.sync_end_finder.find(&data[pos..]) {
        // 收到 sync end → flush 整個 block 到 VT 模擬器
        self.flush_sync_block_to_history();
        self.in_sync_block = false;
    } else {
        // 整個 chunk 都在 sync 區塊內，繼續累積
        self.sync_buffer.extend_from_slice(&data[pos..]);
    }
} else if let Some(idx) = self.sync_start_finder.find(&data[pos..]) {
    // 進入 sync block
    self.in_sync_block = true;
    self.sync_buffer.clear();
}
```

### 5.3 差異渲染

所有 sync block 內容餵給 `vt100` crate 的虛擬終端模擬器，然後：
- `screen.contents_diff(prev_screen)` 計算與上一幀的差異
- 差異包裹在自己產生的 `\x1b[?2026h...\x1b[?2026l` 中送出
- 即使 Claude Code 送出整個螢幕重繪（數 KB），到達終端的可能只有幾十 bytes

### 5.4 時間參數

| 參數 | 值 | 用途 |
|------|-----|------|
| `RENDER_DELAY_MS` | 5ms | 一般輸出到 VT render 的最小間隔 |
| `SYNC_BLOCK_DELAY_MS` | 50ms | sync block 內等待更多資料 |
| `refresh_rate` | 20fps (50ms) | RedrawThrottler 的最小間隔 |

### 5.5 跨 read() 的 Partial Sequence 處理

Best-effort 設計：`memmem::Finder` 在每個 chunk 內搜尋完整 pattern。如果 escape sequence 恰好被切成兩半（如 `\x1b[?20` 和 `26h`），第一個 chunk 找不到 match，資料被當成普通輸出。實務中 PTY read() 很少在 escape sequence 中間切割。

---

## 6. 其他參考專案

### 6.1 tmux-claude-code

[sethdford/tmux-claude-code](https://github.com/sethdford/tmux-claude-code) — 針對 Claude Code 最佳化的 tmux 配置：

```tmux
set -g escape-time 0           # 消除 Escape 鍵延遲
set -g extended-keys on         # 支援 Ctrl+Shift 組合鍵
set -g focus-events on          # 讓 CC 知道 pane 是否 focused
```

### 6.2 gotty / ttyd

兩者都**完全沒有 output coalescing**，每次 `read()` 直接透過 WebSocket 發送。這是 tmux-box 已勝出的地方（Batcher 16ms 合併）。

---

## 7. Go 語言中的 DEC 2026 偵測方案

### 7.1 方法 A：bytes.Index（簡單，best-effort）

```go
var (
    syncStart = []byte("\x1b[?2026h")
    syncEnd   = []byte("\x1b[?2026l")
)

idx := bytes.Index(chunk, syncStart)
```

與 claude-chill 同策略：如果 escape sequence 恰好跨越兩次 read()，漏偵測一次。實務中極少發生。

### 7.2 方法 B：charmbracelet/x/ansi（完整 state machine）

```go
import "github.com/charmbracelet/x/ansi"

// 常數已定義
ansi.SetModeSynchronizedOutput    // "\x1b[?2026h"
ansi.ResetModeSynchronizedOutput  // "\x1b[?2026l"

// DecodeSequence 可處理跨 chunk 的 partial sequence
seq, _, n, newState := ansi.DecodeSequence(data, state, parser)
```

`state` 參數維護跨呼叫的解析狀態，輸入耗盡但序列未完成時回傳非零 `newState`，下次呼叫接續。

### 7.3 方法 C：Bubbletea v2 的寫入端包裹

Bubbletea v2 的 renderer 在 `flush()` 中條件性包裹 sync markers：

```go
if s.syncdUpdates && hasUpdates {
    buf.WriteString(ansi.SetModeSynchronizedOutput)
}
// ... 渲染內容 ...
if s.syncdUpdates && hasUpdates {
    buf.WriteString(ansi.ResetModeSynchronizedOutput)
}
```

---

## 8. tmux-box 的三階段解決方案

### 階段 1：SPA 端 requestAnimationFrame Write Coalescing（立即可做）

**改動**：SPA `spa/src/lib/ws.ts`，約十幾行
**效果**：xterm.js `terminal.write()` 從每秒數百次降至 ~60fps
**複雜度**：極低

```typescript
let pending: Uint8Array[] = [];
let rafId = 0;

ws.onmessage = (e) => {
  pending.push(new Uint8Array(e.data));
  if (!rafId) {
    rafId = requestAnimationFrame(() => {
      const merged = concatBuffers(pending);
      terminal.write(merged);
      pending = [];
      rafId = 0;
    });
  }
};
```

不依賴任何版本升級，所有環境立即有效。gotty/ttyd 都沒做這件事。

### 階段 2：Go Batcher 加入 DEC 2026 Sync-Aware 緩衝

**改動**：Go `internal/terminal/batcher.go`，約 50 行
**效果**：sync block 內的資料不會被拆成多次 WebSocket 寫入
**複雜度**：低

核心邏輯加入 `Batcher`：

```go
type Batcher struct {
    // ... existing fields ...
    inSync       bool
    syncBuf      []byte
    syncTimeout  *time.Timer
}

func (b *Batcher) Write(data []byte) {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.stopped { return }

    for len(data) > 0 {
        if b.inSync {
            if idx := bytes.Index(data, syncEnd); idx >= 0 {
                b.syncBuf = append(b.syncBuf, data[:idx+len(syncEnd)]...)
                b.flushSyncLocked()  // 一次性 flush 整個 sync block
                data = data[idx+len(syncEnd):]
            } else {
                b.syncBuf = append(b.syncBuf, data...)
                return  // 等待更多資料或 timeout
            }
        } else {
            if idx := bytes.Index(data, syncStart); idx >= 0 {
                if idx > 0 {
                    b.buf = append(b.buf, data[:idx]...)
                }
                b.inSync = true
                b.syncBuf = append(b.syncBuf[:0], syncStart...)
                b.startSyncTimeout()
                data = data[idx+len(syncStart):]
            } else {
                b.buf = append(b.buf, data...)
                data = nil
            }
        }
    }
    // 原有的 interval/maxSize flush 邏輯 ...
}
```

Safety timeout 建議 **1 秒**（與 tmux 對齊）。

### 階段 3：升級 tmux + xterm.js（端到端 DEC 2026）

| 元件 | 當前版本 | 目標版本 | 獲得能力 |
|------|---------|---------|---------|
| tmux | 3.6a | master 或 3.7+ | inbound sync consume + outgoing sync re-emit |
| xterm.js | 待確認 | 6.0.0+ | 原生 DEC 2026 支援（BSU/ESU 原子渲染） |

三者配合：
```
CC → tmux (consume + re-emit) → Go Batcher (sync-aware) → xterm.js 6.0 (native DEC 2026)
= 三層防護
```

---

## 9. 效果預估

| 階段 | 閃爍減少 | 延遲增加 | 改動量 |
|------|---------|---------|--------|
| 階段 1（RAF coalescing） | ~60-70% | 0-16ms（一個 frame） | SPA 十幾行 |
| 階段 2（sync-aware batch） | +10-20% | sync block 期間最多 1s timeout | Go ~50 行 |
| 階段 3（端到端 DEC 2026） | 接近零閃爍 | 由各層 safety timeout 控制 | 版本升級 |

---

## 10. 相關 Issue 與資源

### Claude Code 閃爍 Issues（按重要性）

| Issue | 標題 | 重點 |
|-------|------|------|
| [#769](https://github.com/anthropics/claude-code/issues/769) | Screen Flickering | 最早的 issue，700+ upvotes |
| [#9935](https://github.com/anthropics/claude-code/issues/9935) | Excessive scroll events in multiplexers | tmux 下 4,000-6,700/sec scroll |
| [#10794](https://github.com/anthropics/claude-code/issues/10794) | Critical: VSCode Crashes on macOS | 10-20 分鐘後 VSCode 崩潰 |
| [#25749](https://github.com/anthropics/claude-code/issues/25749) | Flickering regression in v2.1.42 | diff renderer 回歸 |
| [#29937](https://github.com/anthropics/claude-code/issues/29937) | Rendering corruption in tmux | 文字重疊覆蓋 |
| [#33350](https://github.com/anthropics/claude-code/issues/33350) | v2.1.73 freeze in tmux | 凍結與記憶體洩漏 |

### 關鍵 PR

| PR | 專案 | 內容 |
|----|------|------|
| [tmux #4744](https://github.com/tmux/tmux/pull/4744) | tmux | DEC 2026 inbound sync support |
| [xterm.js #5453](https://github.com/xtermjs/xterm.js/pull/5453) | xterm.js | DEC 2026 原生支援 |
| [Windows Terminal #18826](https://github.com/microsoft/terminal/pull/18826) | Windows Terminal | DECSET 2026 |

### 第三方工具

| 工具 | 說明 |
|------|------|
| [claude-chill](https://github.com/davidbeesley/claude-chill) | Rust PTY proxy，VT100 差異渲染 |
| [tmux-claude-code](https://github.com/sethdford/tmux-claude-code) | tmux 最佳化配置 |

### 技術文章

- [The Signature Flicker — Peter Steinberger](https://steipete.me/posts/2025/signature-flicker) — Ink 架構問題分析
- [HN: Claude Code TUI engineer 回覆](https://news.ycombinator.com/item?id=46701013) — Anthropic 工程師技術說明
- [HN: claude-chill 討論](https://news.ycombinator.com/item?id=46699072) — PTY proxy 方案討論
- [Synchronized Output Spec](https://gist.github.com/christianparpart/d8a62cc1ab659194337d73e399004036) — 官方規格
