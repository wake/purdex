# P4 — PathHint v1 channel + CC HookInstaller + SPA path cache

> 對應 SPEC.md `# P4` 段。本檔吸收 PLAN 第二輪 4 份 codex review 與 P4 相關修訂（最大宗 — 13 finding）。

## v4 修訂指引（實作前必看，覆寫原 task 對應段）

### Schema / 廣播

| Task | 修訂 | 來源 |
|---|---|---|
| **4.1** | **PathHint schema v1 minimal**：`{schemaVersion: 1, agentId, sessionCode, dir, kind, timestamp}` — **移除 `path / pathKind / baseDir / confidence / toolName / hostId`**。HostId 由 broadcast 帶。完整 path 不廣播（privacy）。Go const 列舉只剩 `Kind` (`read|write|edit`) | C 決議 + 攻擊 critical C6 + 防守 review #3 |
| **4.1** | TS 對應 type 也改 v1 minimal；unknown `kind` 值 defensive drop | 同上 |
| **4.2** | **dedup key 改 `(SessionCode, Dir, Basename)`**（basename 進 dedup key 但不進 payload）— 避免 SPA prune 後 5 秒同 dir 不同 file 不能 reseed 的真空期 | 攻擊 review #13 |
| **4.2** | 純函式 `ExtractPathHint(rawEvent, eventName, agentType) (PathHint, basename, bool)` 抽出獨立可測 | 通用 review B3 |
| **4.3** | **拆 `4.3a` extractor + emit unit / `4.3b` hook handler integration**：`normalized.ToolName / ToolInput / HookEventName` **欄位實際不存在**！改從 `req.RawEvent` decode tool name/input + `req.EventName == "PreToolUse" \|\| "PostToolUse"` 判斷；用 `m.resolveSessionCode(req.TmuxSession)` | 通用 review A4 + C3 |
| **4.3b** | 整合測試用既有 fakes 或測 `emitPathHint` with stub event bus seam（不要做 mockCore 也未對齊 `core.Core` concrete type） | 通用 review B3 |

### SPA store

| Task | 修訂 | 來源 |
|---|---|---|
| **4.4** | STORAGE_KEYS 命名 `PATH_CACHE_V1: 'purdex-path-cache-v1'`（含版本後綴，未來 v2 不撞 namespace） | 防守 review #15 + 體質 |
| **4.4** | TS PathHint type 對齊 v1 minimal；schemaVersion check | C 決議 |
| **4.5** | **store 路徑搬到 `spa/src/stores/path-cache/usePathCacheStore.ts`** | 體質 review #3 |
| **4.5** | `add()` 內建 normalization：非 absolute reject、trim trailing slash、`./..` canonical | 防守 review #7 |
| **4.5** | **加 `storage: purdexStorage`**（與其他 store 一致） | 攻擊 review #2 |
| **4.5** | **加 `onRehydrateStorage` defensive**：localStorage 內容 malformed → reset `dirsByScope = {}` 不炸 | 同上 |
| **4.5** | LRU 補 **duplicate move-to-head + overflow tail eviction** 測試（add 0..49 → touch d0 → add d50 → expect d1 evicted, d0 留 head 區） | 攻擊 review #14 |

### Helpers / dispatch

| Task | 修訂 | 來源 |
|---|---|---|
| **4.6** | helper 重命名 `resolveWorkspaceIdForAgentSession`（更具體，避免被誤用為泛用 workspace resolver），放 `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts` | 體質 review #4 + #9 |
| **4.6** | useWorkspaceStore 實際路徑 `features/workspace/store`，不是 `stores/useWorkspaceStore` | 通用 review A2 |
| **4.6** | 測試 fixture 的 PaneLayout leaf shape **必須是 `{type:'leaf', pane:{id, content}}`**，不是 `{id, content}` | 通用 review B2 |
| **4.6** | helper 語意改：**多重 workspace 命中 → return null**（不取 active 捷徑），避免寫到「使用者剛切過去的 workspace」 | 攻擊 review #6 + 防守 review #5 |
| **4.7** | **拆 `agent-ws-dispatch.ts` 為 `agent-ws/` 子目錄**：`index.ts` (router) + `status-dispatch.ts` (既有) + `path-hint-dispatch.ts` (新) | 體質 review #4 |
| **4.7** | path-hint-dispatch try/catch 包整段 — 加 regression test：mock resolver throw，確保 dispatch 不炸 | 攻擊 review #3 |
| **4.7** | schemaVersion check：`!== 1` defensive drop；malformed JSON drop；non-absolute dir drop | C 決議 |
| **4.7** | prerequisites 列明：PathHint type / usePathCacheStore.add / resolveWorkspaceIdForAgentSession 已存在 | 通用 review B4 |
| **4.7** | fixture leaf shape 同 4.6 | 通用 review B2 |
| **4.8** | **whitelist 三條 event type**：`agent.status` / `agent.status.cleared` / `agent.path_hint` — **禁用 `event.type.startsWith('agent.')` broad filter** | 防守 review #9 |
| **4.8** | regression test：傳 `agent.foo` 不被 dispatch | 同上 |

### Lifecycle

| Task | 修訂 | 來源 |
|---|---|---|
| **4.9** | **拆獨立檔 `spa/src/stores/path-cache/auto-cleanup.ts`**（不讓 store 本體 import workspace/host store；避免循環依賴 + 測試污染） | 體質 review #3 + #17 |
| **4.9** | `attachPathCacheAutoCleanup()` **回 dispose function**（`() => void`）；測試 `afterEach` 必須呼叫；HMR `import.meta.hot.dispose` 也呼叫 | 攻擊 review #7 |
| **4.9** | 用 zustand subscribe 的 **prevState** 算 removed ids（不要用 closure 內 `lastWsIds` Set） | 攻擊 review #7 |
| **4.9** | **hydration race**：等 `useWorkspaceStore.persist.hasHydrated()` / `onFinishHydration` 才 attach；防止以空 workspace set 作 baseline 誤刪 cache | 攻擊 review #2 |
| **4.9** | **`keepSettings: true`（tear-off / merge）→ 只清本 window in-memory**，**保留 persisted localStorage**（避免影響其他同 origin window 的同 workspace cache） | 防守 review #11 修正 |
| **4.9** | `keepSettings: false`（真 delete）→ in-memory + persisted 都清 | 同上 |
| **4.9** | host remove → 整 host 所有 scope 清 (in-memory + persisted) | — |
| **4.9** | 測試：repeat attach 不重複 subscribe；dispose 後不再 cleanup；hydration 順序 race；keepSettings 場景區分 | 攻擊 review #7 |

### Phase

| Task | 修訂 | 來源 |
|---|---|---|
| **All** | commit message lowercase | 通用 review C2 |
| **4.10** | Spec 引用改 `SPEC.md (rev 4, P4)`；verification gate 跑 SPA + Go 全測 | 通用 review C1 |

---

PR 結束標準：CC Read tool 觸發 → SPA 對應 workspace path cache 增加 1 條（payload v1 minimal，dir-level 不含 path/basename）；whitelist 三條 event；workspace remove / host remove 連動清理（區分 keepSettings）；非 absolute path defensive drop；hydration race 不誤殺。

## Task 4.1 — PathHint Go schema + ring buffer

**Files:**
- Create: `internal/module/agent/path_hint.go`
- Test: `internal/module/agent/path_hint_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_test.go`：

```go
package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPathHint_JSONRoundTrip(t *testing.T) {
	h := PathHint{
		AgentID:     "claude-code",
		HostID:      "h1",
		SessionCode: "abc123",
		Kind:        PathHintKindRead,
		Path:        "/a/b/c.go",
		Dir:         "/a/b",
		PathKind:    PathKindAbsolute,
		BaseDir:     "",
		Confidence:  ConfidenceHigh,
		ToolName:    "Read",
		Timestamp:   time.Unix(1000, 0).UTC(),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got PathHint
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Path != "/a/b/c.go" || got.PathKind != PathKindAbsolute {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestPathHintRingBuffer_AddAndCap(t *testing.T) {
	r := NewPathHintRingBuffer(3)
	for i := 0; i < 5; i++ {
		r.Push(PathHint{Dir: "/d/" + string(rune('a'+i))})
	}
	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0].Dir != "/d/c" || got[2].Dir != "/d/e" {
		t.Errorf("unexpected ring contents: %+v", got)
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/agent/...
```

`PathHint` / `NewPathHintRingBuffer` 尚未存在。

- [ ] **Step 3: Implement schema + ring buffer**

新建 `internal/module/agent/path_hint.go`：

```go
package agent

import (
	"sync"
	"time"
)

const (
	PathKindAbsolute = "absolute"
	PathKindRelative = "relative"
	PathKindUnknown  = "unknown"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"

	PathHintKindRead    = "read"
	PathHintKindWrite   = "write"
	PathHintKindEdit    = "edit"
	PathHintKindUnknown = "unknown"
)

// PathHint is the agent-agnostic schema describing a path the agent has
// recently touched.  Kept dir-level — never includes the file basename so
// that downstream consumers can treat it as a working-dir hint, not a file
// reference.
type PathHint struct {
	AgentID     string    `json:"agentId"`
	HostID      string    `json:"hostId"`
	SessionCode string    `json:"sessionCode"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path,omitempty"`
	Dir         string    `json:"dir"`
	PathKind    string    `json:"pathKind"`
	BaseDir     string    `json:"baseDir,omitempty"`
	Confidence  string    `json:"confidence"`
	ToolName    string    `json:"toolName"`
	Timestamp   time.Time `json:"timestamp"`
}

// PathHintRingBuffer holds a fixed-size FIFO of recent hints per host.
// In-memory only; lost on daemon restart.
type PathHintRingBuffer struct {
	mu    sync.Mutex
	cap   int
	items []PathHint
}

func NewPathHintRingBuffer(cap int) *PathHintRingBuffer {
	if cap <= 0 {
		cap = 1
	}
	return &PathHintRingBuffer{cap: cap, items: make([]PathHint, 0, cap)}
}

func (r *PathHintRingBuffer) Push(h PathHint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, h)
	if len(r.items) > r.cap {
		r.items = r.items[len(r.items)-r.cap:]
	}
}

func (r *PathHintRingBuffer) Snapshot() []PathHint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PathHint, len(r.items))
	copy(out, r.items)
	return out
}
```

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run PathHint
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/path_hint.go internal/module/agent/path_hint_test.go
git commit -m "feat(daemon): PathHint schema with bounded ring buffer"
```

---

## Task 4.2 — PathHint extractor with dedup

**Files:**
- Create: `internal/module/agent/path_hint_extractor.go`
- Test: `internal/module/agent/path_hint_extractor_test.go`

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_extractor_test.go`：

```go
package agent

import (
	"testing"
	"time"
)

func TestExtractCCPathHint_AbsoluteRead(t *testing.T) {
	x := NewPathHintExtractor(0) // 0 → no dedup
	now := time.Unix(1000, 0)
	h, ok := x.ExtractCC("h1", "abc123", "Read", map[string]any{"file_path": "/a/b/c.go"}, now)
	if !ok {
		t.Fatal("expected hint, got drop")
	}
	if h.Dir != "/a/b" || h.PathKind != PathKindAbsolute || h.Confidence != ConfidenceHigh {
		t.Errorf("unexpected: %+v", h)
	}
}

func TestExtractCCPathHint_DropsRelative(t *testing.T) {
	x := NewPathHintExtractor(0)
	_, ok := x.ExtractCC("h1", "abc123", "Read", map[string]any{"file_path": "rel/path.go"}, time.Unix(0, 0))
	if ok {
		t.Fatal("expected drop for non-absolute path")
	}
}

func TestExtractCCPathHint_Dedup(t *testing.T) {
	x := NewPathHintExtractor(5 * time.Second)
	t0 := time.Unix(1000, 0)
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/c.go"}, t0); !ok {
		t.Fatal("first hint should pass")
	}
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/d.go"}, t0.Add(2*time.Second)); ok {
		t.Fatal("same dir within window should dedup")
	}
	if _, ok := x.ExtractCC("h1", "s1", "Read", map[string]any{"file_path": "/a/b/d.go"}, t0.Add(6*time.Second)); !ok {
		t.Fatal("dir after window should pass")
	}
}

func TestExtractCCPathHint_UnknownToolDrops(t *testing.T) {
	x := NewPathHintExtractor(0)
	_, ok := x.ExtractCC("h1", "s1", "Bash", map[string]any{}, time.Unix(0, 0))
	if ok {
		t.Fatal("expected drop for non-file tool")
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/agent/ -run Extract
```

- [ ] **Step 3: Implement extractor**

新建 `internal/module/agent/path_hint_extractor.go`：

```go
package agent

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PathHintExtractor pulls PathHint records from raw CC hook tool_input.
// Holds a per-(session, dir) dedup window — same dir within window is dropped.
type PathHintExtractor struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time // key = sessionCode|dir
}

func NewPathHintExtractor(window time.Duration) *PathHintExtractor {
	return &PathHintExtractor{window: window, last: make(map[string]time.Time)}
}

var ccFileTools = map[string]string{
	"Read":         PathHintKindRead,
	"Write":        PathHintKindWrite,
	"Edit":         PathHintKindEdit,
	"NotebookEdit": PathHintKindEdit,
}

// ExtractCC returns (hint, true) if the tool/path qualify; otherwise (zero, false).
func (e *PathHintExtractor) ExtractCC(hostID, sessionCode, toolName string, toolInput map[string]any, now time.Time) (PathHint, bool) {
	kind, ok := ccFileTools[toolName]
	if !ok {
		return PathHint{}, false
	}
	raw, ok := toolInput["file_path"].(string)
	if !ok || raw == "" {
		return PathHint{}, false
	}
	if !filepath.IsAbs(raw) {
		return PathHint{}, false // CC always sends absolute paths; drop defensively.
	}
	dir := filepath.Dir(raw)
	if e.window > 0 {
		key := sessionCode + "|" + dir
		e.mu.Lock()
		if last, found := e.last[key]; found && now.Sub(last) < e.window {
			e.mu.Unlock()
			return PathHint{}, false
		}
		e.last[key] = now
		// opportunistic GC: drop entries older than 10× window
		cutoff := now.Add(-10 * e.window)
		for k, ts := range e.last {
			if ts.Before(cutoff) {
				delete(e.last, k)
			}
		}
		e.mu.Unlock()
	}
	return PathHint{
		AgentID:     "claude-code",
		HostID:      hostID,
		SessionCode: sessionCode,
		Kind:        kind,
		Path:        raw,
		Dir:         dir,
		PathKind:    PathKindAbsolute,
		Confidence:  ConfidenceHigh,
		ToolName:    toolName,
		Timestamp:   now,
	}, true
}

// Used to silence unused-import warnings if `strings` isn't needed at compile time.
var _ = strings.TrimSpace
```

(刪除最後 `var _ = strings.TrimSpace` 若不需要 strings import；保留為示意。)

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run Extract
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/path_hint_extractor.go internal/module/agent/path_hint_extractor_test.go
git commit -m "feat(daemon): CC path hint extractor with dedup window"
```

---

## Task 4.3 — Wire emit into agent handler

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`（加 extractor + ring buffer 欄位）

- [ ] **Step 1: Add fields to Module struct**

在 `internal/module/agent/module.go` 既有 `Module` struct 內加：

```go
type Module struct {
    // ... existing fields
    pathHintExtractor *PathHintExtractor
    pathHintBuffer    *PathHintRingBuffer
}
```

`agent.New(...)` 內初始化（5 秒 dedup window，200 條 ring）：

```go
m.pathHintExtractor = NewPathHintExtractor(5 * time.Second)
m.pathHintBuffer = NewPathHintRingBuffer(200)
```

- [ ] **Step 2: Add emit helper to handler.go**

在 `handler.go` 既有 `emitHookToSession` 附近加：

```go
func (m *Module) emitPathHint(hostID, sessionCode, toolName string, toolInput map[string]any) {
    h, ok := m.pathHintExtractor.ExtractCC(hostID, sessionCode, toolName, toolInput, time.Now())
    if !ok {
        return
    }
    m.pathHintBuffer.Push(h)
    payload, err := json.Marshal(h)
    if err != nil {
        log.Printf("path_hint: marshal failed: %v", err)
        return
    }
    m.core.Events.Broadcast(sessionCode, "agent.path_hint", string(payload))
}
```

- [ ] **Step 3: Call emit in PreToolUse / PostToolUse hook handler**

找既有 hook handler（`handleHookStatus` 或 emit-hook 接點），在已 normalized event 處理之後加：

```go
// Where hostID, sessionCode, normalized.ToolName, normalized.ToolInput are available:
if normalized.HookEventName == "PreToolUse" || normalized.HookEventName == "PostToolUse" {
    if input, ok := normalized.ToolInput.(map[string]any); ok {
        m.emitPathHint(hostID, sessionCode, normalized.ToolName, input)
    }
}
```

> 具體 normalized payload shape 依現行 `agentpkg.NormalizedEvent` 內容調整；emit 只在工具事件上執行。

- [ ] **Step 4: Add test**

擴 `path_hint_test.go`（或新建 handler_path_hint_test.go）：

```go
func TestEmitPathHint_BroadcastFormat(t *testing.T) {
    // Build a Module with stubbed core that captures Broadcast calls
    var got struct{ session, kind, value string }
    core := &mockCore{broadcast: func(s, k, v string) { got.session, got.kind, got.value = s, k, v }}
    m := &Module{
        core:              core,
        pathHintExtractor: NewPathHintExtractor(0),
        pathHintBuffer:    NewPathHintRingBuffer(10),
    }
    m.emitPathHint("h1", "sess1", "Read", map[string]any{"file_path": "/a/b/c.go"})
    if got.kind != "agent.path_hint" {
        t.Errorf("kind = %q", got.kind)
    }
    var hint PathHint
    if err := json.Unmarshal([]byte(got.value), &hint); err != nil {
        t.Fatalf("payload not JSON: %v", err)
    }
    if hint.Dir != "/a/b" {
        t.Errorf("dir mismatch: %s", hint.Dir)
    }
}
```

(`mockCore` 需要 stub — 可參考 `internal/module/agent/fakes_test.go` 內既有 fake patterns。)

- [ ] **Step 5: Run test, expect PASS**

```
go test ./internal/module/agent/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/module/agent/module.go internal/module/agent/handler.go internal/module/agent/path_hint_test.go
git commit -m "feat(daemon): emit agent.path_hint on CC PreToolUse/PostToolUse"
```

---

## Task 4.4 — STORAGE_KEYS.PATH_CACHE + PathHint TS type

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`
- Modify: `spa/src/types/agent-events.ts`

- [ ] **Step 1: Add storage key**

在 `spa/src/lib/storage/keys.ts` `STORAGE_KEYS` object 加：

```ts
PATH_CACHE: 'purdex-path-cache',
```

- [ ] **Step 2: Define TS PathHint type**

在 `spa/src/types/agent-events.ts` 加：

```ts
export const PATH_KIND = ['absolute', 'relative', 'unknown'] as const
export type PathKind = (typeof PATH_KIND)[number]

export const CONFIDENCE = ['high', 'medium', 'low'] as const
export type Confidence = (typeof CONFIDENCE)[number]

export interface PathHint {
  agentId: string
  hostId: string
  sessionCode: string
  kind: 'read' | 'write' | 'edit' | 'unknown'
  path?: string
  dir: string
  pathKind: PathKind
  baseDir?: string
  confidence: Confidence
  toolName: string
  timestamp: string  // ISO 8601
}

export function isValidPathKind(v: unknown): v is PathKind {
  return typeof v === 'string' && (PATH_KIND as readonly string[]).includes(v)
}

export function isValidConfidence(v: unknown): v is Confidence {
  return typeof v === 'string' && (CONFIDENCE as readonly string[]).includes(v)
}
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/types/agent-events.ts
git commit -m "feat(spa): PATH_CACHE storage key + PathHint type with const enums"
```

---

## Task 4.5 — `usePathCacheStore` LRU + persist

**Files:**
- Create: `spa/src/stores/usePathCacheStore.ts`
- Test: `spa/src/stores/usePathCacheStore.test.ts`

- [ ] **Step 1: Write failing test**

新建 `spa/src/stores/usePathCacheStore.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { usePathCacheStore } from './usePathCacheStore'

const reset = () =>
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)

describe('usePathCacheStore', () => {
  beforeEach(reset)

  it('add inserts dir at head and dedups existing dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs).toEqual(['/a/b', '/c/d'])
  })

  it('LRU caps at 50 entries per scope', () => {
    for (let i = 0; i < 60; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d59')
    expect(dirs[49]).toBe('/d10')
  })

  it('lookup combines basename with each cached dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    expect(usePathCacheStore.getState().lookup('h1', 'w1', 'foo.go')).toEqual([
      '/c/d/foo.go', '/a/b/foo.go',
    ])
  })

  it('pruneStaleCandidate removes the dirname entry', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().pruneStaleCandidate('h1', 'w1', '/a/b/foo.go')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
  })

  it('clearScope removes only the targeted scope', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    usePathCacheStore.getState().clearScope('h1', 'w1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
  })

  it('clearHost removes all scopes for that host', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/b')
    usePathCacheStore.getState().clearHost('h1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement store**

新建 `spa/src/stores/usePathCacheStore.ts`：

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { STORAGE_KEYS } from '../lib/storage/keys'

const MAX_DIRS_PER_SCOPE = 50
const scopeKey = (hostId: string, workspaceId: string) => `${hostId}:${workspaceId}`
const dirname = (p: string) => {
  const idx = p.lastIndexOf('/')
  return idx <= 0 ? '/' : p.slice(0, idx)
}

interface PathCacheState {
  dirsByScope: Record<string, string[]>  // LRU; head = most recent
  add: (hostId: string, workspaceId: string, dir: string) => void
  lookup: (hostId: string, workspaceId: string, basename: string) => string[]
  pruneStaleCandidate: (hostId: string, workspaceId: string, candidatePath: string) => void
  clearScope: (hostId: string, workspaceId: string) => void
  clearHost: (hostId: string) => void
}

export const usePathCacheStore = create<PathCacheState>()(
  persist(
    (set, get) => ({
      dirsByScope: {},

      add: (hostId, workspaceId, dir) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key] ?? []
          const filtered = existing.filter((d) => d !== dir)
          const next = [dir, ...filtered].slice(0, MAX_DIRS_PER_SCOPE)
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      lookup: (hostId, workspaceId, basename) => {
        const key = scopeKey(hostId, workspaceId)
        const dirs = get().dirsByScope[key] ?? []
        return dirs.map((d) => `${d}/${basename}`)
      },

      pruneStaleCandidate: (hostId, workspaceId, candidatePath) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key]
          if (!existing) return state
          const dir = dirname(candidatePath)
          const next = existing.filter((d) => d !== dir)
          if (next.length === existing.length) return state
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      clearScope: (hostId, workspaceId) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          if (!(key in state.dirsByScope)) return state
          const { [key]: _, ...rest } = state.dirsByScope
          return { dirsByScope: rest }
        }),

      clearHost: (hostId) =>
        set((state) => {
          const prefix = `${hostId}:`
          const next: Record<string, string[]> = {}
          for (const [k, v] of Object.entries(state.dirsByScope)) {
            if (!k.startsWith(prefix)) next[k] = v
          }
          return { dirsByScope: next }
        }),
    }),
    {
      name: STORAGE_KEYS.PATH_CACHE,
      partialize: (s) => ({ dirsByScope: s.dirsByScope }),
    },
  ),
)
```

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/usePathCacheStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/usePathCacheStore.ts spa/src/stores/usePathCacheStore.test.ts
git commit -m "feat(spa): usePathCacheStore with LRU and persist"
```

---

## Task 4.6 — `resolveWorkspaceForSession` helper

**Files:**
- Create: `spa/src/lib/resolve-workspace-for-session.ts`
- Test: `spa/src/lib/resolve-workspace-for-session.test.ts`

- [ ] **Step 1: Write failing test**

新建 test：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { resolveWorkspaceForSession } from './resolve-workspace-for-session'

describe('resolveWorkspaceForSession', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('returns null when no tab matches the session', () => {
    expect(resolveWorkspaceForSession('h1', 'sess')).toBeNull()
  })

  it('returns active workspace when a tab in it matches', () => {
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: ['t1'], activeTabId: 't1' }, { id: 'w2', tabs: [], activeTabId: null }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceForSession('h1', 'sess')).toBe('w1')
  })

  it('falls back to any workspace if active workspace has no match', () => {
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: [], activeTabId: null }, { id: 'w2', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceForSession('h1', 'sess')).toBe('w2')
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

```ts
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { getPrimaryPane } from './pane-tree'

/**
 * Find the workspace that owns a tab matching (hostId, sessionCode).
 * Active workspace wins if it matches; otherwise any workspace; null if
 * no tab corresponds (standalone session or stale code).
 */
export function resolveWorkspaceForSession(hostId: string, sessionCode: string): string | null {
  const tabs = useTabStore.getState().tabs
  const matchingTabIds: string[] = []
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (!tab) continue
    const c = getPrimaryPane(tab.layout).content
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode) {
      matchingTabIds.push(tabId)
    }
  }
  if (matchingTabIds.length === 0) return null

  const wsState = useWorkspaceStore.getState()
  const active = wsState.workspaces.find((w) => w.id === wsState.activeWorkspaceId)
  if (active && matchingTabIds.some((tid) => active.tabs.includes(tid))) return active.id

  for (const ws of wsState.workspaces) {
    if (matchingTabIds.some((tid) => ws.tabs.includes(tid))) return ws.id
  }
  return null
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/resolve-workspace-for-session.ts spa/src/lib/resolve-workspace-for-session.test.ts
git commit -m "feat(spa): resolveWorkspaceForSession helper with active priority"
```

---

## Task 4.7 — `agent-ws-dispatch.ts` 加 `agent.path_hint` case

**Files:**
- Modify: `spa/src/lib/agent-ws-dispatch.ts`
- Modify: `spa/src/lib/agent-ws-dispatch.test.ts`

- [ ] **Step 1: Write failing test**

擴 既有 test：

```ts
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { usePathCacheStore } from '../stores/usePathCacheStore'

describe('dispatchAgentWsEvent agent.path_hint', () => {
  beforeEach(() => {
    usePathCacheStore.setState({ dirsByScope: {} } as never, false)
    useTabStore.setState({
      tabs: { t1: { id: 't1', layout: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' } } } },
      tabOrder: ['t1'],
    } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'w1',
    } as never, false)
  })

  it('absolute hint adds dir to path cache for resolved workspace', () => {
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', path: '/a/b/c.go', dir: '/a/b',
      pathKind: 'absolute', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('non-absolute path hints are dropped', () => {
    const payload = JSON.stringify({
      agentId: 'codex', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: 'rel/dir',
      pathKind: 'relative', confidence: 'medium', toolName: 'apply_patch',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('unknown pathKind is dropped defensively', () => {
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: '/a/b',
      pathKind: 'galaxy-brain', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('hint with no resolvable workspace is dropped', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    const payload = JSON.stringify({
      agentId: 'claude-code', hostId: 'h1', sessionCode: 'sess',
      kind: 'read', dir: '/a/b',
      pathKind: 'absolute', confidence: 'high', toolName: 'Read',
      timestamp: '2026-04-27T00:00:00Z',
    })
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 'sess', value: payload })
    expect(Object.keys(usePathCacheStore.getState().dirsByScope)).toEqual([])
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Add case to dispatcher**

在 `spa/src/lib/agent-ws-dispatch.ts` 既有 if-block 後加：

```ts
import type { PathHint } from '../types/agent-events'
import { isValidPathKind, isValidConfidence } from '../types/agent-events'
import { resolveWorkspaceForSession } from './resolve-workspace-for-session'
import { usePathCacheStore } from '../stores/usePathCacheStore'

// ... at end of dispatchAgentWsEvent:
  if (event.type === 'agent.path_hint') {
    try {
      const hint = JSON.parse(event.value) as PathHint
      if (!isValidPathKind(hint.pathKind) || !isValidConfidence(hint.confidence)) return
      if (hint.pathKind !== 'absolute' || !hint.dir) return
      const wsId = resolveWorkspaceForSession(hostId, hint.sessionCode)
      if (!wsId) return
      usePathCacheStore.getState().add(hostId, wsId, hint.dir)
    } catch {
      // Malformed payload — drop silently.
    }
    return
  }
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/agent-ws-dispatch.ts spa/src/lib/agent-ws-dispatch.test.ts
git commit -m "feat(spa): dispatch agent.path_hint into usePathCacheStore"
```

---

## Task 4.8 — Extend `useMultiHostEventWs` for `agent.*` dispatch

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: Find current filter**

```bash
grep -n "agent\." spa/src/hooks/useMultiHostEventWs.ts
```

- [ ] **Step 2: Replace filter**

把 `if (event.type === 'agent.status' || event.type === 'agent.status.cleared')` 改成：

```ts
if (event.type.startsWith('agent.')) dispatchAgentWsEvent(hostId, event)
```

- [ ] **Step 3: Run all tests**

```
cd spa && npx vitest run
```

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useMultiHostEventWs.ts
git commit -m "refactor(spa): dispatch all agent.* WS events to dispatcher"
```

---

## Task 4.9 — workspace remove + host remove subscribers

**Files:**
- Modify: `spa/src/stores/usePathCacheStore.ts`（加 `attachAutoCleanup` 助手）
- Modify: `spa/src/main.tsx`（呼叫一次）

- [ ] **Step 1: Write failing test**

擴 `usePathCacheStore.test.ts`：

```ts
import { useWorkspaceStore } from './useWorkspaceStore'
import { useHostStore } from './useHostStore'
import { attachPathCacheAutoCleanup } from './usePathCacheStore'

it('workspace removal clears its scope', () => {
  attachPathCacheAutoCleanup()
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', tabs: [] }, { id: 'w2', tabs: [] }],
    activeWorkspaceId: 'w1',
  } as never, false)
  usePathCacheStore.getState().add('h1', 'w1', '/a')
  usePathCacheStore.getState().add('h1', 'w2', '/b')
  // simulate w1 removal
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w2', tabs: [] }],
    activeWorkspaceId: 'w2',
  } as never, false)
  expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
})

it('host removal clears all its scopes', () => {
  attachPathCacheAutoCleanup()
  useHostStore.setState({ hostOrder: ['h1', 'h2'] } as never, false)
  usePathCacheStore.getState().add('h1', 'w1', '/a')
  usePathCacheStore.getState().add('h2', 'w1', '/b')
  useHostStore.setState({ hostOrder: ['h2'] } as never, false)
  expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement subscribers**

在 `usePathCacheStore.ts` 末尾加：

```ts
import { useWorkspaceStore } from './useWorkspaceStore'
import { useHostStore } from './useHostStore'

let _attached = false

export function attachPathCacheAutoCleanup(): void {
  if (_attached) return
  _attached = true

  let lastWsIds = new Set(useWorkspaceStore.getState().workspaces.map((w) => w.id))
  useWorkspaceStore.subscribe((state) => {
    const current = new Set(state.workspaces.map((w) => w.id))
    for (const id of lastWsIds) {
      if (!current.has(id)) {
        // Iterate all hosts referencing this workspace and clear.
        const dirs = usePathCacheStore.getState().dirsByScope
        for (const key of Object.keys(dirs)) {
          const [hostId, wsId] = key.split(':')
          if (wsId === id) usePathCacheStore.getState().clearScope(hostId, wsId)
        }
      }
    }
    lastWsIds = current
  })

  let lastHostIds = new Set(useHostStore.getState().hostOrder)
  useHostStore.subscribe((state) => {
    const current = new Set(state.hostOrder)
    for (const id of lastHostIds) {
      if (!current.has(id)) usePathCacheStore.getState().clearHost(id)
    }
    lastHostIds = current
  })
}
```

- [ ] **Step 4: Wire into bootstrap**

在 `spa/src/main.tsx` 既有 store 初始化後（registerBuiltinModules 之後）加：

```tsx
import { attachPathCacheAutoCleanup } from './stores/usePathCacheStore'
attachPathCacheAutoCleanup()
```

- [ ] **Step 5: Run test, expect PASS**

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/usePathCacheStore.ts spa/src/stores/usePathCacheStore.test.ts spa/src/main.tsx
git commit -m "feat(spa): path cache auto-clears on workspace/host removal"
```

---

## Task 4.10 — Phase 4 verification + PR

- [ ] **Step 1: Full test + lint + build + go test**

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
go test ./...
```

- [ ] **Step 2: PR + 兩輪 codex review**

---

