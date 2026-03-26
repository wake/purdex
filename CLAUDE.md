# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 專案概述

**tmux-box** (v1.0.0-alpha.20) — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal、Stream（Claude Code `-p` 串流）、JSONL 三種模式。

- Repo: `git@github.com:wake/tmux-box.git`
- 主分支: `main`（v0 備份在 `v0` 分支）
- 版本: `VERSION` 檔案為 SOT，每個 PR bump

## 開發環境

- **Package manager**: pnpm（不是 npm）
- **Daemon**: `100.64.0.2:7860`（Go binary `bin/tbox`）
- **SPA**: `100.64.0.2:5174`（`spa/`）
- **測試**: `cd spa && npx vitest run`
- **Lint**: `cd spa && pnpm run lint`
- **Build**: `cd spa && pnpm run build`

## 技術棧

- **Daemon**: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite
- **SPA**: React 19 / Vite 8 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6

## 開發流程

- 絕對不能直推 main，必須走 PR + review
- Review 使用多 agent 正反角度審查（攻擊方 + 防守方 + 檔案大小）
- TDD：先寫測試再實作
- 圖示統一使用 Phosphor Icons
