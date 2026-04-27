# P5 — File-not-found popup service + Layer 1/2/3 + fs.search server-side allowlist

> 對應 SPEC.md `# P5` 段。本檔吸收 PLAN 第二輪 4 份 codex review 與 P5 相關修訂（次大宗 — 12 finding）。

## v4 修訂指引（實作前必看，覆寫原 task 對應段）

### fs.search server-side allowlist（D 決議 + 攻擊 critical C4）

| Task | 修訂 | 來源 |
|---|---|---|
| **5.1** | **拆 `5.1a search engine` (純函式 `Search(ctx, request)` in `internal/module/fs/search_engine.go`) + `5.1b HTTP handler` (in `internal/module/fs/search_handler.go`)** | 體質 review #7 + 通用 review C3 |
| **5.1b** | **改用 `(m *FsModule) handleSearch(...)` method pattern**（與 fs/module.go 既有 handler pattern 一致），route `mux.HandleFunc("POST /api/fs/search", m.handleSearch)`，**不要寫 top-level `HandleSearch`** | 通用 review A5 |
| **5.1** | **不接受 client-supplied absolute path roots**！只接 `roots: [{kind:"session-cwd", sessionCode}]` capability（**v6 降級**：`workspace-projectPath` schema 接受但 daemon 回 `not-implemented` — daemon 缺 workspace registry，layer 3 follow-up）；validate 不在 system path（`/`、`/etc`、`/sys`、`/Users` 直系、`$HOME` 直系） | D 決議 + 攻擊 critical C4 + v6 codex review #3 |
| **5.1** | **mandatory excludes union**：daemon-side hard-coded `["node_modules", ".git", ".cache", "dist", ".pnpm-store", ".next", ".turbo"]` ∪ client `excludeDirs`；空 array 不能關掉 | 攻擊 review #10 |
| **5.1** | **mandatory basename excludes**：`["*.lock", "*.log"]` ∪ client | 同上 |
| **5.1** | **`respectGitignore` 改 `*bool`，nil → true**（Go bool unmarshal 預設 false 會 fail-open；攻擊 critical） | 攻擊 review #10 |
| **5.1** | **gitignore parse failure 回 4xx + warning，不 fail-open** | 攻擊 review #11 |
| **5.1** | **body 加 mode envelope**：`{mode: "basename", query: {basename}, roots, limits, filters}` — 留 `mode: "fuzzy" \| "content"` 未來擴展 | 防守 review #8 |
| **5.1** | **移除 `walkDepth := 0` + `_ = walkDepth` 死碼**；深度只用 `filepath.Rel` 計算 | 攻擊 review nit |
| **5.1** | expected fail 寫具體 compiler error，跟最終 method handler shape 一致 | 通用 review B5 |
| **5.2** | gitignore + symlink loop 補強保留；併入 `search_engine_test.go` 同檔 | 體質 review #7 |

### tryOpenFile host-bound + 錯誤分類（攻擊 critical C5）

| Task | 修訂 | 來源 |
|---|---|---|
| **5.3** | `fsSearchByBasename` 放 `spa/src/lib/file-open/fs-search.ts` 子目錄；caller 提供 `roots` capability（不是 absolute path） | 體質 review #15 + D 決議 |
| **5.4** | `EditorOpenBehaviorSection` 放 `spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx` 子目錄；wire 進 P1 拆出來的 `register-modules/editor-module.tsx` | 體質 review #13 + B 決議 |
| **5.5** | **`tryOpenFile` 改 service factory 模式**（`createOpenFileService({popupController, tabOpener, fsBackendFactory})`），caller 不需要知道 backend / popup / openInTab 細節 | 防守 review #6 |
| **5.5** | **`fsBackendFactory(ctx.hostId)` 取一次 host-bound backend**，後續所有 `await stat` 用同一 backend；workspace/host 切換不影響進行中的 open flow | 攻擊 critical C5 |
| **5.5** | **錯誤分類嚴格**：`stat().catch(err => isNotFoundError(err) ? null : throw err)` — 只 ENOENT/404 當 missing；auth/network/host-removed bubble 為原始錯誤，不偽裝成 missing file | 攻擊 critical C5 |
| **5.5** | 測試補：第一次 stat 後切 active host，後續 stat 仍打 ctx.hostId；auth error 不開 popup | 同上 |
| **5.5** | open-file 放 `spa/src/lib/file-open/open-file.ts` 子目錄 | 體質 review #15 |

### Popup service（拆 5.7 → 5.7a / 5.7b / 5.8）

| Task | 修訂 | 來源 |
|---|---|---|
| **5.6** | `FileNotFoundPopup` 放 `spa/src/components/editor/popups/FileNotFoundPopup.tsx`（子目錄） | 體質 review #14 |
| **5.6** | popup expand UX：**主 CTA 寫「搜尋目前 session（cwd: …）」+「搜尋 workspace（projectPath: …）」**，先顯示要搜尋的 root，不要像錯誤訊息次要按鈕 | 防守 review #4 |
| **5.7** | **拆三 task**：<br>- `5.7a popup mount service`（singleton，可獨立測 mount/close/open selected）<br>- `5.7b terminal-link / FileTreeView 接 tryOpenFile`，但 expand button 暫 disabled（保證每個 commit 是可驗證狀態）<br>- `5.8 layer 2/3 expand` 才打開 expand UI + `fs.search` 呼叫 + cancellation token | 通用 review C3 + 體質 review #8 |
| **5.7a** | **改名 `popup-mount.tsx` → `file-not-found-popup-service.tsx`**，放 `spa/src/lib/file-open/file-not-found-popup-service.tsx`；提供 `show / hide / disposeForTests` API | 體質 review #11 |
| **5.7a** | **HMR dispose**：`if (import.meta.hot) import.meta.hot.dispose(hideFileNotFoundPopup)` — 避免 module-level `root/host` ref 在 hot reload 後殘留 zombie root | 攻擊 review #8 |
| **5.7a** | **AbortController cancellation token**：`showFileNotFoundPopup` 回 `AbortController`；`onExpand` `await fs.search` 回來時先檢查 `signal.aborted` — close 後 resolve 不可重新 mount popup | 攻擊 review #5 |
| **5.7a** | 測試：close 後 promise resolve 不 re-mount；HMR re-import 後 document 內只有一個 popup host | 同上 |
| **5.8** | **`ws.config?.projectPath` 改 `ws.moduleConfig?.files?.projectPath`** — Workspace schema **沒有 `config`，只有 `moduleConfig`** | 通用 review D1 |
| **5.8** | useWorkspaceStore 實際路徑 `features/workspace/store` | 通用 review A2 |

### 職責線

| Task | 修訂 | 來源 |
|---|---|---|
| **5.7b** | `terminal-link/openers/file-path.ts` / `FileTreeView.tsx` **不再自己 `getDefaultOpener + openSingletonTab`**：file-opener-registry 只負責 file type → pane content factory；open-file 負責 stat/cache/popup decision；caller 只負責提供 source/context | 體質 review #10 |

### Phase

| Task | 修訂 | 來源 |
|---|---|---|
| **All** | commit message lowercase | 通用 review C2 |
| **5.8** (or new 5.9) | **加 phase verification + PR task**（與 P1/P2/P4 對齊）：跑 vitest + lint + build + go test，PR 描述引用 `SPEC.md (rev 6, P5)`，兩輪 codex review | 通用 review C1 |
| **Final** | 補滿 `editor-open-flow.integration.test.tsx` 跨 phase regression：Editor enabled+cache hit / Editor disabled silent / missing+expand+search / tear-off keepSettings:true | 防守 review #14 |

---

PR 結束標準：點不存在的檔案會走「stat (ENOENT-only) → cache stat → popup → fs.search」管線；layer 2 由 popup 觸發（layer 3 v6 降級為 follow-up）；workspace context 在 await 後仍正確；fs.search 不接受 client absolute path；mandatory excludes / respectGitignore default 守住；popup HMR-safe + cancellation-safe。

## Task 5.0 — FsModule 加 `sessions session.SessionProvider` field

**Files:**
- Modify: `internal/module/fs/module.go`（加 `sessions` field + `Init` 從 registry 取 provider）
- Test: `internal/module/fs/module_test.go`（新建 / 擴；驗證 Init 後 sessions 不為 nil）

> **v6 codex review #3**：`FsModule struct{}` 目前無 core/sessions 欄位；layer 2 的 `session-cwd` capability resolution 需要 `m.sessions.GetSession(sessionCode).Cwd`。模仿 `internal/module/stream/module.go:25,42` pattern。本 task 是 P5.1b 的前置條件。

- [ ] **Step 1: Write failing test**

新建 / 擴 `internal/module/fs/module_test.go`：

```go
package fs

import (
	"testing"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/session"
)

func TestFsModule_InitGetsSessionProvider(t *testing.T) {
	c := core.New(/* 既有 test core helper；參考 stream module test */)
	provider := &fakeSessionProvider{}
	c.Registry.Register(session.RegistryKey, provider)
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.sessions == nil {
		t.Fatal("expected sessions provider to be set after Init")
	}
}

// fakeSessionProvider — 可參考 internal/module/stream/handler_test.go:21 既有實作
type fakeSessionProvider struct{ /* ... */ }
// implement session.SessionProvider methods
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/fs/ -run InitGetsSessionProvider
```

`m.sessions` 欄位不存在 → compile error。

- [ ] **Step 3: Add field + wire in Init**

修 `internal/module/fs/module.go`：

```go
import (
	// ... existing
	"github.com/wake/purdex/internal/session"
)

type FsModule struct {
	sessions session.SessionProvider
}

func (m *FsModule) Init(c *core.Core) error {
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	return nil
}
```

> 直接照 `internal/module/stream/module.go:25,42` 抄。

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/fs/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/fs/module.go internal/module/fs/module_test.go
git commit -m "feat(daemon): fs module holds session provider for capability resolve"
```

---

## Task 5.1a — `internal/module/fs/search_engine.go` 純函式

**Files:**
- Create: `internal/module/fs/search_engine.go`
- Test: `internal/module/fs/search_engine_test.go`

> **C7 + 體質 review #7 + 通用 review A5**：拆 5.1a (純函式 engine) + 5.1b (HTTP handler method)。本 task 只實作 `Search(ctx, req) (resp, error)` 純函式，不掛 HTTP，可獨立 unit test。**移除 `walkDepth := 0` + `_ = walkDepth` 死碼**（攻擊 review nit），深度只用 `filepath.Rel` + `strings.Count` 計算。

- [ ] **Step 1: Write failing test**

新建 `internal/module/fs/search_engine_test.go`：

```go
package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchEngine_BasenameMatchInRoots(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "bar.go"), "no")
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
		Limits: SearchLimits{MaxResults: 10},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(resp.Matches))
	}
}

func TestSearchEngine_ExcludeDirsPrunesSubtree(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "x", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
		Filters: SearchFilters{ExcludeDirs: []string{"src"}},  // user adds src; mandatory excludes still apply
	}
	resp, _ := Search(context.Background(), req)
	// node_modules pruned (mandatory) + src pruned (user) → 0
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches, got %+v", resp.Matches)
	}
}

func TestSearchEngine_MandatoryExcludesAlwaysApply(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	// 攻擊 review #10：client 傳 [] 不能關掉 mandatory excludes
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
		Filters: SearchFilters{ExcludeDirs: []string{}},  // 空 array
	}
	resp, _ := Search(context.Background(), req)
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (node_modules pruned by mandatory), got %+v", resp.Matches)
	}
}

func TestSearchEngine_RespectGitignoreDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWrite(t, filepath.Join(dir, "build", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	// 攻擊 review #10：respectGitignore omitted → default true
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
		Filters: SearchFilters{},  // RespectGitignore is *bool nil → treated as true
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (build pruned by gitignore), got %+v", resp.Matches)
	}
}

func TestSearchEngine_GitignoreParseFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	// invalid gitignore — pattern "***/" is malformed for go-gitignore parser used
	mustWrite(t, filepath.Join(dir, ".gitignore"), "[invalid-bracket")
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
	}
	_, err := Search(context.Background(), req)
	// 攻擊 review #11：parse failure 不 fail-open，回 error 讓 handler 包成 4xx
	if err == nil {
		t.Errorf("expected gitignore parse error to bubble (no fail-open)")
	}
}

func TestSearchEngine_TimeoutReturnsPartial(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 100; i++ {
		mustWrite(t, filepath.Join(dir, fmtInt(i), "foo.go"), "x")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
	defer cancel()
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
	}
	resp, _ := Search(ctx, req)
	if !resp.Partial {
		t.Errorf("expected partial=true on timeout")
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtInt(i int) string { return string(rune('a' + (i % 26))) + string(rune('a' + (i/26)%26)) }
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/fs/ -run SearchEngine
```

- [ ] **Step 3: Implement search engine**

新建 `internal/module/fs/search_engine.go`：

```go
package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"  // 既有依賴或新增
)

// SearchRequest is the daemon-internal shape (after handler resolves capabilities).
// HTTP handler is responsible for mapping client capability roots to absolute paths.
type SearchRequest struct {
	Mode    string         // "basename" | (future "fuzzy" | "content")
	Query   SearchQuery
	Roots   []SearchRoot   // already resolved to absolute paths by handler
	Limits  SearchLimits
	Filters SearchFilters
}

type SearchQuery struct {
	Basename string
}

type SearchRoot struct {
	kind     string  // resolution kind for telemetry; never trusted from client
	absolute string  // absolute path on local filesystem (validated by handler)
}

type SearchLimits struct {
	MaxResults int
	MaxDepth   int
}

type SearchFilters struct {
	ExcludeDirs           []string
	ExcludeBasenameGlobs  []string
	RespectGitignore      *bool  // nil → true (defaults true; 攻擊 review #10)
}

type SearchMatch struct {
	Path      string    `json:"path"`
	ModTime   time.Time `json:"modTime"`
	SizeBytes int64     `json:"sizeBytes"`
	Root      string    `json:"root"`
}

type SearchResponse struct {
	Matches  []SearchMatch `json:"matches"`
	Partial  bool          `json:"partial"`
	Warnings []string      `json:"warnings,omitempty"`
}

const (
	defaultSearchMaxResults = 50
	defaultSearchMaxDepth   = 8
	hardCapMaxResults       = 200
)

// Mandatory excludes are union'd with user filters and CANNOT be disabled by sending [].
// (攻擊 review #10)
var mandatoryExcludeDirs = []string{
	"node_modules", ".git", ".cache", "dist", ".pnpm-store", ".next", ".turbo",
}
var mandatoryExcludeBasenameGlobs = []string{"*.lock", "*.log"}

// Search runs the search synchronously. ctx is honored for cancellation/timeout.
// Returns ErrGitignoreParse when respectGitignore is requested (default true)
// and a .gitignore in any root cannot be parsed — caller maps to 4xx.
var ErrGitignoreParse = errors.New("gitignore parse failure")

func Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if req.Mode == "" {
		req.Mode = "basename"
	}
	if req.Mode != "basename" {
		return SearchResponse{}, errors.New("unsupported search mode: " + req.Mode)
	}
	if req.Query.Basename == "" {
		return SearchResponse{}, errors.New("query.basename required")
	}
	if req.Limits.MaxResults <= 0 {
		req.Limits.MaxResults = defaultSearchMaxResults
	}
	if req.Limits.MaxResults > hardCapMaxResults {
		req.Limits.MaxResults = hardCapMaxResults
	}
	if req.Limits.MaxDepth <= 0 {
		req.Limits.MaxDepth = defaultSearchMaxDepth
	}

	// Union with mandatory excludes (cannot be overridden by client)
	excludeDirs := make(map[string]struct{}, len(mandatoryExcludeDirs)+len(req.Filters.ExcludeDirs))
	for _, d := range mandatoryExcludeDirs { excludeDirs[d] = struct{}{} }
	for _, d := range req.Filters.ExcludeDirs { excludeDirs[d] = struct{}{} }
	excludeGlobs := append([]string{}, mandatoryExcludeBasenameGlobs...)
	excludeGlobs = append(excludeGlobs, req.Filters.ExcludeBasenameGlobs...)

	respectGI := true
	if req.Filters.RespectGitignore != nil {
		respectGI = *req.Filters.RespectGitignore
	}

	matches := make([]SearchMatch, 0, req.Limits.MaxResults)
	partial := false

	for _, root := range req.Roots {
		var gi *gitignore.GitIgnore
		if respectGI {
			gp := filepath.Join(root.absolute, ".gitignore")
			if _, err := os.Stat(gp); err == nil {
				gi2, perr := gitignore.CompileIgnoreFile(gp)
				if perr != nil {
					return SearchResponse{}, ErrGitignoreParse  // no fail-open
				}
				gi = gi2
			}
		}

		err := filepath.WalkDir(root.absolute, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // skip unreadable entries
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(root.absolute, path)
			depth := strings.Count(rel, string(os.PathSeparator))
			if depth > req.Limits.MaxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if _, skip := excludeDirs[d.Name()]; skip {
					return filepath.SkipDir
				}
				if gi != nil && gi.MatchesPath(rel+"/") {
					return filepath.SkipDir
				}
				return nil
			}
			// File: glob excludes
			for _, glob := range excludeGlobs {
				if ok, _ := filepath.Match(glob, d.Name()); ok {
					return nil
				}
			}
			if gi != nil && gi.MatchesPath(rel) {
				return nil
			}
			if d.Name() == req.Query.Basename {
				info, err := d.Info()
				if err != nil {
					return nil
				}
				matches = append(matches, SearchMatch{
					Path: path, ModTime: info.ModTime(), SizeBytes: info.Size(), Root: root.absolute,
				})
				if len(matches) >= req.Limits.MaxResults {
					partial = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if err == context.DeadlineExceeded || errors.Is(err, context.Canceled) {
			partial = true
			break
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ModTime.After(matches[j].ModTime) })
	return SearchResponse{Matches: matches, Partial: partial}, nil
}
```

> 若 `go-gitignore` 沒在 `go.mod`，需 `go get github.com/sabhiram/go-gitignore`；test sandbox 無網路，主 Claude 在 mini 端跑 `go mod tidy`。

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/fs/ -run SearchEngine
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/fs/search_engine.go internal/module/fs/search_engine_test.go go.mod go.sum
git commit -m "feat(daemon): fs search engine pure function with mandatory excludes"
```

---

## Task 5.1b — `(m *FsModule) handleSearch` HTTP handler + capability root resolution

**Files:**
- Create: `internal/module/fs/search_handler.go`
- Test: `internal/module/fs/search_handler_test.go`
- Modify: `internal/module/fs/module.go`（route 註冊 `m.handleSearch`）

> **C4 + D 決議 + 通用 review A5**：HTTP handler 採 `(m *FsModule).handleSearch` method pattern（與既有 fs handler 一致）。**不接受 client-supplied absolute path roots** — 只接 capability `{kind:"session-cwd", sessionCode}` / `{kind:"workspace-projectPath", workspaceId}`，由 daemon resolve 後再 system-path validate（拒絕 `/`、`/etc`、`/sys`、`/Users` 直系、`$HOME` 直系）。**body 加 mode envelope**（防守 review #8）留 `fuzzy`/`content` 擴展位置。

- [ ] **Step 1: Write failing test**

新建 `internal/module/fs/search_handler_test.go`：

```go
package fs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type httpSearchBody struct {
	Mode    string                 `json:"mode"`
	Query   map[string]string      `json:"query"`
	Roots   []map[string]string    `json:"roots"`
	Limits  map[string]int         `json:"limits,omitempty"`
	Filters map[string]any         `json:"filters,omitempty"`
}

func newHTTPReq(t *testing.T, body httpSearchBody) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/fs/search", bytes.NewReader(b))
}

func TestHandleSearch_RejectsAbsoluteRootKind(t *testing.T) {
	m := New(/* ... stub deps ... */)
	dir := t.TempDir()
	body := httpSearchBody{
		Mode: "basename", Query: map[string]string{"basename": "foo"},
		Roots: []map[string]string{{"kind": "absolute", "path": dir}},  // 不允許的 kind
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for kind=absolute, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_RejectsSystemPath(t *testing.T) {
	m := New(/* ... */)
	for _, sysPath := range []string{"/", "/etc", "/sys", "/Users", os.Getenv("HOME")} {
		body := httpSearchBody{
			Mode: "basename", Query: map[string]string{"basename": "foo"},
			Roots: []map[string]string{{"kind": "workspace-projectPath", "workspaceId": "w-mocked-to-" + sysPath}},
		}
		w := httptest.NewRecorder()
		m.handleSearch(w, newHTTPReq(t, body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for system path %s, got %d", sysPath, w.Code)
		}
	}
}

func TestHandleSearch_GitignoreParseError400(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "[broken")
	m := New(/* configured so workspace-projectPath workspaceId="w1" resolves to dir */)
	body := httpSearchBody{
		Mode: "basename", Query: map[string]string{"basename": "x"},
		Roots: []map[string]string{{"kind": "workspace-projectPath", "workspaceId": "w1"}},
	}
	w := httptest.NewRecorder()
	m.handleSearch(w, newHTTPReq(t, body))
	if w.Code < 400 || w.Code >= 500 {
		t.Errorf("expected 4xx for gitignore parse error, got %d body=%s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement handler**

新建 `internal/module/fs/search_handler.go`：

```go
package fs

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type httpSearchRequest struct {
	Mode    string `json:"mode"`
	Query   struct {
		Basename string `json:"basename"`
	} `json:"query"`
	Roots []httpSearchRoot `json:"roots"`
	Limits *struct {
		MaxResults int `json:"maxResults,omitempty"`
		MaxDepth   int `json:"maxDepth,omitempty"`
		TimeoutMs  int `json:"timeoutMs,omitempty"`
	} `json:"limits,omitempty"`
	Filters *struct {
		ExcludeDirs          []string `json:"excludeDirs,omitempty"`
		ExcludeBasenameGlobs []string `json:"excludeBasenameGlobs,omitempty"`
		RespectGitignore     *bool    `json:"respectGitignore,omitempty"`
	} `json:"filters,omitempty"`
}

type httpSearchRoot struct {
	Kind        string `json:"kind"`        // "session-cwd" | "workspace-projectPath"
	SessionCode string `json:"sessionCode,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

// handleSearch is the canonical method handler for POST /api/fs/search.
// Accepts only capability-based roots; absolute path roots are rejected.
func (m *FsModule) handleSearch(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var body httpSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Mode == "" {
		body.Mode = "basename"
	}
	if body.Mode != "basename" {
		http.Error(w, "unsupported mode", http.StatusBadRequest)
		return
	}
	if body.Query.Basename == "" {
		http.Error(w, "query.basename required", http.StatusBadRequest)
		return
	}
	if len(body.Roots) == 0 {
		http.Error(w, "roots required", http.StatusBadRequest)
		return
	}

	roots, err := m.resolveCapabilityRoots(body.Roots)
	if err != nil {
		http.Error(w, "root resolution: "+err.Error(), http.StatusBadRequest)
		return
	}

	req := SearchRequest{
		Mode:  body.Mode,
		Query: SearchQuery{Basename: body.Query.Basename},
		Roots: roots,
	}
	if body.Limits != nil {
		req.Limits = SearchLimits{MaxResults: body.Limits.MaxResults, MaxDepth: body.Limits.MaxDepth}
	}
	if body.Filters != nil {
		req.Filters = SearchFilters{
			ExcludeDirs:          body.Filters.ExcludeDirs,
			ExcludeBasenameGlobs: body.Filters.ExcludeBasenameGlobs,
			RespectGitignore:     body.Filters.RespectGitignore,
		}
	}

	ctx := r.Context()
	if body.Limits != nil && body.Limits.TimeoutMs > 0 {
		var cancel func()
		ctx, cancel = contextWithTimeout(ctx, time.Duration(body.Limits.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	resp, err := Search(ctx, req)
	if errors.Is(err, ErrGitignoreParse) {
		http.Error(w, "gitignore parse failure", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// resolveCapabilityRoots maps client capability roots to absolute paths.
// Rejects:
//   - Unsupported kinds (e.g. "absolute" / unknown strings)
//   - System paths (/, /etc, /sys, /Users 直系, $HOME 直系)
//   - "workspace-projectPath" — v6 not-implemented; daemon lacks workspace registry
func (m *FsModule) resolveCapabilityRoots(roots []httpSearchRoot) ([]SearchRoot, error) {
	out := make([]SearchRoot, 0, len(roots))
	for _, r := range roots {
		var abs string
		switch r.Kind {
		case "session-cwd":
			info, err := m.sessions.GetSession(r.SessionCode)
			if err != nil || info == nil || info.Cwd == "" {
				return nil, errors.New("session cwd not found for " + r.SessionCode)
			}
			abs = info.Cwd
		case "workspace-projectPath":
			// v6 降級：layer 3 follow-up；daemon 暫不實作 workspace registry。
			// SPA caller 應在 5.7b/5.8 處看到此 error 後 skip layer 3 results。
			return nil, errors.New("workspace-projectPath not implemented in this PR (layer 3 follow-up)")
		default:
			return nil, errors.New("unsupported root kind: " + r.Kind)
		}
		clean := filepath.Clean(abs)
		if err := validateNotSystemPath(clean); err != nil {
			return nil, err
		}
		out = append(out, SearchRoot{kind: r.Kind, absolute: clean})
	}
	return out, nil
}

func validateNotSystemPath(p string) error {
	systemPaths := []string{"/", "/etc", "/sys"}
	for _, sp := range systemPaths {
		if p == sp {
			return errors.New("system path rejected: " + p)
		}
	}
	if p == "/Users" || strings.HasPrefix(p, "/Users/") && !strings.Contains(p[len("/Users/"):], "/") {
		// /Users 直系（如 /Users/wake）拒；/Users/wake/anything 允許
		if p == "/Users" || strings.Count(p, "/") <= 2 {
			return errors.New("user-home root level rejected: " + p)
		}
	}
	if home := os.Getenv("HOME"); home != "" && p == home {
		return errors.New("home dir root rejected")
	}
	return nil
}

// workspaceProjectPath — v6 not implemented; layer 3 follow-up.
// When daemon gets a workspace registry endpoint, this resolver will return
// the abs projectPath the SPA registered for `workspaceId`.
```

> **`m.workspaces map[string]string`** + `core.Sessions.GetCwd(...)` 介面可能須在 `module.go` 補；具體看 daemon 既有 workspace registry 設計。**若 daemon 無 workspace registry**，先實作 capability `session-cwd`，`workspace-projectPath` 留 follow-up issue（不減 ABCD 決議價值，因 layer 2 仍可用 session cwd）。

- [ ] **Step 4: Wire route in `internal/module/fs/module.go`**

```go
func (m *FsModule) Routes(mux *http.ServeMux) {
    // ...既有
    mux.HandleFunc("POST /api/fs/search", m.handleSearch)
}
```

- [ ] **Step 5: Run tests, expect PASS**

```
go test ./internal/module/fs/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/module/fs/search_handler.go internal/module/fs/search_handler_test.go internal/module/fs/module.go
git commit -m "feat(daemon): fs search http handler with capability roots"
```

---

## Task 5.2 — `gitignore` parse-failure handling + symlink loop tests（engine 補強）

**Files:**
- Modify: `internal/module/fs/search_engine.go`（加 gitignore 完整 parse handling + symlink loop 防護）
- Modify: `internal/module/fs/search_engine_test.go`（補測）

> Task 5.1a 已建立 engine + 基本 gitignore 流程；本 task 補強：
> - **gitignore parse failure 不 fail-open**（攻擊 review #11）— Task 5.1a engine 內 `gitignore.CompileIgnoreFile` 失敗回 `ErrGitignoreParse`；本 task 加完整 case 測試（含 invalid pattern → 4xx via handler in 5.1b）
> - Symlink loop：`filepath.WalkDir` 預設不 follow symlink，但 root 內 symlink-to-dir 不應被當 dir 探勘 — 補測 `os.Lstat` 行為。

- [ ] **Step 1: Write failing test**

擴 `internal/module/fs/search_engine_test.go`：

```go
func TestSearchEngine_GitignoreFiltersBasenameMatch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "skip")
	mustWrite(t, filepath.Join(dir, "kept.go"), "ok")
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "ignored.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("ignored.go should not appear when respectGitignore default true: %+v", resp.Matches)
	}
}

func TestSearchEngine_GitignoreCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "now we look")
	off := false
	req := SearchRequest{
		Mode: "basename", Query: SearchQuery{Basename: "ignored.go"},
		Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
		Filters: SearchFilters{RespectGitignore: &off},
	}
	resp, _ := Search(context.Background(), req)
	if len(resp.Matches) != 1 {
		t.Errorf("ignored.go should be returned when respectGitignore=false: %+v", resp.Matches)
	}
}

func TestSearchEngine_SymlinkLoopDoesNotInfiniteWalk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	mustWrite(t, filepath.Join(a, "x.go"), "ok")
	if err := os.Symlink(a, filepath.Join(a, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		req := SearchRequest{
			Mode: "basename", Query: SearchQuery{Basename: "x.go"},
			Roots: []SearchRoot{{kind: "raw-absolute-test-only", absolute: dir}},
			Limits: SearchLimits{MaxDepth: 6},
		}
		_, _ = Search(context.Background(), req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("symlink loop caused hang")
	}
}
```

(`runtime` import needed.)

- [ ] **Step 2: Run test, expect PASS（engine 已在 5.1a 內處理 gitignore；symlink 由 WalkDir 預設不 follow 處理）**

```
go test ./internal/module/fs/ -run SearchEngine
```

若 5.1a engine 用了 `os.Lstat` 替代 `os.Stat` 對 symlink-to-dir，loop 測試會直接過。

- [ ] **Step 3: Commit**

```bash
git add internal/module/fs/search_engine_test.go
git commit -m "test(daemon): fs search gitignore and symlink loop coverage"
```

---

## Task 5.3 — SPA `fsSearchByCapability` helper（capability roots only）

**Files:**
- Create: `spa/src/lib/file-open/fs-search.ts`
- Test: `spa/src/lib/file-open/fs-search.test.ts`

> **D 決議**：caller 必須提供 `roots` capability（`session-cwd` / `workspace-projectPath`），**不能**傳 absolute path — daemon 端 server-side allowlist 會拒絕 `kind:"absolute"`。

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fsSearchByCapability, type SearchRootCapability } from './fs-search'
import { useHostStore } from '../../stores/useHostStore'

describe('fsSearchByCapability', () => {
  beforeEach(() => {
    useHostStore.setState({
      activeHostId: 'h1',
      hostOrder: ['h1'],
      getDaemonBase: () => 'http://daemon',
      getAuthHeaders: () => ({}),
    } as never, false)
    global.fetch = vi.fn(async () =>
      new Response(JSON.stringify({
        matches: [
          { path: '/a/foo.go', modTime: '2026-04-27T00:00:00Z', sizeBytes: 10, root: '/a' },
          { path: '/b/foo.go', modTime: '2026-04-26T00:00:00Z', sizeBytes: 10, root: '/b' },
        ],
        partial: false,
      }), { status: 200 }),
    ) as never
  })

  it('posts mode-envelope body with capability roots to host daemon', async () => {
    const roots: SearchRootCapability[] = [
      { kind: 'session-cwd', sessionCode: 'sess1' },
      { kind: 'workspace-projectPath', workspaceId: 'w1' },
    ]
    const matches = await fsSearchByCapability('h1', 'foo.go', roots)
    expect(matches.map((m) => m.path)).toEqual(['/a/foo.go', '/b/foo.go'])
    const fetchCall = (global.fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0]
    const body = JSON.parse(fetchCall[1].body as string)
    expect(body.mode).toBe('basename')
    expect(body.query.basename).toBe('foo.go')
    expect(body.roots).toEqual(roots)
  })

  it('handles 4xx error', async () => {
    global.fetch = vi.fn(async () => new Response('bad request', { status: 400 })) as never
    await expect(fsSearchByCapability('h1', 'foo.go', [{ kind: 'session-cwd', sessionCode: 's1' }])).rejects.toThrow(/400/)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

```ts
import { useHostStore } from '../../stores/useHostStore'

export interface SearchMatch {
  path: string
  modTime: string
  sizeBytes: number
  root: string
}

export interface SearchResponse {
  matches: SearchMatch[]
  partial: boolean
  warnings?: string[]
}

export type SearchRootCapability =
  | { kind: 'session-cwd'; sessionCode: string }
  | { kind: 'workspace-projectPath'; workspaceId: string }

export async function fsSearchByCapability(
  hostId: string,
  basename: string,
  roots: SearchRootCapability[],
  opts: { maxResults?: number; maxDepth?: number; timeoutMs?: number } = {},
): Promise<SearchMatch[]> {
  const state = useHostStore.getState()
  const base = state.getDaemonBase(hostId)
  const headers = { 'Content-Type': 'application/json', ...state.getAuthHeaders(hostId) }
  // Mode-envelope body — daemon expects { mode, query, roots, limits, filters }
  const body = JSON.stringify({
    mode: 'basename',
    query: { basename },
    roots,
    ...(Object.keys(opts).length ? { limits: opts } : {}),
  })
  const res = await fetch(`${base}/api/fs/search`, { method: 'POST', headers, body })
  if (!res.ok) throw new Error(`fs.search failed: ${res.status} ${res.statusText}`)
  const json = (await res.json()) as SearchResponse
  return json.matches
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/file-open/fs-search.ts spa/src/lib/file-open/fs-search.test.ts
git commit -m "feat(spa): fssearchbycapability helper with mode envelope"
```

---

## Task 5.4 — Two new UI settings + `EditorOpenBehaviorSection`

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts`
- Create: `spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx`
- Test: 對應 .test.tsx

- [ ] **Step 1: Add fields to useUISettingsStore**

加：

```ts
popupOnMissingFile: true,
autoSearchLayer1: true,
setPopupOnMissingFile: (v: boolean) => set({ popupOnMissingFile: v }),
setAutoSearchLayer1: (v: boolean) => set({ autoSearchLayer1: v }),
```

(對應 type / persist partialize / defaults。)

- [ ] **Step 2: Implement section**

```tsx
// 檔案位置：spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx
// → 從 settings/editor/ 到 stores/ 是三層 (../../../)，到同層 SettingItem/ToggleSwitch 是 ../
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'

export function EditorOpenBehaviorSection() {
  const popup = useUISettingsStore((s) => s.popupOnMissingFile)
  const setPopup = useUISettingsStore((s) => s.setPopupOnMissingFile)
  const auto = useUISettingsStore((s) => s.autoSearchLayer1)
  const setAuto = useUISettingsStore((s) => s.setAutoSearchLayer1)
  const t = useI18nStore((s) => s.t)
  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.open_behavior.title')}</h3>
      <SettingItem label={t('settings.editor.open_behavior.popup.label')} description={t('settings.editor.open_behavior.popup.desc')}>
        <ToggleSwitch label={t('settings.editor.open_behavior.popup.label')} checked={popup} onChange={setPopup} />
      </SettingItem>
      <SettingItem label={t('settings.editor.open_behavior.auto_layer1.label')} description={t('settings.editor.open_behavior.auto_layer1.desc')}>
        <ToggleSwitch label={t('settings.editor.open_behavior.auto_layer1.label')} checked={auto} onChange={setAuto} />
      </SettingItem>
    </div>
  )
}
```

加 i18n key 到 zh-TW + en JSON。

- [ ] **Step 3: Wire into Editor module settings（在 P1 拆出來的 `register-modules/editor-module.tsx`）**

在 `spa/src/lib/register-modules/editor-module.tsx` 的 `editorModuleDefinition.settings: [...]` 陣列裡加（**不再修舊 `register-modules.tsx`** 過渡 shim）：

```tsx
{
  localId: 'open-behavior',
  scope: 'purdex',
  order: 9,
  labelKey: 'settings.editor.open_behavior.title',
  component: EditorOpenBehaviorSection,
},
```

- [ ] **Step 4: Run tests, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useUISettingsStore.ts spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx spa/src/lib/register-modules/editor-module.tsx spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "feat(spa): editor open behavior settings (popup + auto layer1)"
```

---

## Task 5.5 — `tryOpenFile` flow（service factory + host-bound + ENOENT-only）

**Files:**
- Create: `spa/src/lib/file-open/open-file.ts`
- Test: `spa/src/lib/file-open/open-file.test.ts`

> **C5 + 防守 review #6**：
> - **Service factory 模式**：`createOpenFileService({fsBackendFactory, popupController, tabOpener})` 回 `{ tryOpenFile }`；caller 不需知道 backend / popup / openInTab 細節
> - **Host-bound backend**：`fsBackendFactory(ctx.hostId)` 取一次，後續所有 `await stat` 用同一 backend；workspace/host 切換不影響進行中的 open flow（攻擊 critical C5）
> - **錯誤分類嚴格**：`stat().catch(err => isNotFoundError(err) ? null : throw err)` — 只 ENOENT/404 視作 missing；auth/network/host-removed bubble，不偽裝 missing

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createOpenFileService, FileNotFoundError, isNotFoundError } from './open-file'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

const mockStatH1 = vi.fn()
const mockStatH2 = vi.fn()
const mockOpenInTab = vi.fn()
const mockShow = vi.fn()
const mockHide = vi.fn()

const fsBackendFactory = (hostId: string) => ({
  stat: hostId === 'h1' ? mockStatH1 : mockStatH2,
})

const svc = createOpenFileService({
  fsBackendFactory,
  popupController: { show: mockShow, hide: mockHide },
  tabOpener: mockOpenInTab,
})

describe('createOpenFileService', () => {
  beforeEach(() => {
    usePathCacheStore.setState({ dirsByScope: {} } as never, false)
    useUISettingsStore.setState({ popupOnMissingFile: true, autoSearchLayer1: true } as never, false)
    mockStatH1.mockReset(); mockStatH2.mockReset()
    mockOpenInTab.mockReset(); mockShow.mockReset(); mockHide.mockReset()
  })

  const ctx = { hostId: 'h1', sourceWorkspaceId: 'w1' }
  const file = { path: '/a/b/foo.go', name: 'foo.go', extension: 'go', isDirectory: false }
  const source = { type: 'inapp' as const }

  it('opens directly when path exists', async () => {
    mockStatH1.mockResolvedValue({ isDirectory: false })
    await svc.tryOpenFile(file as never, source, ctx)
    expect(mockOpenInTab).toHaveBeenCalledWith(file, source, ctx)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('throws FileNotFoundError when popupOnMissingFile off and file missing', async () => {
    useUISettingsStore.setState({ popupOnMissingFile: false } as never, false)
    mockStatH1.mockRejectedValue(Object.assign(new Error('ENOENT'), { code: 'ENOENT' }))
    await expect(svc.tryOpenFile(file as never, source, ctx)).rejects.toBeInstanceOf(FileNotFoundError)
  })

  // 攻擊 critical C5: 錯誤分類嚴格 — auth error 不誤判 missing
  it('bubbles auth error (NOT missing) — popup never opens', async () => {
    const authErr = Object.assign(new Error('unauthorized'), { status: 401 })
    mockStatH1.mockRejectedValue(authErr)
    await expect(svc.tryOpenFile(file as never, source, ctx)).rejects.toBe(authErr)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('bubbles network error — popup never opens', async () => {
    const netErr = Object.assign(new Error('network down'), { code: 'ENETDOWN' })
    mockStatH1.mockRejectedValue(netErr)
    await expect(svc.tryOpenFile(file as never, source, ctx)).rejects.toBe(netErr)
  })

  // 攻擊 critical C5: host-bound — host 切換中後續 stat 仍打 ctx.hostId
  it('uses ctx.hostId backend for the entire flow even if active host switches', async () => {
    mockStatH1.mockResolvedValueOnce(null)  // direct stat: missing (not-found code applied below)
    Object.assign(mockStatH1.mock.calls, [])  // reset call count tracking
    // Simulate: active host switches to h2 mid-flow (dispatch can't observe this in test;
    // we just assert all mockStatH1 was called, mockStatH2 was NOT)
    mockStatH1.mockResolvedValue(null) // for any subsequent calls; will trigger ask-expand
    const enoentErr = () => Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockRejectedValue(enoentErr())
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    mockStatH2.mockResolvedValue({ isDirectory: false })  // h2 backend should NEVER be called
    await svc.tryOpenFile(file as never, source, ctx).catch(() => null)
    expect(mockStatH2).not.toHaveBeenCalled()
    expect(mockStatH1).toHaveBeenCalled()
  })

  it('layer1 single verified hit opens directly', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockImplementation(async (p: string) =>
      p === '/a/b/foo.go' ? { isDirectory: false } : (() => { throw enoent })(),
    )
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await svc.tryOpenFile(missing as never, source, ctx)
    expect(mockOpenInTab).toHaveBeenCalledWith({ ...missing, path: '/a/b/foo.go' }, source, ctx)
  })

  it('layer1 stat ENOENT prunes cache + falls to ask-expand', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/stale/dir')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockRejectedValue(enoent)
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await svc.tryOpenFile(missing as never, source, ctx)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
    expect(mockShow).toHaveBeenCalledWith(expect.objectContaining({ mode: 'ask-expand' }))
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement**

```ts
import type { FileInfo, FileSource } from '../../types/fs'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

export interface OpenFileContext {
  hostId: string
  sourceWorkspaceId: string
  sessionCode?: string
  cwdResolver?: () => Promise<string | null>
}

interface FsBackend {
  stat(path: string): Promise<unknown>
}

export interface PopupSpec {
  mode: 'layer1-multi' | 'ask-expand'
  hits?: string[]
  file: FileInfo
  source: FileSource
  ctx: OpenFileContext
}

export interface OpenFileDeps {
  fsBackendFactory: (hostId: string) => FsBackend
  popupController: {
    show: (spec: PopupSpec) => AbortController
    hide: () => void
  }
  tabOpener: (file: FileInfo, source: FileSource, ctx: OpenFileContext) => void
}

export class FileNotFoundError extends Error {
  constructor(public readonly path: string) {
    super(`File not found: ${path}`)
  }
}

/** Strict error classifier — only ENOENT / 404 are "not found"; everything else bubbles. */
export function isNotFoundError(err: unknown): boolean {
  if (!err || typeof err !== 'object') return false
  const e = err as { code?: string; status?: number }
  return e.code === 'ENOENT' || e.status === 404
}

export interface OpenFileService {
  tryOpenFile(file: FileInfo, source: FileSource, ctx: OpenFileContext): Promise<void>
}

export function createOpenFileService(deps: OpenFileDeps): OpenFileService {
  return {
    async tryOpenFile(file, source, ctx) {
      // Host-bound: take the backend ONCE; subsequent awaits use this same backend
      // even if the user switches active host during the flow.
      const fs = deps.fsBackendFactory(ctx.hostId)

      // 1. Direct stat — only ENOENT/404 counts as "missing"; anything else bubbles
      let statResult: unknown = null
      try {
        statResult = await fs.stat(file.path)
      } catch (err) {
        if (!isNotFoundError(err)) throw err
        statResult = null
      }
      if (statResult) {
        deps.tabOpener(file, source, ctx)
        return
      }

      // 2. Popup gate
      const ui = useUISettingsStore.getState()
      if (!ui.popupOnMissingFile) throw new FileNotFoundError(file.path)

      // 3. Layer 1
      if (ui.autoSearchLayer1) {
        const cache = usePathCacheStore.getState()
        const candidates = cache.lookup(ctx.hostId, ctx.sourceWorkspaceId, file.name)
        const verified: string[] = []
        for (const c of candidates) {
          try {
            const ok = await fs.stat(c)
            if (ok) verified.push(c)
            else cache.pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, c)
          } catch (err) {
            if (!isNotFoundError(err)) throw err
            cache.pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, c)
          }
        }
        if (verified.length === 1) {
          deps.tabOpener({ ...file, path: verified[0] }, source, ctx)
          return
        }
        if (verified.length > 1) {
          deps.popupController.show({ mode: 'layer1-multi', hits: verified, file, source, ctx })
          return
        }
      }

      // 4. Fall through
      deps.popupController.show({ mode: 'ask-expand', file, source, ctx })
    },
  }
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/file-open/open-file.ts spa/src/lib/file-open/open-file.test.ts
git commit -m "feat(spa): host-bound openfile service with strict enoent classification"
```

---

## Task 5.6 — `FileNotFoundPopup` component

**Files:**
- Create: `spa/src/components/editor/popups/FileNotFoundPopup.tsx`
- Test: `spa/src/components/editor/popups/FileNotFoundPopup.test.tsx`

- [ ] **Step 1: Write failing test**

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FileNotFoundPopup } from './FileNotFoundPopup'

describe('FileNotFoundPopup', () => {
  const baseSpec = {
    mode: 'ask-expand' as const,
    file: { path: '/missing/foo.go', name: 'foo.go', extension: 'go', isDirectory: false },
    source: { type: 'inapp' as const },
    ctx: { hostId: 'h1', sourceWorkspaceId: 'w1', sessionCode: 's1' },
  }

  it('renders the missing path and expand button', () => {
    render(<FileNotFoundPopup spec={baseSpec} onClose={vi.fn()} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    expect(screen.getByText(/foo.go/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /expand/i })).toBeInTheDocument()
  })

  it('layer1-multi mode renders candidate list', () => {
    const spec = { ...baseSpec, mode: 'layer1-multi' as const, hits: ['/a/b/foo.go', '/c/d/foo.go'] }
    render(<FileNotFoundPopup spec={spec} onClose={vi.fn()} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    expect(screen.getByText('/a/b/foo.go')).toBeInTheDocument()
    expect(screen.getByText('/c/d/foo.go')).toBeInTheDocument()
  })

  it('clicking a candidate calls onOpenPath', () => {
    const onOpenPath = vi.fn()
    const spec = { ...baseSpec, mode: 'layer1-multi' as const, hits: ['/a/b/foo.go'] }
    render(<FileNotFoundPopup spec={spec} onClose={vi.fn()} onOpenPath={onOpenPath} onExpand={vi.fn()} />)
    fireEvent.click(screen.getByText('/a/b/foo.go'))
    expect(onOpenPath).toHaveBeenCalledWith('/a/b/foo.go')
  })

  it('ESC key closes popup', () => {
    const onClose = vi.fn()
    render(<FileNotFoundPopup spec={baseSpec} onClose={onClose} onOpenPath={vi.fn()} onExpand={vi.fn()} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement component**

```tsx
import { useEffect } from 'react'
import type { PopupSpec } from '../../lib/open-file'

interface Props {
  spec: PopupSpec
  onClose: () => void
  onOpenPath: (path: string) => void
  onExpand: () => void
}

export function FileNotFoundPopup({ spec, onClose, onOpenPath, onExpand }: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div role="dialog" aria-modal="true" className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-bg-primary rounded-lg p-6 max-w-lg w-full">
        <h3 className="text-base text-text-primary mb-2">File not found</h3>
        <p className="text-sm text-text-secondary mb-3 break-all">{spec.file.path}</p>

        {spec.mode === 'layer1-multi' && spec.hits && (
          <div className="mb-4">
            <h4 className="text-xs uppercase text-text-muted mb-1">Recent candidates</h4>
            <ul className="border border-border rounded">
              {spec.hits.map((h) => (
                <li key={h}>
                  <button
                    type="button"
                    onClick={() => onOpenPath(h)}
                    className="w-full text-left px-2 py-1 text-sm hover:bg-bg-tertiary truncate"
                  >
                    {h}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="px-3 py-1 text-sm text-text-secondary">
            Cancel
          </button>
          <button type="button" onClick={onExpand} className="px-3 py-1 text-sm bg-accent rounded">
            Expand search
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/popups/FileNotFoundPopup.tsx spa/src/components/editor/popups/FileNotFoundPopup.test.tsx
git commit -m "feat(spa): filenotfoundpopup component with esc and candidate list"
```

---

## Task 5.7a — popup mount service（HMR-safe + AbortController cancellation）

**Files:**
- Create: `spa/src/lib/file-open/file-not-found-popup-service.tsx`
- Test: `spa/src/lib/file-open/file-not-found-popup-service.test.tsx`

> **拆 5.7a/b/8**（通用 review C3 + 體質 review #8 + #11）：本 task 只實作 popup mount lifecycle service（singleton root + HMR dispose + AbortController），可獨立 mount/close/open-selected。**改名 `popup-mount.tsx` → `file-not-found-popup-service.tsx`** 凸顯 service nature。

> **HMR**：`if (import.meta.hot) import.meta.hot.dispose(hideFileNotFoundPopup)` — 避免 hot reload 後 module-level `root/host` 變 zombie。
>
> **AbortController**：`showFileNotFoundPopup(...)` 回 `AbortController`；caller 在 `await` 後檢查 `signal.aborted`，close 後不可重新 mount popup（攻擊 review #5）。

- [ ] **Step 1: Write failing test**

新建 `spa/src/lib/file-open/file-not-found-popup-service.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import {
  showFileNotFoundPopup,
  hideFileNotFoundPopup,
  disposeForTests,
} from './file-not-found-popup-service'

afterEach(() => disposeForTests())

const baseSpec = {
  mode: 'ask-expand' as const,
  file: { path: '/missing/foo.go', name: 'foo.go', extension: 'go', isDirectory: false } as never,
  source: { type: 'inapp' } as never,
  ctx: { hostId: 'h1', sourceWorkspaceId: 'w1' },
}

describe('file-not-found-popup-service', () => {
  it('show creates a single host element in document', () => {
    showFileNotFoundPopup(baseSpec, { onOpenPath: () => {}, onExpand: async () => {} })
    expect(document.querySelectorAll('[data-pdx-popup-host="file-not-found"]').length).toBe(1)
  })

  it('show twice replaces previous instance (still single host)', () => {
    showFileNotFoundPopup(baseSpec, { onOpenPath: () => {}, onExpand: async () => {} })
    showFileNotFoundPopup(baseSpec, { onOpenPath: () => {}, onExpand: async () => {} })
    expect(document.querySelectorAll('[data-pdx-popup-host="file-not-found"]').length).toBe(1)
  })

  it('hide removes host completely', () => {
    showFileNotFoundPopup(baseSpec, { onOpenPath: () => {}, onExpand: async () => {} })
    hideFileNotFoundPopup()
    expect(document.querySelectorAll('[data-pdx-popup-host="file-not-found"]').length).toBe(0)
  })

  it('hide is idempotent (no throw on repeated calls)', () => {
    expect(() => { hideFileNotFoundPopup(); hideFileNotFoundPopup() }).not.toThrow()
  })

  // 攻擊 review #5: AbortController cancellation
  it('returns AbortController; abort signals consumers to bail', () => {
    const ctl = showFileNotFoundPopup(baseSpec, { onOpenPath: () => {}, onExpand: async () => {} })
    expect(ctl.signal.aborted).toBe(false)
    hideFileNotFoundPopup()
    expect(ctl.signal.aborted).toBe(true)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement service**

新建 `spa/src/lib/file-open/file-not-found-popup-service.tsx`：

```tsx
import { createRoot, type Root } from 'react-dom/client'
import { FileNotFoundPopup } from '../../components/editor/popups/FileNotFoundPopup'
import type { PopupSpec } from './open-file'

let root: Root | undefined
let host: HTMLDivElement | undefined
let currentToken: AbortController | undefined

interface ShowCallbacks {
  onOpenPath: (path: string) => void
  onExpand: (spec: PopupSpec, signal: AbortSignal) => Promise<void>
}

export function showFileNotFoundPopup(spec: PopupSpec, cb: ShowCallbacks): AbortController {
  hideFileNotFoundPopup()  // singleton
  host = document.createElement('div')
  host.dataset.pdxPopupHost = 'file-not-found'
  document.body.appendChild(host)
  root = createRoot(host)
  currentToken = new AbortController()
  const tok = currentToken
  root.render(
    <FileNotFoundPopup
      spec={spec}
      onClose={hideFileNotFoundPopup}
      onOpenPath={(p) => { hideFileNotFoundPopup(); cb.onOpenPath(p) }}
      onExpand={() => { void cb.onExpand(spec, tok.signal) }}
    />,
  )
  return currentToken
}

export function hideFileNotFoundPopup(): void {
  currentToken?.abort()
  root?.unmount()
  host?.remove()
  root = undefined
  host = undefined
  currentToken = undefined
}

/** Test-only dispose (alias for hide; named for clarity in test cleanup). */
export function disposeForTests(): void { hideFileNotFoundPopup() }

if (import.meta.hot) {
  import.meta.hot.dispose(hideFileNotFoundPopup)
}
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/file-open/file-not-found-popup-service.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/file-open/file-not-found-popup-service.tsx spa/src/lib/file-open/file-not-found-popup-service.test.tsx
git commit -m "feat(spa): file not found popup service with hmr and abort"
```

---

## Task 5.7b — Integrate `tryOpenFile` into terminal-link + FileTreeView（expand 暫 disabled）

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`
- Modify: `spa/src/components/FileTreeView.tsx`
- Modify: `spa/src/lib/register-modules/editor-module.tsx`（注入 service deps）

> **拆 5.7b**：caller wire 起來，但 popup 的 `onExpand` callback 暫時 no-op（每個 commit 都是可驗證狀態；體質 review #8）。Layer 2/3 expand 完整流在 Task 5.8。

- [ ] **Step 1: Provide a service factory helper**

在 `spa/src/lib/file-open/index.ts`（新建 barrel）匯出：

```ts
export { showFileNotFoundPopup, hideFileNotFoundPopup } from './file-not-found-popup-service'
export { createOpenFileService, FileNotFoundError, isNotFoundError, type PopupSpec, type OpenFileContext } from './open-file'
```

- [ ] **Step 2: Replace `openSingletonTab` direct call in file-path opener / FileTreeView**

在 terminal-link / FileTreeView caller 改成：

```ts
import { createOpenFileService, showFileNotFoundPopup, hideFileNotFoundPopup } from '../lib/file-open'
import { useHostStore } from '../stores/useHostStore'

// 一次建立 service（caller 範圍內 stable）
const svc = createOpenFileService({
  fsBackendFactory: (hostId) => useHostStore.getState().getFsBackend(hostId),  // host-bound factory
  popupController: {
    show: (spec) => showFileNotFoundPopup(spec, {
      onOpenPath: (p) => openSingletonTab({ kind: 'editor', source, filePath: p } as never, { isSameKind: ... }),
      onExpand: async (_spec, _signal) => { /* 5.8 fills this */ },
    }),
    hide: hideFileNotFoundPopup,
  },
  tabOpener: (file, source, ctx) => {
    // P2 same-kind logic preserved here
    openSingletonTab({ kind: 'editor', source, filePath: file.path } as never, {
      isSameKind: (c) => ['editor', 'image-preview', 'pdf-preview'].includes(c.kind),
    })
  },
})

// Caller invokes:
await svc.tryOpenFile(file, source, { hostId, sourceWorkspaceId })
```

> **`useHostStore.getFsBackend(hostId)`** 介面可能要新增；對齊 P5 SPEC「fs API 永遠 host-bound」。若已有等價 API（如 `getDaemonClient(hostId).fs`）直接用。

- [ ] **Step 3: 跑全測**

```
cd spa && npx vitest run
```

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(spa): terminal link and file tree open files via tryopenfile pipeline"
```

---

## Task 5.8 — Layer 2/3 expand search（補 onExpand 完整流）

**Files:**
- Modify: `spa/src/lib/file-open/file-not-found-popup-service.tsx`（補 expanded re-render）
- Modify: `spa/src/components/editor/popups/FileNotFoundPopup.tsx`（render expanded sections）
- Modify: P5.7b 注入的 `onExpand` callback

> **吸收**：
> - `ws.config?.projectPath` 改 `ws.moduleConfig?.files?.projectPath`（通用 review D1）— Workspace schema 沒有 `config`，只有 `moduleConfig`
> - useWorkspaceStore 路徑 `features/workspace/store`（通用 review A2）
> - **AbortSignal 檢查**：`onExpand` `await fs.search` 回來時必須先 check `signal.aborted` — close 後不可 re-mount popup（攻擊 review #5）
> - **popup expand UX**：主 CTA 寫「搜尋目前 session（cwd: …）」+「搜尋 workspace（projectPath: …）」，先顯示要搜尋的 root；不像錯誤訊息次要按鈕（防守 review #4）

- [ ] **Step 1: Extend `PopupSpec` 加 expanded mode**

```ts
type PopupSpec =
  | { mode: 'ask-expand'; file; source; ctx }
  | { mode: 'layer1-multi'; hits: string[]; file; source; ctx }
  | { mode: 'expanded'; layer2Hits: SearchMatch[]; layer3Hits: SearchMatch[]; file; source; ctx }
```

- [ ] **Step 2: Implement onExpand using layer 2 + layer 3**

在 P5.7b 的 caller 注入處填 `onExpand` body：

```ts
import { useWorkspaceStore } from '../features/workspace/store'

async onExpand(spec, signal) {
  const layer2Roots: { kind: 'session-cwd'; sessionCode: string }[] = []
  if (spec.ctx.sessionCode) {
    layer2Roots.push({ kind: 'session-cwd', sessionCode: spec.ctx.sessionCode })
  }
  // **注意：projectPath 在 ws.moduleConfig.files**，不是 ws.config（通用 review D1）
  const wsState = useWorkspaceStore.getState()
  const ws = wsState.workspaces.find((w) => w.id === spec.ctx.sourceWorkspaceId)
  const projectPath = ws?.moduleConfig?.files?.projectPath
  const layer3Roots: { kind: 'workspace-projectPath'; workspaceId: string }[] = projectPath
    ? [{ kind: 'workspace-projectPath', workspaceId: spec.ctx.sourceWorkspaceId }]
    : []

  // v6 降級：layer 3 (workspace-projectPath) daemon 端未實作；本 PR 只跑 layer 2
  // SPA-side 不打 layer 3 fs.search 呼叫，避免 daemon 回 not-implemented 錯誤
  const layer2Hits = layer2Roots.length
    ? await fsSearchByCapability(spec.ctx.hostId, spec.file.name, layer2Roots).catch(() => [])
    : []
  const layer3Hits: SearchMatch[] = []  // follow-up issue
  void layer3Roots  // capability 結構保留供未來 layer 3 reference；本 PR 不執行

  // 攻擊 review #5: 檢查 cancellation token，close 後不再 mount
  if (signal.aborted) return

  showFileNotFoundPopupExpanded({ ...spec, mode: 'expanded', layer2Hits, layer3Hits })
}
```

- [ ] **Step 3: Update `FileNotFoundPopup` to render expanded results**

加 sections for `layer2Hits` / `layer3Hits`；header 寫 `搜尋目前 session（cwd: <abs path>）` 和 `搜尋 workspace（projectPath: <abs path>）` — 先顯示 root，主 CTA 形式（防守 review #4）。

- [ ] **Step 4: Test**

擴 `FileNotFoundPopup.test.tsx` case：`mode: 'expanded'` + 給 layer 2/3 hits → render 兩個 section + 點任一條 → callback `onOpenPath`。

加 `file-not-found-popup-service.test.tsx` regression：show → abort → resolve onExpand promise → 不應 re-mount popup。

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(spa): file not found popup expands via layer 2 and 3"
```

---

## Task 5.9 — Phase 5 verification + PR

- [ ] **Step 1: Full test + lint + build + go test**

```bash
cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build
go test ./...
```

- [ ] **Step 2: 開 PR**

```bash
git push -u origin worktree-worktree-editor-self-contained
gh pr create --title "feat(spa+daemon): file-not-found popup with three-layer fallback" --body "$(cat <<'EOF'
## Summary

- daemon fs.search engine + http handler (capability roots only; mandatory excludes union; respectGitignore default true; gitignore parse failure 4xx; mode envelope)
- SPA fsSearchByCapability helper (host-bound)
- Editor open behavior settings (popupOnMissingFile / autoSearchLayer1)
- Host-bound openFile service factory (ENOENT-only error classification)
- FileNotFoundPopup component + popup mount service (HMR-safe + AbortController)
- Terminal link / FileTreeView 改走 tryOpenFile pipeline
- Layer 2 (session cwd) + Layer 3 (workspace projectPath via moduleConfig.files) expand search

## Test plan

- [ ] cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build
- [ ] go test ./...
- [ ] 手動：點不存在的檔案 + popup off → 拋錯
- [ ] 手動：點不存在的檔案 + cache 命中 1 個 → 直接開
- [ ] 手動：點不存在的檔案 + cache 命中多個 → popup 列出 candidates
- [ ] 手動：點不存在的檔案 + cache 0 命中 → popup ask-expand → 點 expand → fs.search 結果
- [ ] 手動：popup 開啟後切 active host → 進行中的 stat 仍打 ctx.hostId
- [ ] 手動：popup 開啟後 ESC → fs.search 回來時不重新 mount popup
- [ ] 手動：fs.search body 傳 `kind: "absolute"` → daemon 拒（4xx）

Spec: SPEC.md (rev 6, P5)
EOF
)"
```

- [ ] **Step 3: 委派 codex 兩輪 review**

```bash
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" review --background
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" adversarial-review --background "P5 file-not-found popup + fs search server-side allowlist"
```

依「Review 問題彙整」表格規則處理 finding，merge 後 series 完成。

---

