# Worker prelude Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A handed-off worker draws the whole earlier conversation (the transcript before its turn 1) above its first turn, in room and chat, loading older pages as the reader scrolls up.

**Architecture:** Nexen v0.16.0 serves `GET /v1/executions/{id}/prelude`, a backward-paged, read-only view of the resume target's transcript (spec §4, delegated to nexen-85). The SPA keeps the pages in a separate `prelude` slice of the execution store. That slice never touches `messages`, `lastSeq`, `turnStarts` or cost. The SPA derives a render view from it and draws it with the existing room / chat components under stable, namespaced keys (`p<pos>`). A layout effect keeps the reader's place when older content is prepended.

**Tech Stack:** React 19, Zustand 5, Vitest + Testing Library (jsdom), Tailwind 4; Go (pin bump only).

**Spec:** `docs/specs/2026-10-05-worker-prelude-spec.md`. Read §2 (decisions), §4.2–§4.5 (the wire contract this plan consumes) and §5 (Purdex design) before any task.

## Global Constraints

- Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/transcript-prelude`. Prefix **every** Bash command with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/transcript-prelude/spa && ` (or `…/transcript-prelude && ` for Go). Commit with `git commit --only <files>`.
- Test: `npx vitest run <file>`. Full suite: `npx vitest run`. Lint: `pnpm run lint`. Types: `npx tsc --noEmit -p tsconfig.app.json` (a bare `npx tsc --noEmit` is a no-op here).
- Prelude frames **never** enter `ExecutionState.messages`. The reducer, the seq guard, the SSE order contract (`useExecutionSubscription`), `costSummary`, `turnStarts` and `tools` stay untouched (spec §5.6).
- The feature is detected by the presence of `capabilities.transcript_prelude`, never by comparing versions. A worker with no `summary.resume_session_id` makes **no** prelude request (spec D4).
- Prelude keys are `p<pos>`, where `pos` matches `^[A-Za-z0-9._-]{1,64}$` (spec §4.3). Main-transcript keys stay `${i}:${j}`, unchanged.
- The prelude follows the room / chat fold rules exactly as the worker's own content does. Nothing is force-expanded (D1).
- `pos` is untrusted. Tool ids are untrusted (own-key lookups only, as `tool-activity.ts` already does).
- User-visible copy comes from `locales/en.json` and `locales/zh-TW.json` (`{{var}}` interpolation). Keys are under `worker.prelude.*`.
- PR size: each phase PR stays ≤ 800 changed lines and ≤ 20 files.

## Review Focus

1. **Two panes open on the same worker at once.** Expected: one request per page, no duplicated rows. The store's `prelude.status === 'loading'` is the lock, and `applyPreludePage` dedupes by `pos`. Owner: Task 4 (`useExecutionPrelude` test "two hooks, one request").
2. **A server bug returns a `prev_cursor` equal to the `before` it was given.** Expected: an error row with Retry, and never an automatic loop. Owner: Task 3 (`applyPreludePage` "stuck cursor"), Task 4 (`loadOlder` ignored while `status === 'error'`), and Task 6 (the sentinel unmounts on `error` and disconnects its observer).
3. **A late response arrives after the entry was cleared and recreated** (host remove + undo, a handoff swap, a second pane reopening it), possibly with a new request already in flight. Expected: only the request whose id is recorded lands; the old answer is dropped. Owner: Task 3 (request-id guard tests) and Task 4 ("clear → recreate → new request: the old answer is dropped").
4. **A prepend lands while the reader is mid-transcript or at the bottom, including in the same commit as a new live message.** Expected: what is on screen does not move, and a reader at the bottom stays at the bottom. Owner: Task 7 (snapshot anchoring tests: mid, bottom, same-commit).
5. **Hostile or malformed items and blocks.** Cases: `pos` with `:` or 200 chars, a duplicate `pos`, string `content`, a missing `message`, an unknown kind, a `__proto__` tool id, a block without a string `type`, a non-object `tool_use.input`, a numeric `tool_result.content`, a string image `source`, a non-boolean `truncated`. Expected: dropped or normalized at the API boundary, and render never throws. Owner: Task 1 (sanitizer tests), Task 3 (`__proto__`), Task 6 (render-level hostile fixture).

---

## Phase P1 — data layer (PR-1)

### Task 1: Wire types, sanitizer, capability selector

**Files:**
- Create: `spa/src/lib/nex/prelude-wire.ts`
- Create: `spa/src/lib/nex/prelude-wire.test.ts`
- Modify: `spa/src/lib/nex/message-types.ts:8-18` (`ContentBlock`)
- Modify: `spa/src/lib/nex/types.ts:228-235` (`NexCapabilities`)
- Modify: `spa/src/stores/useNexHostStore.ts` (add `selectTranscriptPrelude` after `selectWorkerRollup`, ~line 61)
- Test: `spa/src/stores/useNexHostStore.test.ts` (append)

**Interfaces:**
- Produces: `PreludeItem`, `PreludePage`, `sanitizePreludePage(body: unknown): PreludePage | null`, `PRELUDE_POS_RE`, `TranscriptPreludeCapability`, `selectTranscriptPrelude(hostId) => (s) => TranscriptPreludeCapability | null`. `ContentBlock` gains `'image'` plus optional `source`, `truncated` and `total_bytes`.

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/prelude-wire.test.ts
import { describe, it, expect } from 'vitest'
import { sanitizePreludePage } from './prelude-wire'

const asst = (pos: string, text: string) => ({
  pos, kind: 'assistant', at: 1759651200123,
  payload: { type: 'assistant', message: { id: 'msg_1', role: 'assistant', content: [{ type: 'text', text }] }, session_id: 's', uuid: 'u' },
})

describe('sanitizePreludePage', () => {
  it('keeps a well-formed ok page in order and maps the cursor', () => {
    const page = sanitizePreludePage({ state: 'ok', items: [asst('10.0', 'a'), asst('20.0', 'b')], prev_cursor: 'c1', total_bytes: 99 })
    expect(page).not.toBeNull()
    expect(page!.items.map((i) => i.pos)).toEqual(['10.0', '20.0'])
    expect(page!.prevCursor).toBe('c1')
    expect(page!.totalBytes).toBe(99)
  })

  it('reads none / gone as terminal states with no items and no cursor', () => {
    expect(sanitizePreludePage({ state: 'none', items: [], prev_cursor: null })).toEqual({ state: 'none', items: [], prevCursor: null, totalBytes: null })
    expect(sanitizePreludePage({ state: 'gone', items: [asst('1', 'x')], prev_cursor: 'c' })).toEqual({ state: 'gone', items: [], prevCursor: null, totalBytes: null })
  })

  it('rejects a body that is not a page', () => {
    expect(sanitizePreludePage(null)).toBeNull()
    expect(sanitizePreludePage({ state: 'weird' })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: [], prev_cursor: 42 })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: [], prev_cursor: 'x'.repeat(257) })).toBeNull()
  })

  it('drops items with a bad pos, an unknown kind, or no message, and keeps the first of a duplicated pos', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [
        asst('a:b', 'colon'), asst('x'.repeat(65), 'long'), { ...asst('1', 'unknown'), kind: 'result' },
        { pos: '2', kind: 'user', at: 1, payload: { type: 'user' } },
        asst('3', 'first'), asst('3', 'second'),
      ],
    })
    expect(page!.items).toHaveLength(1)
    expect(page!.items[0]).toMatchObject({ pos: '3', kind: 'assistant' })
  })

  it('turns string content into one text block and forces a top-level frame', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '5', kind: 'user', at: 7, payload: { type: 'user', parent_tool_use_id: 'toolu_x', message: { role: 'user', content: 'hello' } } }],
    })
    const it0 = page!.items[0]
    expect(it0.kind).toBe('user')
    if (it0.kind !== 'user') throw new Error('kind')
    expect(it0.msg).toMatchObject({ type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [{ type: 'text', text: 'hello' }], stop_reason: null } })
    expect(it0.at).toBe(7)
  })

  it('reads N2, segment, compaction and note items', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [
        { pos: '6.1', kind: 'tool_use', at: 1, payload: { tool_use_id: 'toolu_1', name: 'Bash' } },
        { pos: '6.2', kind: 'tool_result', at: 2, payload: { tool_use_id: 'toolu_1', status: 'ok' } },
        { pos: '7', kind: 'prelude.segment', at: 0, payload: { entrypoint: 'cli' } },
        { pos: '8', kind: 'prelude.compaction', at: 0, payload: { trigger: 'auto', pre_tokens: 9 } },
        { pos: '9', kind: 'prelude.note', at: 0, payload: { source: 'command_output', text: 'ok', truncated: true } },
        { pos: '10', kind: 'tool_use', at: 1, payload: { name: 'NoId' } },
      ],
    })
    expect(page!.items.map((i) => i.kind)).toEqual(['tool_use', 'tool_result', 'prelude.segment', 'prelude.compaction', 'prelude.note'])
    expect(page!.items[4]).toMatchObject({ source: 'command_output', text: 'ok', truncated: true })
    expect(page!.items[3]).toMatchObject({ trigger: 'auto' })
  })

  it('cleans every content block so nothing downstream can throw (Review Focus 5)', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{
        pos: '1', kind: 'assistant', at: 1,
        payload: { type: 'assistant', message: { role: 'assistant', content: [
          { text: 'no type' },
          { type: 'text', text: 42 },
          { type: 'tool_use', id: 't', name: 'Bash', input: 'rm -rf' },
          { type: 'tool_result', tool_use_id: 't', content: 7 },
          { type: 'image', source: 'base64…' },
          { type: 'text', text: 'ok', truncated: 'yes', total_bytes: -3 },
        ] } },
      }],
    })
    const it0 = page!.items[0]
    if (it0.kind !== 'assistant') throw new Error('kind')
    expect((it0.msg as { message: { content: unknown[] } }).message.content).toEqual([
      { type: 'text' },
      { type: 'tool_use', id: 't', name: 'Bash', input: {} },
      { type: 'tool_result', tool_use_id: 't', content: '' },
      { type: 'text', text: 'ok' },
    ])
  })
})

describe('contract sample (spec §4.3)', () => {
  it('every kind of the hand-written sample page survives the sanitiser unchanged', () => {
    const page = sanitizePreludePage(sample)!
    expect(page.state).toBe('ok')
    expect(page.items).toHaveLength(sample.items.length)
    expect(new Set(page.items.map((i) => i.kind))).toEqual(new Set(['prelude.segment', 'user', 'assistant', 'tool_use', 'tool_result', 'prelude.note', 'prelude.compaction']))
  })
})
```

Add `import sample from './__fixtures__/prelude-contract-sample.json'` at the top. Create `spa/src/lib/nex/__fixtures__/prelude-contract-sample.json`, **hand-written from spec §4.3**, holding one `ok` page in this order: a `prelude.segment` (`cli`); a human `user` prompt; an `assistant` text; an `assistant` `tool_use` (Bash, `input: {command:'ls'}`) plus its N2 `tool_use` item (`pos` `<offset>.1`); a `user` `tool_result` plus its N2 `tool_result` item (`status:'ok'`, `duration_ms`, `output: {text, total_lines, total_bytes, truncated:false}`); a `prelude.note` `command_output`; a `prelude.compaction` (`auto`); a `user` with an omitted image block; a `prelude.segment` (`sdk-cli`). Use the `pos` format `<byteOffset>` / `<byteOffset>.<n>` and a `prev_cursor` string. **When nexen-85 delivers the early golden page (spec §4.6), add it next to this sample as `prelude-golden-nexen.json` and run the same test over it.** Where the two disagree, the golden page wins and the spec is corrected.

Append to `spa/src/stores/useNexHostStore.test.ts`, inside the describe that holds the `selectWorkerRollup` case. Mirror that case, using the file's own `seed()` and `caps()` helpers:

```ts
  it('selectTranscriptPrelude returns the capability object when present and ready, else null', () => {
    const cap = { route: { method: 'GET', path: '/api/nex/v1/executions/{id}/prelude' }, page_max_items: 500, page_max_bytes: 1048576, max_block_bytes: 65536 }
    seed({ capabilities: caps({ transcript_prelude: cap }) })
    const got = selectTranscriptPrelude(H)(useNexHostStore.getState())
    expect(got).toEqual(cap)
    expect(selectTranscriptPrelude(H)(useNexHostStore.getState())).toBe(got)
    seed({})
    expect(selectTranscriptPrelude(H)(useNexHostStore.getState())).toBeNull()
    seed({ phase: 'unavailable', capabilities: caps({ transcript_prelude: cap }) })
    expect(selectTranscriptPrelude(H)(useNexHostStore.getState())).toBeNull()
    expect(selectTranscriptPrelude('ghost')(useNexHostStore.getState())).toBeNull()
  })
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/lib/nex/prelude-wire.test.ts src/stores/useNexHostStore.test.ts`
Expected: FAIL. The module is not found, and `selectTranscriptPrelude` is not exported.

- [ ] **Step 3: Implement**

`message-types.ts`: widen `ContentBlock`:

```ts
export interface ContentBlock {
  type: 'text' | 'tool_use' | 'tool_result' | 'thinking' | 'image' | 'document'
  text?: string
  id?: string
  name?: string
  input?: Record<string, unknown>
  content?: string
  is_error?: boolean
  thinking?: string
  tool_use_id?: string
  /** `image` / `document` blocks. In the prelude: `{type:'omitted', media_type, bytes}` (spec §4.3), never data; `bytes` is the decoded size. */
  source?: { type: string; media_type?: string; bytes?: number }
  /** Prelude only (spec §4.3): the block was cut at `max_block_bytes`. */
  truncated?: boolean
  total_bytes?: number
}
```

`types.ts`: add the interface next to `WorkerRollupCapability`, and the field in `NexCapabilities` before the index signature:

```ts
/** `capabilities.transcript_prelude` (worker prelude spec §4.5). Presence is the only feature detect. */
export interface TranscriptPreludeCapability {
  route: { method: string; path: string }
  page_max_items: number
  page_max_bytes: number
  max_block_bytes: number
}
  // in NexCapabilities:
  /** Presence = `GET /v1/executions/{id}/prelude` exists (worker prelude spec §4). */
  transcript_prelude?: TranscriptPreludeCapability
```

`useNexHostStore.ts`, after `selectWorkerRollup`:

```ts
/**
 * `capabilities.transcript_prelude` of a ready host, or null (not ready, an
 * unknown host, or a daemon older than Nexen v0.16.0). Presence is the only
 * feature detect (worker prelude spec §4.5). Returns the cached object
 * itself, so it is a stable selector result.
 */
export function selectTranscriptPrelude(hostId: string): (s: Pick<NexHostState, 'byHost'>) => TranscriptPreludeCapability | null {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return null
    const cap = entry.capabilities.transcript_prelude
    return typeof cap === 'object' && cap !== null ? cap : null
  }
}
```

`prelude-wire.ts`:

```ts
// spa/src/lib/nex/prelude-wire.ts — the API boundary of the worker prelude
// (spec §4.2–§4.3): a page from GET /v1/executions/{id}/prelude, checked and
// normalised so nothing downstream ever sees a shape the renderer would
// throw on (a string `content`, a colon in a key). Pure.
import type { StreamMessage } from './message-types'

/** Spec §4.3: opaque, and never holds ':' — it is spliced into colon-separated keys. */
export const PRELUDE_POS_RE = /^[A-Za-z0-9._-]{1,64}$/
const MAX_CURSOR_BYTES = 256

export type PreludeItem =
  | { pos: string; at: number; kind: 'assistant' | 'user'; msg: StreamMessage }
  | { pos: string; at: number; kind: 'tool_use' | 'tool_result'; payload: Record<string, unknown> }
  | { pos: string; at: number; kind: 'prelude.segment'; entrypoint: string }
  | { pos: string; at: number; kind: 'prelude.compaction'; trigger: string }
  | { pos: string; at: number; kind: 'prelude.note'; source: string; text: string; truncated: boolean; totalBytes: number | null; stream: string | null }

export interface PreludePage {
  state: 'ok' | 'none' | 'gone'
  /** Oldest → newest within the page. */
  items: PreludeItem[]
  /** The next `before`; null = this page reached the start of the file. */
  prevCursor: string | null
  totalBytes: number | null
}

function rec(v: unknown): Record<string, unknown> | null {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

/**
 * One content block, cleaned so no renderer can throw on it (spec §5.2):
 * null drops it. Only the fields the room reads are checked; anything else
 * on the block passes through untouched.
 */
function cleanBlock(raw: unknown): Record<string, unknown> | null {
  const b = rec(raw)
  if (!b || typeof b.type !== 'string') return null
  const out: Record<string, unknown> = { ...b }
  for (const k of ['text', 'thinking'] as const) if (k in out && typeof out[k] !== 'string') delete out[k]
  if (out.type === 'tool_use' && rec(out.input) === null) out.input = {}
  if (out.type === 'tool_result' && 'content' in out) {
    const c = out.content
    if (!(typeof c === 'string' || (Array.isArray(c) && c.every((x) => rec(x) !== null)))) out.content = ''
  }
  if ((out.type === 'image' || out.type === 'document') && rec(out.source) === null) return null
  if ('truncated' in out && typeof out.truncated !== 'boolean') delete out.truncated
  if ('total_bytes' in out && !(Number.isSafeInteger(out.total_bytes) && (out.total_bytes as number) >= 0)) delete out.total_bytes
  return out
}

/** A frame as the room reads it: `content` always an array of clean blocks, always top level (spec §4.3). */
function frame(kind: 'assistant' | 'user', payload: Record<string, unknown>): StreamMessage | null {
  const message = rec(payload.message)
  if (!message) return null
  const raw = message.content
  const content = typeof raw === 'string'
    ? [{ type: 'text', text: raw }]
    : Array.isArray(raw) ? raw.map(cleanBlock).filter((b): b is Record<string, unknown> => b !== null) : null
  if (!content) return null
  return {
    ...payload,
    type: kind,
    parent_tool_use_id: null,
    message: { ...message, role: kind, content, stop_reason: message.stop_reason ?? null },
  } as unknown as StreamMessage
}

function item(raw: unknown): PreludeItem | null {
  const r = rec(raw)
  if (!r || typeof r.pos !== 'string' || !PRELUDE_POS_RE.test(r.pos)) return null
  const p = rec(r.payload)
  if (!p) return null
  const at = Number.isSafeInteger(r.at) && (r.at as number) > 0 ? (r.at as number) : 0
  const pos = r.pos
  const kind = r.kind
  if (kind === 'assistant' || kind === 'user') {
    const msg = frame(kind, p)
    return msg ? { pos, at, kind, msg } : null
  }
  if (kind === 'tool_use' || kind === 'tool_result') {
    return typeof p.tool_use_id === 'string' && p.tool_use_id !== '' ? { pos, at, kind, payload: p } : null
  }
  if (kind === 'prelude.segment') return typeof p.entrypoint === 'string' ? { pos, at, kind, entrypoint: p.entrypoint } : null
  if (kind === 'prelude.compaction') return { pos, at, kind, trigger: typeof p.trigger === 'string' ? p.trigger : '' }
  if (kind === 'prelude.note') {
    if (typeof p.source !== 'string' || typeof p.text !== 'string') return null
    const tb = p.total_bytes
    return {
      pos, at, kind, source: p.source, text: p.text, truncated: p.truncated === true,
      totalBytes: Number.isSafeInteger(tb) && (tb as number) >= 0 ? (tb as number) : null,
      // `bash_output` only (spec §4.3): which stream the text came from.
      stream: typeof p.stream === 'string' ? p.stream : null,
    }
  }
  return null
}

/** null = not a page at all (the caller treats it as an error, never as "no prelude"). */
export function sanitizePreludePage(body: unknown): PreludePage | null {
  const b = rec(body)
  if (!b) return null
  const state = b.state
  if (state !== 'ok' && state !== 'none' && state !== 'gone') return null
  const cursor = b.prev_cursor
  let prevCursor: string | null = null
  if (cursor !== null && cursor !== undefined) {
    if (typeof cursor !== 'string' || cursor === '' || new TextEncoder().encode(cursor).length > MAX_CURSOR_BYTES) return null
    prevCursor = cursor
  }
  if (state !== 'ok') return { state, items: [], prevCursor: null, totalBytes: null }
  const items: PreludeItem[] = []
  const seen = new Set<string>()
  if (Array.isArray(b.items)) {
    for (const raw of b.items) {
      const it = item(raw)
      if (it && !seen.has(it.pos)) { seen.add(it.pos); items.push(it) }
    }
  }
  const tb = b.total_bytes
  return { state, items, prevCursor, totalBytes: Number.isSafeInteger(tb) && (tb as number) >= 0 ? (tb as number) : null }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/lib/nex/prelude-wire.test.ts src/stores/useNexHostStore.test.ts && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS, no type errors. If widening `ContentBlock.type` breaks an exhaustive `switch` elsewhere, add an `'image'` arm that returns what the `default` / fall-through returned before.

- [ ] **Step 5: Commit**

```bash
git commit --only src/lib/nex/prelude-wire.ts src/lib/nex/prelude-wire.test.ts src/lib/nex/__fixtures__/prelude-contract-sample.json src/lib/nex/message-types.ts src/lib/nex/types.ts src/stores/useNexHostStore.ts src/stores/useNexHostStore.test.ts -m "feat(spa): prelude wire types, page sanitizer and capability selector"
```

### Task 2: API client

**Files:**
- Modify: `spa/src/lib/nex/nex-api.ts` (after `fetchExecutionEvents`, ~line 163)
- Test: `spa/src/lib/nex/nex-api.test.ts` (append inside the `describe('nex-api')`)

**Interfaces:**
- Consumes: `sanitizePreludePage`, `PreludePage` (Task 1).
- Produces: `fetchExecutionPrelude(hostId: string, executionId: string, opts?: { before?: string; limit?: number }): Promise<PreludePage>`. It rejects with `NexApiError` (`code: 'malformed_response'` for a non-page body).

- [ ] **Step 1: Write the failing tests**

```ts
  it('fetchExecutionPrelude GETs /prelude with before/limit and sanitises the page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ state: 'ok', items: [], prev_cursor: 'c2', total_bytes: 5 }))
    const page = await fetchExecutionPrelude(hostId, 'exc_1', { before: 'c1', limit: 200 })
    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/prelude?before=c1&limit=200')
    expect(page).toEqual({ state: 'ok', items: [], prevCursor: 'c2', totalBytes: 5 })
  })

  it('fetchExecutionPrelude sends no query for the first page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ state: 'none', items: [], prev_cursor: null }))
    await fetchExecutionPrelude(hostId, 'exc_1')
    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/prelude')
  })

  it('fetchExecutionPrelude rejects a body that is not a page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ hello: 1 }))
    await expect(fetchExecutionPrelude(hostId, 'exc_1')).rejects.toMatchObject({ code: 'malformed_response' })
  })
```

Add `fetchExecutionPrelude` to the import list at the top of the test file.

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/lib/nex/nex-api.test.ts`
Expected: FAIL (`fetchExecutionPrelude` is not exported).

- [ ] **Step 3: Implement** (in `nex-api.ts`; add `NexApiError` to the existing `./types` import if it is only imported as a type there, and import `sanitizePreludePage, type PreludePage` from `./prelude-wire`)

```ts
/**
 * One page of the worker prelude (spec §4.2), newest page first: no
 * `before` = the page ending at the execution's turn-1 boundary. A body that
 * is not a page is an error (`malformed_response`), never "no prelude".
 */
export function fetchExecutionPrelude(
  hostId: string,
  executionId: string,
  opts: { before?: string; limit?: number } = {},
): Promise<PreludePage> {
  const q = new URLSearchParams()
  if (opts.before) q.set('before', opts.before)
  if (opts.limit) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return nexFetch(hostId, `${execPath(executionId, '/prelude')}${qs ? `?${qs}` : ''}`)
    .then((r) => okJson<unknown>(r))
    .then((body) => {
      const page = sanitizePreludePage(body)
      if (!page) throw new NexApiError(0, 'malformed_response', 'malformed prelude page')
      return page
    })
}
```

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/lib/nex/nex-api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only src/lib/nex/nex-api.ts src/lib/nex/nex-api.test.ts -m "feat(spa): fetchExecutionPrelude"
```

### Task 3: Prelude state, page reducer, derived view, store actions

**Files:**
- Create: `spa/src/lib/nex/prelude.ts`
- Create: `spa/src/lib/nex/prelude.test.ts`
- Modify: `spa/src/lib/nex/tool-activity.ts:222-252` (extract tools-level N2 functions; the existing ones become wrappers)
- Modify: `spa/src/lib/nex/event-reducer.ts:51-151` (`ExecutionState.prelude` and its default)
- Modify: `spa/src/stores/useExecutionStore.ts` (three actions)
- Test: `spa/src/stores/useExecutionStore.test.ts` (append)

**Interfaces:**
- Consumes: `PreludeItem`, `PreludePage` (Task 1).
- Produces:
  - `PreludeState { status: 'idle'|'loading'|'ok'|'none'|'gone'|'error'; items: PreludeItem[]; cursor: string|null; done: boolean; error: string|null; totalBytes: number|null; request: number|null; pages: number }`
  - `defaultPreludeState()`, `preludeLoading(p, request: number)`, `preludeFailed(p, message, request: number)`, `applyPreludePage(p, page, sentBefore: string|null, request: number)`. Each of the last two returns `p` itself, unchanged, unless `p.request === request`.
  - `PreludeEntry`, `PreludeView { entries; messages: StreamMessage[]; ids: string[]; tools: Record<string, ToolActivity> }`, `derivePrelude(items): PreludeView`, `preludeId(pos): string`
  - In `tool-activity.ts`: `recordN2ToolUseIn(tools, p, at)` and `recordN2ToolResultIn(tools, p, at)`, both returning `Record<string, ToolActivity>` (the same object when unchanged).
  - Store actions: `preludeLoading(h, e, request)`, `applyPreludePage(h, e, page, sentBefore, request)`, `preludeFailed(h, e, message, request)`, `resetPrelude(h, e)`.

- [ ] **Step 1: Write the failing tests**

```ts
// spa/src/lib/nex/prelude.test.ts
import { describe, it, expect } from 'vitest'
import { applyPreludePage, defaultPreludeState, derivePrelude, preludeFailed, preludeLoading, type PreludeState } from './prelude'
import type { PreludeItem, PreludePage } from './prelude-wire'
import type { StreamMessage } from './message-types'

const msg = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ pos, at: 1000, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })
const ok = (items: PreludeItem[], prevCursor: string | null): PreludePage => ({ state: 'ok', items, prevCursor, totalBytes: null })

/** Load one page as request `r` — the way the hook does it. */
const load = (p: PreludeState, page: PreludePage, sentBefore: string | null, r: number) =>
  applyPreludePage(preludeLoading(p, r), page, sentBefore, r)

describe('applyPreludePage', () => {
  it('prepends an older page and moves the cursor', () => {
    let p = load(defaultPreludeState(), ok([msg('20', 'user', [])], 'c1'), null, 1)
    expect(p).toMatchObject({ status: 'ok', cursor: 'c1', done: false, request: null })
    p = load(p, ok([msg('10', 'user', [])], null), 'c1', 2)
    expect(p.items.map((i) => i.pos)).toEqual(['10', '20'])
    expect(p).toMatchObject({ status: 'ok', cursor: null, done: true })
  })

  it('dedupes by pos', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', []), msg('20', 'user', [])], 'c1'), null, 1)
    const p2 = load(p1, ok([msg('5', 'user', []), msg('10', 'user', [])], null), 'c1', 2)
    expect(p2.items.map((i) => i.pos)).toEqual(['5', '10', '20'])
  })

  it('only the recorded request lands (Review Focus 3)', () => {
    const loading = preludeLoading(defaultPreludeState(), 2)
    expect(applyPreludePage(loading, ok([msg('10', 'user', [])], null), null, 1)).toBe(loading)
    expect(preludeFailed(loading, 'late', 1)).toBe(loading)
    expect(applyPreludePage(defaultPreludeState(), ok([], null), null, 1)).toEqual(defaultPreludeState())
  })

  it('stuck cursor: a prev_cursor equal to the before it answered is an error, not a loop', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    const p2 = load(p1, ok([], 'c1'), 'c1', 2)
    expect(p2.status).toBe('error')
    expect(p2.items).toBe(p1.items)
    expect(p2.cursor).toBe('c1')
  })

  it('gone ends the prelude and keeps what was loaded; none on an older page is an error', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    const g = load(p1, { state: 'gone', items: [], prevCursor: null, totalBytes: null }, 'c1', 2)
    expect(g).toMatchObject({ status: 'gone', done: true, cursor: null })
    expect(g.items).toHaveLength(1)
    const n = load(p1, { state: 'none', items: [], prevCursor: null, totalBytes: null }, 'c1', 3)
    expect(n).toMatchObject({ status: 'error', cursor: 'c1' })
    expect(load(defaultPreludeState(), { state: 'none', items: [], prevCursor: null, totalBytes: null }, null, 4))
      .toMatchObject({ status: 'none', done: true })
  })

  it('preludeFailed keeps items and cursor', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    expect(preludeFailed(preludeLoading(p1, 2), 'boom', 2)).toMatchObject({ status: 'error', error: 'boom', cursor: 'c1', items: p1.items, request: null })
  })
})

describe('derivePrelude', () => {
  it('lists messages and markers in order, with stable ids', () => {
    const v = derivePrelude([
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      msg('2', 'user', [{ type: 'text', text: 'hi' }]),
      { pos: '3', at: 0, kind: 'prelude.note', source: 'command_output', text: 'out', truncated: false, totalBytes: null, stream: null },
      msg('4', 'assistant', [{ type: 'text', text: 'yo' }]),
    ])
    expect(v.entries.map((e) => e.kind)).toEqual(['segment', 'message', 'note', 'message'])
    expect(v.ids).toEqual(['p2', 'p4'])
    expect(v.messages).toHaveLength(2)
    expect(v.entries[3]).toMatchObject({ kind: 'message', m: 1 })
  })

  it('builds the tool overlay from N2 items and closes a call that was never answered', () => {
    const v = derivePrelude([
      msg('1', 'assistant', [{ type: 'tool_use', id: 'toolu_a', name: 'Bash', input: {} }]),
      { pos: '1.1', at: 1000, kind: 'tool_use', payload: { tool_use_id: 'toolu_a', name: 'Bash' } },
      msg('2', 'assistant', [{ type: 'tool_use', id: 'toolu_b', name: 'Read', input: {} }]),
      { pos: '2.1', at: 2000, kind: 'tool_use', payload: { tool_use_id: 'toolu_b', name: 'Read' } },
      msg('3', 'user', [{ type: 'tool_result', tool_use_id: 'toolu_a', content: 'x' }]),
      { pos: '3.1', at: 3000, kind: 'tool_result', payload: { tool_use_id: 'toolu_a', status: 'ok', duration_ms: 2000 } },
    ])
    expect(v.tools.toolu_a.status).toBe('done')
    expect(v.tools.toolu_b.status).toBe('aborted')
  })

  it('a __proto__ tool id is an own key, never the prototype', () => {
    const v = derivePrelude([{ pos: '1', at: 1, kind: 'tool_use', payload: { tool_use_id: '__proto__', name: 'X' } }])
    expect(Object.hasOwn(v.tools, '__proto__')).toBe(true)
    expect(({} as Record<string, unknown>).name).toBeUndefined()
  })
})
```

Append to `useExecutionStore.test.ts`:

```ts
describe('prelude actions', () => {
  it('loading → page → never touches messages, lastSeq or tools', () => {
    const s = useExecutionStore.getState()
    s.applyEvents('h', 'e', [{ seq: 1, execution_id: 'e', kind: 'execution.delegated', payload: { brief: 'b' }, created_at: 1 }])
    const before = useExecutionStore.getState().executions[executionKey('h', 'e')]
    s.preludeLoading('h', 'e', 1)
    s.applyPreludePage('h', 'e', { state: 'ok', items: [], prevCursor: null, totalBytes: null }, null, 1)
    const after = useExecutionStore.getState().executions[executionKey('h', 'e')]
    expect(after.prelude).toMatchObject({ status: 'ok', done: true })
    expect(after.messages).toBe(before.messages)
    expect(after.lastSeq).toBe(before.lastSeq)
    expect(after.tools).toBe(before.tools)
  })
})
```

(Reset the store in that file's existing `beforeEach`. If there is none, add `beforeEach(() => useExecutionStore.setState({ executions: {} }))`.)

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/lib/nex/prelude.test.ts src/stores/useExecutionStore.test.ts`
Expected: FAIL (module missing / actions missing).

- [ ] **Step 3: Implement**

`tool-activity.ts`: move the bodies of `recordN2ToolUse` / `recordN2ToolResult` into tools-level functions. The originals delegate, so behaviour is byte-for-byte the same for every existing caller:

```ts
function putIn(tools: Record<string, ToolActivity>, id: string, next: ToolActivity): Record<string, ToolActivity> {
  return sameEntry(lookup(tools, id), next) ? tools : { ...tools, [id]: next }
}

/** N1 over a bare tools map (the prelude's overlay, spec §5.2); `recordN2ToolUse` is this on `s.tools`. */
export function recordN2ToolUseIn(tools: Record<string, ToolActivity>, p: Record<string, unknown>, at: number): Record<string, ToolActivity> {
  const id = p.tool_use_id
  if (!str(id)) return tools
  const overlay: Pick<Overlay, 'primaryArg' | 'known'> = { ...readPrimaryArg(p), ...(bool(p.known) ? { known: p.known } : {}) }
  const t = lookup(tools, id)
  const next: ToolActivity = t
    ? { ...t, ...overlay, name: t.name === '' && str(p.name) ? p.name : t.name }
    : { name: str(p.name) ? p.name : '', startedAt: at, endedAt: null, status: 'running', ...overlay }
  return putIn(tools, id, next)
}

export function recordN2ToolUse(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  const tools = recordN2ToolUseIn(s.tools, p, at)
  return tools === s.tools ? s : { ...s, tools }
}
```

Do the same for the result side. `recordN2ToolResultIn` gets the current `recordN2ToolResult` body with `s.tools` → `tools`, and `recordN2ToolResult` becomes the same two-line wrapper. Keep `putEntry` only if something else still uses it; otherwise delete it.

`prelude.ts`:

```ts
// spa/src/lib/nex/prelude.ts — the worker prelude's state and its render view
// (spec §5.2). The state is the pages as fetched (oldest first, growing only
// at the front); the view is derived from it on render and is what both
// transcripts draw. Nothing here reads or writes the execution's own
// messages, seq, turns or tools. Pure.
import type { StreamMessage } from './message-types'
import type { PreludeItem, PreludePage } from './prelude-wire'
import { recordN2ToolResultIn, recordN2ToolUseIn, type ToolActivity } from './tool-activity'

export interface PreludeState {
  status: 'idle' | 'loading' | 'ok' | 'none' | 'gone' | 'error'
  /** Oldest → newest; only ever grows at the front. */
  items: PreludeItem[]
  /** The next `before`; null before the first page and once `done`. */
  cursor: string | null
  done: boolean
  error: string | null
  totalBytes: number | null
  /**
   * The page request in flight (spec §5.2). An answer lands only while its
   * id is still this one, so a late answer cannot land on an entry that
   * was cleared and recreated in between (Review Focus 3).
   */
  request: number | null
  /**
   * Pages applied so far. The sentinel re-arms on it, not on the item count:
   * a page may legally hold no items and still have a cursor (spec §4.3).
   */
  pages: number
}

export function defaultPreludeState(): PreludeState {
  return { status: 'idle', items: [], cursor: null, done: false, error: null, totalBytes: null, request: null, pages: 0 }
}

export function preludeLoading(p: PreludeState, request: number): PreludeState {
  return { ...p, status: 'loading', error: null, request }
}

export function preludeFailed(p: PreludeState, message: string, request: number): PreludeState {
  if (p.request !== request) return p
  return { ...p, status: 'error', error: message, request: null }
}

/**
 * Fold one page in, if `request` is the one in flight. `sentBefore` is the
 * cursor the request carried (null for the first page):
 * - a page that hands back the same cursor made no progress (a server bug)
 *   and becomes an error the reader can retry, never a loop (Review Focus 2);
 * - `none` is only ever the first page's answer (spec §4.2); on an older
 *   page it is a contract violation, so an error;
 * - `gone` ends the prelude and keeps what was loaded (the D5 line is drawn
 *   above it).
 */
export function applyPreludePage(p: PreludeState, page: PreludePage, sentBefore: string | null, request: number): PreludeState {
  if (p.request !== request) return p
  if (page.state === 'none' && sentBefore !== null) {
    return { ...p, status: 'error', error: 'prelude: none on an older page', request: null }
  }
  if (page.state !== 'ok') return { ...p, status: page.state, cursor: null, done: true, error: null, request: null }
  if (page.prevCursor !== null && page.prevCursor === sentBefore) {
    return { ...p, status: 'error', error: 'prelude cursor did not advance', request: null }
  }
  const known = new Set(p.items.map((i) => i.pos))
  const fresh = page.items.filter((i) => !known.has(i.pos))
  return {
    status: 'ok',
    items: fresh.length > 0 ? [...fresh, ...p.items] : p.items,
    cursor: page.prevCursor,
    done: page.prevCursor === null,
    error: null,
    totalBytes: page.totalBytes ?? p.totalBytes,
    request: null,
    pages: p.pages + 1,
  }
}

export type PreludeEntry =
  | { pos: string; kind: 'message'; m: number }
  | { pos: string; kind: 'segment'; entrypoint: string }
  | { pos: string; kind: 'compaction'; trigger: string }
  | { pos: string; kind: 'note'; source: string; text: string; truncated: boolean; totalBytes: number | null; stream: string | null }

export interface PreludeView {
  /** In drawing order. A message entry points into `messages` by `m`. */
  entries: PreludeEntry[]
  messages: StreamMessage[]
  /** Index-aligned with `messages`: the stable name every key is built from. */
  ids: string[]
  tools: Record<string, ToolActivity>
}

/** The prelude's message names: `p<pos>` never collides with the live list's `${i}`. */
export const preludeId = (pos: string): string => `p${pos}`

export function derivePrelude(items: readonly PreludeItem[]): PreludeView {
  const entries: PreludeEntry[] = []
  const messages: StreamMessage[] = []
  const ids: string[] = []
  let tools: Record<string, ToolActivity> = {}
  for (const it of items) {
    switch (it.kind) {
      case 'assistant':
      case 'user':
        entries.push({ pos: it.pos, kind: 'message', m: messages.length })
        messages.push(it.msg)
        ids.push(preludeId(it.pos))
        break
      case 'tool_use':
        tools = recordN2ToolUseIn(tools, it.payload, it.at)
        break
      case 'tool_result':
        tools = recordN2ToolResultIn(tools, it.payload, it.at)
        break
      case 'prelude.segment':
        entries.push({ pos: it.pos, kind: 'segment', entrypoint: it.entrypoint })
        break
      case 'prelude.compaction':
        entries.push({ pos: it.pos, kind: 'compaction', trigger: it.trigger })
        break
      case 'prelude.note':
        entries.push({ pos: it.pos, kind: 'note', source: it.source, text: it.text, truncated: it.truncated, totalBytes: it.totalBytes, stream: it.stream })
        break
    }
  }
  // Pages load newest first, so everything newer than any loaded call is
  // loaded too: a call still 'running' has no answer anywhere — it was cut
  // off (the session exited mid-call). Never a live clock (spec §5.2).
  for (const id of Object.keys(tools)) {
    const t = tools[id]
    if (t.status === 'running') tools = { ...tools, [id]: { ...t, status: 'aborted' } }
  }
  return { entries, messages, ids, tools }
}
```

`event-reducer.ts`: `import { defaultPreludeState, type PreludeState } from './prelude'`. Add the field to `ExecutionState`:

```ts
  /** The transcript before turn 1 (worker prelude spec §5.2). Its own slice: never read by the rules above. */
  prelude: PreludeState
```

and `prelude: defaultPreludeState(),` to `defaultExecutionState()`.

`useExecutionStore.ts`: import `applyPreludePage as reducePreludePage, preludeFailed as failPrelude, preludeLoading as loadingPrelude` from `../lib/nex/prelude` and `type PreludePage` from `../lib/nex/prelude-wire`. Add these to the interface:

```ts
  /** Worker prelude (spec §5.2): request `request` is in flight — also the lock two panes share. */
  preludeLoading: (hostId: string, executionId: string, request: number) => void
  /** No-op unless `request` is the one in flight. */
  applyPreludePage: (hostId: string, executionId: string, page: PreludePage, sentBefore: string | null, request: number) => void
  /** No-op unless `request` is the one in flight. */
  preludeFailed: (hostId: string, executionId: string, message: string, request: number) => void
  /** Back to idle, which reloads from the first page (spec §5.2: a cursor rejected as foreign). */
  resetPrelude: (hostId: string, executionId: string) => void
```

and these to the implementation (`patch` already skips the write when the reducer hands back the same object):

```ts
    preludeLoading: (h, e, r) => patch(h, e, (c) => ({ ...c, prelude: loadingPrelude(c.prelude, r) })),
    applyPreludePage: (h, e, page, sentBefore, r) => patch(h, e, (c) => {
      const prelude = reducePreludePage(c.prelude, page, sentBefore, r)
      return prelude === c.prelude ? c : { ...c, prelude }
    }),
    preludeFailed: (h, e, message, r) => patch(h, e, (c) => {
      const prelude = failPrelude(c.prelude, message, r)
      return prelude === c.prelude ? c : { ...c, prelude }
    }),
    resetPrelude: (h, e) => patch(h, e, (c) => ({ ...c, prelude: defaultPreludeState() })),
```

(also import `defaultPreludeState` there.)

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/lib/nex/prelude.test.ts src/stores/useExecutionStore.test.ts src/lib/nex/tool-activity.test.ts src/lib/nex/event-reducer.test.ts src/lib/nex/n2-replay.test.ts && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS. The existing tool-activity, reducer and N2 replay suites are unchanged and green.

- [ ] **Step 5: Commit**

```bash
git commit --only src/lib/nex/prelude.ts src/lib/nex/prelude.test.ts src/lib/nex/tool-activity.ts src/lib/nex/event-reducer.ts src/stores/useExecutionStore.ts src/stores/useExecutionStore.test.ts -m "feat(spa): prelude state slice, page reducer and derived view"
```

### Task 4: `useExecutionPrelude` hook

**Files:**
- Create: `spa/src/hooks/useExecutionPrelude.ts`
- Create: `spa/src/hooks/useExecutionPrelude.test.ts`

**Interfaces:**
- Consumes: `fetchExecutionPrelude` (Task 2), `selectTranscriptPrelude` (Task 1), the store actions (Task 3).
- Produces: `useExecutionPrelude(hostId: string, executionId: string): { loadOlder: () => void; loadAll: () => Promise<void>; retry: () => void }`, `PRELUDE_PAGE_LIMIT = 200`, `MAX_LOAD_ALL_PAGES = 1000`.

- [ ] **Step 1: Write the failing tests** (mock `nex-api` the way `useExecutionSubscription.test.ts` does, at line 14)

```ts
// spa/src/hooks/useExecutionPrelude.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import type { PreludePage } from '../lib/nex/prelude-wire'
import { NexApiError } from '../lib/nex/types'

const fetchExecutionPrelude = vi.fn<(h: string, e: string, o?: { before?: string; limit?: number }) => Promise<PreludePage>>()
vi.mock('../lib/nex/nex-api', () => ({ fetchExecutionPrelude: (...a: Parameters<typeof fetchExecutionPrelude>) => fetchExecutionPrelude(...a) }))
import { useExecutionPrelude, PRELUDE_PAGE_LIMIT } from './useExecutionPrelude'

const CAP = { route: { method: 'GET', path: '/x' }, page_max_items: 500, page_max_bytes: 1, max_block_bytes: 1 }
const ok = (pos: string, prevCursor: string | null): PreludePage => ({
  state: 'ok', prevCursor, totalBytes: null,
  items: [{ pos, at: 1, kind: 'prelude.segment', entrypoint: 'cli' }],
})

/** A ready host with (or without) the capability, and an execution whose history is loaded. */
function seed({ cap = true, resume = 'sid' }: { cap?: boolean; resume?: string | null } = {}) {
  useNexHostStore.setState({ byHost: { h: { phase: 'ready', capabilities: { transcript_prelude: cap ? CAP : undefined } } } } as never)
  useExecutionStore.setState({ executions: {} })
  const s = useExecutionStore.getState()
  s.setSummary('h', 'e', { id: 'e', state: 'idle', resume_session_id: resume ?? undefined } as never)
  s.setHistoryLoaded('h', 'e', true)
}
const prelude = () => useExecutionStore.getState().executions[executionKey('h', 'e')].prelude

describe('useExecutionPrelude', () => {
  beforeEach(() => { fetchExecutionPrelude.mockReset() })

  it('loads the first page at once, with no before', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    expect(fetchExecutionPrelude).toHaveBeenCalledWith('h', 'e', { limit: PRELUDE_PAGE_LIMIT })
  })

  it('makes no request without the capability or without resume_session_id', async () => {
    seed({ cap: false })
    renderHook(() => useExecutionPrelude('h', 'e'))
    seed({ resume: null })
    renderHook(() => useExecutionPrelude('h', 'e'))
    await new Promise((r) => setTimeout(r, 0))
    expect(fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('two hooks, one request (the store status is the lock)', async () => {
    seed()
    let resolve!: (p: PreludePage) => void
    fetchExecutionPrelude.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    renderHook(() => useExecutionPrelude('h', 'e'))
    renderHook(() => useExecutionPrelude('h', 'e'))
    await act(async () => { resolve(ok('20', null)) })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1)
  })

  it('loadOlder sends the cursor; it is ignored while loading, done or in error', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1')) // stuck cursor → error
    await act(async () => { result.current.loadOlder() })
    expect(fetchExecutionPrelude).toHaveBeenLastCalledWith('h', 'e', { before: 'c1', limit: PRELUDE_PAGE_LIMIT })
    await waitFor(() => expect(prelude().status).toBe('error'))
    await act(async () => { result.current.loadOlder() })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(2)
  })

  it('retry re-asks after an error', async () => {
    seed()
    fetchExecutionPrelude.mockRejectedValueOnce(new Error('net'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'error', error: 'net' }))
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', null))
    await act(async () => { result.current.retry() })
    await waitFor(() => expect(prelude().status).toBe('ok'))
  })

  it('late page after clearExecution is dropped', async () => {
    seed()
    let resolve!: (p: PreludePage) => void
    fetchExecutionPrelude.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalled())
    act(() => useExecutionStore.getState().clearExecution('h', 'e'))
    await act(async () => { resolve(ok('20', null)) })
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBeUndefined()
  })

  it('clear → recreate → new request: the old answer is dropped, the new one lands (Review Focus 3)', async () => {
    seed()
    let resolveOld!: (p: PreludePage) => void
    let resolveNew!: (p: PreludePage) => void
    fetchExecutionPrelude
      .mockReturnValueOnce(new Promise((r) => { resolveOld = r }))
      .mockReturnValueOnce(new Promise((r) => { resolveNew = r }))
    const first = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1))
    first.unmount()
    act(() => useExecutionStore.getState().clearExecution('h', 'e'))
    seed()                                          // the entry comes back (undo / a new pane)
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(2))
    await act(async () => { resolveOld(ok('OLD', null)) })
    expect(prelude().status).toBe('loading')        // still waiting for its own request
    await act(async () => { resolveNew(ok('NEW', null)) })
    expect(prelude().items.map((i) => i.pos)).toEqual(['NEW'])
  })

  it('a 400 malformed_parameter on an older page restarts from the first page', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    fetchExecutionPrelude
      .mockRejectedValueOnce(new NexApiError(400, 'malformed_parameter', 'bad cursor'))
      .mockResolvedValueOnce(ok('20', null))
    await act(async () => { result.current.loadOlder() })
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'ok', done: true }))
    expect(fetchExecutionPrelude).toHaveBeenLastCalledWith('h', 'e', { limit: PRELUDE_PAGE_LIMIT })
  })

  it('a page with no items but a cursor is not the end (spec §4.3)', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce({ state: 'ok', items: [], prevCursor: 'c1', totalBytes: null })
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'ok', done: false, cursor: 'c1', pages: 1 }))
  })

  it('loadAll pages until done', async () => {
    seed()
    fetchExecutionPrelude
      .mockResolvedValueOnce(ok('30', 'c2'))
      .mockResolvedValueOnce(ok('20', 'c1'))
      .mockResolvedValueOnce(ok('10', null))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    await act(async () => { await result.current.loadAll() })
    expect(prelude()).toMatchObject({ done: true })
    expect(prelude().items.map((i) => i.pos)).toEqual(['10', '20', '30'])
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/hooks/useExecutionPrelude.test.ts`
Expected: FAIL (module missing).

- [ ] **Step 3: Implement**

```ts
// spa/src/hooks/useExecutionPrelude.ts — loads a handed-off worker's prelude
// (spec §5.2): the first page as soon as the execution's own history is in,
// older pages on demand. Kept out of useExecutionSubscription — the prelude
// is immutable and has no part in its summary → history → SSE order
// contract. The store's `prelude.status === 'loading'` is the one lock, so
// two panes on the same worker never ask twice.
import { useCallback, useEffect } from 'react'
import { fetchExecutionPrelude } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import { selectTranscriptPrelude, useNexHostStore } from '../stores/useNexHostStore'

export const PRELUDE_PAGE_LIMIT = 200
/** loadAll's safety stop: far past any real transcript (a page may also hold 0 items, spec §4.3). */
export const MAX_LOAD_ALL_PAGES = 1000

/** Request ids, unique for the module's lifetime: a late answer is told apart by its id, not by the entry's status. */
let nextRequest = 1

export function useExecutionPrelude(hostId: string, executionId: string): {
  loadOlder: () => void
  loadAll: () => Promise<void>
  retry: () => void
} {
  const key = executionKey(hostId, executionId)
  const cap = useNexHostStore(selectTranscriptPrelude(hostId))
  // Spec D4: no resume id, no prelude — and no request at all.
  const eligible = useExecutionStore((s) => {
    const st = s.executions[key]
    return !!st?.historyLoaded && !!st.summary?.resume_session_id
  })
  const status = useExecutionStore((s) => s.executions[key]?.prelude.status ?? 'idle')

  /** One page; false when nothing was asked (locked, done, ended) or the answer was dropped. */
  const fetchOne = useCallback(async (): Promise<boolean> => {
    const store = useExecutionStore.getState()
    const p = store.executions[key]?.prelude
    if (!p || p.status === 'loading' || p.status === 'none' || p.status === 'gone' || p.done) return false
    const before = p.cursor
    const request = nextRequest++
    store.preludeLoading(hostId, executionId, request)
    // Only this request's own answer lands: the store actions are no-ops
    // unless `request` is still the one recorded, so an entry that was
    // cleared and recreated (with a new request in flight) never takes it.
    const ours = () => useExecutionStore.getState().executions[key]?.prelude.request === request
    try {
      const page = await fetchExecutionPrelude(hostId, executionId, { ...(before !== null ? { before } : {}), limit: PRELUDE_PAGE_LIMIT })
      if (!ours()) return false
      useExecutionStore.getState().applyPreludePage(hostId, executionId, page, before, request)
      return true
    } catch (e) {
      if (!ours()) return false
      // Spec §4.2: the daemon rejected an older page's cursor (it was
      // upgraded, or the file changed): start over from the first page.
      if (before !== null && e instanceof NexApiError && e.code === 'malformed_parameter') {
        useExecutionStore.getState().resetPrelude(hostId, executionId)
        return false
      }
      useExecutionStore.getState().preludeFailed(hostId, executionId, e instanceof Error ? e.message : String(e), request)
      return false
    }
  }, [hostId, executionId, key])

  useEffect(() => {
    if (cap && eligible && status === 'idle') void fetchOne()
  }, [cap, eligible, status, fetchOne])

  const loadOlder = useCallback(() => {
    const p = useExecutionStore.getState().executions[key]?.prelude
    if (p?.status === 'ok' && !p.done) void fetchOne()
  }, [key, fetchOne])

  const retry = useCallback(() => {
    if (useExecutionStore.getState().executions[key]?.prelude.status === 'error') void fetchOne()
  }, [key, fetchOne])

  const loadAll = useCallback(async () => {
    for (let i = 0; i < MAX_LOAD_ALL_PAGES; i++) {
      const p = useExecutionStore.getState().executions[key]?.prelude
      if (!p || p.done || p.status === 'error' || p.status === 'none' || p.status === 'gone' || p.status === 'idle') return
      if (p.status === 'loading') {
        // Another caller (the sentinel) holds the lock: wait for it to settle.
        await new Promise<void>((resolve) => {
          const off = useExecutionStore.subscribe(
            (s) => s.executions[key]?.prelude.status,
            (st) => { if (st !== 'loading') { off(); resolve() } },
          )
        })
        continue
      }
      if (!(await fetchOne())) return
    }
  }, [key, fetchOne])

  return { loadOlder, loadAll, retry }
}
```

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/hooks/useExecutionPrelude.test.ts && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only src/hooks/useExecutionPrelude.ts src/hooks/useExecutionPrelude.test.ts -m "feat(spa): useExecutionPrelude — first page on open, older pages on demand"
```

**PR-1 gate:** full `npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`, `pnpm run build`; then a PR "worker prelude P1: data layer" with review R1 → attacker → critic (project CLAUDE.md).

---

## Phase P2 — room rendering + scroll (PR-2)

### Task 5: Stable message ids in keys

Keys are built from a message's position. The prelude grows at the front, so its keys must come from a stable id. Add an optional `idOf` everywhere a key is built from `(i, j)`. With `idOf` absent, every key is exactly what it is today.

**Files:**
- Create: `spa/src/lib/nex/message-keys.ts`
- Modify: `spa/src/lib/nex/operations.ts:14` (`blockKey` accepts a string id) and `:105-155` (`indexOperations(messages, idOf?)`)
- Modify: `spa/src/lib/nex/operation-status.ts:65-100` (`classifyTurnOperations(..., idOf?)`)
- Modify: `spa/src/components/room/render-message.tsx` (`RenderCtx.idOf`, `renderMessage` uses `rowKey`)
- Modify: `spa/src/components/room/MessageRow.tsx` (every `blockKey(i, j)` → `keyAt(ctx, i, j)`; `${ctx.keyPrefix}-${ci}` → `rowKey(ctx, ci)`)
- Modify: `spa/src/components/chat/ChatTranscript.tsx` (`ChatMessage` gets `idOf?`; `blockKey(i, j)` → `keyAt({ idOf }, i, j)`; row keys → `rowKey`)
- Test: `spa/src/lib/nex/operations.test.ts`, `spa/src/components/room/RoomTranscript.test.tsx` (append)

**Interfaces:**
- Produces:
  - `MessageIdOf = (m: number) => string`
  - `keyAt(ctx: { idOf?: MessageIdOf }, i: number, j: number): BlockKey`
  - `rowKey(ctx: { idOf?: MessageIdOf; keyPrefix: string }, i: number): string`
  - `indexOperations(messages, idOf?)`, `classifyTurnOperations(messages, turn, index, tools, idOf?)`, `RenderCtx.idOf?`

- [ ] **Step 1: Write the failing tests**

```ts
// operations.test.ts (append)
it('names keys by idOf when given, by position otherwise', () => {
  const msgs = [
    { type: 'assistant', message: { role: 'assistant', content: [{ type: 'tool_use', id: 't1', name: 'Bash', input: {} }], stop_reason: null } },
    { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: 'ok' }], stop_reason: null } },
  ] as StreamMessage[]
  expect([...indexOperations(msgs).resultForCall.keys()]).toEqual(['0:0'])
  const byId = indexOperations(msgs, (m) => `p${m * 10}`)
  expect([...byId.resultForCall.keys()]).toEqual(['p0:0'])
  expect([...byId.consumedResults]).toEqual(['p10:0'])
})
```

```tsx
// RoomTranscript.test.tsx (append) — MessageRow under an idOf names its anchors by it
it('a render context with idOf names search anchors and fold keys by the id', () => {
  const msgs = [asst([{ type: 'text', text: 'hello' }])]
  const index = indexOperations(msgs, () => 'p42')
  const { container } = render(
    <FoldContext.Provider value={createFoldStore()}>
      {renderMessage(msgs[0], 0, { messages: msgs, index, keyPrefix: 'k', depth: 0, idOf: () => 'p42' })}
    </FoldContext.Provider>,
  )
  expect(container.querySelector('[data-search-unit="p42:0:text"]')).not.toBeNull()
})
```

(`asst` is the helper already in that file. The fold store is constructed as `fold-context.test.tsx` constructs one. If no exported factory exists, mount inside `RoomTranscript`'s provider by rendering `<RoomTranscript …/>`, and assert on a fixture with no prelude instead.)

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/lib/nex/operations.test.ts src/components/room/RoomTranscript.test.tsx`
Expected: FAIL (`indexOperations` ignores its second argument; `idOf` is not on `RenderCtx`).

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/nex/message-keys.ts — how a transcript names its messages in
// keys (worker prelude spec §5.3). The live list names them by position —
// it only grows at the end, so a position never changes. The prelude grows
// at the front, so it names them by a stable id (`p<pos>`). Every key a
// block owns — React row, fold, search anchor, operation pairing — goes
// through here.
import { blockKey, type BlockKey } from './operations'

export type MessageIdOf = (m: number) => string

export function keyAt(ctx: { idOf?: MessageIdOf }, i: number, j: number): BlockKey {
  return blockKey(ctx.idOf ? ctx.idOf(i) : i, j)
}

export function rowKey(ctx: { idOf?: MessageIdOf; keyPrefix: string }, i: number): string {
  return `${ctx.keyPrefix}-${ctx.idOf ? ctx.idOf(i) : i}`
}
```

In `operations.ts`: `export const blockKey = (m: number | string, b: number): BlockKey => \`${m}:${b}\``. Change `indexOperations(messages: StreamMessage[], idOf?: (m: number) => string)`, add `const key = (mi: number, bi: number) => blockKey(idOf ? idOf(mi) : mi, bi)` at its top, and replace the four `blockKey(mi, bi)` calls inside it (current lines 132, 133, 145, 150) with `key(mi, bi)`. `childrenByParent` values and `childIndexes` stay positional indexes, unchanged.

In `operation-status.ts`: add the trailing parameter `idOf?: (m: number) => string`, and at line 79 write `const key = blockKey(idOf ? idOf(mi) : mi, bi)`.

In `render-message.tsx`: add the field to `RenderCtx`:

```ts
  /**
   * How this list names its messages in keys (`lib/nex/message-keys`). Absent
   * = by position (the live list). The prelude passes its stable ids.
   */
  idOf?: MessageIdOf
```

and change `renderMessage` to `return <MessageRow key={rowKey(ctx, i)} msg={msg} i={i} ctx={ctx} />`.

In `MessageRow.tsx`: replace every `blockKey(i, j)` (lines 79, 93, 111, 112, 114, 128, 141, 142, 164, 165, 168, 208) with `keyAt(ctx, i, j)`, and `key={\`${ctx.keyPrefix}-${ci}\`}` (line 100) with `key={rowKey(ctx, ci)}`. Drop the now-unused `blockKey` import.

In `ChatTranscript.tsx`:
- Give `ChatMessage` a prop `idOf?: MessageIdOf`, and use `searchUnitId(keyAt({ idOf }, i, j), 'text')` at lines 103 and 141.
- The top-level loop keeps calling it without `idOf`, so `lineAt={(j) => lines.get(keyAt(ctx, i, j))}` and `key={rowKey(ctx, i)}` (ctx has no idOf there) produce today's keys.

- [ ] **Step 4: Run to verify pass, including every existing transcript suite**

Run: `npx vitest run src/lib/nex src/components/room src/components/chat && npx tsc --noEmit -p tsconfig.app.json`
Expected: PASS. All existing room / chat / search tests are unchanged.

- [ ] **Step 5: Commit**

```bash
git commit --only src/lib/nex/message-keys.ts src/lib/nex/operations.ts src/lib/nex/operations.test.ts src/lib/nex/operation-status.ts src/components/room/render-message.tsx src/components/room/MessageRow.tsx src/components/chat/ChatTranscript.tsx src/components/room/RoomTranscript.test.tsx -m "refactor(spa): name transcript keys through idOf (positional by default)"
```

### Task 6: Prelude room section, markers, notes, image / truncation placeholders

**Files:**
- Create: `spa/src/components/room/prelude/PreludeSection.tsx`
- Create: `spa/src/components/room/prelude/PreludeMarker.tsx`
- Create: `spa/src/components/room/prelude/PreludeNote.tsx`
- Create: `spa/src/components/room/prelude/PreludeSentinel.tsx`
- Create: `spa/src/components/room/prelude/Placeholders.tsx`
- Create: `spa/src/components/room/prelude/PreludeSection.test.tsx`
- Modify: `spa/src/components/room/MessageRow.tsx` (image placeholder + truncation hint)
- Modify: `spa/src/lib/nex/format.ts` (add `formatBytes`) and `spa/src/lib/nex/format.test.ts`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `PreludeView`, `PreludeState['status']`, `preludeId` (Task 3); `keyAt`/`rowKey`/`RenderCtx.idOf` (Task 5).
- Produces: `<PreludeSection view status done error keyPrefix now mode pages onLoadOlder onRetry />`, plus `OmittedMedia`, `isOmittedMedia`, `TruncatedHint`, `blockShownBytes`, where `mode: 'room' | 'chat'` (chat lands in Task 8; until then `'chat'` renders the room form). Also `formatBytes(n: number): string`.

- [ ] **Step 1: Write the failing tests**

```tsx
// spa/src/components/room/prelude/PreludeSection.test.tsx
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import PreludeSection from './PreludeSection'
import { derivePrelude } from '../../../lib/nex/prelude'
import { sanitizePreludePage, type PreludeItem } from '../../../lib/nex/prelude-wire'
import type { StreamMessage } from '../../../lib/nex/message-types'

const m = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ pos, at: 1, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })

let observed: Array<(entries: Array<{ isIntersecting: boolean }>) => void> = []
let disconnects = 0
beforeEach(() => {
  observed = []
  disconnects = 0
  vi.stubGlobal('IntersectionObserver', class {
    constructor(cb: (e: Array<{ isIntersecting: boolean }>) => void) { observed.push(cb) }
    observe() {}
    disconnect() { disconnects++ }
  })
})
afterEach(() => vi.unstubAllGlobals())

const base = { keyPrefix: 'exc', mode: 'room' as const, onLoadOlder: vi.fn(), onRetry: vi.fn(), error: null, pages: 1 }

describe('PreludeSection', () => {
  it('draws markers, user lines, prose and notes in order', () => {
    const view = derivePrelude([
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      m('2', 'user', [{ type: 'text', text: 'fix the build' }]),
      m('3', 'assistant', [{ type: 'text', text: 'on it' }]),
      { pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'Model set to opus', truncated: false, totalBytes: null, stream: null },
      { pos: '5', at: 0, kind: 'prelude.compaction', trigger: 'auto' },
      { pos: '6', at: 0, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const text = screen.getByTestId('worker-prelude').textContent ?? ''
    expect(text.indexOf('In the terminal')).toBeLessThan(text.indexOf('fix the build'))
    expect(text.indexOf('fix the build')).toBeLessThan(text.indexOf('on it'))
    expect(text).toContain('Model set to opus')
    expect(text).toContain('Conversation compacted here (auto)')
    expect(text).toContain('Headless (worker)')
    expect(document.querySelector('[data-search-unit="p2:0:text"]')).not.toBeNull()
  })

  it('asks for older pages when the sentinel shows, only while ok and not done', () => {
    const onLoadOlder = vi.fn()
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} />)
    observed.at(-1)!([{ isIntersecting: true }])
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    rerender(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done />)
    expect(screen.queryByTestId('prelude-sentinel')).toBeNull()
    expect(disconnects).toBe(1)
  })

  it('an error unmounts the sentinel and disconnects it (Review Focus 2)', () => {
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} view={view} status="ok" done={false} />)
    rerender(<PreludeSection {...base} view={view} status="error" error="stuck" done={false} />)
    expect(screen.queryByTestId('prelude-sentinel')).toBeNull()
    expect(disconnects).toBe(1)
  })

  it('re-arms after every page — even one with no items — so a short prelude keeps loading', () => {
    const onLoadOlder = vi.fn()
    const view = derivePrelude([m('2', 'user', [{ type: 'text', text: 'x' }])])
    const { rerender } = render(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} pages={1} />)
    expect(observed).toHaveLength(1)
    // A page that brought no items still counts: same view, pages 2.
    rerender(<PreludeSection {...base} onLoadOlder={onLoadOlder} view={view} status="ok" done={false} pages={2} />)
    expect(observed).toHaveLength(2)
    observed[1]([{ isIntersecting: true }])        // the fresh observer's first report: still in view
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
  })

  it('shows loading, error with retry, and gone', () => {
    const onRetry = vi.fn()
    const view = derivePrelude([])
    const { rerender } = render(<PreludeSection {...base} view={view} status="loading" done={false} />)
    expect(screen.getByTestId('prelude-loading')).toBeTruthy()
    rerender(<PreludeSection {...base} onRetry={onRetry} view={view} status="error" error="net" done={false} />)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
    rerender(<PreludeSection {...base} view={view} status="gone" done />)
    expect(screen.getByTestId('prelude-gone')).toBeTruthy()
  })

  it('renders nothing for idle / none with no items', () => {
    const { container, rerender } = render(<PreludeSection {...base} view={derivePrelude([])} status="idle" done={false} />)
    expect(container.firstChild).toBeNull()
    rerender(<PreludeSection {...base} view={derivePrelude([])} status="none" done />)
    expect(container.firstChild).toBeNull()
  })

  it('draws omitted images / documents as placeholders in user and assistant content', () => {
    const view = derivePrelude([
      m('2', 'user', [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 122880 } }]),
      m('3', 'assistant', [{ type: 'document', source: { type: 'omitted', media_type: 'application/pdf', bytes: 5 * 1024 * 1024 } }]),
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(screen.getByText('[image · png · 120 KB]')).toBeTruthy()
    expect(screen.getByText('[document · pdf · 5.0 MB]')).toBeTruthy()
  })

  it('every cut block and every cut note says so', () => {
    const view = derivePrelude([
      m('2', 'assistant', [
        { type: 'text', text: 'long…', truncated: true, total_bytes: 200000 },
        { type: 'thinking', thinking: 'mulling', truncated: true, total_bytes: 100000 },
        { type: 'tool_use', id: 't', name: 'Write', input: { content: 'x' }, truncated: true, total_bytes: 90000 },
      ]),
      m('3', 'user', [{ type: 'tool_result', tool_use_id: 't', content: 'out', truncated: true, total_bytes: 80000 }]),
      { pos: '4', at: 0, kind: 'prelude.note', source: 'command_output', text: 'big', truncated: true, totalBytes: 70000, stream: null },
    ])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    const hints = screen.getAllByTestId('prelude-truncated').map((h) => h.textContent)
    expect(hints).toHaveLength(5)
    expect(hints[0]).toContain('195 KB')
    expect(hints[4]).toContain('68 KB')
  })

  it('a bash stderr note is drawn in the error tone', () => {
    const view = derivePrelude([{ pos: '4', at: 0, kind: 'prelude.note', source: 'bash_output', text: 'boom', truncated: false, totalBytes: null, stream: 'stderr' }])
    render(<PreludeSection {...base} view={view} status="ok" done />)
    expect(screen.getByTestId('prelude-note-bash_output').innerHTML).toContain('text-status-error')
  })

  it('hostile blocks that passed the sanitiser still render (Review Focus 5)', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '9', kind: 'assistant', at: 1, payload: { type: 'assistant', message: { role: 'assistant', content: [
        { type: 'tool_use', id: '__proto__', name: 'Bash', input: 'x' },
        { type: 'text', text: 42 },
        { type: 'mystery', blob: [1, 2] },
      ] } } }, { pos: '10', kind: 'user', at: 1, payload: { type: 'user', message: { role: 'user', content: [
        { type: 'tool_result', tool_use_id: '__proto__', content: { not: 'an array' } },
        { type: 'image', source: { type: 'omitted' } },
      ] } } }],
    })!
    expect(() => render(<PreludeSection {...base} view={derivePrelude(page.items)} status="ok" done />)).not.toThrow()
  })
})
```

Tests run in English. The test setup's default locale is `en` (as in `RoomTranscript.test.tsx`).

```ts
// format.test.ts (append)
it('formatBytes', () => {
  expect(formatBytes(512)).toBe('512 B')
  expect(formatBytes(122880)).toBe('120 KB')
  expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/components/room/prelude src/lib/nex/format.test.ts`
Expected: FAIL (modules missing).

- [ ] **Step 3: Implement**

Locale keys. Add both files, with the same keys:

| key | en | zh-TW |
|---|---|---|
| `worker.prelude.loading` | Loading earlier conversation… | 載入更早的內容… |
| `worker.prelude.error` | Couldn't load the earlier conversation: {{message}} | 更早的內容載入失敗：{{message}} |
| `worker.prelude.retry` | Retry | 重試 |
| `worker.prelude.gone` | The earlier conversation can't be read (its transcript was removed, or is not reachable from this daemon) | 更早的內容無法讀取（transcript 已被清除，或這台 daemon 讀不到） |
| `worker.prelude.segment_cli` | In the terminal | 在終端機 |
| `worker.prelude.segment_headless` | Headless (worker) | Headless（worker） |
| `worker.prelude.compaction_auto` | Conversation compacted here (auto) | 對話在此壓縮（自動） |
| `worker.prelude.compaction_manual` | Conversation compacted here (manual) | 對話在此壓縮（手動） |
| `worker.prelude.compaction` | Conversation compacted here | 對話在此壓縮 |
| `worker.prelude.note_peer` | Peer message | Peer 訊息 |
| `worker.prelude.note_task` | Background task | 背景工作 |
| `worker.prelude.image` | [image · {{type}} · {{size}}] | [圖片 · {{type}} · {{size}}] |
| `worker.prelude.document` | [document · {{type}} · {{size}}] | [文件 · {{type}} · {{size}}] |
| `worker.prelude.truncated` | Too long — showing the first {{shown}} of {{total}} | 內容過長，只顯示前 {{shown}}（共 {{total}}） |
| `worker.prelude.search_incomplete` | Earlier conversation not fully loaded | 更早的內容尚未全部載入 |
| `worker.prelude.load_all` | Load all | 全部載入 |

`format.ts`:

```ts
/** A byte count for a label: B under 1 KiB, whole KB under 1 MiB, one decimal MB above. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}
```

`PreludeMarker.tsx`:

```tsx
// spa/src/components/room/prelude/PreludeMarker.tsx — a thin labelled rule
// in the prelude (spec D2): where the transcript switched between the
// terminal and headless turns, or was compacted.
export default function PreludeMarker({ label, testId }: { label: string; testId: string }) {
  return (
    <div data-testid={testId} role="separator" aria-label={label}
      className="flex items-center gap-2 text-[11px] text-text-muted select-none">
      <span className="flex-1 border-t border-border-subtle" />
      <span>{label}</span>
      <span className="flex-1 border-t border-border-subtle" />
    </div>
  )
}
```

`PreludeNote.tsx` (the output notes fold like tool output, using the same plan and key shape):

```tsx
// spa/src/components/room/prelude/PreludeNote.tsx — a non-message record of
// the prelude (spec §4.3 `prelude.note`): a slash command's or `!` command's
// output, the `!` input itself, a background-task notice, a peer message.
import { TerminalWindow } from '@phosphor-icons/react'
import { useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { foldPlan, utf8Length } from '../../../lib/nex/fold'
import { searchUnitId } from '../../../lib/nex/transcript-search'
import { FoldedOutput } from '../FoldedOutput'
import { useFold } from '../fold-context'
import { TruncatedHint } from './Placeholders'

export interface PreludeNoteProps {
  /** `p<pos>` — fold key `${id}:note`, search anchor `${id}:note:text`. */
  id: string
  source: string
  text: string
  truncated: boolean
  totalBytes: number | null
  /** `bash_output` only: 'stdout' | 'stderr' (spec §4.3). */
  stream: string | null
}

export default function PreludeNote({ id, source, text, truncated, totalBytes, stream }: PreludeNoteProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${id}:note`)
  const plan = useMemo(() => foldPlan({ text }), [text])
  const anchor = searchUnitId(`${id}:note`, 'text')
  const hint: ReactNode = truncated ? <TruncatedHint shown={utf8Length(text)} total={totalBytes ?? 0} /> : null
  if (source === 'bash_input') {
    return (
      <div data-testid="prelude-bash-input">
        <div className="flex items-center gap-1.5 text-[13px] text-status-warning font-mono">
          <TerminalWindow size={14} weight="bold" />
          <span data-search-unit={anchor}>! {text}</span>
        </div>
        {hint}
      </div>
    )
  }
  if (source === 'task_notification') {
    return (
      <div data-testid="prelude-task" className="text-xs text-text-muted">
        <span>{t('worker.prelude.note_task')}: </span><span data-search-unit={anchor}>{text}</span>
        {hint}
      </div>
    )
  }
  const label = source === 'peer_message' ? t('worker.prelude.note_peer') : null
  return (
    <div data-testid={`prelude-note-${source}`} className="space-y-1">
      {label && <div className="text-xs text-text-muted">{label}</div>}
      <FoldedOutput text={text} plan={plan} expanded={expanded} onToggle={toggle} searchUnit={anchor}
        tone={stream === 'stderr' ? 'error' : 'normal'} />
      {hint}
    </div>
  )
}
```

(If `FoldedOutput` is a default export, import it that way. Check `components/room/FoldedOutput.tsx`'s export.)

`PreludeSentinel.tsx`:

```tsx
// spa/src/components/room/prelude/PreludeSentinel.tsx — the prelude's top
// edge (spec §5.4). Seen → ask for the next older page. `generation` re-arms
// the observer after each page: an IntersectionObserver reports only
// changes, and a page too short to push the sentinel out of view must still
// ask again (the viewport keeps filling until done).
import { useEffect, useRef } from 'react'

export default function PreludeSentinel({ onVisible, generation }: { onVisible: () => void; generation: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const latest = useRef(onVisible)
  useEffect(() => { latest.current = onVisible }, [onVisible])
  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) latest.current()
    })
    io.observe(el)
    return () => io.disconnect()
  }, [generation])
  return <div ref={ref} data-testid="prelude-sentinel" aria-hidden className="h-px" />
}
```

`PreludeSection.tsx`:

```tsx
// spa/src/components/room/prelude/PreludeSection.tsx — the conversation
// before this worker's first turn (worker prelude spec §5.3), drawn above
// turn 1 with the room's own renderer. Its messages are named by stable ids
// (`p<pos>`), so loading an older page never re-keys what is on screen. It
// is not a RoomTurnGroup: no data-turn-index (the scroll memory's first
// turn stays the worker's), no hover strip.
import { useCallback, useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { indexOperations } from '../../../lib/nex/operations'
import type { PreludeState, PreludeView } from '../../../lib/nex/prelude'
import { preludeId } from '../../../lib/nex/prelude'
import { renderMessage, type RenderCtx } from '../render-message'
import PreludeMarker from './PreludeMarker'
import PreludeNote from './PreludeNote'
import PreludeSentinel from './PreludeSentinel'

export interface PreludeSectionProps {
  view: PreludeView
  status: PreludeState['status']
  done: boolean
  error: string | null
  /** The transcript's keyPrefix; the section keys its rows under `${keyPrefix}-prelude`. */
  keyPrefix: string
  now?: number
  mode: 'room' | 'chat'
  /** PreludeState.pages — re-arms the sentinel after every page, an empty one included. */
  pages: number
  onLoadOlder: () => void
  onRetry: () => void
}

export default function PreludeSection({ view, status, done, error, keyPrefix, now, pages, onLoadOlder, onRetry }: PreludeSectionProps) {
  const t = useI18nStore((s) => s.t)
  const idOf = useCallback((i: number) => view.ids[i], [view.ids])
  const index = useMemo(() => indexOperations(view.messages, idOf), [view.messages, idOf])
  if (view.entries.length === 0 && (status === 'idle' || status === 'none')) return null
  const ctx: RenderCtx = { messages: view.messages, index, tools: view.tools, now, keyPrefix: `${keyPrefix}-prelude`, depth: 0, idOf }

  const top: ReactNode =
    status === 'loading' ? <div data-testid="prelude-loading" className="text-xs text-text-muted text-center">{t('worker.prelude.loading')}</div>
    : status === 'error' ? (
      <div data-testid="prelude-error" className="flex items-center justify-center gap-2 text-xs text-status-error">
        <span>{t('worker.prelude.error', { message: error ?? '' })}</span>
        <button type="button" onClick={onRetry} className="underline hover:text-text-primary">{t('worker.prelude.retry')}</button>
      </div>
    )
    : status === 'gone' ? <div data-testid="prelude-gone" className="text-xs text-text-muted text-center">{t('worker.prelude.gone')}</div>
    : null

  return (
    <section data-testid="worker-prelude" className="space-y-4">
      {status === 'ok' && !done && <PreludeSentinel onVisible={onLoadOlder} generation={pages} />}
      {top}
      {view.entries.map((e) => {
        if (e.kind === 'message') return index.childIndexes.has(e.m) ? null : renderMessage(view.messages[e.m], e.m, ctx)
        const id = preludeId(e.pos)
        if (e.kind === 'segment') {
          const label = e.entrypoint === 'cli' ? t('worker.prelude.segment_cli')
            : e.entrypoint.startsWith('sdk') ? t('worker.prelude.segment_headless')
            : e.entrypoint
          return <PreludeMarker key={id} testId="prelude-segment" label={label} />
        }
        if (e.kind === 'compaction') {
          const label = e.trigger === 'auto' ? t('worker.prelude.compaction_auto')
            : e.trigger === 'manual' ? t('worker.prelude.compaction_manual')
            : t('worker.prelude.compaction')
          return <PreludeMarker key={id} testId="prelude-compaction" label={label} />
        }
        return <PreludeNote key={id} id={id} source={e.source} text={e.text} truncated={e.truncated} totalBytes={e.totalBytes} stream={e.stream} />
      })}
    </section>
  )
}
```

`Placeholders.tsx` (shared by the room and, in Task 8, chat):

```tsx
// spa/src/components/room/prelude/Placeholders.tsx — what the prelude draws
// for content the daemon left out or cut (spec §4.3, D6): an image or
// document as its kind and size (never data), and a one-line hint after
// any block or note that was cut at max_block_bytes.
import type { ContentBlock } from '../../../lib/nex/message-types'
import { utf8Length } from '../../../lib/nex/fold'
import { formatBytes } from '../../../lib/nex/format'
import { toolResultText } from '../../../lib/nex/operations'
import { useI18nStore } from '../../../stores/useI18nStore'

/** An image / document block whose data the daemon omitted. */
export function isOmittedMedia(block: ContentBlock): boolean {
  return (block.type === 'image' || block.type === 'document') && block.source?.type === 'omitted'
}

export function OmittedMedia({ block }: { block: ContentBlock }) {
  const t = useI18nStore((s) => s.t)
  const type = (block.source?.media_type ?? '').replace(/^[a-z]+\//, '') || '?'
  const size = formatBytes(block.source?.bytes ?? 0)
  return (
    <div data-testid="prelude-media" className="text-xs text-text-muted font-mono">
      {t(block.type === 'document' ? 'worker.prelude.document' : 'worker.prelude.image', { type, size })}
    </div>
  )
}

/** How many bytes of a cut block are shown — what the daemon kept. */
export function blockShownBytes(block: ContentBlock): number {
  switch (block.type) {
    case 'thinking': return utf8Length(block.thinking ?? '')
    case 'tool_use': return utf8Length(JSON.stringify(block.input ?? {}))
    case 'tool_result': return utf8Length(toolResultText(block.content))
    default: return utf8Length(block.text ?? '')
  }
}

export function TruncatedHint({ shown, total }: { shown: number; total: number }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="prelude-truncated" className="text-xs text-text-muted">
      {t('worker.prelude.truncated', { shown: formatBytes(shown), total: formatBytes(total) })}
    </div>
  )
}
```

`MessageRow.tsx`: move the bodies of the two `content.map` callbacks into local per-block functions, `assistantBlock(block, j)` and `userBlock(block, j)`, keeping today's bodies exactly. Then route both maps through one decorator, so **every** block kind gets the same treatment:

```tsx
  /** Spec §5.3: omitted media becomes its placeholder; a cut block keeps its own drawing plus one hint line. */
  const decorate = (block: ContentBlock, j: number, el: ReactNode): ReactNode => {
    if (isOmittedMedia(block)) return <OmittedMedia key={j} block={block} />
    if (!block.truncated) return el
    return <Fragment key={j}>{el}<TruncatedHint shown={blockShownBytes(block)} total={block.total_bytes ?? 0} /></Fragment>
  }
  // assistant: {am.message.content.map((block, j) => decorate(block, j, assistantBlock(block, j)))}
  // user:      {um.message.content.map((block, j) => decorate(block, j, userBlock(block, j)))}
```

The live list never carries `truncated` or omitted media, because Nexen sets them only on prelude items, so the live transcript renders exactly as before. A consumed `tool_result` draws `null`, because its call shows the output. Its hint still lands on the result's row, right after the call's block, which is where the reader looks.

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/components/room src/lib/nex/format.test.ts && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only src/components/room/prelude src/components/room/MessageRow.tsx src/lib/nex/format.ts src/lib/nex/format.test.ts src/locales/en.json src/locales/zh-TW.json -m "feat(spa): prelude section — markers, notes, image and truncation placeholders"
```

### Task 7: Keep the reader's place on prepend; wire the prelude into the room

The prelude section's height is snapshotted right before the commit that changes it, and `scrollTop` moves by exactly its growth (spec §5.4). Measuring the section, not the whole box, keeps a live message or typewriter frame landing in the **same** commit out of the correction (codex plan review #6).

**Files:**
- Create: `spa/src/components/room/prelude/PreludeAnchor.tsx`
- Modify: `spa/src/hooks/useTranscriptScroll.ts` (add `shiftBy`)
- Modify: `spa/src/components/room/RoomTranscript.tsx` (`prelude` / `preludeVersion` props, `PreludeAnchor`, `[overflow-anchor:none]`)
- Modify: `spa/src/components/chat/ChatTranscript.tsx` (the same, rendered before the turns; Task 8 swaps in the chat form)
- Modify: `spa/src/components/execution/ExecutionView.tsx` (call the hook, derive the view, pass the props)
- Test: `spa/src/components/room/transcript-scroll.test.tsx` (append, `describe.each(views)`)
- Test: `spa/src/components/execution/ExecutionView.test.tsx` (append one wiring test)

**Interfaces:**
- Consumes: `PreludeSection` (Task 6), `useExecutionPrelude` (Task 4), `derivePrelude` (Task 3).
- Produces: `TranscriptScroll.shiftBy(delta: number): void`; `RoomTranscriptProps.prelude?: ReactNode` and `RoomTranscriptProps.preludeVersion?: string` (also on chat through the shared props type); `<PreludeAnchor version onGrow>`.

- [ ] **Step 1: Write the failing tests**

```tsx
// transcript-scroll.test.tsx (append)
/** jsdom has no layout: the prelude wrapper is 100px per [data-row] child, everything else 0. */
let offsetHeightDesc: PropertyDescriptor | undefined
beforeEach(() => {
  offsetHeightDesc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) { return this.dataset?.testid === 'prelude-anchor' ? this.querySelectorAll('[data-row]').length * 100 : 0 },
  })
})
afterEach(() => { if (offsetHeightDesc) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', offsetHeightDesc) })

const rows = (n: number) => <div>{Array.from({ length: n }, (_, i) => <div key={i} data-row />)}</div>

describe.each(views)('%s: prepending the prelude keeps the reader in place (Review Focus 4)', (_name, View) => {
  const props = { messages: [said('a')], keyPrefix: 'k', showThinking: false, showEmptyHint: false } as RoomTranscriptProps

  it('mid-transcript: what is on screen does not move', () => {
    const { container, rerender } = render(<View {...props} prelude={rows(1)} preludeVersion="1:ok" />)
    const box = container.firstChild as HTMLElement
    geometry(box, 1000, 400, 300)
    fireEvent.scroll(box)
    rerender(<View {...props} prelude={rows(7)} preludeVersion="2:ok" />)   // 600px more above
    expect(box.scrollTop).toBe(900)
  })

  it('at the bottom: the distance to the end is unchanged', () => {
    const { container, rerender } = render(<View {...props} prelude={rows(1)} preludeVersion="1:ok" />)
    const box = container.firstChild as HTMLElement
    geometry(box, 1000, 400, 600)
    fireEvent.scroll(box)
    rerender(<View {...props} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(1200)
  })

  it('a live message in the same commit is not counted as growth above', () => {
    const { container, rerender } = render(<View {...props} prelude={rows(1)} preludeVersion="1:ok" />)
    const box = container.firstChild as HTMLElement
    geometry(box, 1000, 400, 300)
    fireEvent.scroll(box)
    rerender(<View {...props} messages={[said('a'), said('b')]} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(900)
  })

  it('no version change, no correction', () => {
    const { container, rerender } = render(<View {...props} prelude={rows(1)} preludeVersion="1:ok" />)
    const box = container.firstChild as HTMLElement
    geometry(box, 1000, 400, 300)
    fireEvent.scroll(box)
    rerender(<View {...props} prelude={rows(3)} preludeVersion="1:ok" />)
    expect(box.scrollTop).toBe(300)
  })

  it('the box opts out of the browser’s own scroll anchoring', () => {
    const { container } = render(<View messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect((container.firstChild as HTMLElement).className).toContain('[overflow-anchor:none]')
  })
})
```

These cases rely on the file's existing `scrollTo` stub. jsdom has no `Element.prototype.scrollTo`, and `follow()` returns early without it, which would leave the first placement undone and `shiftBy` a no-op. Put them inside the same `beforeEach` scope that the file's other `describe.each(views)` blocks use.

`ExecutionView.test.tsx`: follow the file's existing setup that seeds a loaded execution. Give the summary `resume_session_id: 'sid'` and the host capabilities `transcript_prelude`. In the existing `nex-api` mock, make `fetchExecutionPrelude` return one `ok` page holding `{ pos: '2', kind: 'user', msg: said('earlier') }`. Then assert that `await screen.findByText('earlier')` comes **before** the brief line in document order (`compareDocumentPosition`).

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/components/room/transcript-scroll.test.tsx src/components/execution/ExecutionView.test.tsx`
Expected: FAIL (props ignored; no class).

- [ ] **Step 3: Implement**

`PreludeAnchor.tsx`:

```tsx
// spa/src/components/room/prelude/PreludeAnchor.tsx — keeps the reader's
// place when the prelude grows above them (spec §5.4). React's documented
// prepend pattern: getSnapshotBeforeUpdate reads the section's height right
// before the commit that changes it; componentDidUpdate hands the growth to
// the transcript's scroll. Measuring this wrapper, not the whole box, keeps
// a live message landing in the same commit out of the correction.
import { Component, createRef, type ReactNode } from 'react'

interface PreludeAnchorProps {
  /** Changes whenever the section may change height: pages applied + status (a loading row comes and goes). */
  version: string
  onGrow: (delta: number) => void
  children: ReactNode
}

export default class PreludeAnchor extends Component<PreludeAnchorProps> {
  private el = createRef<HTMLDivElement>()

  getSnapshotBeforeUpdate(prev: PreludeAnchorProps): number | null {
    return prev.version !== this.props.version ? (this.el.current?.offsetHeight ?? 0) : null
  }

  componentDidUpdate(_prev: PreludeAnchorProps, _state: unknown, before: number | null) {
    if (before === null) return
    const grown = (this.el.current?.offsetHeight ?? 0) - before
    if (grown !== 0) this.props.onGrow(grown)
  }

  render() {
    return <div ref={this.el} data-testid="prelude-anchor">{this.props.children}</div>
  }
}
```

`useTranscriptScroll.ts`: add to `TranscriptScroll` and to the returned `useMemo`:

```ts
  /**
   * Content above the reader changed height by `delta` (the prelude, spec
   * §5.4): move scrollTop by exactly that, so what is on screen stays put —
   * and a reader at the bottom stays at the bottom, the distance to the end
   * being unchanged (the at-bottom flag is left as it is). Before the first
   * placement there is nothing to keep: `follow`'s first call places the
   * reader. The box opts out of the browser's own anchoring
   * (`overflow-anchor: none`), so this is the only correction.
   */
  const shiftBy = useCallback((delta: number) => {
    const el = box.current
    if (!el || !scrolled.current || delta === 0) return
    el.scrollTop += delta
    lastTop.current = el.scrollTop
    if (released.current !== null) released.current = el.scrollTop
    remember(el)
  }, [remember])
```

`RoomTranscript.tsx`:
- Add to `RoomTranscriptProps`:
  - `prelude?: ReactNode`: spec §5.3, the conversation before turn 1, drawn first.
  - `preludeVersion?: string`: `${pages}:${status}`; drives `PreludeAnchor`.
- Destructure `shiftBy` from `scroll`.
- Right after the empty-hint block and before `shown.map(...)`, render `{prelude !== undefined && <PreludeAnchor version={preludeVersion ?? ''} onGrow={shiftBy}>{prelude}</PreludeAnchor>}`.
- Change the box class to `"flex-1 overflow-y-auto p-4 space-y-4 [overflow-anchor:none]"`.

`ChatTranscript.tsx`: the same changes: destructure the new props (the props type is shared), render `PreludeAnchor` with `{prelude}` before the turns, and add `[overflow-anchor:none]` to its box.

`ExecutionView.tsx`:
- Import `useExecutionPrelude`, `derivePrelude` and `PreludeSection`.
- After `useExecutionSubscription(...)` (line 105), add:

```tsx
  const preludeApi = useExecutionPrelude(hostId, executionId)
  const preludeView = useMemo(() => derivePrelude(st.prelude.items), [st.prelude.items])
```

- Next to `transcriptProps`, add:

```tsx
  const preludeNode = (
    <PreludeSection view={preludeView} status={st.prelude.status} done={st.prelude.done} error={st.prelude.error}
      keyPrefix={executionId} now={now} mode={chat ? 'chat' : 'room'} pages={st.prelude.pages}
      onLoadOlder={preludeApi.loadOlder} onRetry={preludeApi.retry} />
  )
```

- Add to `transcriptProps`:

```tsx
    prelude: preludeNode, preludeVersion: `${st.prelude.pages}:${st.prelude.status}`,
```

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/components src/hooks && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only src/components/room/prelude/PreludeAnchor.tsx src/hooks/useTranscriptScroll.ts src/components/room/RoomTranscript.tsx src/components/chat/ChatTranscript.tsx src/components/execution/ExecutionView.tsx src/components/room/transcript-scroll.test.tsx src/components/execution/ExecutionView.test.tsx -m "feat(spa): draw the prelude above turn 1 and keep the reader's place on prepend"
```

**PR-2 gate:** as PR-1. PR title "worker prelude P2: room rendering + scroll".

---

## Phase P3 — chat + search (PR-3)

### Task 8: Chat form of the prelude

Chat groups plain operations per turn. The prelude has no daemon turns, so it is split into **spans**: maximal runs of consecutive message entries, cut again at every opening line (`isOpeningLine`). A non-message entry (marker, note) always closes the span before it, so drawing order is the entry order.

**Files:**
- Modify: `spa/src/lib/nex/prelude.ts` (add `preludeBlocks`)
- Modify: `spa/src/lib/nex/prelude.test.ts`
- Create: `spa/src/components/chat/ChatTurnBody.tsx` (moved out of `ChatTranscript`'s turn loop body)
- Modify: `spa/src/components/chat/ChatTranscript.tsx` (use `ChatTurnBody`)
- Modify: `spa/src/components/room/prelude/PreludeSection.tsx` (`mode === 'chat'`)
- Modify: `spa/src/lib/nex/transcript-search.ts:77-80` (`chatToolsKey(keyPrefix, turn: number | string)`)
- Test: `spa/src/components/room/prelude/PreludeSection.test.tsx` (append chat cases)

**Interfaces:**
- Produces:
  - `PreludeBlock = { kind: 'span'; start: number; end: number } | { kind: 'entry'; entry: PreludeEntry }`
  - `preludeBlocks(view: PreludeView): PreludeBlock[]`
  - `ChatTurnBody` props: `{ messages; turn: {start,end}; ops: TurnOperation[]; ctx: RenderCtx; toolsKey: string; interrupted: string; partial?: PartialAssembly | null; withPartial?: boolean; footer?: ReactNode }`

- [ ] **Step 1: Write the failing tests**

```ts
// prelude.test.ts (append)
describe('preludeBlocks', () => {
  it('cuts spans at opening lines and around non-message entries', () => {
    const v = derivePrelude([
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      msg('2', 'user', [{ type: 'text', text: 'one' }]),
      msg('3', 'assistant', [{ type: 'text', text: 'a' }]),
      msg('4', 'user', [{ type: 'text', text: 'two' }]),
      { pos: '5', at: 0, kind: 'prelude.note', source: 'task_notification', text: 'n', truncated: false, totalBytes: null, stream: null },
      msg('6', 'assistant', [{ type: 'text', text: 'b' }]),
    ])
    expect(preludeBlocks(v)).toEqual([
      { kind: 'entry', entry: v.entries[0] },
      { kind: 'span', start: 0, end: 2 },
      { kind: 'span', start: 2, end: 3 },
      { kind: 'entry', entry: v.entries[4] },
      { kind: 'span', start: 3, end: 4 },
    ])
  })
})
```

```tsx
// PreludeSection.test.tsx (append)
it('chat: your lines are bubbles, an agent turn’s tools collapse into one line', () => {
  const view = derivePrelude([
    m('2', 'user', [{ type: 'text', text: 'run it' }]),
    m('3', 'assistant', [{ type: 'tool_use', id: 't1', name: 'Bash', input: { command: 'ls' } }]),
    m('4', 'user', [{ type: 'tool_result', tool_use_id: 't1', content: 'a\nb' }]),
    m('5', 'assistant', [{ type: 'text', text: 'done' }]),
  ])
  render(<PreludeSection {...base} mode="chat" view={view} status="ok" done />)
  expect(document.querySelector('[data-search-unit="p2:0:text"]')).not.toBeNull()
  expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
  expect(screen.getByText('done')).toBeTruthy()
})
```

(If `ChatToolsLine` has no `data-testid="chat-tools-line"`, assert on the test id it does carry. Check `components/chat/ChatToolsLine.tsx`.)

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/lib/nex/prelude.test.ts src/components/room/prelude`
Expected: FAIL.

- [ ] **Step 3: Implement**

`prelude.ts`:

```ts
export type PreludeBlock = { kind: 'span'; start: number; end: number } | { kind: 'entry'; entry: PreludeEntry }

/**
 * Chat's grouping of the prelude (spec §5.3): runs of consecutive messages,
 * cut at every line that opens a turn (the human's own line) and closed by
 * any marker or note, so drawing order stays entry order. Search walks the
 * same blocks (transcript-search).
 */
export function preludeBlocks(view: PreludeView): PreludeBlock[] {
  const out: PreludeBlock[] = []
  let span: { start: number; end: number } | null = null
  const close = () => { if (span) out.push({ kind: 'span', ...span }); span = null }
  for (const e of view.entries) {
    if (e.kind !== 'message') { close(); out.push({ kind: 'entry', entry: e }); continue }
    if (span && isOpeningLine(view.messages[e.m])) close()
    span = span ? { start: span.start, end: e.m + 1 } : { start: e.m, end: e.m + 1 }
  }
  close()
  return out
}
```

(import `isOpeningLine` from `./turns`.)

`ChatTurnBody.tsx` is a **pure move** of the body of `ChatTranscript`'s `shown.map((turn, ti) => …)`: everything inside `<RoomTurnGroup …>`, from the `plain` / `streaming` / `toolsLine` / `lines` computation to the closing `</div>`. Move `ChatMessage`, `ChatOperationLine` and `blockAt` into the same file as non-exported helpers. Turn the closure's free variables into props:
- `messages`, `turn`, `ops`, `ctx`, `interrupted`
- `toolsKey` (was `chatToolsKey(keyPrefix, ti)`)
- `partial` and `withPartial` (was `ti === lastTurn && hasPartial`)
- `footer` (was the `TurnFooter` expression)

`ChatMessage` gets `idOf={ctx.idOf}`. It also gets the same prelude treatment as the room (Task 6 `Placeholders`):
- an omitted media block becomes `<ChatBubble side={user ? 'user' : 'agent'}><OmittedMedia block={block} /></ChatBubble>`;
- a text bubble whose block is `truncated` is followed by `<TruncatedHint shown={blockShownBytes(block)} total={block.total_bytes ?? 0} />`.

Live frames never carry either, so the live chat is unchanged. Add one PreludeSection chat-mode test asserting `[image · png · 120 KB]` appears in a user bubble.

`ChatTranscript` keeps the `RoomTurnGroup` wrapper and renders:

```tsx
<ChatTurnBody messages={messages} turn={turn} ops={opsByTurn[ti] ?? []} ctx={ctx}
  toolsKey={chatToolsKey(keyPrefix, ti)} interrupted={interrupted}
  partial={partial} withPartial={ti === lastTurn && hasPartial}
  footer={turn.boundary !== null && turnMeta?.[turn.boundary] ? <TurnFooter meta={turnMeta[turn.boundary]} /> : null} />
```

Verify the move: the existing `ChatTranscript` tests and `search-anchors.test.tsx` must pass unchanged.

`transcript-search.ts`: change `chatToolsKey(keyPrefix: string, turn: number | string)` (the body is unchanged).

`PreludeSection.tsx`, chat mode. Compute `blocks = useMemo(() => preludeBlocks(view), [view])` (declared with the other hooks, **before** the early `return null`) and the per-span ops `classifyTurnOperations(view.messages, span, index, view.tools, idOf)`. Render each block: an `entry` exactly as in room mode; a `span` as:

```tsx
<ChatTurnBody key={`${keyPrefix}-prelude-span-${view.ids[b.start]}`} messages={view.messages} turn={b} ops={ops}
  ctx={ctx} toolsKey={chatToolsKey(`${keyPrefix}-prelude`, view.ids[b.start])} interrupted={t('stream.interrupted')} />
```

Room mode is unchanged.

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run src/components src/lib/nex && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint`
Expected: PASS (existing chat suites unchanged).

- [ ] **Step 5: Commit** (two commits: the pure move first, then the prelude chat form)

```bash
git commit --only src/components/chat/ChatTurnBody.tsx src/components/chat/ChatTranscript.tsx -m "refactor(spa): move chat's turn body into ChatTurnBody"
git commit --only src/lib/nex/prelude.ts src/lib/nex/prelude.test.ts src/lib/nex/transcript-search.ts src/components/room/prelude/PreludeSection.tsx src/components/room/prelude/PreludeSection.test.tsx -m "feat(spa): chat form of the prelude"
```

### Task 9: Search covers the loaded prelude; "Load all"

**Files:**
- Modify: `spa/src/lib/nex/transcript-search.ts` (`Walk.idOf`, key helper, `chatTurnUnits` over given ranges, a prelude walk, `buildSearchUnits({…, prelude})`)
- Modify: `spa/src/components/room/TranscriptSearch.tsx` (props `prelude`, `preludeDone`, `onLoadAll`; the banner)
- Modify: `spa/src/components/execution/ExecutionView.tsx` (pass them)
- Test: `spa/src/lib/nex/transcript-search.test.ts`, `spa/src/components/room/search-anchors.test.tsx`, `spa/src/components/room/TranscriptSearch.test.tsx`

**Interfaces:**
- Consumes: `PreludeView`, `preludeBlocks`, `preludeId` (Tasks 3, 8).
- Produces: `SearchUnitOptions.prelude?: PreludeView`. Units from the prelude come first, in drawing order, with ids `p<pos>:<j>:<part>` and `p<pos>:note:text`.

- [ ] **Step 1: Write the failing tests**

```ts
// transcript-search.test.ts (append)
it('walks the loaded prelude first, by its stable ids, in both views', () => {
  const prelude = derivePrelude([
    { pos: '2', at: 0, kind: 'user', msg: said('needle early') },
    { pos: '3', at: 0, kind: 'prelude.note', source: 'command_output', text: 'needle note', truncated: false, totalBytes: null, stream: null },
  ])
  for (const view of ['room', 'chat'] as const) {
    const units = buildSearchUnits({ messages: [said('needle late')], index: indexOperations([said('needle late')]), view, keyPrefix: 'k', turnStarts: [], prelude })
    expect(units.map((u) => u.id)).toEqual(['p2:0:text', 'p3:note:text', '0:0:text'])
    expect(units[1].reveal).toEqual(['p3:note'])
  }
})
```

(`said` and `derivePrelude` imports: `said` is the helper this test file already has. Otherwise build the user frame inline.)

`search-anchors.test.tsx`: extend its room × chat parity case. Render the transcripts with `prelude={<PreludeSection …/>}` built from the same `prelude` view, pass `prelude` to `buildSearchUnits`, and keep the existing assertions (every index id has exactly one DOM anchor, and the drawn anchors equal the index's).

`TranscriptSearch.test.tsx`:

```tsx
it('says the prelude is incomplete and loads it all on demand', async () => {
  const onLoadAll = vi.fn(() => Promise.resolve())
  renderSearch({ prelude: derivePrelude([]), preludeDone: false, onLoadAll })   // the file's existing render helper, extended
  fireEvent.click(screen.getByRole('button', { name: 'Load all' }))
  expect(onLoadAll).toHaveBeenCalled()
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npx vitest run src/lib/nex/transcript-search.test.ts src/components/room/search-anchors.test.tsx src/components/room/TranscriptSearch.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

`transcript-search.ts`:
- Add `idOf?: (m: number) => string` to `Walk`, and a helper `const keyOf = (w: Walk, mi: number, bj: number) => blockKey(w.idOf ? w.idOf(mi) : mi, bj)`. Use it at the three walk sites (current lines 117, 153, 195). `toolUseUnit` (line 265) stays main-list-only.
- Split `chatUnits(w, keyPrefix, turnStarts)` into `chatTurnUnits(w, turns: readonly { start: number; end: number }[], toolsKeyOf: (ti: number) => string)` (the old body, with `chatToolsKey(keyPrefix, ti)` → `toolsKeyOf(ti)` and `classifyTurnOperations(…, w.idOf)`). Leave a thin `chatUnits` that calls it with `groupTurns(w.messages, turnStarts)` and `(ti) => chatToolsKey(keyPrefix, ti)`.
- Add the prelude walk:

```ts
/** The prelude as PreludeSection draws it (spec §5.5): entries in order, notes by their own anchor. */
function preludeUnits(opts: SearchUnitOptions, prelude: PreludeView, push: Push) {
  const idOf = (m: number) => prelude.ids[m]
  const w: Walk = { messages: prelude.messages, index: indexOperations(prelude.messages, idOf), tools: prelude.tools, push, idOf }
  const note = (e: Extract<PreludeEntry, { kind: 'note' }>) => {
    const id = preludeId(e.pos)
    // As PreludeNote draws it: bash_input and task_notification whole (the
    // anchor holds the text only, never the "! " prefix); peer_message as
    // prose (RoomProse: the rendered markdown, never folded); the rest
    // (command / bash output, unknown sources) inside a fold.
    if (e.source === 'peer_message') { push(searchUnitId(`${id}:note`, 'text'), proseText(e.text), []); return }
    const folded = e.source !== 'bash_input' && e.source !== 'task_notification'
    push(searchUnitId(`${id}:note`, 'text'), e.text, folded ? [`${id}:note`] : [])
  }
  if (opts.view === 'chat') {
    for (const b of preludeBlocks(prelude)) {
      if (b.kind === 'entry') { if (b.entry.kind === 'note') note(b.entry); continue }
      // Keyed by the span's LAST message (Task 8 review): pages grow only at
      // the front, so a span's end never moves while its start can.
      chatTurnUnits(w, [b], () => chatToolsKey(`${opts.keyPrefix}-prelude`, prelude.ids[b.end - 1]))
    }
    return
  }
  for (const e of prelude.entries) {
    if (e.kind === 'message') { if (!w.index.childIndexes.has(e.m)) messageUnits(w, e.m, []) }
    else if (e.kind === 'note') note(e)
  }
}
```

`chatTurnUnits(w, [b], …)` is called with one range, so its `toolsKeyOf(0)` receives index 0. That is why the callback above ignores its argument and closes over `b`.

In `buildSearchUnits`, call `if (opts.prelude) preludeUnits(opts, opts.prelude, w.push)` **before** the existing view walk.

The **FoldedOutput anchor:** `PreludeNote` passes `searchUnit` to `FoldedOutput`, so a note's anchor appears only once it is expanded or shown whole. That matches the `reveal` above. This is the same contract tool output has.

`TranscriptSearch.tsx`:
- Add the props `prelude?: PreludeView`, `preludeDone?: boolean` and `onLoadAll?: () => Promise<void>`.
- Pass `prelude` into `buildSearchUnits` and add it to that `useMemo`'s deps.
- Under the input row, when `prelude && preludeDone === false`, render:

```tsx
<div data-testid="search-prelude-incomplete" className="flex items-center gap-2 px-2 pb-1 text-xs text-text-muted">
  <span>{t('worker.prelude.search_incomplete')}</span>
  <button type="button" disabled={loadingAll} onClick={() => { setLoadingAll(true); void onLoadAll?.().finally(() => setLoadingAll(false)) }}
    className="underline hover:text-text-primary disabled:opacity-50">{t('worker.prelude.load_all')}</button>
</div>
```

(`const [loadingAll, setLoadingAll] = useState(false)`.)

`ExecutionView.tsx`: pass `prelude={st.prelude.status === 'idle' || st.prelude.status === 'none' ? undefined : preludeView}`, `preludeDone={st.prelude.done}` and `onLoadAll={preludeApi.loadAll}` to `<TranscriptSearch>`.

- [ ] **Step 4: Run to verify pass**

Run: `npx vitest run && npx tsc --noEmit -p tsconfig.app.json && pnpm run lint && pnpm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only src/lib/nex/transcript-search.ts src/lib/nex/transcript-search.test.ts src/components/room/TranscriptSearch.tsx src/components/room/TranscriptSearch.test.tsx src/components/room/search-anchors.test.tsx src/components/execution/ExecutionView.tsx -m "feat(spa): search covers the loaded prelude; Load all"
```

**PR-3 gate:** as PR-1. PR title "worker prelude P3: chat + search". R1 only, unless R1 reports P1+.

---

## Phase P4 — pin, deploy, real data, live acceptance (PR-4)

**Precondition:** nexen-85 reports v0.16.0 tagged, plus the contract doc locations.

### Task 10: Pin v0.16.0, real-fixture replay, deploy, acceptance

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `spa/src/lib/nex/__fixtures__/prelude-<execId>.json` (a real captured page)
- Create: `spa/src/lib/nex/prelude-replay.test.ts`

- [ ] **Step 1: Bump the pin and build**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/transcript-prelude && go get lab.protype.tw/wake/nexen@v0.16.0 && go mod tidy && go build ./... && go test ./internal/module/nex/...
```

Expected: build OK, tests PASS. If the Nexen CHANGELOG lists a schema bump (the spec says it does not), stop and report.

- [ ] **Step 2: Make a harmless real handoff to capture**

Create a scratch dir (`mkdir -p ~/Workspace/wake/purdex-prelude-scratch` and `git init` there so it is a valid repo root for the nex `repo_roots`, or use an existing configured root). In a tmux session run `claude`. Do: one prompt that makes it run `ls` (a tool call), one `!pwd`, one `/model` (open and close), and one prompt typed while it is busy. Then Hand to nex from the SPA on a worktree dev server.

- [ ] **Step 3: Capture the page**

The token goes into a variable and only its length is printed. Never cat, grep or sed the config file:

```bash
TOK=$(awk -F'"' '/^token *=/ {print $2; exit}' ~/.config/pdx/config.toml); echo "len=${#TOK}"
curl -s -H "Authorization: Bearer $TOK" "http://100.64.0.2:7860/api/nex/v1/executions/<execId>/prelude" > /Users/wake/Workspace/wake/purdex/.claude/worktrees/transcript-prelude/spa/src/lib/nex/__fixtures__/prelude-<execId>.json
```

(This needs the deployed daemon from Step 5 first. Do Steps 4–5 before Step 3 if the dev daemon is not the pinned build.)

- [ ] **Step 4: Replay test**

```ts
// spa/src/lib/nex/prelude-replay.test.ts — a real page from Nexen v0.16.0
// through the sanitiser and the view, so a contract drift fails here and
// not on screen.
import { describe, it, expect } from 'vitest'
import page from './__fixtures__/prelude-<execId>.json'
import { sanitizePreludePage } from './prelude-wire'
import { derivePrelude } from './prelude'

describe('prelude replay (real capture)', () => {
  it('sanitises without dropping anything and draws every kind it carries', () => {
    const p = sanitizePreludePage(page)!
    expect(p.state).toBe('ok')
    expect(p.items).toHaveLength((page as { items: unknown[] }).items.length)
    const v = derivePrelude(p.items)
    expect(v.entries.some((e) => e.kind === 'segment')).toBe(true)
    expect(v.entries.some((e) => e.kind === 'note' && e.source === 'bash_output')).toBe(true)
    expect(Object.values(v.tools).some((t) => t.status === 'done')).toBe(true)
  })
})
```

Run: `npx vitest run src/lib/nex/prelude-replay.test.ts`. Expected: PASS.

- [ ] **Step 5: Deploy the daemon** (mlab, after merge; see `reference_pdx_daemon_runtime`)

```bash
cd /Users/wake/Workspace/wake/purdex && git pull --ff-only && make build && rm -f bin/pdx.new && cp bin/pdx bin/pdx.new && mv bin/pdx.new ~/.config/pdx/bin/pdx && ./bin/pdx stop && env PDX_DEV_MODE=1 ./bin/pdx start && curl -s http://100.64.0.2:7860/api/health
```

(If the deployed binary path differs, follow the deploy note in `kickoff_pd_teardown` / `reference_pdx_daemon_runtime`. The new-inode rule is what matters: `rm` → `cp` to `.new` → `mv`.) Expected: `/api/health` shows the merge hash. No `nex.db` delete.

- [ ] **Step 6: Live acceptance — spec §7 items 1–7** on :5174 after the main checkout fast-forwards. Record each as pass or fail in the PR description.

- [ ] **Step 7: Commit**

```bash
git commit --only go.mod go.sum spa/src/lib/nex/__fixtures__/prelude-<execId>.json spa/src/lib/nex/prelude-replay.test.ts -m "chore: pin nexen v0.16.0 (transcript_prelude) + real prelude replay"
```

---

### Task 11: Pasted text in the prelude (U3, spec §5.3 "Pasted text")

Added 2026-10-06 after the live acceptance. One PR (P4e), ≤ 800 lines.

**Files:**
- Modify: `spa/src/lib/nex/prelude.ts` (split in `derivePrelude`), `spa/src/lib/nex/message-types.ts` (`ContentBlock.pasted?`)
- Create: `spa/src/lib/nex/pasted-text.ts` (pure splitter) + test
- Modify: `spa/src/components/room/MessageRow.tsx` (user branch), `spa/src/components/chat/ChatTurnBody.tsx` (user line), a shared `PastedBlock` component (room/prelude), `spa/src/lib/nex/transcript-search.ts` (reveal for pasted units, room and chat walks)
- Modify: `spa/src/locales/en.json`, `zh-TW.json` (`worker.prelude.pasted_one` / `worker.prelude.pasted_other` with `{{count}}`, picked by the caller as `lines === 1 ? 'one' : 'other'` — the i18n store has no plural rules, see `ChatToolsLine.tsx:29-31`; `worker.prelude.pasted_cut` with `{{count}}` for 「N+ 行」 / "N+ lines")
- Test: `pasted-text.test.ts`, `prelude.test.ts`, `PreludeSection.test.tsx`, `search-anchors.test.tsx` (a fixture with a paste joins the parity set), `transcript-search.test.ts`

**Interfaces:**
- `splitPasted(block: ContentBlock): ContentBlock[]` — pure. Input a `text` block; output `[block]` unchanged when it holds no well-formed opening tag. Otherwise typed parts (`{type:'text', text}`, empty ones dropped) and pasted bodies (`{type:'text', text: body, pasted: { lines, cut }}`) in order; `lines` = the body's line count (`splitLines` semantics used by `foldPlan`); `cut` = no closing tag (runs to the end). One `\n` after the opening tag and one before `</pasted_content>` belong to the wrapper. The input's `truncated` / `total_bytes` move to the LAST output block. A stray `</pasted_content>` without an opener stays literal.
- `derivePrelude` applies it to every `text` block of a top-level human `user` message (not `tool_result` carriers, not frames with `parent_tool_use_id`), so `view.messages` carry the split blocks. Block indexes `j` therefore refer to the split list everywhere (keys, folds, search) — render and search both read `view.messages`, so they stay consistent.
- Render: a `text` block with `pasted` draws `PastedBlock` — a muted title 「貼上的文字 · N 行」 (`worker.prelude.pasted_one/_other`; 「N+ 行」 via `worker.prelude.pasted_cut` when `cut`) over `FoldedOutput` with `foldPlan({ text })` — NOT `truncated: cut`: `PastedBlock` draws no truncation hint and `FoldedOutput` must not add its daemon-truncated note; the block's single `TruncatedHint` comes from the existing decoration (room `MessageRow` `decorate`, `MessageRow.tsx:174-178`; chat's user-text branch in `ChatTurnBody`), fold key `${keyAt(ctx, i, j)}:paste`, search anchor `searchUnitId(key, 'text')` on the body. Checked BEFORE the slash-command / interrupt / user-line branches (a body starting with `/` is not a command). In chat, the same component inside the user line's place in ChatTurnBody (the opening line of a span may be a pasted-only message — it is still an opening line; `isOpeningLine` sees a `text` block).
- Search: a `pasted` text block's unit stays `searchUnitId(key, 'text')`, text = body verbatim, reveal = the inherited reveal plus `${key}:paste` — in both the room walk (`messageUnits`) and the chat walk (`chatTurnUnits`).

- [ ] **Step 1: failing tests** — splitter table (no tag; one paste whole message; typed + paste + typed; two pastes; unclosed → cut; stray closer literal; wrapper newlines; truncated flags moved to the last block; empty typed parts dropped; **closing tag present but the block truncated inside a typed suffix → the paste is NOT cut, the flags sit on the typed suffix**); i18n: `1 line` vs plural, and `N+`; **wire path: `sanitizePreludePage → derivePrelude` over a > 64 KB user text block whose closing tag was cut (`truncated: true`, `total_bytes`)**; **chat spans: `preludeBlocks` / `isOpeningLine` start a new span at a pasted-only prompt (assistant/tool messages before it stay in the previous span)**; **exactly one truncation hint per cut block** in room and chat (none from `FoldedOutput`); derive (only human user text blocks split; tool_result carrier and subagent frame untouched); render room + chat (title with count / N+, folded body, no wrapper text anywhere, a `/`-leading body not drawn as a command, truncation hint after the cut paste); search parity fixture with a paste (every unit has exactly one anchor; expanding reveal draws the text), and **a typed + paste + typed message yields three ordered, distinct `j`-based unit ids in both room and chat** (pinned as literals).
- [ ] **Step 2: implement** to green.
- [ ] **Step 3: mutation check** — splitter keeps the wrapper → tests fail; reveal key missing → parity "expanding reveal" fails; pasted check after the command branch → `/` body test fails.
- [ ] **Step 4: gate** — vitest nex/room/chat/execution/hooks, lint, `tsc -p tsconfig.app.json`, build.

## Self-review notes (coordinator)

- **Spec coverage:**
  - §5.1 → T10
  - §5.2 → T1–T4
  - §5.3 → T5, T6, T8
  - §5.4 → T6 (sentinel), T7
  - §5.5 → T9
  - §5.6 → the Global Constraints, guarded by the T3 store test (messages, lastSeq and tools untouched)
  - D1 → T6 (no force-expand)
  - D2 → T6 markers
  - D3 → nothing touches cost
  - D4 → T4 gate
  - D5 → T6 `gone`
  - D6 → T6 image placeholder; subagents simply aren't sent
  - D7 → T9
- **Interface checks:**
  - `preludeId`, `derivePrelude`, `PreludeView.ids` and `idOf` are used consistently in T3, T5, T6, T8 and T9.
  - `chatToolsKey` widened in T8 is used with a string in T8 and T9.
  - `ChatTurnBody` props (T8) match their use in `ChatTranscript` and `PreludeSection`.
- **Anchoring:** a prepend and a live message in one commit are covered by measuring the prelude section only (Task 7, "same commit" test).
