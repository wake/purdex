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

## 文件

- [`PRODUCT.md`](./PRODUCT.md) — 產品定位
- [`CLAUDE.md`](./CLAUDE.md) — 開發流程
- [`CHANGELOG.md`](./CHANGELOG.md) — 版本歷史

## License

[MIT](./LICENSE)
