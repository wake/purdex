# Purdex Web P1 — daemon 靜態託管 SPA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 daemon 在 `/` 提供 build 好的 SPA，並以明確路由/中介層矩陣確保 `/api/*`、`/ws/*` 仍走既有 auth，靜態殼在 auth 之前——使 `https://purdex.mlab.host/` 純瀏覽器可載入 app。

**Architecture:** 新增 `internal/webui` 套件：`embed.FS`（production 烘焙）+ `PDX_SPA_DIR` 磁碟覆寫（dev 迭代），對外 `Handler(spaDir)` 回傳含 SPA history fallback、GET/HEAD 限定、path-traversal 安全（`fs.FS` + `http.FileServerFS`）的 handler。`cmd/pdx/main.go` 的 outer mux 抽成可測函式 `buildHTTPHandler(...)`，套用路由矩陣。

**Tech Stack:** Go / net/http（Go 1.22+ ServeMux method+pattern 路由）/ `embed` / `io/fs`。

## Global Constraints

- **路由 / 中介層矩陣（不得偏離）**：
  | 路徑 | 中介層 | 目的 |
  |---|---|---|
  | `GET /api/health` | `CORS` only | 維持現況 bypass |
  | `/api/` prefix | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth`→`mux` | protected |
  | `/ws/` prefix | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth`→`mux` | protected；三條 WS 皆 `/ws/...` |
  | 其餘（static/SPA fallback） | `CORS`→`IPWhitelist`（**保留**）→ **bypass `PairingGuard` 與 `TokenAuth`** | 靜態殼 pre-auth |
- 靜態 handler **僅接受 `GET`/`HEAD`**；非此二者回 `405`。
- SPA history fallback：requested path 不對應實體檔時回 `index.html`（讓前端路由運作），**但 `/api/` 與 `/ws/` 不經過 static**（由 outer mux 前綴路由保證）。
- **Path traversal 安全**：一律經 `io/fs`（`fs.Sub` / `os.DirFS`）+ `http.FileServerFS`，**禁止**手刻 `filepath.Join(dir, r.URL.Path)`。
- 已驗證：repo 內所有 daemon 路由都在 `/api/` 或 `/ws/` 前綴下（無裸路徑），故 static 不會吃到任何 API/WS 路由。
- 現有 middleware 簽章：`middleware.CORS(h)`、`middleware.IPWhitelist(cfg.Allow)(h)`、`middleware.PairingGuard(func() bool)(h)`、`middleware.TokenAuth(func() string, tickets)(h)`。
- 測試：`go test ./...`；建置：`go build ./...`（在 worktree 根目錄）。
- 每個 task 獨立 commit。

---

## File Structure

- **Create** `internal/webui/webui.go` — `Handler(spaDir string) (http.Handler, error)`：選 fsys（`spaDir` 非空→`os.DirFS`，否則 `fs.Sub(embedded,"dist")`）；SPA fallback；GET/HEAD 限定。單一責任。
- **Create** `internal/webui/embed.go` — `//go:embed all:dist` → `var embedded embed.FS`。
- **Create** `internal/webui/dist/index.html` — 佔位頁（production build 會以真 `spa/dist` 覆蓋；確保 `go:embed` 可編譯）。
- **Create** `internal/webui/webui_test.go` — Handler 行為測試（用 temp dir 當 spaDir）。
- **Modify** `.gitignore` — 忽略 `internal/webui/dist/` 內除 `index.html` 佔位以外的建置產物（`assets/`、`icons/` 等）。
- **Modify** `cmd/pdx/main.go` — 抽出 `buildHTTPHandler(...)`；套路由矩陣；讀 `PDX_SPA_DIR`；`Handler` 掛入 outer mux。
- **Create** `cmd/pdx/serve_routing_test.go` — 路由矩陣測試（health bypass / `/api/` 與 `/ws/` protected / static 服務 / `/ws/` 不被 fallback 吃 / token-on 仍載靜態殼）。
- **Modify** `CLAUDE.md`（「打包與更新」段）— 補 production embed 的 build 步驟與 dev 的 `PDX_SPA_DIR` 用法。

---

## Task 1: internal/webui 套件（embed + dev-dir override + SPA fallback）

**Files:**
- Create: `internal/webui/webui.go`, `internal/webui/embed.go`, `internal/webui/dist/index.html`
- Modify: `.gitignore`
- Test: `internal/webui/webui_test.go`

**Interfaces:**
- Produces: `func webui.Handler(spaDir string) (http.Handler, error)` — `spaDir==""` 用 embedded；否則用磁碟目錄。

- [ ] **Step 1: 佔位頁 + embed + .gitignore**

建 `internal/webui/dist/index.html`：

```html
<!doctype html>
<meta charset="utf-8">
<title>Purdex</title>
<!-- Placeholder. Production builds overwrite internal/webui/dist with spa/dist.
     Dev serves the real SPA via PDX_SPA_DIR (see CLAUDE.md). -->
<body>Purdex SPA placeholder — build the SPA to populate this.</body>
```

建 `internal/webui/embed.go`：

```go
package webui

import "embed"

// embedded holds the production SPA build. During dev the daemon serves from
// PDX_SPA_DIR instead (see Handler). The committed dist/index.html is a
// placeholder; production builds overwrite internal/webui/dist with spa/dist.
//
//go:embed all:dist
var embedded embed.FS
```

於 `.gitignore` 末尾加入（忽略建置產物但保留佔位頁）：

```gitignore
# Purdex web (P1): embedded SPA build artifacts — populated at build time,
# placeholder index.html is tracked so go:embed always compiles.
/internal/webui/dist/*
!/internal/webui/dist/index.html
```

- [ ] **Step 2: 寫失敗測試**

```go
// internal/webui/webui_test.go
package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// tmpSPA writes a minimal SPA tree to a temp dir and returns its path.
func tmpSPA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ROOT</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("APP_JS"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doReq(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler_ServesAsset(t *testing.T) {
	h, err := Handler(tmpSPA(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, h, "GET", "/assets/app.js")
	if rec.Code != 200 || rec.Body.String() != "APP_JS" {
		t.Fatalf("asset: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_ServesIndexAtRoot(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "GET", "/")
	if rec.Code != 200 || rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("root: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_SPAFallbackForUnknownPath(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "GET", "/some/client/route")
	if rec.Code != 200 || rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("fallback: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandler_RejectsNonGet(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "POST", "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got %d, want 405", rec.Code)
	}
}

func TestHandler_NoPathTraversal(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	// A traversal attempt must not escape the SPA root. With fs.FS semantics
	// the cleaned path stays contained; worst case it falls back to index.
	rec := doReq(t, h, "GET", "/../../etc/passwd")
	if rec.Code == 200 && rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("traversal leaked: %q", rec.Body.String())
	}
}
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `go test ./internal/webui/`
Expected: FAIL（`undefined: Handler`）。

- [ ] **Step 4: 實作 Handler**

```go
// internal/webui/webui.go
package webui

import (
	"io/fs"
	"net/http"
	"os"
	"path"
)

// Handler serves the SPA. When spaDir is non-empty it serves that directory
// from disk (dev iteration via PDX_SPA_DIR); otherwise it serves the embedded
// production build. Unknown paths fall back to index.html (SPA history
// routing). Only GET/HEAD are accepted — callers route /api/* and /ws/* to
// the protected mux before reaching here.
func Handler(spaDir string) (http.Handler, error) {
	var fsys fs.FS
	if spaDir != "" {
		fsys = os.DirFS(spaDir)
	} else {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, err
		}
		fsys = sub
	}

	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Resolve to an fs path. fs.FS rejects paths with ".." elements, so
		// traversal cannot escape the SPA root.
		p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if p == "" || p == "." {
			p = "index.html"
		}

		if info, err := fs.Stat(fsys, p); err != nil || info.IsDir() {
			// Unknown path (or a directory) → SPA history fallback to index.html.
			serveIndex(w, r, fsys)
			return
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	data, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
```

於 import 區加入 `"strings"`（`path.Clean` 與 `strings.TrimPrefix` 都會用到）：確保 import 為 `io/fs`、`net/http`、`os`、`path`、`strings`。

- [ ] **Step 5: 跑測試確認通過**

Run: `go test ./internal/webui/`
Expected: PASS（5 tests）。

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add internal/webui/ .gitignore
git commit -m "feat(webui): SPA serving handler with embed + PDX_SPA_DIR override (P1)"
```

---

## Task 2: main.go 路由矩陣（抽成可測函式 + wiring）

**Files:**
- Modify: `cmd/pdx/main.go`
- Test: `cmd/pdx/serve_routing_test.go`

**Interfaces:**
- Consumes: `webui.Handler`（Task 1）。
- Produces: `func buildHTTPHandler(inner http.Handler, spa http.Handler, allow []string, isPairing func() bool, tokenFn func() string, tickets middleware.TicketValidator, health http.Handler) http.Handler` — 組出套用路由矩陣的 outer handler。**已確認型別**：`tickets` 用介面 `middleware.TicketValidator`（`c.Tickets` 為 `*core.TicketStore`，實作此介面，可直接傳入；test 傳 `nil` 亦合法）。module path = `github.com/wake/purdex`，故 import `github.com/wake/purdex/internal/webui`。

- [ ] **Step 1: 寫失敗測試**

```go
// cmd/pdx/serve_routing_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubHandler records whether it was reached and returns a marker.
func stubHandler(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(marker))
	})
}

func buildTestHandler(tokenFn func() string) http.Handler {
	inner := http.NewServeMux()
	inner.Handle("GET /api/info", stubHandler("API_INFO"))
	inner.Handle("/ws/host-events", stubHandler("WS"))
	spa := stubHandler("SPA")
	health := stubHandler("HEALTH")
	return buildHTTPHandler(inner, spa, nil /*allow=all*/, func() bool { return false }, tokenFn, nil, health)
}

func req(t *testing.T, h http.Handler, method, target string, auth string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRouting_HealthBypassesAuth(t *testing.T) {
	h := buildTestHandler(func() string { return "SEKRIT" }) // token ON
	rec := req(t, h, "GET", "/api/health", "")
	if rec.Body.String() != "HEALTH" {
		t.Fatalf("health: got %q", rec.Body.String())
	}
}

func TestRouting_StaticServedWithoutToken(t *testing.T) {
	h := buildTestHandler(func() string { return "SEKRIT" }) // token ON
	// Static shell must load even when a token is required (pre-auth).
	rec := req(t, h, "GET", "/", "")
	if rec.Body.String() != "SPA" {
		t.Fatalf("static: got %q (code %d)", rec.Body.String(), rec.Code)
	}
	rec2 := req(t, h, "GET", "/assets/app.js", "")
	if rec2.Body.String() != "SPA" {
		t.Fatalf("static asset: got %q", rec2.Body.String())
	}
}

func TestRouting_ApiRequiresAuthWhenTokenSet(t *testing.T) {
	h := buildTestHandler(func() string { return "SEKRIT" }) // token ON
	rec := req(t, h, "GET", "/api/info", "") // no auth
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api no-auth: got %d, want 401", rec.Code)
	}
	rec2 := req(t, h, "GET", "/api/info", "Bearer SEKRIT")
	if rec2.Body.String() != "API_INFO" {
		t.Fatalf("api with-auth: got %q (code %d)", rec2.Body.String(), rec2.Code)
	}
}

func TestRouting_WsNotEatenByStaticFallback(t *testing.T) {
	h := buildTestHandler(func() string { return "" }) // token OFF
	// /ws/* must route to the protected mux (WS marker), not the SPA fallback.
	rec := req(t, h, "GET", "/ws/host-events", "")
	if rec.Body.String() != "WS" {
		t.Fatalf("ws routing: got %q — static fallback ate the WS route", rec.Body.String())
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./cmd/pdx/ -run TestRouting`
Expected: FAIL（`undefined: buildHTTPHandler`）。

- [ ] **Step 3: 實作 buildHTTPHandler + 改 wiring**

在 `cmd/pdx/main.go` 新增函式（放在 `main` 之外、檔案下方）：

```go
// buildHTTPHandler applies the P1 routing/middleware matrix:
//   GET /api/health         → CORS only (bypass)
//   /api/ , /ws/ (prefix)   → CORS→IPWhitelist→PairingGuard→TokenAuth→inner
//   everything else         → CORS→IPWhitelist→spa (static shell, pre-auth)
// All daemon routes live under /api/ or /ws/, so the static catch-all never
// shadows an API/WS route; the SPA handler itself restricts to GET/HEAD.
func buildHTTPHandler(
	inner http.Handler,
	spa http.Handler,
	allow []string,
	isPairing func() bool,
	tokenFn func() string,
	tickets middleware.TicketValidator, // c.Tickets (*core.TicketStore) satisfies this
	health http.Handler,
) http.Handler {
	protected := func(h http.Handler) http.Handler {
		return middleware.CORS(
			middleware.IPWhitelist(allow)(
				middleware.PairingGuard(isPairing)(
					middleware.TokenAuth(tokenFn, tickets)(h))))
	}

	outer := http.NewServeMux()
	outer.Handle("GET /api/health", middleware.CORS(health))
	outer.Handle("/api/", protected(inner))
	outer.Handle("/ws/", protected(inner))
	outer.Handle("/", middleware.CORS(middleware.IPWhitelist(allow)(spa)))
	return outer
}
```

> 型別已確認：`tickets` 用 `middleware.TicketValidator`（介面），`c.Tickets`（`*core.TicketStore`）實作它，`buildHTTPHandler(..., c.Tickets, ...)` 直接傳入即可。`core` import 在 `main.go` 已存在（`core.StatePairing`），無需為此新增。

在 `main` 內，把現有的：

```go
	outerMux := http.NewServeMux()
	outerMux.Handle("GET /api/health", middleware.CORS(
		http.HandlerFunc(c.HandleHealth)))
	outerMux.Handle("/", middleware.CORS(
		middleware.IPWhitelist(cfg.Allow)(
			middleware.PairingGuard(func() bool {
				return c.Pairing.Get() == core.StatePairing
			})(
				middleware.TokenAuth(func() string {
					c.CfgMu.RLock()
					defer c.CfgMu.RUnlock()
					return c.Cfg.Token
				}, c.Tickets)(mux)))))
```

替換為：

```go
	spaHandler, err := webui.Handler(os.Getenv("PDX_SPA_DIR"))
	if err != nil {
		log.Fatalf("webui handler: %v", err)
	}
	isPairing := func() bool { return c.Pairing.Get() == core.StatePairing }
	tokenFn := func() string {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return c.Cfg.Token
	}
	outerMux := buildHTTPHandler(
		mux, spaHandler, cfg.Allow, isPairing, tokenFn, c.Tickets,
		http.HandlerFunc(c.HandleHealth),
	)
```

並確認 `cmd/pdx/main.go` import 區含 `"os"`、`"github.com/<module>/internal/webui"`（module path 依 `go.mod`）。`srv.Handler = outerMux` 之後維持不變（`outerMux` 現為 `http.Handler`，`http.Server{Handler: outerMux}` 需相容——`buildHTTPHandler` 回傳 `http.Handler`，直接指派即可）。

> 若 `srv := &http.Server{ Handler: outerMux }` 因型別（原為 `*http.ServeMux`）需調整，把欄位型別視為 `http.Handler` 即可（`http.Server.Handler` 本就是 `http.Handler`）。

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./cmd/pdx/ -run TestRouting`
Expected: PASS（4 tests）。

- [ ] **Step 5: 全套 daemon 測試 + build**

Run: `go test ./...`
Expected: 全綠（既有 events_test 等不受影響——路由改為前綴分流，`/api/health` 與各 `/api/`、`/ws/` 行為等價）。
Run: `go build ./...`
Expected: 成功。

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add cmd/pdx/main.go cmd/pdx/serve_routing_test.go
git commit -m "feat(serve): routing/middleware matrix with static SPA pre-auth (P1)"
```

---

## Task 3: build 步驟 + dev 用法文件

**Files:**
- Modify: `CLAUDE.md`（「打包與更新」段落）

**Interfaces:** 無程式碼介面；文件化 production embed build 與 dev `PDX_SPA_DIR`。

- [ ] **Step 1: 於 CLAUDE.md「打包與更新」段補入下列小節**

```markdown
### Web 版靜態託管（P1）

- **Dev（本分支迭代）**：daemon 以環境變數 `PDX_SPA_DIR` 指向已 build 的 SPA 目錄即可即時服務，不必重編 Go binary：
  `cd spa && pnpm run build`（產出 `spa/dist`）→ 啟動 daemon 時帶 `PDX_SPA_DIR=<repo>/spa/dist`。
- **Production（單一 binary）**：build 前把 SPA 產出複製進 embed 目錄再編 Go：
  `cd spa && pnpm run build && rm -rf ../internal/webui/dist && mkdir -p ../internal/webui/dist && cp -r dist/* ../internal/webui/dist/ && cd .. && go build ./cmd/pdx`
  （`internal/webui/dist/` 的建置產物已於 `.gitignore` 忽略，僅 `index.html` 佔位頁入版控以確保 `go:embed` 恆可編譯。）
- **掛 `purdex.mlab.host`**：於 repo 根 `herd proxy purdex.mlab http://127.0.0.1:7860`（或既有 valet proxy 機制），TLS 走 `*.mlab.host` wildcard 憑證。daemon 須綁可達位址（`bind` = tailnet IP 或 `127.0.0.1`，視 proxy 而定）。
```

- [ ] **Step 2: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add CLAUDE.md
git commit -m "docs(webui): document P1 SPA build/serve (embed + PDX_SPA_DIR + proxy)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `go test ./...` — 全綠。
- [ ] `go build ./...` — 成功。
- [ ] 手動 smoke（主 Claude，於 worktree）：`cd spa && pnpm run build`，然後以 `PDX_SPA_DIR=$(pwd)/spa/dist` 啟一個臨時 daemon（非動 mlab live daemon），`curl -s localhost:<port>/` 應回 SPA index、`curl -s localhost:<port>/api/health` 應回 health JSON、`curl -so /dev/null -w '%{http_code}' localhost:<port>/some/spa/route` 應為 200（fallback）。
- [ ] **交付使用者手動步驟**（碰 mlab live daemon，不由主 Claude 執行）：Mini 重 build `bin/pdx`、以 `PDX_SPA_DIR` 或 embed 方式啟動、`herd proxy purdex.mlab → :7860`，瀏覽器驗 `https://purdex.mlab.host/`。

---

## Self-Review 對照 spec

- **spec §5.1 靜態託管 + embed.FS/fs.Sub/FileServerFS** → Task 1（`internal/webui`，`http.FileServerFS` + `fs.FS`，無手刻 join）。✅
- **spec §5.1 路由/中介層矩陣**（health bypass / `/api/`、`/ws/` protected / static 保留 IPWhitelist 繞 TokenAuth+PairingGuard / 僅 GET·HEAD / `/ws/` 不被 fallback 吃）→ Task 2（`buildHTTPHandler` + 4 routing tests）。✅
- **spec §6.4 靜態殼 pre-auth、token 開啟時仍載得出** → `TestRouting_StaticServedWithoutToken`（token ON 仍回 SPA）。✅
- **spec §5.1 Dev vs Prod serving** → Task 1（`PDX_SPA_DIR` override）+ Task 3（文件）。✅
- **spec §6 防繞路**：static handler 不連任何 daemon、SPA 內連線仍走 P0 顯式 host+ticket（本 phase 不改前端連線）。✅
- 無 placeholder；`buildHTTPHandler` 簽章跨 Task 2 test/impl 一致（`tickets` 型別以 `c.Tickets` 實際型別對齊）。✅
