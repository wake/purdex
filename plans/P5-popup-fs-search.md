# P5 — File-not-found popup service + Layer 1/2/3 + fs.search server-side allowlist

> 對應 SPEC.md `# P5` 段。本檔吸收 PLAN 第二輪 4 份 codex review 與 P5 相關修訂（次大宗 — 12 finding）。

## v4 修訂指引（實作前必看，覆寫原 task 對應段）

### fs.search server-side allowlist（D 決議 + 攻擊 critical C4）

| Task | 修訂 | 來源 |
|---|---|---|
| **5.1** | **拆 `5.1a search engine` (純函式 `Search(ctx, request)` in `internal/module/fs/search_engine.go`) + `5.1b HTTP handler` (in `internal/module/fs/search_handler.go`)** | 體質 review #7 + 通用 review C3 |
| **5.1b** | **改用 `(m *FsModule) handleSearch(...)` method pattern**（與 fs/module.go 既有 handler pattern 一致），route `mux.HandleFunc("POST /api/fs/search", m.handleSearch)`，**不要寫 top-level `HandleSearch`** | 通用 review A5 |
| **5.1** | **不接受 client-supplied absolute path roots**！只接 `roots: [{kind:"session-cwd", sessionCode} \| {kind:"workspace-projectPath", projectPath}]` capability，daemon 端解析；validate 不在 system path（`/`、`/etc`、`/sys`、`/Users` 直系、`$HOME` 直系） | D 決議 + 攻擊 critical C4 |
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
| **5.8** (or new 5.9) | **加 phase verification + PR task**（與 P1/P2/P4 對齊）：跑 vitest + lint + build + go test，PR 描述引用 `SPEC.md (rev 4, P5)`，兩輪 codex review | 通用 review C1 |
| **Final** | 補滿 `editor-open-flow.integration.test.tsx` 跨 phase regression：Editor enabled+cache hit / Editor disabled silent / missing+expand+search / tear-off keepSettings:true | 防守 review #14 |

---

PR 結束標準：點不存在的檔案會走「stat (ENOENT-only) → cache stat → popup → fs.search」管線；layer 2/3 由 popup 觸發；workspace context 在 await 後仍正確；fs.search 不接受 client absolute path；mandatory excludes / respectGitignore default 守住；popup HMR-safe + cancellation-safe。

## Task 5.1 — daemon `fs.search` endpoint

**Files:**
- Create: `internal/module/fs/search.go`
- Test: `internal/module/fs/search_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/fs/search_test.go`：

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

func TestFsSearch_BasenameMatchInRoots(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "bar.go"), "no")

	req := newSearchReq(t, searchRequest{Basename: "foo.go", Roots: []string{dir}, MaxResults: 10})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(resp.Matches))
	}
}

func TestFsSearch_ExcludeDirsPrunesSubtree(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "x", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")

	req := newSearchReq(t, searchRequest{
		Basename: "foo.go", Roots: []string{dir},
		ExcludeDirs: []string{"node_modules"},
	})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 1 || filepath.Base(filepath.Dir(resp.Matches[0].Path)) != "src" {
		t.Errorf("unexpected matches: %+v", resp.Matches)
	}
}

func TestFsSearch_MaxDepthLimits(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for i := 0; i < 5; i++ {
		deep = filepath.Join(deep, "d")
	}
	mustWrite(t, filepath.Join(deep, "foo.go"), "ok")
	req := newSearchReq(t, searchRequest{Basename: "foo.go", Roots: []string{dir}, MaxDepth: 3})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches due to depth, got %d", len(resp.Matches))
	}
}

func TestFsSearch_RejectsNonAbsoluteRoot(t *testing.T) {
	req := newSearchReq(t, searchRequest{Basename: "x", Roots: []string{"rel/dir"}})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-absolute root, got %d", w.Code)
	}
}

// helpers
func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSearchReq(t *testing.T, body searchRequest) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/fs/search", bytes.NewReader(b))
}

func mustDecode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/fs/ -run Search
```

- [ ] **Step 3: Implement search.go**

新建 `internal/module/fs/search.go`：

```go
package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type searchRequest struct {
	Basename             string   `json:"basename"`
	Roots                []string `json:"roots"`
	MaxResults           int      `json:"maxResults,omitempty"`
	MaxDepth             int      `json:"maxDepth,omitempty"`
	TimeoutMs            int      `json:"timeoutMs,omitempty"`
	ExcludeDirs          []string `json:"excludeDirs,omitempty"`
	ExcludeBasenameGlobs []string `json:"excludeBasenameGlobs,omitempty"`
	RespectGitignore     bool     `json:"respectGitignore,omitempty"`
}

type searchMatch struct {
	Path       string    `json:"path"`
	ModTime    time.Time `json:"modTime"`
	SizeBytes  int64     `json:"sizeBytes"`
	Root       string    `json:"root"`
}

type searchResponse struct {
	Matches  []searchMatch `json:"matches"`
	Truncated bool         `json:"truncated"`
}

const (
	defaultSearchMaxResults = 50
	defaultSearchMaxDepth   = 8
	defaultSearchTimeoutMs  = 5000
	hardCapMaxResults       = 200
)

var defaultExcludeDirs = []string{"node_modules", ".git", ".cache", "dist"}
var defaultExcludeGlobs = []string{"*.lock", "*.log"}

func HandleSearch(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxBodySize)
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Basename == "" {
		http.Error(w, "basename required", http.StatusBadRequest)
		return
	}
	for _, root := range req.Roots {
		if !filepath.IsAbs(filepath.Clean(root)) {
			http.Error(w, "roots must be absolute", http.StatusBadRequest)
			return
		}
	}
	if req.MaxResults <= 0 {
		req.MaxResults = defaultSearchMaxResults
	}
	if req.MaxResults > hardCapMaxResults {
		req.MaxResults = hardCapMaxResults
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = defaultSearchMaxDepth
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = defaultSearchTimeoutMs
	}
	if req.ExcludeDirs == nil {
		req.ExcludeDirs = defaultExcludeDirs
	}
	if req.ExcludeBasenameGlobs == nil {
		req.ExcludeBasenameGlobs = defaultExcludeGlobs
	}

	excludeDirSet := make(map[string]struct{}, len(req.ExcludeDirs))
	for _, d := range req.ExcludeDirs {
		excludeDirSet[d] = struct{}{}
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutMs)*time.Millisecond)
	defer cancel()

	matches := make([]searchMatch, 0, req.MaxResults)
	truncated := false

	for _, root := range req.Roots {
		walkDepth := 0
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(root, path)
			depth := strings.Count(rel, string(os.PathSeparator))
			_ = walkDepth
			if depth > req.MaxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if _, skip := excludeDirSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			// File: check basename glob excludes
			for _, glob := range req.ExcludeBasenameGlobs {
				if ok, _ := filepath.Match(glob, d.Name()); ok {
					return nil
				}
			}
			if d.Name() == req.Basename {
				info, err := d.Info()
				if err != nil {
					return nil
				}
				matches = append(matches, searchMatch{
					Path: path, ModTime: info.ModTime(), SizeBytes: info.Size(), Root: root,
				})
				if len(matches) >= req.MaxResults {
					truncated = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if err != nil && err != context.DeadlineExceeded {
			break
		}
		if err == context.DeadlineExceeded || ctx.Err() != nil {
			truncated = true
			break
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].ModTime.After(matches[j].ModTime) })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(searchResponse{Matches: matches, Truncated: truncated})
}
```

- [ ] **Step 4: Wire route into module**

在 `internal/module/fs/module.go`（或既有 routes 註冊處）加：

```go
mux.HandleFunc("POST /api/fs/search", HandleSearch)
```

- [ ] **Step 5: Run tests, expect PASS**

```
go test ./internal/module/fs/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/module/fs/search.go internal/module/fs/search_test.go internal/module/fs/module.go
git commit -m "feat(daemon): fs.search endpoint with depth/exclude/timeout"
```

---

## Task 5.2 — `gitignore` + symlink loop tests（補強）

**Files:**
- Modify: `internal/module/fs/search_test.go`

- [ ] **Step 1: Write failing test**

加 case：

```go
func TestFsSearch_GitignoreFiltersResults(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "skip")
	mustWrite(t, filepath.Join(dir, "kept.go"), "ok")
	req := newSearchReq(t, searchRequest{
		Basename: "ignored.go", Roots: []string{dir},
		RespectGitignore: true,
	})
	w := httptest.NewRecorder()
	HandleSearch(w, req)
	var resp searchResponse
	mustDecode(t, w, &resp)
	if len(resp.Matches) != 0 {
		t.Errorf("ignored.go should not appear with respectGitignore: %+v", resp.Matches)
	}
}

func TestFsSearch_SymlinkLoopDoesNotInfiniteWalk(t *testing.T) {
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
		req := newSearchReq(t, searchRequest{Basename: "x.go", Roots: []string{dir}, MaxDepth: 6})
		w := httptest.NewRecorder()
		HandleSearch(w, req)
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

- [ ] **Step 2: Run test, expect FAIL（gitignore 還沒實作）**

- [ ] **Step 3: Implement gitignore via go-gitignore**

Add dependency:

```bash
go get github.com/sabhiram/go-gitignore
go mod tidy
```

在 `search.go` 內 walk loop 起點加 root-level `.gitignore` 解析：

```go
import gitignore "github.com/sabhiram/go-gitignore"

// inside HandleSearch, before WalkDir:
var ignore *gitignore.GitIgnore
if req.RespectGitignore {
    if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
        ignore, _ = gitignore.CompileIgnoreLines(strings.Split(string(data), "\n")...)
    }
}

// inside WalkDir func, after d.IsDir() handling:
if ignore != nil {
    rel, _ := filepath.Rel(root, path)
    if ignore.MatchesPath(rel) {
        if d.IsDir() {
            return filepath.SkipDir
        }
        return nil
    }
}
```

Symlink loop：`filepath.WalkDir` 預設不 follow symlink（`SkipDir` for symlink-to-dir 自動避免），但若需要可加 `os.Lstat` 檢查。本實作預設不 follow，loop 測試應通過。

- [ ] **Step 4: Run tests, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add internal/module/fs/search.go internal/module/fs/search_test.go go.mod go.sum
git commit -m "feat(daemon): fs.search respects root .gitignore and avoids symlink loops"
```

---

## Task 5.3 — SPA `fsSearchByBasename` helper

**Files:**
- Create: `spa/src/lib/fs-search.ts`
- Test: `spa/src/lib/fs-search.test.ts`

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fsSearchByBasename } from './fs-search'
import { useHostStore } from '../stores/useHostStore'

describe('fsSearchByBasename', () => {
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
        truncated: false,
      }), { status: 200 }),
    ) as never
  })

  it('posts search request to host daemon and returns matches', async () => {
    const matches = await fsSearchByBasename('h1', 'foo.go', ['/a', '/b'])
    expect(matches.map((m) => m.path)).toEqual(['/a/foo.go', '/b/foo.go'])
  })

  it('rejects non-absolute roots before calling daemon', async () => {
    await expect(fsSearchByBasename('h1', 'foo.go', ['rel/dir'])).rejects.toThrow(/absolute/i)
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

```ts
import { useHostStore } from '../stores/useHostStore'

export interface SearchMatch {
  path: string
  modTime: string
  sizeBytes: number
  root: string
}

export interface SearchResponse {
  matches: SearchMatch[]
  truncated: boolean
}

export async function fsSearchByBasename(
  hostId: string,
  basename: string,
  roots: string[],
  opts: { maxResults?: number; maxDepth?: number; timeoutMs?: number } = {},
): Promise<SearchMatch[]> {
  for (const r of roots) {
    if (!r.startsWith('/')) throw new Error(`Search root must be absolute: ${r}`)
  }
  const state = useHostStore.getState()
  const base = state.getDaemonBase(hostId)
  const headers = { 'Content-Type': 'application/json', ...state.getAuthHeaders(hostId) }
  const body = JSON.stringify({ basename, roots, ...opts })
  const res = await fetch(`${base}/api/fs/search`, { method: 'POST', headers, body })
  if (!res.ok) throw new Error(`fs.search failed: ${res.status} ${res.statusText}`)
  const json = (await res.json()) as SearchResponse
  return json.matches
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/fs-search.ts spa/src/lib/fs-search.test.ts
git commit -m "feat(spa): fsSearchByBasename helper hits per-host daemon"
```

---

## Task 5.4 — Two new UI settings + `EditorOpenBehaviorSection`

**Files:**
- Modify: `spa/src/stores/useUISettingsStore.ts`
- Create: `spa/src/components/settings/EditorOpenBehaviorSection.tsx`
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
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

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

- [ ] **Step 3: Wire into Editor module settings**

在 `register-modules.tsx` Editor module `settings` 陣列加：

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
git add spa/src/stores/useUISettingsStore.ts spa/src/components/settings/EditorOpenBehaviorSection.tsx spa/src/lib/register-modules.tsx spa/src/i18n/*.json
git commit -m "feat(spa): editor open behavior settings (popup + auto layer1)"
```

---

## Task 5.5 — `tryOpenFile` flow

**Files:**
- Create: `spa/src/lib/open-file.ts`
- Test: `spa/src/lib/open-file.test.ts`

- [ ] **Step 1: Write failing test**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { tryOpenFile } from './open-file'
import { usePathCacheStore } from '../stores/usePathCacheStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'

const mockBackend = {
  stat: vi.fn(),
  // ... other methods optional
}

const mockOpenInTab = vi.fn()
const mockOpenPopup = vi.fn()

describe('tryOpenFile', () => {
  beforeEach(() => {
    usePathCacheStore.setState({ dirsByScope: {} } as never, false)
    useUISettingsStore.setState({ popupOnMissingFile: true, autoSearchLayer1: true } as never, false)
    mockBackend.stat.mockReset()
    mockOpenInTab.mockReset()
    mockOpenPopup.mockReset()
  })

  const ctx = { hostId: 'h1', sourceWorkspaceId: 'w1' }
  const file = { path: '/a/b/foo.go', name: 'foo.go', extension: 'go', isDirectory: false }
  const source = { type: 'inapp' as const }

  it('opens directly when path exists', async () => {
    mockBackend.stat.mockResolvedValue({ isDirectory: false })
    await tryOpenFile(file as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenInTab).toHaveBeenCalledWith(file, source, ctx)
    expect(mockOpenPopup).not.toHaveBeenCalled()
  })

  it('throws when popupOnMissingFile is off and file missing', async () => {
    useUISettingsStore.setState({ popupOnMissingFile: false } as never, false)
    mockBackend.stat.mockResolvedValue(null)
    await expect(tryOpenFile(file as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })).rejects.toThrow(/not found/i)
  })

  it('layer1 single hit opens directly (after stat verify)', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    mockBackend.stat.mockImplementation(async (p: string) => p === '/a/b/foo.go' ? { isDirectory: false } : null)
    // Simulate: original file.path is '/elsewhere/foo.go' which doesn't exist
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenInTab).toHaveBeenCalledWith({ ...missing, path: '/a/b/foo.go' }, source, ctx)
  })

  it('layer1 multi hits opens popup with verified candidates', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    mockBackend.stat.mockImplementation(async (p: string) => p.endsWith('foo.go') && p !== '/elsewhere/foo.go' ? { isDirectory: false } : null)
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(mockOpenPopup).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'layer1-multi',
      hits: ['/c/d/foo.go', '/a/b/foo.go'],
    }))
  })

  it('layer1 stat fail prunes cache and falls to ask-expand', async () => {
    usePathCacheStore.getState().add('h1', 'w1', '/stale/dir')
    mockBackend.stat.mockResolvedValue(null)  // every stat fails
    const missing = { ...file, path: '/elsewhere/foo.go' }
    await tryOpenFile(missing as never, source, ctx, { backend: mockBackend as never, openInTab: mockOpenInTab, openPopup: mockOpenPopup })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
    expect(mockOpenPopup).toHaveBeenCalledWith(expect.objectContaining({ mode: 'ask-expand' }))
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement**

```ts
import type { FileInfo, FileSource } from '../types/fs'
import { usePathCacheStore } from '../stores/usePathCacheStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'

export interface OpenFileContext {
  hostId: string
  sourceWorkspaceId: string
  sessionCode?: string
  cwdResolver?: () => Promise<string | null>
}

interface FsLike {
  stat(path: string): Promise<unknown | null>
}

export interface PopupSpec {
  mode: 'layer1-multi' | 'ask-expand'
  hits?: string[]
  file: FileInfo
  source: FileSource
  ctx: OpenFileContext
}

export interface OpenDeps {
  backend: FsLike
  openInTab: (file: FileInfo, source: FileSource, ctx: OpenFileContext) => void
  openPopup: (spec: PopupSpec) => void
}

export class FileNotFoundError extends Error {
  constructor(public readonly path: string) {
    super(`File not found: ${path}`)
  }
}

export async function tryOpenFile(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
  deps: OpenDeps,
): Promise<void> {
  // 1. Direct stat
  if (await deps.backend.stat(file.path).catch(() => null)) {
    deps.openInTab(file, source, ctx)
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
      if (await deps.backend.stat(c).catch(() => null)) {
        verified.push(c)
      } else {
        cache.pruneStaleCandidate(ctx.hostId, ctx.sourceWorkspaceId, c)
      }
    }
    if (verified.length === 1) {
      deps.openInTab({ ...file, path: verified[0] }, source, ctx)
      return
    }
    if (verified.length > 1) {
      deps.openPopup({ mode: 'layer1-multi', hits: verified, file, source, ctx })
      return
    }
  }

  // 4. Fall through
  deps.openPopup({ mode: 'ask-expand', file, source, ctx })
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/open-file.ts spa/src/lib/open-file.test.ts
git commit -m "feat(spa): tryOpenFile flow with stat-verified layer 1"
```

---

## Task 5.6 — `FileNotFoundPopup` component

**Files:**
- Create: `spa/src/components/editor/FileNotFoundPopup.tsx`
- Test: `spa/src/components/editor/FileNotFoundPopup.test.tsx`

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
git add spa/src/components/editor/FileNotFoundPopup.tsx spa/src/components/editor/FileNotFoundPopup.test.tsx
git commit -m "feat(spa): FileNotFoundPopup component with ESC + candidate list"
```

---

## Task 5.7 — Integrate into terminal-link + FileTreeView

**Files:**
- Modify: `spa/src/lib/terminal-link/openers/file-path.ts`
- Modify: `spa/src/components/FileTreeView.tsx`
- Modify: `spa/src/lib/register-modules.tsx`（注入 deps）

- [ ] **Step 1: Replace existing open-on-click with `tryOpenFile`**

Terminal link 內：把點擊呼叫 `openSingletonTab` 的部分改成走 `tryOpenFile`。`tryOpenFile` 把 deps（backend + openInTab + openPopup）注入；`openInTab` 內部仍呼叫 `openSingletonTab` 做最後 tab 創建（保留 P2 的 same-kind 行為）。

具體：在 `register-modules.tsx` 的 `terminalLink.filePathOpener` deps 注入 `tryOpenFile` adapter；popup 開關以 React portal mount。

實作 popup mount 的 helper（`spa/src/lib/popup-mount.tsx`，新建）：

```tsx
import { createRoot, type Root } from 'react-dom/client'
import { FileNotFoundPopup } from '../components/editor/FileNotFoundPopup'
import type { PopupSpec } from './open-file'

let root: Root | null = null
let host: HTMLDivElement | null = null

export function showFileNotFoundPopup(
  spec: PopupSpec,
  onOpenPath: (path: string) => void,
  onExpand: () => void,
): void {
  if (!host) {
    host = document.createElement('div')
    document.body.appendChild(host)
    root = createRoot(host)
  }
  const close = () => {
    root?.render(<></>)
  }
  root!.render(
    <FileNotFoundPopup
      spec={spec}
      onClose={close}
      onOpenPath={(p) => { close(); onOpenPath(p) }}
      onExpand={() => { close(); onExpand() }}
    />,
  )
}
```

(Layer 2/3 fs.search 邏輯放在 `onExpand` callback 內 — 呼叫 `fsSearchByBasename` 並產出新的 popup。為簡化，本 task 不做 layer 2/3 完整流，先把 popup 框架接上；layer 2/3 在 Task 5.8。)

- [ ] **Step 2: Wire in register-modules.tsx**

Replace existing `filePathOpener` openSingletonTab call with `tryOpenFile` indirection — 詳細替換留實作期間根據實際 file-path.ts 結構決定。

- [ ] **Step 3: 跑全測**

```
cd spa && npx vitest run
```

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(spa): terminal-link/FileTree open files via tryOpenFile pipeline"
```

---

## Task 5.8 — Layer 2/3 expand search + popup re-render

**Files:**
- Modify: `spa/src/lib/popup-mount.tsx`（onExpand 真正實作）
- Modify: `spa/src/components/editor/FileNotFoundPopup.tsx`（顯示 expand results 區段）

- [ ] **Step 1: Extend popup spec to support expanded mode**

`PopupSpec` 加 `mode: 'expanded'`，內含 `layer2Hits` / `layer3Hits` arrays。

- [ ] **Step 2: Implement onExpand using layer 2 + layer 3**

```ts
async function onExpand(spec: PopupSpec): Promise<void> {
  const layer2Roots: string[] = []
  if (spec.ctx.cwdResolver) {
    const cwd = await spec.ctx.cwdResolver()
    if (cwd) layer2Roots.push(cwd)
  }

  const wsState = useWorkspaceStore.getState()
  const ws = wsState.workspaces.find((w) => w.id === spec.ctx.sourceWorkspaceId)
  const projectPath = ws?.config?.projectPath as string | undefined
  const layer3Roots = projectPath ? [projectPath] : []

  const [layer2, layer3] = await Promise.all([
    layer2Roots.length ? fsSearchByBasename(spec.ctx.hostId, spec.file.name, layer2Roots) : [],
    layer3Roots.length ? fsSearchByBasename(spec.ctx.hostId, spec.file.name, layer3Roots) : [],
  ])

  // Re-render popup with expanded results
  showFileNotFoundPopupExpanded(spec, layer2, layer3)
}
```

- [ ] **Step 3: Update FileNotFoundPopup to render expanded results**

加 sections for `layer2Hits` / `layer3Hits`，相同的 `<button onClick={() => onOpenPath(...)}>` pattern。

- [ ] **Step 4: Test expansion path**

加 `FileNotFoundPopup.test.tsx` case：`mode: 'expanded'` + 給 layer 2/3 hits → render 兩個 section + 點任一條 callback `onOpenPath`。

- [ ] **Step 5: Commit + PR**

```bash
git commit -m "feat(spa): file-not-found popup expands search via layer 2/3"
```

PR + 兩輪 codex review。

---

