# AGENTS.md

## 重要規範

再次詳細讀取 ~/.config/opencode/AGENTS.md，遵守所有語句和表達上的規範和準則。

## 專案邊界

- 這是 `pnpm` workspace；workspace package 只有 `spa/` 與 `electron/`。根目錄 `package.json` 負責 Electron dev/build，Go 走 `Makefile`。
- Go 入口在 `cmd/pdx/main.go`。`serve` 會建立 `internal/core`，再掛 `session`、`stream`、`agent`、`fs`、`logs`、`sync` 模組；`dev` module 只有 `config.Dev.Update=true` 時才會載入。
- Electron 真正的 build wiring 在根目錄 `electron.vite.config.ts`：它同時編 `electron/main.ts`、`electron/preload.ts`、`electron/browser-view-preload.ts`，以及 `spa/` renderer 到 `out/`。

## 常用指令

- Go：`make build`、`make test`、`make lint`。聚焦測試直接用 `go test ./path/to/pkg -count=1`。
- SPA：`pnpm --prefix spa run dev`、`pnpm --prefix spa run lint`、`pnpm --prefix spa run build`、`pnpm --prefix spa exec vitest run [path]`。
- Electron 測試：`pnpm --prefix electron test`。聚焦單檔可用 `pnpm --prefix electron exec vitest run electron/keybindings.test.ts`。
- `pnpm run electron:build` 不是單純 renderer build：它會先跑 `spa/scripts/generate-icon-data.mjs`，再 `electron-vite build`，最後由 `scripts/build-electron.mjs` 產出 `dist/mac/` 與 `dist/mac-arm64/`。

## 易踩坑

- SPA dev server 會把 `/api` 與 `/ws` proxy 到 `localhost:7860`；只跑前端時，daemon 也要在本機 `7860`。
- `spa/scripts/generate-icon-data.mjs` 會重建 `spa/public/icons/*.json` 與 `spa/src/features/workspace/generated/icon-meta.json`；這些是產物，不要手改。
- `out/` 與 `dist/` 都是建置輸出，不要手改。
- Dev update 是雙重 gate：daemon 端要 `config.Dev.Update=true`，而且 `PDX_DEV_MODE=1` 也必須存在，`/api/dev/*` 路由與 Electron preload API 才會真的露出。
- `VERSION` 是版本 SOT；bump 時同步 `package.json` 與 `spa/package.json`。`electron.vite.config.ts` 和 `internal/module/dev` 都直接讀這個檔。
- Locale 變更要同時更新 `spa/src/locales/en.json` 與 `spa/src/locales/zh-TW.json`；repo 內有 completeness tests 會檢查兩邊 key set。

## 工作規則

- 不要直推 `main`。
- 開發新工作一律從最新 `origin/main` 開獨立 worktree；不要直接沿用舊 feature branch / 舊 worktree 當實作基線。
- 先寫 failing test 再實作。
- 每個 task 一個 commit；commit message 用 Conventional Commits，例如 `fix(spa): ...`。
- 依 repo 慣例，工作流程是 `spec -> plan -> subagent / TDD 實作 -> PR -> 兩輪 review -> merge -> bump`。
- 依 repo 慣例，`VERSION` + `CHANGELOG.md` 是 PR merge 後才補的版本提交，不是在一般功能實作 PR 內先改。
