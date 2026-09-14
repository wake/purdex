# Purdex

人與 agent 協作的工作站。把 tmux session、Claude Code 串流、對話式 agent 統一在一個介面，跨 workspace、跨 host 並行作業。

> 仍在 alpha (`1.0.0-alpha.233`)，僅支援 macOS。原名 `tmux-box` / `tmux-ai-term`。

## 能做什麼

- 多 workspace 並行，每個 workspace 內可開多 tab、tab 可分割多 pane
- Agent 三種模式：`terminal` / `stream(wrap)` / `對話`
- 跨機（Tailnet）多 host 操作
- 整合 Claude Code、Codex、OpenCode 的 hook 與狀態指示

詞彙與設計定錨見 [`PRODUCT.md`](./PRODUCT.md)。

## 技術棧

Go daemon + React 19 SPA + Electron 41 殼。

## 開發

```bash
cd spa && pnpm install
pnpm dev         # SPA dev server
pnpm test        # vitest
pnpm build       # SPA build
```

```bash
pnpm electron:build   # 從 root；產出 dist/mac/ + dist/mac-arm64/
```

環境與跨機開發流程見 [`CLAUDE.md`](./CLAUDE.md)。

## 建置

Daemon（Go）需要存取 private module `lab.protype.tw/wake/nexen`。建置前，
這台機器需完成兩項**一次性**設定：

```sh
go env -w GOPRIVATE=lab.protype.tw
git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"
```

`make build`（或單獨 `make check-goenv`）會先驗證這兩項，缺一就印出對應指令
並以非零狀態結束，不會進入 `go build`。

冷快取驗證（確認上述設定不依賴呼叫端的 shell 環境變數也能運作；spec §6 step 0）：

```sh
GOWORK=off GOMODCACHE=$(mktemp -d) go build ./cmd/pdx
```

## 文件

- [`PRODUCT.md`](./PRODUCT.md) — 產品定位
- [`CLAUDE.md`](./CLAUDE.md) — 開發流程
- [`CHANGELOG.md`](./CHANGELOG.md) — 版本歷史

## License

[MIT](./LICENSE)
