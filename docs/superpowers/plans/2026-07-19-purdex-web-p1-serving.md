# Purdex Web P1 — daemon 靜態託管 SPA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **修訂 r2**（codex plan review 後）：dev override 改 `os.OpenRoot` containment；fallback 只在 stat 失敗時觸發（保留目錄語意）；`Handler` fail-fast 驗 `index.html`；補 IPWhitelist / pairing-mode / 裸路徑 redirect / OPTIONS preflight / HEAD 測試。

**Goal:** 讓 daemon 在 `/` 提供 build 好的 SPA，並以明確路由/中介層矩陣確保 `/api/*`、`/ws/*` 仍走既有 auth，靜態殼在 auth 之前——使 `https://purdex.mlab.host/` 純瀏覽器可載入 app。

**Architecture:** 新增 `internal/webui` 套件：`embed.FS`（production 烘焙）+ `PDX_SPA_DIR` 磁碟覆寫（dev 迭代，`os.OpenRoot` containment），對外 `Handler(spaDir)` 回傳含 SPA history fallback、GET/HEAD 限定、fail-fast 的 handler。`cmd/pdx/main.go` 的 outer mux 抽成可測函式 `buildHTTPHandler(...)`，套用路由矩陣。

**Tech Stack:** Go 1.25 / net/http（Go 1.22+ ServeMux method+pattern 路由）/ `embed` / `io/fs` / `os.OpenRoot`（Go 1.24+）。

## Global Constraints

- **路由 / 中介層矩陣（不得偏離）**：
  | 路徑 | 中介層 | 目的 |
  |---|---|---|
  | `GET /api/health` | `CORS` only | 維持現況 bypass |
  | `/api/` prefix | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth`→`mux` | protected |
  | `/ws/` prefix | `CORS`→`IPWhitelist`→`PairingGuard`→`TokenAuth`→`mux` | protected；三條 WS 皆 `/ws/...` |
  | 其餘（static/SPA fallback） | `CORS`→`IPWhitelist`（**保留**）→ **bypass `PairingGuard` 與 `TokenAuth`** | 靜態殼 pre-auth |
- 靜態 handler **僅接受 `GET`/`HEAD`**；非此二者回 `405`。**CORS preflight（`OPTIONS`）由最外層 `CORS` middleware 回 `204`，不會進到 static handler 的 405 分支。**
- SPA history fallback：requested path **無法 stat 到既有 entry**（未知 client route、或 invalid/traversal 路徑）時回 `index.html`；**既有的檔案與目錄一律交給 `http.FileServerFS`**（保留標準靜態/目錄語意）。`/api/`、`/ws/` 不經 static（outer mux 前綴路由保證）。
- **Path 安全**：embedded 用 `fs.Sub(embedded,"dist")`；dev override 用 `os.OpenRoot(spaDir).FS()`（symlink-contained，不會逃出 `spaDir`；Go 1.24+）。**禁止**手刻 `filepath.Join(dir, r.URL.Path)`。
- `Handler` **fail-fast**：建構時 `fs.Stat(fsys,"index.html")`，缺檔即回 error（dev 路徑打錯/未 build 時立即失敗，而非首個請求才 404）。
- 已驗證：repo 內所有 daemon 路由都在 `/api/` 或 `/ws/` 前綴下（無裸路徑），故 static 不會吃到任何 API/WS 路由。
- 現有 middleware：`middleware.CORS(h)`（OPTIONS→204）、`middleware.IPWhitelist(cfg.Allow)(h)`（`allow` 為空→放行；非空比對 `RemoteAddr`，不符回 `403`）、`middleware.PairingGuard(func()bool)(h)`（pairing 且非 `/api/pair/`→`503`）、`middleware.TokenAuth(func()string, middleware.TicketValidator)(h)`（token 為空→放行；否則需 Bearer/ticket，不符 `401`）。
- 測試：`go test ./...`；建置：`go build ./...`（worktree 根）。
- 每個 task 獨立 commit。

---

## File Structure

- **Create** `internal/webui/webui.go` — `Handler(spaDir string) (http.Handler, error)`。
- **Create** `internal/webui/embed.go` — `//go:embed all:dist` → `var embedded embed.FS`。
- **Create** `internal/webui/dist/index.html` — 佔位頁（production build 以真 `spa/dist` 覆蓋；確保 `go:embed` 可編譯）。
- **Create** `internal/webui/webui_test.go` — Handler 行為測試（temp dir 當 spaDir）。
- **Modify** `.gitignore` — 忽略 `internal/webui/dist/` 內除 `index.html` 佔位外的建置產物。
- **Modify** `cmd/pdx/main.go` — 抽 `buildHTTPHandler(...)`；套路由矩陣；讀 `PDX_SPA_DIR`。
- **Create** `cmd/pdx/serve_routing_test.go` — 路由矩陣測試。
- **Modify** `CLAUDE.md`（「打包與更新」段）— production embed build 步驟 + dev `PDX_SPA_DIR`。

---

## Task 1: internal/webui 套件

**Files:**
- Create: `internal/webui/webui.go`, `internal/webui/embed.go`, `internal/webui/dist/index.html`
- Modify: `.gitignore`
- Test: `internal/webui/webui_test.go`

**Interfaces:**
- Produces: `func webui.Handler(spaDir string) (http.Handler, error)` — `spaDir==""` 用 embedded；否則用磁碟目錄（`os.OpenRoot`）。缺 `index.html` 回 error。

- [ ] **Step 1: 佔位頁 + embed + .gitignore**

`internal/webui/dist/index.html`：

```html
<!doctype html>
<meta charset="utf-8">
<title>Purdex</title>
<!-- Placeholder. Production builds overwrite internal/webui/dist with spa/dist.
     Dev serves the real SPA via PDX_SPA_DIR (see CLAUDE.md). -->
<body>Purdex SPA placeholder — build the SPA to populate this.</body>
```

`internal/webui/embed.go`：

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

`.gitignore` 末尾：

```gitignore
# Purdex web (P1): embedded SPA build artifacts — populated at build time.
# The placeholder index.html stays tracked so go:embed always compiles.
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

func TestHandler_HeadRequests(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	// HEAD on an unknown client route must still succeed (fallback), body empty.
	rec := doReq(t, h, "HEAD", "/some/client/route")
	if rec.Code != 200 {
		t.Fatalf("HEAD fallback: got %d", rec.Code)
	}
	rec2 := doReq(t, h, "HEAD", "/assets/app.js")
	if rec2.Code != 200 {
		t.Fatalf("HEAD asset: got %d", rec2.Code)
	}
}

func TestHandler_RejectsNonGetHead(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	rec := doReq(t, h, "POST", "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got %d, want 405", rec.Code)
	}
}

func TestHandler_NoPathTraversal(t *testing.T) {
	h, _ := Handler(tmpSPA(t))
	// A traversal attempt must not escape the SPA root; worst case it falls
	// back to index.html (never leaks a file outside the root).
	rec := doReq(t, h, "GET", "/../../etc/passwd")
	if rec.Code == 200 && rec.Body.String() != "<html>ROOT</html>" {
		t.Fatalf("traversal leaked: %q", rec.Body.String())
	}
}

func TestHandler_FailFastMissingIndex(t *testing.T) {
	// A dir without index.html must fail at construction, not at first request.
	if _, err := Handler(t.TempDir()); err == nil {
		t.Fatal("expected error for missing index.html, got nil")
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
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// Handler serves the SPA. When spaDir is non-empty it serves that directory
// from disk via os.OpenRoot (symlink-contained; cannot escape spaDir) for dev
// iteration through PDX_SPA_DIR; otherwise it serves the embedded production
// build. Unknown paths fall back to index.html (SPA history routing). Only
// GET/HEAD are accepted — callers route /api/* and /ws/* to the protected mux
// before reaching here, and CORS handles OPTIONS preflight upstream.
func Handler(spaDir string) (http.Handler, error) {
	var fsys fs.FS
	if spaDir != "" {
		root, err := os.OpenRoot(spaDir)
		if err != nil {
			return nil, fmt.Errorf("webui: open spa dir %q: %w", spaDir, err)
		}
		fsys = root.FS()
	} else {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, err
		}
		fsys = sub
	}

	// Fail-fast: the SPA is unusable without index.html.
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, fmt.Errorf("webui: index.html missing under spa root: %w", err)
	}

	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if p == "" || p == "." {
			p = "index.html"
		}

		// SPA history fallback: a path that doesn't stat to an existing entry
		// (unknown client route, or an invalid/traversal path) serves
		// index.html. Existing files AND directories fall through to
		// FileServerFS, preserving standard static/dir semantics.
		if _, err := fs.Stat(fsys, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}
```

- [ ] **Step 5: 跑測試確認通過**

Run: `go test ./internal/webui/`
Expected: PASS（7 tests）。

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add internal/webui/ .gitignore
git commit -m "feat(webui): SPA serving handler (embed + PDX_SPA_DIR via os.OpenRoot) (P1)"
```

---

## Task 2: main.go 路由矩陣

**Files:**
- Modify: `cmd/pdx/main.go`
- Test: `cmd/pdx/serve_routing_test.go`

**Interfaces:**
- Consumes: `webui.Handler`（Task 1）。
- Produces: `func buildHTTPHandler(inner http.Handler, spa http.Handler, allow []string, isPairing func() bool, tokenFn func() string, tickets middleware.TicketValidator, health http.Handler) http.Handler`。**型別已確認**：`tickets` 用介面 `middleware.TicketValidator`（`c.Tickets` 為 `*core.TicketStore`，實作它；test 傳 `nil` 合法）。`core` import 在 `main.go` 已存在。

- [ ] **Step 1: 寫失敗測試**

```go
// cmd/pdx/serve_routing_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func stubHandler(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(marker))
	})
}

// buildTestHandler wires a minimal inner mux + SPA/health stubs through the
// real buildHTTPHandler with the given auth/whitelist/pairing knobs.
func buildTestHandler(tokenFn func() string, allow []string, isPairing func() bool) http.Handler {
	inner := http.NewServeMux()
	inner.Handle("GET /api/info", stubHandler("API_INFO"))
	inner.Handle("/ws/host-events", stubHandler("WS"))
	spa := stubHandler("SPA")
	health := stubHandler("HEALTH")
	return buildHTTPHandler(inner, spa, allow, isPairing, tokenFn, nil, health)
}

func req(t *testing.T, h http.Handler, method, target, auth, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if remote != "" {
		r.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func tokenOn() string  { return "SEKRIT" }
func tokenOff() string { return "" }
func noPairing() bool  { return false }

func TestRouting_HealthBypassesAuth(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	rec := req(t, h, "GET", "/api/health", "", "")
	if rec.Body.String() != "HEALTH" {
		t.Fatalf("health: got %q", rec.Body.String())
	}
}

func TestRouting_StaticServedWithoutToken(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing) // token ON
	rec := req(t, h, "GET", "/", "", "")
	if rec.Body.String() != "SPA" {
		t.Fatalf("static root: got %q (code %d)", rec.Body.String(), rec.Code)
	}
	rec2 := req(t, h, "GET", "/assets/app.js", "", "")
	if rec2.Body.String() != "SPA" {
		t.Fatalf("static asset: got %q", rec2.Body.String())
	}
}

func TestRouting_ApiRequiresAuthWhenTokenSet(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	rec := req(t, h, "GET", "/api/info", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api no-auth: got %d, want 401", rec.Code)
	}
	rec2 := req(t, h, "GET", "/api/info", "Bearer SEKRIT", "")
	if rec2.Body.String() != "API_INFO" {
		t.Fatalf("api with-auth: got %q (code %d)", rec2.Body.String(), rec2.Code)
	}
}

func TestRouting_WsNotEatenByStaticFallback(t *testing.T) {
	h := buildTestHandler(tokenOff, nil, noPairing)
	rec := req(t, h, "GET", "/ws/host-events", "", "")
	if rec.Body.String() != "WS" {
		t.Fatalf("ws routing: got %q — static fallback ate the WS route", rec.Body.String())
	}
}

func TestRouting_StaticStillSubjectToIPWhitelist(t *testing.T) {
	h := buildTestHandler(tokenOff, []string{"127.0.0.1"}, noPairing)
	// Disallowed source (httptest default RemoteAddr 192.0.2.1) → static blocked.
	rec := req(t, h, "GET", "/", "", "192.0.2.1:1234")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("static from disallowed IP: got %d, want 403", rec.Code)
	}
	// Allowed source → static served.
	rec2 := req(t, h, "GET", "/", "", "127.0.0.1:1234")
	if rec2.Body.String() != "SPA" {
		t.Fatalf("static from allowed IP: got %q (code %d)", rec2.Body.String(), rec2.Code)
	}
}

func TestRouting_PairingModeBlocksApiButNotStatic(t *testing.T) {
	inPairing := func() bool { return true }
	h := buildTestHandler(tokenOff, nil, inPairing)
	// Static shell must load during pairing (bypasses PairingGuard).
	recStatic := req(t, h, "GET", "/", "", "")
	if recStatic.Body.String() != "SPA" {
		t.Fatalf("pairing static: got %q (code %d)", recStatic.Body.String(), recStatic.Code)
	}
	// A non-pairing API route is blocked with 503 while pairing.
	recApi := req(t, h, "GET", "/api/info", "", "")
	if recApi.Code != http.StatusServiceUnavailable {
		t.Fatalf("pairing /api/info: got %d, want 503", recApi.Code)
	}
}

func TestRouting_BareApiPrefixRedirects(t *testing.T) {
	h := buildTestHandler(tokenOff, nil, noPairing)
	// Go 1.22+ ServeMux redirects the bare prefix "/api" → "/api/". Lock this
	// documented behavior change (no client depends on bare /api or /ws).
	rec := req(t, h, "GET", "/api", "", "")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("/api redirect: got %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/" {
		t.Fatalf("/api redirect Location: got %q, want /api/", loc)
	}
}

func TestRouting_OptionsPreflightHandledByCORS(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	// CORS middleware answers OPTIONS with 204 before reaching the SPA handler.
	rec := req(t, h, "OPTIONS", "/", "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS preflight: got %d, want 204", rec.Code)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./cmd/pdx/ -run TestRouting`
Expected: FAIL（`undefined: buildHTTPHandler`）。

- [ ] **Step 3: 實作 buildHTTPHandler + 改 wiring**

`cmd/pdx/main.go` 新增函式（`main` 之外）：

```go
// buildHTTPHandler applies the P1 routing/middleware matrix:
//   GET /api/health         → CORS only (bypass)
//   /api/ , /ws/ (prefix)   → CORS→IPWhitelist→PairingGuard→TokenAuth→inner
//   everything else         → CORS→IPWhitelist→spa (static shell, pre-auth)
// All daemon routes live under /api/ or /ws/, so the static catch-all never
// shadows an API/WS route; the SPA handler itself restricts to GET/HEAD and
// CORS answers OPTIONS upstream.
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

在 `main` 內，把現有的（`cmd/pdx/main.go:193-205`）：

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

Import：確認 `cmd/pdx/main.go` 含 `"os"` 與 `"github.com/wake/purdex/internal/webui"`。`err` 若在該作用域已宣告，用 `spaHandler, err := ...` 前確認無重複宣告衝突（必要時改 `spaHandler, herr := webui.Handler(...)` 並檢查 `herr`）。`srv := &http.Server{ Handler: outerMux }` 中 `Handler` 欄位本為 `http.Handler`，`buildHTTPHandler` 回傳 `http.Handler`，直接指派相容。

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./cmd/pdx/ -run TestRouting`
Expected: PASS（8 tests）。

- [ ] **Step 5: 全套 daemon 測試 + build**

Run: `go test ./...`
Expected: 全綠（路由改前綴分流，各既有 `/api/`、`/ws/` 行為等價）。
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

- [ ] **Step 1: 於 CLAUDE.md「打包與更新」段補入**

```markdown
### Web 版靜態託管（P1）

- **Dev（本分支迭代）**：daemon 以 `PDX_SPA_DIR` 指向已 build 的 SPA 目錄即可即時服務，不必重編 Go binary：
  `cd spa && pnpm run build`（產出 `spa/dist`）→ 啟動 daemon 時帶 `PDX_SPA_DIR=<repo>/spa/dist`。目錄或 `index.html` 不存在時 daemon 會啟動即失敗（fail-fast）。
- **Production（單一 binary）**：build 前把 SPA 產出複製進 embed 目錄再編 Go：
  `cd spa && pnpm run build && rm -rf ../internal/webui/dist && mkdir -p ../internal/webui/dist && cp -r dist/* ../internal/webui/dist/ && cd .. && go build ./cmd/pdx`
  （`internal/webui/dist/` 的建置產物已於 `.gitignore` 忽略，僅 `index.html` 佔位入版控以確保 `go:embed` 恆可編譯。）
- **掛 `purdex.mlab.host`**：於 repo 根 `herd proxy purdex.mlab http://127.0.0.1:7860`（或既有 valet proxy），TLS 走 `*.mlab.host` wildcard 憑證。daemon 綁可達位址（`bind` 依 proxy 而定）。
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
- [ ] 手動 smoke（主 Claude，於 worktree，**不動 mlab live daemon**）：`cd spa && pnpm run build`，以 `PDX_SPA_DIR=$(pwd)/dist` 啟臨時 daemon（隨機 port），`curl -s localhost:<port>/` 回 SPA index、`/api/health` 回 health JSON、`/some/spa/route` 回 200（fallback）、`/assets/...` 回實體檔。
- [ ] **交付使用者手動步驟**（碰 mlab live daemon）：Mini 重 build/啟動 daemon（`PDX_SPA_DIR` 或 embed）、`herd proxy purdex.mlab → :7860`，瀏覽器驗 `https://purdex.mlab.host/`。

---

## Self-Review 對照 spec

- **spec §5.1 靜態託管 + fs.Sub/FileServerFS + path 安全** → Task 1（`http.FileServerFS` + `fs.Sub`/`os.OpenRoot`，無手刻 join；fail-fast）。✅
- **spec §5.1 路由/中介層矩陣** → Task 2（`buildHTTPHandler` + 8 routing tests，含 health bypass / static pre-auth / `/api` 需 auth / `/ws` 不被 fallback 吃 / **static 仍受 IPWhitelist** / **pairing mode 擋 /api 不擋 static** / 裸路徑 redirect / OPTIONS 204）。✅
- **spec §6.4 靜態殼 pre-auth、token 開啟仍載得出、IPWhitelist 保留** → `TestRouting_StaticServedWithoutToken` + `TestRouting_StaticStillSubjectToIPWhitelist` + `TestRouting_PairingModeBlocksApiButNotStatic`。✅
- **spec §5.1 Dev vs Prod serving** → Task 1（`PDX_SPA_DIR` via `os.OpenRoot`）+ Task 3（文件）。✅
- **spec §6 防繞路**：static handler 不連任何 daemon；SPA 內連線仍走 P0 顯式 host+ticket（本 phase 不改前端連線）。✅
- 無 placeholder；`buildHTTPHandler` 簽章跨 Task 2 test/impl 一致。✅
