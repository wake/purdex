# Automated Test Suite Stabilization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓最新 `origin/main` 基線上的主要自動化測試入口重新可跑完，修正 `internal/module/files` 孤兒測試與 `EditorPane` 過期測試，同時維持現行 `fs` module 與 `FsBackend` editor 架構。

**Spec:** `docs/superpowers/specs/2026-04-21-automated-test-suite-stabilization-design.md`

**Architecture:** Go 端不回補已刪除的 `files` module，而是把仍有意義的 handler coverage 遷到現行 `internal/module/fs/`。SPA 端不恢復舊的 `docId` / `bindingStatus` editor 模型，而是用可控的 `FsBackend` mock 重寫 `EditorPane` 測試，覆蓋目前真實存在的 load / save / cleanup / reload 行為。所有驗證都必須在目標 worktree 內完成。

**Tech Stack:** Go `testing` / `httptest`, React 19, Zustand 5, Vitest, Testing Library, pnpm workspace

---

## File Structure

### New Files

- `internal/module/fs/handler_test.go` — 現行 `fs` list handler 契約與 edge cases 測試

### Modified Files

- `spa/src/components/editor/__tests__/EditorPane.test.tsx` — 改寫為 `FsBackend` 導向測試

### Deleted Files

- `internal/module/files/handler_test.go` — 移除已失效的孤兒測試 package

### Invariants Not To Break

- `internal/module/fs/` 仍是唯一有效的 daemon filesystem module；不要重建 `internal/module/files` 相容層。
- `EditorPane` 仍使用 `getFsBackend(source)` 與 `bufferKey(source, filePath)`；不要把 store/keying 拉回 `docId` 模型。
- save error 仍維持目前語意：不標記成已儲存、不新增 `orphaned` 狀態機。
- 驗證結果必須來自 `.worktrees/fix-test-suite` 本身，不能借用其他 workspace 的測試結果。

---

## Task 0: Prepare The Target Worktree For Verification

**Files:** None

- [ ] **Step 1: Confirm tool availability inside the target worktree**

Run in `.worktrees/fix-test-suite`:

```bash
git status --short --branch
pnpm --version
go version
```

Expected: branch is `feature/fix-test-suite`; `pnpm` and `go` are available.

- [ ] **Step 2: Ensure SPA dependencies are runnable in the target worktree**

If `pnpm --prefix spa exec vitest --version` fails in the target worktree, install dependencies from the workspace root:

```bash
pnpm install
```

Expected: `pnpm --prefix spa exec vitest --version` succeeds afterward.

- [ ] **Step 3: Capture the failing baseline from the target worktree**

Run:

```bash
go test ./internal/module/files -count=1
pnpm --prefix spa exec vitest run "src/components/editor/__tests__/EditorPane.test.tsx"
```

Expected:
- Go fails with undefined `New` / `FileEntry` in `internal/module/files`
- SPA fails in `EditorPane.test.tsx` because the test still assumes the removed in-app document model

---

## Task 1: Migrate The Orphan `files` Test Coverage To `fs`

**Files:**
- Create: `internal/module/fs/handler_test.go`
- Delete: `internal/module/files/handler_test.go`

- [ ] **Step 1: Write failing `fs` list handler tests before deleting the orphan test**

Create `internal/module/fs/handler_test.go` covering the current `POST /api/fs/list` contract:

- module metadata: `Name() == "fs"`, `Dependencies() == nil`
- normal directory listing: directories first, alphabetical within dir/file groups
- request body must provide JSON `path`
- missing or non-absolute `path` returns `400`
- hidden files filtered out
- non-existent path handling
- empty directory returns `entries: []` rather than `null`
- file path input keeps current error behavior
- broken symlink does not crash the handler and still returns the rest of the directory entries

Use JSON request bodies that match the real handler, for example:

```json
{"path":"/absolute/path"}
```

- [ ] **Step 2: Run the focused Go tests against the new suite**

Run:

```bash
go test ./internal/module/fs -count=1
```

Expected: the command exercises the new `fs` suite in the target worktree. It may fail while the test body is still being aligned, but do not manufacture failing assertions just to force a red step.

- [ ] **Step 3: Fix the new `fs` tests to align with the real handler behavior**

Adjust the assertions against the current behavior in `internal/module/fs/handler.go` without changing production code unless a real bug is exposed.

- [ ] **Step 4: Remove the orphan `files` test package**

Delete:

```text
internal/module/files/handler_test.go
```

- [ ] **Step 5: Re-run focused Go verification**

Run:

```bash
go test ./internal/module/fs -count=1
```

Expected: `./internal/module/fs` passes, and the orphan `files` test no longer exists to break `go test ./...`.

---

## Task 2: Rewrite `EditorPane` Tests Around The Current `FsBackend` Contract

**Files:**
- Modify: `spa/src/components/editor/__tests__/EditorPane.test.tsx`

- [ ] **Step 1: Replace the obsolete backend mocks with an explicit `FsBackend` test double**

Update the test setup so it mocks `getFsBackend(source)` and returns a backend object with at least:

- `read(path)`
- `write(path, content)`
- `stat(path)`
- `available()`
- stable placeholders for the rest of the interface if needed by typing

Do not mock `getFsBackend` as `undefined`; the point is to drive the real load path.

- [ ] **Step 2: Write tests for the current runtime responsibilities**

Replace the old `docId` / `bindingStatus` assertions with tests that verify the real behavior in `EditorPane.tsx`:

1. initial load:
   - calls `read()` and `stat()`
   - opens a buffer under the current buffer key
   - renders the filename derived from `pane.content.filePath`
2. save success:
   - mark the buffer dirty
   - click `Save (⌘S)`
   - verify `write()` then `stat()` are called
   - verify dirty state is cleared
3. save failure:
   - `write()` rejects
   - dirty state remains true
   - buffer is not marked as saved
4. unmount cleanup:
   - after render/load, unmount the component
   - verify the corresponding buffer key is removed from the store
5. active reload:
   - when the pane becomes active and external stat/read data changes, a non-dirty buffer reloads
   - when the buffer is dirty, the same external change does not overwrite content

- [ ] **Step 3: Run the focused SPA test file during the rewrite**

Run:

```bash
pnpm --prefix spa exec vitest run "src/components/editor/__tests__/EditorPane.test.tsx"
```

Expected: use the result to verify the rewritten test matches the real component behavior. It may be red mid-rewrite, but do not add artificial failures solely to satisfy a TDD checkpoint.

- [ ] **Step 4: Finish the test rewrite and get the focused file green**

Keep the rewrite minimal:

- prefer existing helpers over new test utilities unless repetition becomes material
- do not change `EditorPane` production code solely to preserve deleted product semantics

- [ ] **Step 5: Re-run the focused SPA test file**

Run:

```bash
pnpm --prefix spa exec vitest run "src/components/editor/__tests__/EditorPane.test.tsx"
```

Expected: PASS.

---

## Task 3: Full Verification In The Same Worktree

**Files:** None

- [ ] **Step 1: Run Go verification from the target worktree**

```bash
go test ./internal/module/fs -count=1
make test
```

Expected: PASS.

- [ ] **Step 2: Run SPA verification from the target worktree**

```bash
pnpm --prefix spa exec vitest run "src/components/editor/__tests__/EditorPane.test.tsx"
pnpm --prefix spa exec vitest run
```

Expected: PASS.

- [ ] **Step 3: Inspect the final diff for scope control**

Run:

```bash
git status --short
git diff -- internal/module/fs/handler_test.go internal/module/files/handler_test.go spa/src/components/editor/__tests__/EditorPane.test.tsx
```

Expected: only the planned test files changed for implementation work.

---

## Completion Checklist

- [ ] `internal/module/files/handler_test.go` is removed
- [ ] `internal/module/fs/handler_test.go` covers the current list handler contract and retained edge cases
- [ ] `EditorPane.test.tsx` no longer depends on `openDocument/saveDocument`, `docId`, or `bindingStatus`
- [ ] `EditorPane.test.tsx` covers load, save success, save failure, unmount cleanup, and active reload behavior
- [ ] `make test` passes in `.worktrees/fix-test-suite`
- [ ] `pnpm --prefix spa exec vitest run` passes in `.worktrees/fix-test-suite`
