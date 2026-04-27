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
| **4.9** | **`keepSettings: true`（tear-off / merge）→ auto-cleanup SKIP 整個 wsId**，in-memory + persisted 都保留（v6 簡化：tear-off 後 workspace 在本 window 不可見、無 lookup 路徑；其他 window 各自獨立；避免依賴 Zustand 不存在的 `persist.pause/resume` API） | v6 codex review #2 |
| **4.9** | `keepSettings: false`（真 delete）→ `clearScope`（in-memory + persisted 都清） | 同上 |
| **4.9** | host remove → 整 host 所有 scope 清 (in-memory + persisted) | — |
| **4.9** | 測試：repeat attach 不重複 subscribe；dispose 後不再 cleanup；hydration 順序 race；keepSettings 場景區分 | 攻擊 review #7 |

### Phase

| Task | 修訂 | 來源 |
|---|---|---|
| **All** | commit message lowercase | 通用 review C2 |
| **4.10** | Spec 引用改 `SPEC.md (rev 6, P4)`；verification gate 跑 SPA + Go 全測 | 通用 review C1 |

---

PR 結束標準：CC Read tool 觸發 → SPA 對應 workspace path cache 增加 1 條（payload v1 minimal，dir-level 不含 path/basename）；whitelist 三條 event；workspace remove / host remove 連動清理（區分 keepSettings）；非 absolute path defensive drop；hydration race 不誤殺。

## Task 4.1 — PathHint v1 minimal schema + ring buffer

**Files:**
- Create: `internal/module/agent/path_hint.go`
- Test: `internal/module/agent/path_hint_test.go`

> **C 決議 + 攻擊 critical C6 + 防守 review #3**：v1 minimal schema 只 6 個欄位 — `schemaVersion / agentId / sessionCode / dir / kind / timestamp`。**移除** `path / pathKind / baseDir / confidence / toolName / hostId`（hostId 由 broadcast 路徑帶；完整 path 永不廣播 privacy 邊界）。未來 codex apply_patch adapter 需 relative path 時整批升 v2。

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_test.go`：

```go
package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPathHint_V1Minimal_JSON(t *testing.T) {
	h := PathHint{
		SchemaVersion: 1,
		AgentID:       "claude-code",
		SessionCode:   "abc123",
		Dir:           "/a/b",
		Kind:          PathHintKindRead,
		Timestamp:     time.Unix(1000, 0).UTC(),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	// privacy: payload must NOT contain path / basename / pathKind / baseDir / confidence / toolName / hostId
	for _, banned := range []string{`"path"`, `"basename"`, `"pathKind"`, `"baseDir"`, `"confidence"`, `"toolName"`, `"hostId"`} {
		if strings.Contains(s, banned) {
			t.Errorf("payload must not contain %s; got %s", banned, s)
		}
	}
	var got PathHint
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != 1 || got.Dir != "/a/b" || got.Kind != PathHintKindRead {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestPathHintRingBuffer_AddAndCap(t *testing.T) {
	r := NewPathHintRingBuffer(3)
	for i := 0; i < 5; i++ {
		r.Push(PathHint{SchemaVersion: 1, Dir: "/d/" + string(rune('a'+i))})
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

- [ ] **Step 3: Implement v1 minimal schema + ring buffer**

新建 `internal/module/agent/path_hint.go`：

```go
package agent

import (
	"sync"
	"time"
)

const PathHintSchemaVersion = 1

// PathHint Kind enumeration (only these three are valid in v1).
const (
	PathHintKindRead  = "read"
	PathHintKindWrite = "write"
	PathHintKindEdit  = "edit"
)

// PathHint v1 — minimal schema. Dir-level only (no `path`, no `basename`).
// HostId is carried by the broadcast envelope (core.HostEvent), not by
// payload. To bump fields, raise SchemaVersion to 2 in a coordinated SPA +
// daemon change; SPA must defensive-drop unknown versions.
type PathHint struct {
	SchemaVersion int       `json:"schemaVersion"` // always 1 in this version
	AgentID       string    `json:"agentId"`       // "claude-code" (future: "codex" | "opencode")
	SessionCode   string    `json:"sessionCode"`   // 6-char base36
	Dir           string    `json:"dir"`           // dirname (absolute)
	Kind          string    `json:"kind"`          // "read" | "write" | "edit"
	Timestamp     time.Time `json:"timestamp"`
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
git commit -m "feat(daemon): pathhint v1 minimal schema with ring buffer"
```

---

## Task 4.2 — PathHint extractor (純函式) + dedup-by-(session, dir, basename)

**Files:**
- Create: `internal/module/agent/path_hint_extractor.go`
- Test: `internal/module/agent/path_hint_extractor_test.go`

> **C3 + 攻擊 review #13**：
> - 純函式 `ExtractPathHint(rawEvent, eventName, agentType) (hint, basename, bool)` — **不依賴 `agentpkg.NormalizedEvent`**（其欄位實際只有 `AgentType / Status / RawEventName / Detail`，沒有 `ToolName / ToolInput / HookEventName`）。改從 raw event JSON decode `tool_name` + `tool_input.file_path`。
> - dedup key 加 basename：`(SessionCode, Dir, Basename)` — 避免 SPA prune 後 5 秒同 dir 不同 file 不能 reseed 的真空期。Basename 進 dedup key 但**不進 payload**（payload 仍 dir-level）。
> - **payload 移除 `path / pathKind / baseDir / confidence / toolName / hostId`**（v1 minimal）。

- [ ] **Step 1: Write failing test**

新建 `internal/module/agent/path_hint_extractor_test.go`：

```go
package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func mkRaw(toolName, filePath string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	})
	return b
}

func TestExtractPathHint_AbsoluteRead(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	h, basename, ok := ExtractPathHint(mkRaw("Read", "/a/b/c.go"), "PreToolUse", "claude-code", "abc123", now)
	if !ok {
		t.Fatal("expected hint, got drop")
	}
	if h.SchemaVersion != 1 || h.AgentID != "claude-code" || h.SessionCode != "abc123" ||
		h.Dir != "/a/b" || h.Kind != PathHintKindRead {
		t.Errorf("unexpected hint: %+v", h)
	}
	if basename != "c.go" {
		t.Errorf("basename = %q", basename)
	}
}

func TestExtractPathHint_WriteEditNotebookEdit(t *testing.T) {
	for _, tc := range []struct{ tool, kind string }{
		{"Write", PathHintKindWrite},
		{"Edit", PathHintKindEdit},
		{"NotebookEdit", PathHintKindEdit},
	} {
		h, _, ok := ExtractPathHint(mkRaw(tc.tool, "/a/b/c"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0))
		if !ok || h.Kind != tc.kind {
			t.Errorf("%s expected kind=%s, got ok=%v kind=%s", tc.tool, tc.kind, ok, h.Kind)
		}
	}
}

func TestExtractPathHint_DropsRelative(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "rel/path.go"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-absolute path")
	}
}

func TestExtractPathHint_DropsUnknownTool(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Bash", "/a/b"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-file tool")
	}
}

func TestExtractPathHint_DropsWrongEventName(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "/a/b"), "SessionStart", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-PreToolUse/PostToolUse event")
	}
}

func TestDedupCache_BasenameDistinguishes(t *testing.T) {
	c := NewPathHintDedupCache(5 * time.Second)
	t0 := time.Unix(1000, 0)
	if !c.Mark("s1", "/a/b", "c.go", t0) {
		t.Fatal("first call should be fresh")
	}
	// 同 (session, dir, basename) 在 window 內 → dedup
	if c.Mark("s1", "/a/b", "c.go", t0.Add(2*time.Second)) {
		t.Fatal("same key within window should dedup")
	}
	// 同 dir 不同 basename → 不 dedup（避免 SPA prune 後真空期）
	if !c.Mark("s1", "/a/b", "d.go", t0.Add(2*time.Second)) {
		t.Fatal("different basename should NOT dedup (basename in key)")
	}
	// 過 window → 重新通過
	if !c.Mark("s1", "/a/b", "c.go", t0.Add(6*time.Second)) {
		t.Fatal("after window should be fresh again")
	}
}
```

- [ ] **Step 2: Run test, expect FAIL**

```
go test ./internal/module/agent/ -run "Extract|Dedup"
```

- [ ] **Step 3: Implement pure function + dedup cache**

新建 `internal/module/agent/path_hint_extractor.go`：

```go
package agent

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"time"
)

var ccFileTools = map[string]string{
	"Read":         PathHintKindRead,
	"Write":        PathHintKindWrite,
	"Edit":         PathHintKindEdit,
	"NotebookEdit": PathHintKindEdit,
}

// rawCCEvent matches the JSON CC sends as PreToolUse/PostToolUse event payload.
type rawCCEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// ExtractPathHint is a pure function. Returns (hint, basename, true) on success.
// Caller is responsible for dedup (using NewPathHintDedupCache) and broadcast.
//
// - Only PreToolUse / PostToolUse events qualify.
// - Only Read / Write / Edit / NotebookEdit tools qualify.
// - Only absolute file_path qualifies; relative paths drop defensively.
// - basename is NOT included in the returned PathHint payload (privacy / dir-level rule);
//   it is returned separately so the caller can use it as part of the dedup key.
func ExtractPathHint(rawEvent json.RawMessage, eventName, agentID, sessionCode string, now time.Time) (PathHint, string, bool) {
	if eventName != "PreToolUse" && eventName != "PostToolUse" {
		return PathHint{}, "", false
	}
	var ev rawCCEvent
	if err := json.Unmarshal(rawEvent, &ev); err != nil {
		return PathHint{}, "", false
	}
	kind, ok := ccFileTools[ev.ToolName]
	if !ok {
		return PathHint{}, "", false
	}
	raw := ev.ToolInput.FilePath
	if raw == "" || !filepath.IsAbs(raw) {
		return PathHint{}, "", false
	}
	return PathHint{
		SchemaVersion: PathHintSchemaVersion,
		AgentID:       agentID,
		SessionCode:   sessionCode,
		Dir:           filepath.Dir(raw),
		Kind:          kind,
		Timestamp:     now,
	}, filepath.Base(raw), true
}

// PathHintDedupCache implements (session, dir, basename) dedup with a sliding window.
// Basename is in the key (per attacker review #13) so different files in the same dir
// can both seed the SPA cache; this prevents the 5-second blackout after SPA prune.
type PathHintDedupCache struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time // key = sessionCode|dir|basename
}

func NewPathHintDedupCache(window time.Duration) *PathHintDedupCache {
	return &PathHintDedupCache{window: window, last: make(map[string]time.Time)}
}

// Mark returns true if (session, dir, basename) is fresh enough to broadcast.
// Returns false (and does not refresh timestamp) when within window.
func (c *PathHintDedupCache) Mark(sessionCode, dir, basename string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := sessionCode + "|" + dir + "|" + basename
	if c.window > 0 {
		if last, found := c.last[key]; found && now.Sub(last) < c.window {
			return false
		}
	}
	c.last[key] = now
	// Opportunistic GC of entries older than 10× window
	if c.window > 0 {
		cutoff := now.Add(-10 * c.window)
		for k, ts := range c.last {
			if ts.Before(cutoff) {
				delete(c.last, k)
			}
		}
	}
	return true
}
```

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run "Extract|Dedup"
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/path_hint_extractor.go internal/module/agent/path_hint_extractor_test.go
git commit -m "feat(daemon): pure pathhint extractor with session-dir-basename dedup"
```

---

## Task 4.3a — `emitPathHint` helper + Module fields

**Files:**
- Modify: `internal/module/agent/module.go`（加 dedup cache + ring buffer 欄位）
- Modify: `internal/module/agent/handler.go`（加 `emitPathHint` helper）
- Test: `internal/module/agent/handler_path_hint_test.go`（新建）

> **C3 拆檔之 4.3a**：本 task 只證明 `emitPathHint(rawEvent, eventName, sessionCode)` 走完 extract → dedup → broadcast 流程，broadcast payload 是 v1 minimal JSON 且不含 path / basename。**不接 hook handler**（4.3b）。

- [ ] **Step 1: Add fields to Module struct**

在 `internal/module/agent/module.go` 既有 `Module` struct 內加：

```go
type Module struct {
    // ... existing fields
    pathHintDedup  *PathHintDedupCache
    pathHintBuffer *PathHintRingBuffer
}
```

`agent.New(...)` 內初始化（5 秒 dedup window，200 條 ring）：

```go
m.pathHintDedup = NewPathHintDedupCache(5 * time.Second)
m.pathHintBuffer = NewPathHintRingBuffer(200)
```

- [ ] **Step 2: Add broadcaster seam interface + pure helper + Module method**

> **v6 codex review #1 修正**：原版 `&mockCore{broadcast: ...}` 假設 `Module.core` 是 interface — 實際是 `*core.Core` concrete type，無法塞 stub。改為**純函式 helper + 小 broadcaster interface**，測試直接呼叫 helper 不經 `Module`。

新建 `internal/module/agent/path_hint_emit.go`（或加入 `handler.go`）：

```go
// Small seam to allow unit-testing emit logic without spinning up real core.
type pathHintBroadcaster interface {
    Broadcast(session, kind, value string)
}

// EmitPathHint is a pure helper. Production Module.emitPathHint wraps this
// with m.core.Events / m.pathHintDedup / m.pathHintBuffer; tests pass stubs.
func EmitPathHint(
    b pathHintBroadcaster,
    dedup *PathHintDedupCache,
    buf *PathHintRingBuffer,
    rawEvent json.RawMessage,
    eventName, agentID, sessionCode string,
) {
    hint, basename, ok := ExtractPathHint(rawEvent, eventName, agentID, sessionCode, time.Now())
    if !ok {
        return
    }
    if !dedup.Mark(hint.SessionCode, hint.Dir, basename, hint.Timestamp) {
        return // dedup window hit
    }
    buf.Push(hint)
    payload, err := json.Marshal(hint)
    if err != nil {
        log.Printf("path_hint: marshal failed: %v", err)
        return
    }
    b.Broadcast(sessionCode, "agent.path_hint", string(payload))
}
```

Module method（`handler.go`，wrapper）：

```go
func (m *Module) emitPathHint(rawEvent json.RawMessage, eventName, sessionCode string) {
    EmitPathHint(m.core.Events, m.pathHintDedup, m.pathHintBuffer, rawEvent, eventName, "claude-code", sessionCode)
}
```

> 假設 `core.Core.Events` 已有 `Broadcast(session, kind, value string)` method（既有 agent event 慣例如此）— 跟 stream module 既有 broadcast pattern 對齊。若 `Events` 不直接提供，改 wrapper：`broadcasterAdapter{events: m.core.Events}.Broadcast(...)`。

- [ ] **Step 3: Add test (broadcast format + privacy + dedup)**

新建 `internal/module/agent/path_hint_emit_test.go`：

```go
package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubBroadcaster implements pathHintBroadcaster for tests.
type stubBroadcaster struct {
	mu    sync.Mutex
	calls []struct{ session, kind, value string }
}

func (s *stubBroadcaster) Broadcast(session, kind, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, struct{ session, kind, value string }{session, kind, value})
}

func TestEmitPathHint_BroadcastV1MinimalPayload(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(0)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "/a/b/c.go"},
	})
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "claude-code", "sess1")

	if len(b.calls) != 1 || b.calls[0].session != "sess1" || b.calls[0].kind != "agent.path_hint" {
		t.Fatalf("envelope mismatch: %+v", b.calls)
	}
	// privacy: payload must NOT contain path / basename
	v := b.calls[0].value
	for _, banned := range []string{"/a/b/c.go", `"path"`, "c.go", `"basename"`} {
		if strings.Contains(v, banned) {
			t.Errorf("payload must not contain %q; got %s", banned, v)
		}
	}
	var hint PathHint
	if err := json.Unmarshal([]byte(v), &hint); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if hint.SchemaVersion != 1 || hint.Dir != "/a/b" || hint.Kind != PathHintKindRead {
		t.Errorf("payload mismatch: %+v", hint)
	}
}

func TestEmitPathHint_DedupSuppresses(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(5 * time.Second)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "/a/b/c.go"},
	})
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "claude-code", "sess1")
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "claude-code", "sess1") // dedup
	if len(b.calls) != 1 {
		t.Errorf("expected 1 broadcast, got %d", len(b.calls))
	}
}
```

> 純測 helper 不經 Module，避免假設 `core.Core` 內部結構。Module integration test 在 4.3b 用既有 `internal/module/agent/fakes_test.go` 內的 fake setup 跑 `m.handleEvent`。

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/ -run PathHint
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/module.go internal/module/agent/handler.go internal/module/agent/handler_path_hint_test.go
git commit -m "feat(daemon): emitpathhint helper with dedup and broadcast"
```

---

## Task 4.3b — Hook handler integration（從 `req.RawEvent` decode）

**Files:**
- Modify: `internal/module/agent/handler.go`（在 `handleEvent` 既有接點呼叫 `emitPathHint`）

> **C3 拆檔之 4.3b + 通用 review A4**：**`agentpkg.NormalizedEvent` 不含 `ToolName / ToolInput / HookEventName`** — 它只有 `AgentType / Status / RawEventName / Detail`。本 task 改在 `handleEvent` 內、**`req.EventName` 已驗證且 `req.RawEvent` 可用**之處呼叫 `emitPathHint(req.RawEvent, req.EventName, m.resolveSessionCode(req.TmuxSession))`。

- [ ] **Step 1: 找實際接點**

```bash
grep -n "func (m \*Module) handleEvent\|req\.EventName\|req\.RawEvent" internal/module/agent/handler.go
```

確認 `handleEvent` 簽名 + `req.EventName / req.RawEvent / req.TmuxSession` 欄位實際拼字。

- [ ] **Step 2: 在 handler 加呼叫**

在 `handleEvent` 內，CC agent type + EventName == "PreToolUse" or "PostToolUse" 的分支加：

```go
if req.AgentType == "claude-code" && (req.EventName == "PreToolUse" || req.EventName == "PostToolUse") {
    sessionCode := m.resolveSessionCode(req.TmuxSession)
    if sessionCode != "" {
        m.emitPathHint(req.RawEvent, req.EventName, sessionCode)
    }
}
```

> **不依賴 normalized 結構** — 完全用 raw event JSON + EventName。

- [ ] **Step 3: Integration test (走 既有 fakes 設定真 Module)**

> **v6 codex review #1 修正**：不再用 `&mockCore{...}` stub（`Module.core` 是 concrete `*core.Core`，無法塞）。改用 `internal/module/agent/fakes_test.go` 既有 fake setup（`frame_ops_test.go` 內已有 `setupAgentModule(t, ...)` 之類 helper），用真 `core.New(...)` + `Registry.Register(session.RegistryKey, fake)`。`Events.Broadcast` 觀察用 `core.Events.AddSubscriber(handler)` 註冊 test subscriber。

擴 `internal/module/agent/handler_path_hint_test.go`：

```go
func TestHandleEvent_EmitsPathHintForCC(t *testing.T) {
    // 依 internal/module/agent/frame_ops_test.go:195 既有 setup pattern 起 module
    m, c := setupAgentModuleForTest(t, &fakeSessionProvider{ /* sessions w/ "abc" */ })
    received := make(chan string, 1)
    c.Events.AddSubscriber(func(session, kind, value string) {
        if kind == "agent.path_hint" {
            select { case received <- value: default: }
        }
    })

    raw, _ := json.Marshal(map[string]any{
        "tool_name": "Read",
        "tool_input": map[string]any{"file_path": "/x/y/z.go"},
    })
    err := m.handleEvent(buildHookEventReq(t, "claude-code", "PreToolUse", raw, "abc"))
    if err != nil { t.Fatalf("handleEvent: %v", err) }

    select {
    case got := <-received:
        if !strings.Contains(got, `"dir":"/x/y"`) {
            t.Errorf("expected dir broadcast; got %q", got)
        }
        if strings.Contains(got, "z.go") || strings.Contains(got, `"path"`) {
            t.Errorf("payload must not contain basename / path; got %q", got)
        }
    case <-time.After(time.Second):
        t.Fatal("expected agent.path_hint broadcast within 1s")
    }
}
```

> `setupAgentModuleForTest` / `buildHookEventReq` 是新 helper；參考 `frame_ops_test.go:195+` 既有 test setup 抽出。`core.Events.AddSubscriber` 介面假設既有；若不存在改用 `c.Events.Subscribe(ctx, ...)` 等 idiomatic API。實作期間先 grep `core.Events` 既有 method set 再寫。

- [ ] **Step 4: Run test, expect PASS**

```
go test ./internal/module/agent/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/handler.go internal/module/agent/handler_path_hint_test.go
git commit -m "feat(daemon): wire pathhint emit into cc pretooluse posttooluse handler"
```

---

## Task 4.4 — `STORAGE_KEYS.PATH_CACHE_V1` + PathHint TS v1 minimal type

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`
- Modify: `spa/src/types/agent-events.ts`

> **C 決議 + 攻擊 review #14**：localStorage key 含**版本後綴 `_V1`**（未來 v2 schema 不撞 namespace）；TS type 對齊 daemon v1 minimal schema（6 欄位）。

- [ ] **Step 1: Add storage key (with version suffix)**

在 `spa/src/lib/storage/keys.ts` `STORAGE_KEYS` object 加：

```ts
PATH_CACHE_V1: 'purdex-path-cache-v1',
```

- [ ] **Step 2: Define TS PathHint v1 minimal type**

在 `spa/src/types/agent-events.ts` 加：

```ts
export const PATH_HINT_SCHEMA_VERSION = 1 as const

export const PATH_HINT_KIND = ['read', 'write', 'edit'] as const
export type PathHintKind = (typeof PATH_HINT_KIND)[number]

/**
 * PathHint v1 minimal — must mirror daemon `internal/module/agent/path_hint.go`.
 * Dir-level only (no `path`, no `basename`). HostId is carried by the WS
 * envelope (HostEvent), not by payload.
 */
export interface PathHint {
  schemaVersion: 1
  agentId: string             // 'claude-code' | future 'codex' | 'opencode'
  sessionCode: string
  dir: string                 // absolute dirname
  kind: PathHintKind
  timestamp: string           // ISO 8601
}

export function isValidPathHintKind(v: unknown): v is PathHintKind {
  return typeof v === 'string' && (PATH_HINT_KIND as readonly string[]).includes(v)
}
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/types/agent-events.ts
git commit -m "feat(spa): pathhint v1 minimal type and storage key v1"
```

---

## Task 4.5 — `usePathCacheStore` LRU + purdexStorage + add normalization

**Files:**
- Create: `spa/src/stores/path-cache/usePathCacheStore.ts`（**子目錄**；體質 review #3）
- Test: `spa/src/stores/path-cache/usePathCacheStore.test.ts`

> **吸收**：路徑搬到 `path-cache/` 子目錄；`add()` 內建 normalization（防守 review #7）；`storage: purdexStorage` 與其他 store 一致（攻擊 review #2）；LRU 補 duplicate move-to-head + overflow tail eviction 測試（攻擊 review #14）；`onRehydrateStorage` defensive。

- [ ] **Step 1: Write failing test**

新建 `spa/src/stores/path-cache/usePathCacheStore.test.ts`：

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
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b', '/c/d'])
  })

  it('LRU caps at 50 entries per scope', () => {
    for (let i = 0; i < 60; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d59')
    expect(dirs[49]).toBe('/d10')
  })

  // 攻擊 review #14: duplicate move-to-head + overflow tail eviction
  it('LRU touches existing dir back to head + evicts tail on overflow', () => {
    for (let i = 0; i < 50; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    // dirs = ['/d49' .. '/d0']
    usePathCacheStore.getState().add('h1', 'w1', '/d0')      // touch d0 → head
    usePathCacheStore.getState().add('h1', 'w1', '/d50')     // overflow
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d50')
    expect(dirs[1]).toBe('/d0')   // touched, not evicted
    expect(dirs.includes('/d1')).toBe(false)  // d1 evicted (was tail after touch)
  })

  // 防守 review #7: add() normalize
  it('add silently rejects non-absolute path', () => {
    usePathCacheStore.getState().add('h1', 'w1', 'rel/path')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('add normalizes trailing slash and `./..`', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b/')
    usePathCacheStore.getState().add('h1', 'w1', '/a/./b')
    usePathCacheStore.getState().add('h1', 'w1', '/a/c/../b')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])  // all dedup to /a/b
  })

  it('lookup combines basename with each cached dir (head first)', () => {
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

  it('clearScope removes in-memory + persisted by default', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    usePathCacheStore.getState().clearScope('h1', 'w1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toBeUndefined() // 沒污染
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
  })

  // v6 簡化：clearScope 永遠清 in-memory + persisted，不再有 keepPersisted opts
  // tear-off 保留行為由 auto-cleanup.ts (Task 4.9) skip 整個 cleanup 來達成；本測 cover 一般情況

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

```
cd spa && npx vitest run src/stores/path-cache/usePathCacheStore.test.ts
```

- [ ] **Step 3: Implement store**

新建 `spa/src/stores/path-cache/usePathCacheStore.ts`：

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { STORAGE_KEYS } from '../../lib/storage/keys'
import { purdexStorage } from '../../lib/storage/purdex-storage'
import path from 'path-browserify'

const MAX_DIRS_PER_SCOPE = 50
const scopeKey = (hostId: string, workspaceId: string) => `${hostId}:${workspaceId}`

function normalizeDir(raw: string): string | null {
  if (typeof raw !== 'string' || !raw.startsWith('/')) return null
  // path.normalize: collapses ./.. and dedup slashes; trim trailing /
  const norm = path.normalize(raw).replace(/\/+$/, '')
  return norm === '' ? '/' : norm
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
          const norm = normalizeDir(dir)
          if (!norm) return state  // silently reject non-absolute
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key] ?? []
          const filtered = existing.filter((d) => d !== norm)
          const next = [norm, ...filtered].slice(0, MAX_DIRS_PER_SCOPE)
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
          const dir = normalizeDir(path.dirname(candidatePath))
          if (!dir) return state
          const next = existing.filter((d) => d !== dir)
          if (next.length === existing.length) return state
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      // clearScope 永遠清 in-memory + persisted（Zustand persist 在每次 set 後 stringify 寫回）。
      // tear-off (keepSettings:true) 場景**不該呼叫 clearScope** — 由 auto-cleanup.ts (Task 4.9)
      // 在 dispatch 前 skip 整個 cleanup，這樣 in-memory + persisted 都自然保留。
      // 此設計避免依賴 zustand persist.pause/resume — 該 API 在 Zustand 5 不存在
      // (v6 codex review #2 修正)。
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
      name: STORAGE_KEYS.PATH_CACHE_V1,
      storage: purdexStorage,
      partialize: (s) => ({ dirsByScope: s.dirsByScope }),
      // defensive：localStorage 內容 malformed → reset
      onRehydrateStorage: () => (state, error) => {
        if (error || !state || typeof state.dirsByScope !== 'object') {
          usePathCacheStore.setState({ dirsByScope: {} } as never, false)
          return
        }
        // sanitize: 確保所有 value 都是 string array
        const cleaned: Record<string, string[]> = {}
        for (const [k, v] of Object.entries(state.dirsByScope)) {
          if (Array.isArray(v) && v.every((x) => typeof x === 'string')) cleaned[k] = v
        }
        usePathCacheStore.setState({ dirsByScope: cleaned } as never, false)
      },
    },
  ),
)
```

> **`PathCacheState.clearScope` 不再有 opts 參數**（v6 簡化）— `keepSettings:true` tear-off 場景由 auto-cleanup (Task 4.9) 在 dispatch 前 skip 整個 cleanup 流程，不呼叫 clearScope；in-memory + persisted 都自然保留，避免依賴 Zustand 不存在的 `persist.pause/resume` API。

- [ ] **Step 4: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/path-cache/usePathCacheStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/path-cache/usePathCacheStore.ts spa/src/stores/path-cache/usePathCacheStore.test.ts
git commit -m "feat(spa): pathcache store with normalization and lru and purdex storage"
```

---

## Task 4.6 — `resolveWorkspaceIdForAgentSession` helper（多重命中 drop）

**Files:**
- Create: `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts`（**子目錄**；體質 review #4 + #9）
- Test: `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.test.ts`

> **吸收**：
> - 改名 `resolveWorkspaceIdForAgentSession`（更具體，避免被誤用為泛用 workspace resolver；體質 review #9）
> - 放 `lib/agent-ws/` 子目錄（體質 review #4）
> - useWorkspaceStore 實際路徑 `features/workspace/store`（通用 review A2）
> - **多重 workspace 命中 → return null**（不取 active 捷徑），避免寫到「使用者剛切過去的 workspace」（攻擊 review #6 + 防守 review #5）
> - 測試 fixture **必須用 `{type:'leaf', pane:{id, content}}` PaneLayout shape**（通用 review B2）

- [ ] **Step 1: Write failing test**

新建 `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore, createTab } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { resolveWorkspaceIdForAgentSession } from './resolve-workspace-id-for-agent-session'

const seedTab = (id: string, content: { kind: string; hostId?: string; sessionCode?: string }) => ({
  id,
  layout: { type: 'leaf' as const, pane: { id: `p_${id}`, content } },
})

describe('resolveWorkspaceIdForAgentSession', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('returns null when no tab matches the session', () => {
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBeNull()
  })

  it('returns the unique workspace when only one tab matches', () => {
    const t = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' })
    useTabStore.setState({ tabs: { t1: t }, tabOrder: ['t1'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', tabs: ['t1'], moduleConfig: {} }, { id: 'w2', tabs: [], moduleConfig: {} }],
      activeWorkspaceId: 'w2',  // active 是 w2 但匹配在 w1 — 必須回 w1（不取 active 捷徑）
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBe('w1')
  })

  it('returns null when multiple workspaces own matching tabs (avoids racy active-priority)', () => {
    const t1 = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' })
    const t2 = seedTab('t2', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' })
    useTabStore.setState({ tabs: { t1, t2 }, tabOrder: ['t1', 't2'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', tabs: ['t1'], moduleConfig: {} },
        { id: 'w2', tabs: ['t2'], moduleConfig: {} },
      ],
      activeWorkspaceId: 'w2',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBeNull()
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement helper**

新建 `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts`：

```ts
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { getPrimaryPane } from '../pane-tree'

/**
 * Find the workspace that owns a tab matching (hostId, sessionCode) for an
 * agent session event (e.g. PathHint dispatch).
 *
 * Returns:
 *   - workspaceId  — when exactly one workspace owns a matching tab
 *   - null          — when no tab matches OR multiple workspaces match
 *
 * The "multiple match → null" rule is intentional (attacker review #6):
 * it avoids racy writes to the workspace the user just switched to during
 * tear-off / merge transitions. PathHint with no clear owner is dropped.
 */
export function resolveWorkspaceIdForAgentSession(hostId: string, sessionCode: string): string | null {
  const tabs = useTabStore.getState().tabs
  const matchingTabIds = new Set<string>()
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (!tab) continue
    const c = getPrimaryPane(tab.layout).content
    if (
      c.kind === 'tmux-session' &&
      (c as { hostId?: string }).hostId === hostId &&
      (c as { sessionCode?: string }).sessionCode === sessionCode
    ) {
      matchingTabIds.add(tabId)
    }
  }
  if (matchingTabIds.size === 0) return null

  const wsState = useWorkspaceStore.getState()
  const owners = wsState.workspaces.filter((w) => w.tabs.some((tid: string) => matchingTabIds.has(tid)))
  if (owners.length !== 1) return null  // 0 or multiple → drop
  return owners[0].id
}
```

- [ ] **Step 4: Run test, expect PASS**

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.test.ts
git commit -m "feat(spa): resolve workspace id for agent session with multi-hit drop"
```

---

## Task 4.7 — `agent-ws/path-hint-dispatch.ts` + 拆 `agent-ws-dispatch.ts` 為子目錄

**Files:**
- Create: `spa/src/lib/agent-ws/index.ts`（router 入口；體質 review #4）
- Create: `spa/src/lib/agent-ws/status-dispatch.ts`（既有 status 邏輯搬入）
- Create: `spa/src/lib/agent-ws/path-hint-dispatch.ts`
- Create: `spa/src/lib/agent-ws/path-hint-dispatch.test.ts`
- Modify: `spa/src/lib/agent-ws-dispatch.ts` → `export * from './agent-ws'` 過渡 shim

> **吸收**：
> - 拆子目錄（體質 review #4）— `agent-ws-dispatch.ts` 不應同時兼 status + path-hint router；改名 `agent-ws/index.ts`
> - PathHint v1 minimal payload（只有 `schemaVersion / agentId / sessionCode / dir / kind / timestamp`）— 不再 check `pathKind / confidence`
> - **schemaVersion check：!== 1 → defensive drop**（C 決議）
> - **try/catch 包整段 + resolver throw regression test**（攻擊 review #3）
> - useWorkspaceStore 路徑 `features/workspace/store`
> - fixture leaf shape `{type:'leaf', pane:{...}}`

- [ ] **Step 1: 拆子目錄 + 過渡 shim（先做架構搬遷，不改行為）**

```bash
mkdir -p spa/src/lib/agent-ws
git mv spa/src/lib/agent-ws-dispatch.ts spa/src/lib/agent-ws/status-dispatch.ts
```

新建 `spa/src/lib/agent-ws/index.ts`：

```ts
import { handleStatusEvent } from './status-dispatch'
import { handlePathHintEvent } from './path-hint-dispatch'

export function dispatchAgentWsEvent(hostId: string, event: { type: string; session: string; value: string }): void {
  if (event.type === 'agent.status' || event.type === 'agent.status.cleared') {
    handleStatusEvent(hostId, event)
    return
  }
  if (event.type === 'agent.path_hint') {
    handlePathHintEvent(hostId, event)
    return
  }
}
```

`spa/src/lib/agent-ws-dispatch.ts` 重寫為 `export * from './agent-ws'`（過渡 shim，避免破 caller import path）。

`status-dispatch.ts` 內把原本的 `dispatchAgentWsEvent` body 抽成 `export function handleStatusEvent(hostId, event)`。

- [ ] **Step 2: Write failing test for path-hint-dispatch**

新建 `spa/src/lib/agent-ws/path-hint-dispatch.test.ts`：

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useTabStore, createTab } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { handlePathHintEvent } from './path-hint-dispatch'

const seedTab = (id: string, content: { kind: string; hostId?: string; sessionCode?: string }) => ({
  id,
  layout: { type: 'leaf' as const, pane: { id: `p_${id}`, content } },
})

const v1 = (overrides: Record<string, unknown> = {}) => JSON.stringify({
  schemaVersion: 1,
  agentId: 'claude-code',
  sessionCode: 'sess',
  dir: '/a/b',
  kind: 'read',
  timestamp: '2026-04-27T00:00:00Z',
  ...overrides,
})

beforeEach(() => {
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)
  const t = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess' })
  useTabStore.setState({ tabs: { t1: t }, tabOrder: ['t1'] } as never, false)
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', tabs: ['t1'], moduleConfig: {} }],
    activeWorkspaceId: 'w1',
  } as never, false)
})

describe('handlePathHintEvent', () => {
  it('v1 payload adds dir to resolved workspace cache', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('schemaVersion !== 1 → defensive drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ schemaVersion: 2 }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('non-absolute dir → defensive drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ dir: 'rel/dir' }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('malformed JSON → drop without throwing', () => {
    expect(() => handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: 'not-json' })).not.toThrow()
  })

  it('unresolvable workspace → drop', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })
    expect(Object.keys(usePathCacheStore.getState().dirsByScope)).toEqual([])
  })

  it('resolver throwing → does not crash dispatcher', async () => {
    // 攻擊 review #3：mock resolver throw
    const mod = await import('./resolve-workspace-id-for-agent-session')
    const spy = vi.spyOn(mod, 'resolveWorkspaceIdForAgentSession').mockImplementation(() => {
      throw new Error('boom')
    })
    expect(() => handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })).not.toThrow()
    spy.mockRestore()
  })
})
```

- [ ] **Step 3: Run test, expect FAIL**

`handlePathHintEvent` not exported.

- [ ] **Step 4: Implement path-hint-dispatch**

新建 `spa/src/lib/agent-ws/path-hint-dispatch.ts`：

```ts
import type { PathHint } from '../../types/agent-events'
import {
  PATH_HINT_SCHEMA_VERSION,
  isValidPathHintKind,
} from '../../types/agent-events'
import { resolveWorkspaceIdForAgentSession } from './resolve-workspace-id-for-agent-session'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'

export function handlePathHintEvent(hostId: string, event: { type: string; session: string; value: string }): void {
  try {
    const hint = JSON.parse(event.value) as PathHint
    if (hint.schemaVersion !== PATH_HINT_SCHEMA_VERSION) return  // unknown version → drop
    if (typeof hint.dir !== 'string' || !hint.dir.startsWith('/')) return
    if (!isValidPathHintKind(hint.kind)) return
    const wsId = resolveWorkspaceIdForAgentSession(hostId, hint.sessionCode)
    if (!wsId) return
    usePathCacheStore.getState().add(hostId, wsId, hint.dir)
  } catch {
    // malformed payload OR resolver throw — drop silently, never crash WS pipeline
  }
}
```

- [ ] **Step 5: Run test, expect PASS**

```
cd spa && npx vitest run src/lib/agent-ws/
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/agent-ws/ spa/src/lib/agent-ws-dispatch.ts
git commit -m "feat(spa): split agent-ws dispatch and add path-hint handler"
```

---

## Task 4.8 — Extend `useMultiHostEventWs` for `agent.*` dispatch

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: Find current filter**

```bash
grep -n "agent\." spa/src/hooks/useMultiHostEventWs.ts
```

- [ ] **Step 2: Replace filter — whitelist 三條 event type**（**禁用 `startsWith('agent.')` broad filter**；防守 review #9）

把既有 filter 改成：

```ts
const AGENT_DISPATCH_TYPES = new Set([
  'agent.status',
  'agent.status.cleared',
  'agent.path_hint',
])

// inside event handler:
if (AGENT_DISPATCH_TYPES.has(event.type)) {
  dispatchAgentWsEvent(hostId, event)
}
```

未來新 `agent.*` event 必須**顯式加進 whitelist**，避免被錯誤歸類為 agent store event。

- [ ] **Step 3: Add regression test**

擴 `spa/src/hooks/useMultiHostEventWs.test.ts`（若無檔案則新建）：

```ts
import { describe, it, expect, vi } from 'vitest'

it('agent.foo (not in whitelist) is NOT dispatched', () => {
  const dispatchSpy = vi.fn()
  // setup hook with mock dispatch
  // simulate event { type: 'agent.foo', ... } → expect dispatchSpy NOT called
})

it('agent.path_hint IS dispatched', () => {
  // simulate event { type: 'agent.path_hint', ... } → expect dispatchSpy called once
})
```

- [ ] **Step 4: Run all tests**

```
cd spa && npx vitest run
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useMultiHostEventWs.ts spa/src/hooks/useMultiHostEventWs.test.ts
git commit -m "refactor(spa): whitelist three agent event types for dispatch"
```

---

## Task 4.9 — workspace/host remove subscribers — `path-cache/auto-cleanup.ts`

**Files:**
- Create: `spa/src/stores/path-cache/auto-cleanup.ts`（**獨立檔**；體質 review #3 + #17）
- Test: `spa/src/stores/path-cache/auto-cleanup.test.ts`
- Modify: `spa/src/main.tsx`（呼叫一次，並保留回傳的 dispose function）

> **吸收**：
> - 獨立檔（store 本體不 import workspace/host store；避免循環依賴）
> - **回傳 dispose function**（攻擊 review #7）— 測試 `afterEach` 必須呼叫；HMR `import.meta.hot.dispose` 也呼叫
> - 用 zustand subscribe 的 **`prevState` 算 removed ids**（不要用 closure `lastWsIds` Set）
> - **hydration race**：等 `useWorkspaceStore.persist.hasHydrated()` / `onFinishHydration` 才 attach；防止以空 workspace set 作 baseline 誤刪 cache（攻擊 review #2）
> - **`keepSettings: true`（tear-off）→ auto-cleanup SKIP 整個 wsId**（in-memory + persisted 都保留；v6 簡化）；`keepSettings: false`（真 delete）→ in-memory + persisted 都清
> - host remove → `clearHost` 清整 host

- [ ] **Step 1: Write failing test**

新建 `spa/src/stores/path-cache/auto-cleanup.test.ts`：

```ts
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../useHostStore'
import { usePathCacheStore } from './usePathCacheStore'
import { attachPathCacheAutoCleanup } from './auto-cleanup'

let dispose: (() => void) | undefined

beforeEach(() => {
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', tabs: [], moduleConfig: {} }, { id: 'w2', tabs: [], moduleConfig: {} }],
    activeWorkspaceId: 'w1',
  } as never, false)
  useHostStore.setState({ hostOrder: ['h1', 'h2'] } as never, false)
  dispose = attachPathCacheAutoCleanup()
})

afterEach(() => {
  dispose?.()
  dispose = undefined
})

describe('attachPathCacheAutoCleanup', () => {
  it('returns a dispose function', () => {
    expect(typeof dispose).toBe('function')
  })

  it('workspace removal (real delete) clears in-memory + persisted', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', tabs: [], moduleConfig: {} }],
      activeWorkspaceId: 'w2',
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
  })

  // v6 簡化：tear-off (keepSettings:true) → auto-cleanup skip，in-memory + persisted 都保留
  it('workspace tear-off (keepSettings:true) skips cleanup entirely (in-memory + persisted retained)', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    // 模擬 tear-off: workspaces 移除 w1；workspace store 在 removeWorkspace({keepSettings:true}) 時
    // set workspaceMeta._lastRemovedKeepSettings = 'w1'
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', tabs: [], moduleConfig: {} }],
      activeWorkspaceId: 'w2',
      _lastRemovedKeepSettings: 'w1',
    } as never, false)
    // expect: in-memory 仍保留 (auto-cleanup skip 整個 wsId)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])
    // 理由：tear-off 後 workspace 在本 window 不再 visible，無 cache lookup 路徑；persisted
    // 也不該動，避免影響其他 window
  })

  it('host removal clears all its scopes', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/b')
    useHostStore.setState({ hostOrder: ['h2'] } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
  })

  it('repeated attach without dispose still installs single subscriber', () => {
    const dispose2 = attachPathCacheAutoCleanup()
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', tabs: [], moduleConfig: {} }],
      activeWorkspaceId: 'w2',
    } as never, false)
    // 不應 double-clear / 不應拋
    expect(() => dispose2()).not.toThrow()
  })

  it('after dispose, no longer cleans on workspace removal', () => {
    dispose?.()
    dispose = undefined
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', tabs: [], moduleConfig: {} }],
      activeWorkspaceId: 'w2',
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])  // 仍在
  })
})
```

- [ ] **Step 2: Run test, expect FAIL**

- [ ] **Step 3: Implement auto-cleanup**

新建 `spa/src/stores/path-cache/auto-cleanup.ts`：

```ts
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../useHostStore'
import { usePathCacheStore } from './usePathCacheStore'

/**
 * Subscribe path cache to workspace/host removal events. Returns a dispose
 * function — caller MUST call it on HMR dispose / test cleanup.
 *
 * Honors workspace tear-off semantics (v6 simplified):
 *   - real delete (keepSettings absent / false) → clearScope (in-memory + persisted)
 *   - tear-off (keepSettings: true) → SKIP cleanup entirely; in-memory + persisted
 *     both retained. Rationale: tear-off removes workspace from this window's
 *     viewport so cache lookups for that workspace cease anyway; other windows
 *     of the same origin keep their own in-memory state untouched and the
 *     shared persisted cache survives. Avoids relying on Zustand's nonexistent
 *     persist.pause/resume API (v6 codex review #2).
 *
 * Hydration-aware: defers attach until workspace store finishes hydration to
 * avoid using empty baseline (which would erase persisted cache on mount).
 */
export function attachPathCacheAutoCleanup(): () => void {
  let unsubWs: (() => void) | undefined
  let unsubHost: (() => void) | undefined
  let disposed = false

  const start = () => {
    if (disposed) return

    unsubWs = useWorkspaceStore.subscribe((state, prevState) => {
      const prevIds = new Set((prevState as { workspaces: { id: string }[] }).workspaces.map((w) => w.id))
      const currIds = new Set(state.workspaces.map((w: { id: string }) => w.id))
      const removed: string[] = []
      for (const id of prevIds) if (!currIds.has(id)) removed.push(id)
      if (removed.length === 0) return

      // workspaceMeta hint: caller (workspace store) sets `_lastRemovedKeepSettings`
      // when removeWorkspace was invoked with keepSettings:true. Treat unset as false.
      const keepSettingsId = (state as { _lastRemovedKeepSettings?: string })._lastRemovedKeepSettings

      const dirs = usePathCacheStore.getState().dirsByScope
      for (const wsId of removed) {
        if (wsId === keepSettingsId) continue  // tear-off: skip cleanup; retain both layers
        for (const key of Object.keys(dirs)) {
          const [hostId, scopeWsId] = key.split(':')
          if (scopeWsId === wsId) {
            usePathCacheStore.getState().clearScope(hostId, scopeWsId)
          }
        }
      }
    })

    unsubHost = useHostStore.subscribe((state, prevState) => {
      const prevIds = new Set((prevState as { hostOrder: string[] }).hostOrder)
      const currIds = new Set(state.hostOrder)
      for (const id of prevIds) if (!currIds.has(id)) usePathCacheStore.getState().clearHost(id)
    })
  }

  if (useWorkspaceStore.persist.hasHydrated()) start()
  else useWorkspaceStore.persist.onFinishHydration(start)

  return () => {
    disposed = true
    unsubWs?.()
    unsubHost?.()
    unsubWs = unsubHost = undefined
  }
}
```

> **`features/workspace/store` 需要設定 `_lastRemovedKeepSettings` metadata field** — 在 `removeWorkspace(id, opts)` 內 `set({ workspaces, _lastRemovedKeepSettings: opts?.keepSettings ? id : undefined })`。若該 store 沒這欄位，須一併加（小改動）。

- [ ] **Step 4: Wire into bootstrap**

在 `spa/src/main.tsx` 既有 store 初始化後（registerBuiltinModules 之後）加：

```tsx
import { attachPathCacheAutoCleanup } from './stores/path-cache/auto-cleanup'
const disposePathCache = attachPathCacheAutoCleanup()
if (import.meta.hot) import.meta.hot.dispose(() => disposePathCache())
```

- [ ] **Step 5: Run test, expect PASS**

```
cd spa && npx vitest run src/stores/path-cache/
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/path-cache/auto-cleanup.ts spa/src/stores/path-cache/auto-cleanup.test.ts spa/src/main.tsx
git commit -m "feat(spa): pathcache auto-cleanup with dispose and keepsettings semantics"
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

