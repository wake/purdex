# P-B.2 — Execution pane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `{kind:'execution'}` panes render a live Nexen execution — history + SSE, send/interrupt/terminate through a lazily acquired control lease — reusing the Stream renderer, and retire the M0 execution detail page.

**Architecture:** The Stream message list is extracted into a pure `ConversationMessages` component (behaviour-preserving, snapshot-guarded). Two hooks own the network: `useExecutionSubscription` (summary → history pages → SSE, cursor from the store) and `useExecutionLease` (attach(control)/renew/release with an idle policy). `ExecutionView` composes header + messages + `StreamInput` and reads everything from `useExecutionStore`. Pane identity becomes `(hostId, executionId)` end to end (route, deeplink, singleton matching).

**Tech Stack:** React 19 / Zustand 5 / Vitest + Testing Library (jsdom) / Phosphor icons / Tailwind 4.

**Spec:** `docs/specs/2026-09-15-pb-execution-pane-spec.md` §4.3 (all of it), §4.5, §5 I3, I6, I7, I10, I11 (hook half), I12 (UI half), I13. P-B.1 (`2026-09-15-pb1-nex-client-store-plan.md`) is already merged into this branch and provides `spa/src/lib/nex/*` and `useExecutionStore`.

## Global Constraints

- Pane identity is `(hostId, executionId)`; the same execution id on two hosts is two panes (spec §4.3.3, I10).
- `ExecutionState.lease` is written only from `attach(control)`/`renew` responses (I11); `lease.acquired`/`released` events touch `summary.lease` only.
- Subscription order is fixed: `getExecution` → `attachObserve` → history pages ascending from `after = 0` → **then** `openNexSse` with `Last-Event-ID = store.lastSeq` (P-B.1 Task 7 contract).
- Lease: renew every `ttl/3` s (ttl from `capabilities.lease.ttl_seconds`, default 120); stop renewing after `2 × ttl` without a send/interrupt (I3); release exactly once on teardown when held (I6).
- Send failure path (I12): `pendingLocal = null`, `pendingSend = false`, `sendError` set, text restored to the input.
- Stream mode must render byte-identical DOM before and after the extraction (I7 snapshot).
- Every new user-visible string goes in both `spa/src/locales/en.json` and `zh-TW.json` (nested JSON; `locale-completeness.test.ts` enforces parity).
- Files: one responsibility each, ≤ ~300 lines; tests next to the file.
- Commands from the worktree: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run <file>`; `pnpm run lint`; `pnpm run build`. One commit per task with `git commit --only <files>`.

---

## File map

| File | Responsibility |
|---|---|
| `spa/src/components/StreamInput.tsx` | + `showAttach?: boolean` (default true) |
| `spa/src/components/ConversationMessages.tsx` | pure message list: scroll container, auto-scroll, empty hint, block mapping, thinking indicator, `children` slot |
| `spa/src/components/ConversationView.tsx` | Stream-only shell; renders `ConversationMessages` |
| `spa/src/lib/nex/resolve-host.ts` | `resolveExecutionHostId` (moved out of `nex-api.ts` so light modules can import it) |
| `spa/src/lib/pane-utils.ts` | host-aware `contentMatches` for `execution` |
| `spa/src/lib/route-utils.ts`, `spa/src/hooks/useRouteSync.ts` | `/execution/<host>/<id>` (+ legacy 2-segment) |
| `spa/src/lib/deeplink/deeplinkResolver.ts` | resolve host → open pane; no M0 fetch |
| `spa/src/lib/nex/lease-ttl.ts` | per-host cached `ttl_seconds` |
| `spa/src/hooks/useExecutionLease.ts` | lazy control lease, renew, idle policy, release |
| `spa/src/hooks/useExecutionSubscription.ts` | summary/history/SSE lifecycle, stale-summary refetch, host removal |
| `spa/src/components/execution/ExecutionHeader.tsx` | state/profile/cwd/lease/observers/cost + Interrupt/Terminate |
| `spa/src/components/execution/ExecutionView.tsx` | composes hooks + header + messages + input; send flow |
| `spa/src/lib/register-modules/index.tsx` | `ExecutionPaneWrapper` → `ExecutionView` |
| `spa/src/lib/host-lifecycle.ts` | close execution tabs on host removal (`closeTabs` mode) |
| deleted | `ExecutionDetailPage.tsx(+test)`, `lib/execution-api.ts(+test)` |

---

### Task 1: `showAttach` + extract `ConversationMessages` (snapshot-guarded)

**Files:**
- Modify: `spa/src/components/StreamInput.tsx`
- Create: `spa/src/components/ConversationMessages.tsx`
- Modify: `spa/src/components/ConversationView.tsx`
- Test: `spa/src/components/ConversationView.snapshot.test.tsx` (new), `spa/src/components/ConversationMessages.test.tsx` (new), `spa/src/components/StreamInput.test.tsx` (extend)

**Interfaces:**
- Produces:

```ts
// ConversationMessages.tsx
export interface ConversationMessagesProps {
  messages: StreamMessage[]
  keyPrefix: string            // stable per pane; keys are `${keyPrefix}-${i}`
  showThinking: boolean        // ThinkingIndicator visibility
  showEmptyHint: boolean       // "waiting" hint (caller decides: Stream = no messages && !isStreaming)
  emptyText?: string           // override for the hint (default t('stream.waiting'))
  scrollKey?: number           // extra auto-scroll dependency (prompt count / optimistic bubble)
  children?: ReactNode         // rendered after the list, BEFORE ThinkingIndicator (Execution: optimistic bubble)
  afterThinking?: ReactNode    // rendered AFTER ThinkingIndicator (Stream: pending prompts — today's DOM order)
}
export default function ConversationMessages(props: ConversationMessagesProps): JSX.Element
// StreamInput.tsx
interface Props { …existing; showAttach?: boolean }   // default true
```

This task is three commits: (a) snapshot test of the *current* `ConversationView` DOM, (b) the extraction that keeps it green, (c) `showAttach`.

- [ ] **Step 1: Snapshot the current Stream DOM (commit a)**

```tsx
// spa/src/components/ConversationView.snapshot.test.tsx
// I7 guard: the ConversationMessages extraction must not change Stream
// mode's DOM. This snapshot is taken BEFORE the refactor and must stay
// byte-identical after it.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'
import type { StreamMessage } from '../lib/stream-ws'

const HOST = 'snap-host'
const SESSION = 'snap-session'

const FIXTURE: StreamMessage[] = [
  { type: 'assistant', message: { role: 'assistant', content: [
    { type: 'thinking', thinking: 'let me think' },
    { type: 'text', text: 'Hello **world**' },
    { type: 'tool_use', id: 'tu1', name: 'Read', input: { file_path: '/a.ts' } },
  ], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'tu1', content: 'file body', is_error: false },
  ], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '[Request interrupted by user]' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '/compact' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'plain question' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu2', content: 'boom', is_error: true }], stop_reason: null } },
] as StreamMessage[]

beforeEach(() => {
  cleanup()
  useStreamStore.setState({ sessions: {}, relayStatus: {}, handoffProgress: {} })
})

describe('ConversationView DOM snapshot (I7)', () => {
  it('connected relay with mixed messages', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      for (const m of FIXTURE) useStreamStore.getState().addMessage(HOST, SESSION, m)
    })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay, empty, not streaming (waiting hint)', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => { useStreamStore.getState().setRelayStatus(HOST, SESSION, true) })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay, streaming with no assistant message (thinking indicator)', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().setStreaming(HOST, SESSION, true)
    })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay with a pending permission prompt', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().addControlRequest(HOST, SESSION, {
        type: 'control_request', request_id: 'r1',
        request: { subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'ls' } },
      })
    })
    expect(container.innerHTML).toMatchSnapshot()
  })
})
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run src/components/ConversationView.snapshot.test.tsx` — it writes `__snapshots__/ConversationView.snapshot.test.tsx.snap`. Open the `.snap` and confirm it contains real markup for all four cases (a thinking block, a tool call, the interrupted bubble, the command bubble, the permission prompt). Commit the test **and** the `.snap`:

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/ConversationView.snapshot.test.tsx spa/src/components/__snapshots__/ConversationView.snapshot.test.tsx.snap && git commit --only spa/src/components/ConversationView.snapshot.test.tsx spa/src/components/__snapshots__/ConversationView.snapshot.test.tsx.snap -m "test(spa): snapshot Stream ConversationView DOM before extraction (I7)"
```

- [ ] **Step 2: Write the failing `ConversationMessages` test**

```tsx
// spa/src/components/ConversationMessages.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import ConversationMessages from './ConversationMessages'
import type { StreamMessage } from '../lib/stream-ws'

const assistantText: StreamMessage = { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: 'Hi there' }], stop_reason: null } } as StreamMessage

describe('ConversationMessages', () => {
  it('renders assistant text and shows nothing else when empty hint is off', () => {
    render(<ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect(screen.getByText('Hi there')).toBeInTheDocument()
    expect(screen.queryByText(/waiting/i)).not.toBeInTheDocument()
  })

  it('shows the default waiting hint, or the override, when asked', () => {
    const { rerender } = render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint />)
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
    rerender(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint emptyText="No history yet" />)
    expect(screen.getByText('No history yet')).toBeInTheDocument()
  })

  it('renders children before the thinking indicator and afterThinking after it', () => {
    render(
      <ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking showEmptyHint={false}
        afterThinking={<div data-testid="after">after</div>}>
        <div data-testid="child">child</div>
      </ConversationMessages>,
    )
    const child = screen.getByTestId('child')
    const indicator = screen.getByTestId('thinking-indicator')
    const after = screen.getByTestId('after')
    // DOM order: list → children → ThinkingIndicator → afterThinking (Stream's prompts)
    expect(child.compareDocumentPosition(indicator) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(indicator.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('renders the four user block styles', () => {
    const msgs: StreamMessage[] = [
      { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't', content: 'out', is_error: false }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '[Request interrupted by user]' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '/compact' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'plain' }], stop_reason: null } },
    ] as StreamMessage[]
    render(<ConversationMessages messages={msgs} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
    expect(screen.getByTestId('command-bubble')).toHaveTextContent('/compact')
    expect(screen.getByText('plain')).toBeInTheDocument()
    expect(screen.getByText('out')).toBeInTheDocument()
  })
})
```

Run: `npx vitest run src/components/ConversationMessages.test.tsx` — FAIL, module not found.

- [ ] **Step 3: Extract the component and rewire `ConversationView` (commit b)**

`ConversationMessages.tsx` — move, verbatim, from `ConversationView.tsx`: the `scrollRef` + auto-scroll `useEffect`, the `<div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-4">` container, the empty hint, the `messages.map(...)` block (assistant → ThinkingBlock/MessageBubble/ToolCallBlock; user → ToolResultBlock/interrupted/command/MessageBubble), then `{children}`, then `<ThinkingIndicator visible={showThinking} />`, then `{afterThinking}`. **Today's Stream DOM is list → ThinkingIndicator → pending prompts** (`ConversationView.tsx:307-312`), so Stream passes its prompts as `afterThinking`, not as `children` — that is what keeps the I7 snapshot byte-identical. Keys become `${keyPrefix}-${i}`. The auto-scroll effect depends on `[messages, scrollKey]`. The empty hint renders when `showEmptyHint` and reads `emptyText ?? t('stream.waiting')`. **Keep every className and data-testid identical** — the snapshot enforces it. Nothing else moves: the `TODO: theme token` comments travel with their lines.

`ConversationView.tsx` after the extraction keeps: store reads, handoff branch, send/permission/ask handlers, file attach + drag overlay, and renders

```tsx
<ConversationMessages
  messages={messages}
  keyPrefix={sessionCode}
  showThinking={showThinking}
  showEmptyHint={messages.length === 0 && !isStreaming}
  scrollKey={pendingControlRequests.length}
  afterThinking={pendingControlRequests.map((req) => /* unchanged AskUserQuestion / PermissionPrompt mapping */)}
/>
```

followed by `FileAttachment` and `StreamInput` exactly as before. Remove the now-unused imports (`useRef` stays only if still used; `ThinkingIndicator`, `MessageBubble`, `ToolCallBlock`, `ThinkingBlock`, `ToolResultBlock`, `Prohibit`, `TerminalWindow` move out).

Run: `npx vitest run src/components/ConversationView.snapshot.test.tsx src/components/ConversationView.test.tsx src/components/ConversationMessages.test.tsx` — all PASS with **no snapshot update** (if vitest reports "obsolete" or "mismatch", the extraction changed the DOM: fix the extraction, never `-u`).

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/ConversationMessages.tsx spa/src/components/ConversationMessages.test.tsx spa/src/components/ConversationView.tsx && git commit --only spa/src/components/ConversationMessages.tsx spa/src/components/ConversationMessages.test.tsx spa/src/components/ConversationView.tsx -m "refactor(spa): extract ConversationMessages from ConversationView (DOM-identical)"
```

- [ ] **Step 4: `showAttach` (commit c)**

Test to add to `spa/src/components/StreamInput.test.tsx` (follow its existing render helper):

```tsx
  it('hides the attach button when showAttach is false', () => {
    const { container, rerender } = render(<StreamInput onSend={() => {}} />)
    expect(container.querySelectorAll('button').length).toBeGreaterThanOrEqual(1)
    rerender(<StreamInput onSend={() => {}} showAttach={false} />)
    expect(container.querySelector('button svg')).toBeNull()
  })
```

Implementation: add `showAttach = true` to the destructured props and wrap the attach `<button>` in `{showAttach && (...)}`. Run `npx vitest run src/components/StreamInput.test.tsx` → PASS.

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx && git commit --only spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx -m "feat(spa): StreamInput showAttach prop"
```

---

### Task 2: Pane identity `(host, executionId)` — matcher, route, deeplink

**Files:**
- Create: `spa/src/lib/nex/resolve-host.ts`; Modify: `spa/src/lib/nex/nex-api.ts` (re-export instead of defining)
- Modify: `spa/src/lib/pane-utils.ts` (`contentMatches`, ~line 50)
- Modify: `spa/src/lib/route-utils.ts` (`parseRoute` ~line 75, `tabToUrl` ~line 135, `ParsedRoute` union ~line 14)
- Modify: `spa/src/hooks/useRouteSync.ts` (~line 117)
- Modify: `spa/src/lib/deeplink/deeplinkResolver.ts`
- Test: `spa/src/lib/pane-utils.test.ts`, `spa/src/lib/route-utils.test.ts`, `spa/src/lib/deeplink/deeplinkResolver.test.ts` (rewrite the M0-view cases), `spa/src/lib/nex/nex-api.test.ts` (import path unchanged — re-export keeps it green)

**Interfaces:**
- Produces:

```ts
// resolve-host.ts
export function resolveExecutionHostId(host?: string): string   // same body as before; nex-api.ts does `export { resolveExecutionHostId } from './resolve-host'`
// route-utils.ts
type ParsedRoute = … | { kind: 'execution'; executionId: string; host?: string }
// parseRoute('/execution/<host>/<id>') → { kind:'execution', executionId, host }; '/execution/<id>' → host undefined
// tabToUrl(_, { kind:'execution', executionId, host }) → host ? `/execution/${host}/${executionId}` : `/execution/${executionId}`
// deeplinkResolver.ts
export interface ResolveDeeplinkDeps {
  resolveHostId: (host?: string) => string
  openDetail: (executionId: string, host: string) => void
}
export function openExecutionDetailTab(executionId: string, host: string): void
```

- [ ] **Step 1: Failing tests**

`pane-utils.test.ts` — add:

```ts
  it('execution panes match on (host, executionId), same id on another host is a different pane', () => {
    useHostStore.setState({ hosts: { a: { id: 'a', name: 'A', ip: '1', port: 1 }, b: { id: 'b', name: 'B', ip: '2', port: 1 } } as never, hostOrder: ['a', 'b'], activeHostId: 'a', runtime: {} })
    expect(contentMatches({ kind: 'execution', executionId: 'exc_1', host: 'a' }, { kind: 'execution', executionId: 'exc_1', host: 'a' })).toBe(true)
    expect(contentMatches({ kind: 'execution', executionId: 'exc_1', host: 'a' }, { kind: 'execution', executionId: 'exc_1', host: 'b' })).toBe(false)
    // an absent/unknown host resolves to the first host
    expect(contentMatches({ kind: 'execution', executionId: 'exc_1' }, { kind: 'execution', executionId: 'exc_1', host: 'a' })).toBe(true)
    expect(contentMatches({ kind: 'execution', executionId: 'exc_1', host: 'zzz' }, { kind: 'execution', executionId: 'exc_1', host: 'a' })).toBe(true)
  })
```

`route-utils.test.ts` — replace the `parseRoute execution (P.12)` block:

```ts
describe('parseRoute execution', () => {
  it('parses /execution/<host>/<id>', () => {
    expect(parseRoute('/execution/h1/exc_deadbeef')).toEqual({ kind: 'execution', executionId: 'exc_deadbeef', host: 'h1' })
  })
  it('parses the legacy /execution/<id> with no host', () => {
    expect(parseRoute('/execution/exc_deadbeef')).toEqual({ kind: 'execution', executionId: 'exc_deadbeef' })
  })
  it('rejects malformed ids/hosts and extra segments', () => {
    expect(parseRoute('/execution/..')).toBeNull()
    expect(parseRoute('/execution/a%2Fb')).toBeNull()
    expect(parseRoute('/execution/h1/exc_1/extra')).toBeNull()
    expect(parseRoute('/execution/bad host/exc_1')).toBeNull()
    expect(parseRoute('/execution')).toBeNull()
  })
  it('round-trips through tabToUrl with and without host', () => {
    const withHost = tabToUrl('abc123', { kind: 'execution', executionId: 'exc_1', host: 'h1' })
    expect(withHost).toBe('/execution/h1/exc_1')
    expect(parseRoute(withHost)).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
    expect(tabToUrl('abc123', { kind: 'execution', executionId: 'exc_1' })).toBe('/execution/exc_1')
  })
})
```

`deeplinkResolver.test.ts` — rewrite: delete `makeView`/`ExecutionView` import; `makeDeps` now stubs `resolveHostId` and `openDetail` only; cases: (1) `resolveDeeplink({executionId:'exc_1', host:'h9'})` calls `resolveHostId('h9')` and `openDetail('exc_1', <resolved>)`; (2) empty executionId → nothing called; (3) `registerDeeplinkResolver` still subscribes via `window.electronAPI.onDeeplinkNavigate` (keep the existing test for that).

- [ ] **Step 2: Run to fail** — `npx vitest run src/lib/pane-utils.test.ts src/lib/route-utils.test.ts src/lib/deeplink/deeplinkResolver.test.ts`.

- [ ] **Step 3: Implement**

`resolve-host.ts`:

```ts
// spa/src/lib/nex/resolve-host.ts — maps an optional `host` hint (pane
// content, route segment, deeplink) onto a known hostId; falls back to the
// first host so an execution always has a daemon to talk to. Kept free of
// fetch imports so pane-utils / route code can use it.
import { useHostStore } from '../../stores/useHostStore'

export function resolveExecutionHostId(host?: string): string {
  const { hostOrder } = useHostStore.getState()
  if (host && hostOrder.includes(host)) return host
  return hostOrder[0] ?? ''
}
```

`nex-api.ts`: delete the local definition, add `export { resolveExecutionHostId } from './resolve-host'` and drop the now-unused `useHostStore` import if nothing else uses it.

`pane-utils.ts`:

```ts
  // Execution panes are singletons per (host, execution id): Nexen ids are
  // per-daemon, so the same id on two hosts is two executions (spec §4.3.3).
  if (a.kind === 'execution' && b.kind === 'execution') {
    return a.executionId === b.executionId
      && resolveExecutionHostId(a.host) === resolveExecutionHostId(b.host)
  }
```

`route-utils.ts`: add `const HOST_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/`; in `parseRoute`:

```ts
  // /execution/<host>/<id> — execution pane (P-B). The legacy two-segment
  // form (no host) is still accepted and resolves to the first host.
  if (segments[0] === 'execution' && (segments.length === 2 || segments.length === 3)) {
    const id = segments[segments.length - 1]
    if (!EXECUTION_ID_PATTERN.test(id)) return null
    if (segments.length === 3) {
      if (!HOST_ID_PATTERN.test(segments[1])) return null
      return { kind: 'execution', executionId: id, host: segments[1] }
    }
    return { kind: 'execution', executionId: id }
  }
```

and in `tabToUrl`: `return content.host ? `/execution/${content.host}/${content.executionId}` : `/execution/${content.executionId}``.

`useRouteSync.ts` case `'execution'`:

```ts
        openSingletonTab({ kind: 'execution', executionId: parsed.executionId, host: resolveExecutionHostId(parsed.host) })
```

`deeplinkResolver.ts`: remove `fetchExecutionView`/`ExecutionView`/`focusExistingSessionTab`/`findTabBySessionCode` usage from the resolver path (keep `focusExistingSessionTab` exported only if another module imports it — `grep -rn focusExistingSessionTab spa/src` — otherwise delete it); `resolveDeeplink` becomes:

```ts
export async function resolveDeeplink(payload: DeeplinkPayload, deps: ResolveDeeplinkDeps = defaultDeps): Promise<void> {
  const { executionId, host } = payload
  if (!executionId) return
  deps.openDetail(executionId, deps.resolveHostId(host))
}
```

Update the header comment: a Nexen execution has no tmux session, so the only landing is the execution pane.

Also add to `spa/src/hooks/useRouteSync.test.ts` (it exists; reuse its `memoryLocation`/`createWrapper`/`resetStore` helpers and import `useHostStore`, `getPrimaryPane`):

```ts
  it('opens /execution/<host>/<id> as an execution pane with the resolved host', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'H1', ip: '1', port: 1, order: 0 } } as never,
      hostOrder: ['h1'], activeHostId: 'h1', runtime: {},
    })
    const mem = memoryLocation({ path: '/execution/h1/exc_1', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })
    const tab = useTabStore.getState().tabs[useTabStore.getState().activeTabId!]
    expect(getPrimaryPane(tab.layout).content).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
  })
```

- [ ] **Step 4: Run to pass** — the three test files above + `src/lib/nex/nex-api.test.ts` + `src/hooks/useRouteSync.test.ts`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/resolve-host.ts spa/src/lib/nex/nex-api.ts spa/src/lib/pane-utils.ts spa/src/lib/pane-utils.test.ts spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts spa/src/hooks/useRouteSync.ts spa/src/hooks/useRouteSync.test.ts spa/src/lib/deeplink/deeplinkResolver.ts spa/src/lib/deeplink/deeplinkResolver.test.ts && git commit --only spa/src/lib/nex/resolve-host.ts spa/src/lib/nex/nex-api.ts spa/src/lib/pane-utils.ts spa/src/lib/pane-utils.test.ts spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts spa/src/hooks/useRouteSync.ts spa/src/hooks/useRouteSync.test.ts spa/src/lib/deeplink/deeplinkResolver.ts spa/src/lib/deeplink/deeplinkResolver.test.ts -m "feat(spa): execution pane identity is (host, executionId)"
```

---

### Task 3: Lease TTL cache + `useExecutionLease`

**Files:**
- Create: `spa/src/lib/nex/lease-ttl.ts`, `spa/src/hooks/useExecutionLease.ts`
- Modify: `spa/src/lib/nex/nex-api.ts` (`postJson` + `releaseLease` gain an optional `init?: RequestInit` for `keepalive`)
- Test: `spa/src/lib/nex/lease-ttl.test.ts`, `spa/src/hooks/useExecutionLease.test.ts`, `spa/src/lib/nex/nex-api.test.ts` (+1 case)

**Interfaces:**
- Consumes: `fetchNexCapabilities`, `attachControl`, `renewLease`, `releaseLease`, `NexApiError` (P-B.1), `useExecutionStore` setters.
- Produces:

```ts
// lease-ttl.ts
export const DEFAULT_LEASE_TTL_S = 120
export function getLeaseTtlSeconds(hostId: string): Promise<number>   // cached per host; DEFAULT on failure
export function resetLeaseTtlCacheForTests(): void
// nex-api.ts (changed signatures)
function postJson(hostId: string, path: string, body: unknown, method = 'POST', init?: RequestInit): Promise<Response>
   // = nexFetch(hostId, path, { ...init, method, body: JSON.stringify(body) })
export function releaseLease(hostId: string, executionId: string, leaseId: string, init?: RequestInit): Promise<void>
// useExecutionLease.ts
export interface ExecutionLeaseApi {
  ensureLease(): Promise<string>     // lease_id; throws NexApiError (lease_held etc.)
  release(): Promise<void>           // idempotent, best-effort
  touch(): void                      // marks activity for the idle policy
}
export const LEASE_IDLE_MULTIPLIER = 2
export const LEASE_MIN_REMAINING_MS = 5000
export function useExecutionLease(hostId: string, executionId: string): ExecutionLeaseApi
```

- [ ] **Step 1: Failing tests**

Add to `spa/src/lib/nex/nex-api.test.ts` (inside the existing `describe`):

```ts
  it('releaseLease forwards a RequestInit (keepalive for beforeunload)', async () => {
    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await releaseLease(hostId, 'exc_1', 'ls_1', { keepalive: true })
    const [, init] = testGlobal.fetch.mock.calls.at(-1)!
    expect(init.keepalive).toBe(true)
    expect(init.method).toBe('DELETE')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })
```

```ts
// spa/src/lib/nex/lease-ttl.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getLeaseTtlSeconds, resetLeaseTtlCacheForTests, DEFAULT_LEASE_TTL_S } from './lease-ttl'
import * as api from './nex-api'

vi.mock('./nex-api', () => ({ fetchNexCapabilities: vi.fn() }))

describe('getLeaseTtlSeconds', () => {
  beforeEach(() => { resetLeaseTtlCacheForTests(); vi.mocked(api.fetchNexCapabilities).mockReset() })

  it('reads capabilities once per host and caches', async () => {
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue({ lease: { ttl_seconds: 90 } } as never)
    expect(await getLeaseTtlSeconds('h')).toBe(90)
    expect(await getLeaseTtlSeconds('h')).toBe(90)
    expect(api.fetchNexCapabilities).toHaveBeenCalledTimes(1)
  })

  it('falls back to the default on failure and does not cache the failure', async () => {
    vi.mocked(api.fetchNexCapabilities).mockRejectedValueOnce(new Error('503'))
    expect(await getLeaseTtlSeconds('h')).toBe(DEFAULT_LEASE_TTL_S)
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue({ lease: { ttl_seconds: 60 } } as never)
    expect(await getLeaseTtlSeconds('h')).toBe(60)
  })
})
```

```ts
// spa/src/hooks/useExecutionLease.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionLease, LEASE_IDLE_MULTIPLIER } from './useExecutionLease'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'

vi.mock('../lib/nex/nex-api', () => ({
  attachControl: vi.fn(), renewLease: vi.fn(), releaseLease: vi.fn(),
}))
vi.mock('../lib/nex/lease-ttl', () => ({ getLeaseTtlSeconds: vi.fn(async () => 30), DEFAULT_LEASE_TTL_S: 120 }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
const lease = () => useExecutionStore.getState().executions[KEY]?.lease ?? null

describe('useExecutionLease', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-15T00:00:00Z'))
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'H', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    vi.mocked(api.attachControl).mockReset().mockResolvedValue({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 })
    vi.mocked(api.renewLease).mockReset().mockImplementation(async () => ({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }))
    vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
  })
  afterEach(() => { vi.useRealTimers() })

  it('ensureLease attaches lazily once and shares the in-flight promise', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    let ids: string[] = []
    await act(async () => { ids = await Promise.all([result.current.ensureLease(), result.current.ensureLease()]) })
    expect(ids).toEqual(['ls_1', 'ls_1'])
    expect(api.attachControl).toHaveBeenCalledTimes(1)
    expect(lease()).toEqual({ leaseId: 'ls_1', expiresAt: expect.any(Number) })
  })

  it('renews at ttl/3 while active and keeps the lease across three cycles (I3)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    for (let i = 0; i < 3; i++) {
      act(() => { result.current.touch() })
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    }
    expect(api.renewLease).toHaveBeenCalledTimes(3)
    expect(lease()?.leaseId).toBe('ls_1')
  })

  it('stops renewing after 2×ttl without activity and re-acquires on the next ensureLease (I3)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000 * LEASE_IDLE_MULTIPLIER + 10_000) })
    const renewsWhileIdle = vi.mocked(api.renewLease).mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(vi.mocked(api.renewLease).mock.calls.length).toBe(renewsWhileIdle) // no more renews once idle
    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    let id = ''
    await act(async () => { id = await result.current.ensureLease() })
    expect(id).toBe('ls_2')
  })

  it('surfaces lease_held with the holder from the summary and rethrows', async () => {
    useExecutionStore.getState().setSummary(H, E, { id: E, lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } } as never)
    vi.mocked(api.attachControl).mockRejectedValueOnce(new NexApiError(409, 'lease_held', 'held'))
    const { result } = renderHook(() => useExecutionLease(H, E))
    await expect(act(async () => { await result.current.ensureLease() })).rejects.toMatchObject({ code: 'lease_held' })
    expect(useExecutionStore.getState().executions[KEY].leaseError).toEqual({ code: 'lease_held', heldBy: 'pdx:mlab/t-other' })
  })

  it('drops the local lease silently when renew says lease_expired', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    vi.mocked(api.renewLease).mockRejectedValueOnce(new NexApiError(409, 'lease_expired', 'gone'))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(lease()).toBeNull()
  })

  it('a renew that resolves after release() cannot write the lease back', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    let resolveRenew!: (v: { mode: 'control'; lease_id: string; expires_at: number }) => void
    vi.mocked(api.renewLease).mockImplementationOnce(() => new Promise((r) => { resolveRenew = r }))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) }) // renew in flight
    await act(async () => { await result.current.release() })
    expect(lease()).toBeNull()
    await act(async () => { resolveRenew({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }) })
    expect(lease()).toBeNull()
  })

  it('host removal stops the renew timer and drops the local lease without a release call (I13)', async () => {
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'H', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(lease()).toBeNull()
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(api.renewLease).not.toHaveBeenCalled()
    expect(api.releaseLease).not.toHaveBeenCalled()
  })

  it('releases exactly once on unmount when held, never when not (I6)', async () => {
    const a = renderHook(() => useExecutionLease(H, E))
    a.unmount()
    expect(api.releaseLease).not.toHaveBeenCalled()
    const b = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await b.result.current.ensureLease() })
    b.unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.releaseLease).toHaveBeenCalledTimes(1)
    expect(api.releaseLease).toHaveBeenCalledWith(H, E, 'ls_1')
    expect(lease()).toBeNull()
  })
})
```

- [ ] **Step 2: Run to fail** — `npx vitest run src/lib/nex/lease-ttl.test.ts src/hooks/useExecutionLease.test.ts`.

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/nex/lease-ttl.ts — the lease TTL a host advertises in
// GET /v1/capabilities, cached per host for the life of the page. The renew
// cadence (ttl/3) and the idle window (2×ttl) both derive from it.
import { fetchNexCapabilities } from './nex-api'

export const DEFAULT_LEASE_TTL_S = 120

const cache = new Map<string, number>()
const inflight = new Map<string, Promise<number>>()

export function getLeaseTtlSeconds(hostId: string): Promise<number> {
  const hit = cache.get(hostId)
  if (hit != null) return Promise.resolve(hit)
  const pending = inflight.get(hostId)
  if (pending) return pending
  const p = fetchNexCapabilities(hostId)
    .then((caps) => {
      const ttl = caps.lease?.ttl_seconds
      const value = typeof ttl === 'number' && ttl > 0 ? ttl : DEFAULT_LEASE_TTL_S
      cache.set(hostId, value)
      return value
    })
    .catch(() => DEFAULT_LEASE_TTL_S) // not cached: the next call asks again
    .finally(() => inflight.delete(hostId))
  inflight.set(hostId, p)
  return p
}

export function resetLeaseTtlCacheForTests(): void {
  cache.clear()
  inflight.clear()
}
```

```ts
// spa/src/hooks/useExecutionLease.ts — the control lease THIS pane holds on
// a Nexen execution (spec §4.3.2). Lazy: nothing is acquired until the user
// sends or interrupts. Renewed at ttl/3 while the user is active; after
// 2×ttl of silence the lease is allowed to lapse so another client (phone,
// other tab) can take over without this one actively holding it; the next
// send re-acquires. Released exactly once on teardown. The store's `lease`
// field is written ONLY from attach(control)/renew responses (I11).
import { useCallback, useEffect, useRef } from 'react'
import { attachControl, releaseLease, renewLease } from '../lib/nex/nex-api'
import { getLeaseTtlSeconds } from '../lib/nex/lease-ttl'
import { NexApiError } from '../lib/nex/types'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'

export const LEASE_IDLE_MULTIPLIER = 2
export const LEASE_MIN_REMAINING_MS = 5000

export interface ExecutionLeaseApi {
  ensureLease(): Promise<string>
  release(): Promise<void>
  touch(): void
}

export function useExecutionLease(hostId: string, executionId: string): ExecutionLeaseApi {
  const key = executionKey(hostId, executionId)
  const inflight = useRef<Promise<string> | null>(null)
  const lastActivity = useRef<number>(Date.now())
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)
  const ttlMs = useRef<number>(120_000)
  // disposed: unmount or host removal happened — no async continuation may
  // write the store again. releasing: a release() is under way — an
  // in-flight renew/attach that resolves afterwards must not resurrect it.
  const disposed = useRef(false)
  const releasing = useRef(false)

  const stopTimer = useCallback(() => {
    if (timer.current) { clearInterval(timer.current); timer.current = null }
  }, [])

  const writeLease = useCallback((lease: { leaseId: string; expiresAt: number } | null) => {
    if (disposed.current) return
    useExecutionStore.getState().setLease(hostId, executionId, lease)
  }, [hostId, executionId])

  const startTimer = useCallback(() => {
    stopTimer()
    timer.current = setInterval(async () => {
      const cur = useExecutionStore.getState().executions[key]?.lease
      if (!cur || disposed.current) { stopTimer(); return }
      if (Date.now() - lastActivity.current > ttlMs.current * LEASE_IDLE_MULTIPLIER) {
        // Idle policy: stop heart-beating and let the server expire it.
        stopTimer()
        return
      }
      try {
        const r = await renewLease(hostId, executionId, cur.leaseId)
        if (!releasing.current) writeLease({ leaseId: r.lease_id, expiresAt: r.expires_at })
      } catch (e) {
        if (e instanceof NexApiError && (e.code === 'lease_expired' || e.code === 'lease_mismatch')) {
          writeLease(null)
          stopTimer()
        }
        // anything else: keep trying at the same cadence
      }
    }, Math.max(1000, ttlMs.current / 3))
  }, [hostId, executionId, key, writeLease, stopTimer])

  const ensureLease = useCallback((): Promise<string> => {
    const cur = useExecutionStore.getState().executions[key]?.lease
    if (cur && cur.expiresAt - Date.now() > LEASE_MIN_REMAINING_MS) return Promise.resolve(cur.leaseId)
    if (inflight.current) return inflight.current
    releasing.current = false
    const p = (async () => {
      ttlMs.current = (await getLeaseTtlSeconds(hostId)) * 1000
      try {
        const r = await attachControl(hostId, executionId)
        if (disposed.current || releasing.current) {
          // Acquired for nobody: give it straight back rather than leave a
          // lease the pane will never renew.
          void releaseLease(hostId, executionId, r.lease_id).catch(() => {})
          throw new NexApiError(0, 'lease_abandoned', 'pane went away while acquiring the lease')
        }
        writeLease({ leaseId: r.lease_id, expiresAt: r.expires_at })
        useExecutionStore.getState().setLeaseError(hostId, executionId, null)
        lastActivity.current = Date.now()
        startTimer()
        return r.lease_id
      } catch (e) {
        if (e instanceof NexApiError && !disposed.current) {
          const heldBy = useExecutionStore.getState().executions[key]?.summary?.lease?.principal_id
          useExecutionStore.getState().setLeaseError(hostId, executionId, { code: e.code, heldBy })
        }
        throw e
      } finally {
        inflight.current = null
      }
    })()
    inflight.current = p
    return p
  }, [hostId, executionId, key, writeLease, startTimer])

  const release = useCallback(async () => {
    releasing.current = true
    stopTimer()
    const cur = useExecutionStore.getState().executions[key]?.lease
    if (!cur) return
    useExecutionStore.getState().setLease(hostId, executionId, null)
    try { await releaseLease(hostId, executionId, cur.leaseId) } catch { /* best-effort */ }
  }, [hostId, executionId, key, stopTimer])

  const touch = useCallback(() => { lastActivity.current = Date.now() }, [])

  // Host removal (keep-tabs mode, spec §4.3.4): the daemon is gone, so drop
  // local authority and stop the heartbeat without a release call.
  // useHostStore has no subscribeWithSelector — compare prev/next by hand.
  useEffect(() => {
    return useHostStore.subscribe((state, prev) => {
      if (prev.hosts[hostId] && !state.hosts[hostId]) {
        disposed.current = true
        stopTimer()
        useExecutionStore.getState().setLease(hostId, executionId, null)
      }
    })
  }, [hostId, executionId, stopTimer])

  // Teardown: unmount / execution change → release once if held. beforeunload
  // gets a keepalive fetch because the page is going away.
  useEffect(() => {
    disposed.current = false
    const onUnload = () => {
      const cur = useExecutionStore.getState().executions[key]?.lease
      if (!cur) return
      void releaseLease(hostId, executionId, cur.leaseId, { keepalive: true }).catch(() => {})
    }
    window.addEventListener('beforeunload', onUnload)
    return () => {
      window.removeEventListener('beforeunload', onUnload)
      disposed.current = true
      void release()
    }
  }, [hostId, executionId, key, release])

  return { ensureLease, release, touch }
}
```

`nex-api.ts` change (do this first so the hook compiles):

```ts
function postJson(hostId: string, path: string, body: unknown, method = 'POST', init?: RequestInit): Promise<Response> {
  return nexFetch(hostId, path, { ...init, method, body: JSON.stringify(body) })
}
export function releaseLease(hostId: string, executionId: string, leaseId: string, init?: RequestInit): Promise<void> {
  return postJson(hostId, execPath(executionId, '/attach'), { lease_id: leaseId }, 'DELETE', init).then(okVoid)
}
```

Also add the hook's imports: `import { useHostStore } from '../stores/useHostStore'`. The `releasing`/`disposed` guards are what make the two new tests pass (renew-after-release, host removal).

- [ ] **Step 4: Run to pass** — `npx vitest run src/lib/nex/lease-ttl.test.ts src/hooks/useExecutionLease.test.ts src/lib/nex/nex-api.test.ts` (8 lease tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/nex/lease-ttl.ts spa/src/lib/nex/lease-ttl.test.ts spa/src/hooks/useExecutionLease.ts spa/src/hooks/useExecutionLease.test.ts spa/src/lib/nex/nex-api.ts spa/src/lib/nex/nex-api.test.ts && git commit --only spa/src/lib/nex/lease-ttl.ts spa/src/lib/nex/lease-ttl.test.ts spa/src/hooks/useExecutionLease.ts spa/src/hooks/useExecutionLease.test.ts spa/src/lib/nex/nex-api.ts spa/src/lib/nex/nex-api.test.ts -m "feat(spa): useExecutionLease with renew cadence and idle policy"
```

---

### Task 4: `useExecutionSubscription`

**Files:**
- Create: `spa/src/hooks/useExecutionSubscription.ts`
- Test: `spa/src/hooks/useExecutionSubscription.test.ts`

**Interfaces:**
- Consumes: `getExecution`, `attachObserve`, `fetchExecutionEvents` (nex-api); `openNexSse` (nex-sse); `frameToEvent` (event-reducer); `useExecutionStore`; `useHostStore`.
- Produces:

```ts
export type SubscriptionProblem = null | 'not_found' | 'host_removed' | 'nex_unavailable' | 'nex_disabled'
   // nex_disabled = the daemon answered a plain 404 (NexApiError code 'http_404'): nothing is mounted at /api/nex
export const HISTORY_PAGE_LIMIT = 500
export const SUMMARY_REFETCH_DEBOUNCE_MS = 300
export function useExecutionSubscription(hostId: string, executionId: string): { problem: SubscriptionProblem }
```

- [ ] **Step 1: Failing tests**

```ts
// spa/src/hooks/useExecutionSubscription.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionSubscription, HISTORY_PAGE_LIMIT, SUMMARY_REFETCH_DEBOUNCE_MS } from './useExecutionSubscription'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'
import type { NexSseOptions } from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', () => ({ getExecution: vi.fn(), attachObserve: vi.fn(), fetchExecutionEvents: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a', E = 'exc_1', KEY = 'host-a:exc_1'
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 2, observers: 0, archived: false, ...extra })
const ev = (seq: number, kind = 'assistant') => ({ seq, execution_id: E, kind, payload: { type: kind }, created_at: 0 })

let sseOpts: NexSseOptions | null
let sseClose: ReturnType<typeof vi.fn>

describe('useExecutionSubscription', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'A', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    sseOpts = null
    sseClose = vi.fn()
    vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
    vi.mocked(api.getExecution).mockReset().mockResolvedValue(summary() as never)
    vi.mocked(api.attachObserve).mockReset().mockResolvedValue({ mode: 'observe', stream_url: '/api/nex/v1/events?execution_id=exc_1', cursor: 2, state: 'idle' })
    vi.mocked(api.fetchExecutionEvents).mockReset()
      .mockResolvedValueOnce({ items: [ev(1), ev(2)], next_cursor: 0 })
  })
  afterEach(() => vi.useRealTimers())

  it('loads summary, pages history ascending, then opens SSE at lastSeq (order contract)', async () => {
    const { result } = renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledWith(H, E)
    expect(api.attachObserve).toHaveBeenCalledWith(H, E)
    expect(api.fetchExecutionEvents).toHaveBeenCalledWith(H, E, { after: 0, limit: HISTORY_PAGE_LIMIT })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.messages).toHaveLength(2)
    expect(st.historyLoaded).toBe(true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(sseOpts!.url).toBe('/api/nex/v1/events?execution_id=exc_1')
    expect(sseOpts!.getLastEventId()).toBe(2)
    expect(result.current.problem).toBeNull()
    // history was applied before SSE opened
    const order = [api.fetchExecutionEvents, sse.openNexSse].map((f) => vi.mocked(f).mock.invocationCallOrder[0])
    expect(order[0]).toBeLessThan(order[1])
  })

  it('follows next_cursor across pages and stops at 0', async () => {
    vi.mocked(api.fetchExecutionEvents).mockReset()
      .mockResolvedValueOnce({ items: [ev(1)], next_cursor: 1 })
      .mockResolvedValueOnce({ items: [ev(2)], next_cursor: 0 })
    renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionEvents).toHaveBeenNthCalledWith(2, H, E, { after: 1, limit: HISTORY_PAGE_LIMIT })
    expect(useExecutionStore.getState().executions[KEY].lastSeq).toBe(2)
  })

  it('applies durable SSE frames, drops transient ones, mirrors status', async () => {
    renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: '{"type":"assistant"}' })
      sseOpts!.onFrame({ id: null, event: 'stream_event', data: '{}' })
      sseOpts!.onFrame({ id: '4', event: 'assistant', data: '{not json' })
    })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.sse).toBe('open')
    expect(st.messages).toHaveLength(3)
    expect(st.lastSeq).toBe(3)
  })

  it('refetches the summary when a lifecycle event marks it stale (debounced) and after reconnect', async () => {
    renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.getExecution).mockClear()
    act(() => {
      sseOpts!.onFrame({ id: '3', event: 'execution.running', data: '{}' })
      sseOpts!.onFrame({ id: '4', event: 'execution.terminal', data: '{"reason":"completed","state":"idle","turn_id":"t"}' })
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].summaryStale).toBe(false)
    act(() => { sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledTimes(2)
  })

  it('reports not_found and opens nothing when the summary 404s', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'execution_not_found', 'nope'))
    const { result } = renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.problem).toBe('not_found')
    expect(sse.openNexSse).not.toHaveBeenCalled()
  })

  it('reports nex_unavailable on 503 nex_unavailable and nex_disabled on a bare 404', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(503, 'nex_unavailable', 'init failed'))
    const a = renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(a.result.current.problem).toBe('nex_unavailable')
    a.unmount()
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'http_404', 'nex: HTTP 404'))
    const b = renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(b.result.current.problem).toBe('nex_disabled')
  })

  it('warns once per connection on a malformed durable frame and does not advance the cursor', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onFrame({ id: '9', event: 'assistant', data: '{oops' })
      sseOpts!.onFrame({ id: '10', event: 'assistant', data: '{oops again' })
    })
    expect(warn).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].lastSeq).toBe(2)
    warn.mockRestore()
  })

  it('closes the SSE on unmount and when the executionId changes', async () => {
    const { rerender, unmount } = renderHook(({ id }) => useExecutionSubscription(H, id), { initialProps: { id: E } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.fetchExecutionEvents).mockResolvedValueOnce({ items: [], next_cursor: 0 })
    rerender({ id: 'exc_2' })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sseClose).toHaveBeenCalledTimes(1)
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(2)
  })

  it('host removal closes the SSE and reports host_removed (keep-tabs mode, I13)', async () => {
    const { result } = renderHook(() => useExecutionSubscription(H, E))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(sseClose).toHaveBeenCalledTimes(1)
    expect(result.current.problem).toBe('host_removed')
    expect(useExecutionStore.getState().executions[KEY]?.sse ?? 'closed').toBe('closed')
  })
})
```

- [ ] **Step 2: Run to fail** — `npx vitest run src/hooks/useExecutionSubscription.test.ts`.

- [ ] **Step 3: Implement**

```ts
// spa/src/hooks/useExecutionSubscription.ts — one pane's observe path onto a
// Nexen execution (spec §4.3.2): summary → attach(observe) → history pages in
// ascending seq order → THEN the SSE with Last-Event-ID = store.lastSeq.
// That order is a contract with the reducer's single high-water mark (a live
// frame applied first would make every older history event look like a
// duplicate). The summary is authoritative: lifecycle events only mark it
// stale and this hook refetches, debounced; it also refetches once after a
// reconnect. Host removal in keep-tabs mode tears everything down here.
import { useEffect, useRef, useState } from 'react'
import { attachObserve, fetchExecutionEvents, getExecution } from '../lib/nex/nex-api'
import { openNexSse, type NexSseHandle } from '../lib/nex/nex-sse'
import { frameToEvent } from '../lib/nex/event-reducer'
import { NexApiError } from '../lib/nex/types'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'

export type SubscriptionProblem = null | 'not_found' | 'host_removed' | 'nex_unavailable'
export const HISTORY_PAGE_LIMIT = 500
export const SUMMARY_REFETCH_DEBOUNCE_MS = 300

export function useExecutionSubscription(hostId: string, executionId: string): { problem: SubscriptionProblem } {
  const [problem, setProblem] = useState<SubscriptionProblem>(null)
  const key = executionKey(hostId, executionId)
  const sseRef = useRef<NexSseHandle | null>(null)

  useEffect(() => {
    let cancelled = false
    let refetchTimer: ReturnType<typeof setTimeout> | null = null
    let wasReconnecting = false
    setProblem(null)
    const store = () => useExecutionStore.getState()

    const refetchSummary = async () => {
      try {
        const s = await getExecution(hostId, executionId)
        if (!cancelled) store().setSummary(hostId, executionId, s)
      } catch {
        // transient — the next stale mark or reconnect tries again
      }
    }
    const scheduleRefetch = () => {
      if (refetchTimer) return
      refetchTimer = setTimeout(() => { refetchTimer = null; void refetchSummary() }, SUMMARY_REFETCH_DEBOUNCE_MS)
    }

    const unsubStale = useExecutionStore.subscribe(
      (s) => s.executions[key]?.summaryStale ?? false,
      (stale) => { if (stale && !cancelled) scheduleRefetch() },
    )

    const teardown = (reason?: SubscriptionProblem) => {
      sseRef.current?.close()
      sseRef.current = null
      if (refetchTimer) { clearTimeout(refetchTimer); refetchTimer = null }
      if (reason) { setProblem(reason); store().setSse(hostId, executionId, 'closed', reason) }
    }

    // useHostStore has no subscribeWithSelector: compare prev/next by hand.
    const unsubHost = useHostStore.subscribe((state, prev) => {
      if (prev.hosts[hostId] && !state.hosts[hostId] && !cancelled) {
        cancelled = true
        teardown('host_removed')
      }
    })

    ;(async () => {
      store().setSse(hostId, executionId, 'connecting')
      try {
        const s = await getExecution(hostId, executionId)
        if (cancelled) return
        store().setSummary(hostId, executionId, s)
        const obs = await attachObserve(hostId, executionId)
        if (cancelled) return
        // History: forward-only paging from 0; stop at the last page or once
        // we have reached the cursor attach reported.
        let after = 0
        for (;;) {
          const page = await fetchExecutionEvents(hostId, executionId, { after, limit: HISTORY_PAGE_LIMIT })
          if (cancelled) return
          store().applyEvents(hostId, executionId, page.items)
          if (page.next_cursor === 0 || page.next_cursor >= obs.cursor) break
          after = page.next_cursor
        }
        store().setHistoryLoaded(hostId, executionId, true)
        let warnedMalformed = false
        sseRef.current = openNexSse({
          hostId,
          url: obs.stream_url,
          getLastEventId: () => store().executions[key]?.lastSeq ?? null,
          onFrame: (frame) => {
            const ev = frameToEvent(frame)
            if (!ev) {
              // Transient frames are expected (P-B2 renders them); a durable
              // frame that does not parse is dropped without moving the cursor
              // and warned about once per connection (spec §4.5).
              if (frame.id != null && !warnedMalformed) {
                warnedMalformed = true
                console.warn(`nex sse: dropped malformed durable frame id=${frame.id} kind=${frame.event}`)
              }
              return
            }
            store().applyEvents(hostId, executionId, [ev])
          },
          onStatus: (status, err) => {
            if (cancelled) return
            store().setSse(hostId, executionId, status, err?.message ?? null)
            if (status === 'reconnecting') wasReconnecting = true
            if (status === 'open') { warnedMalformed = false; if (wasReconnecting) { wasReconnecting = false; void refetchSummary() } }
          },
        })
      } catch (e) {
        if (cancelled) return
        if (e instanceof NexApiError && e.code === 'execution_not_found') teardown('not_found')
        else if (e instanceof NexApiError && e.code === 'nex_unavailable') teardown('nex_unavailable')
        else if (e instanceof NexApiError && e.code === 'http_404') teardown('nex_disabled')
        else store().setSse(hostId, executionId, 'closed', e instanceof Error ? e.message : String(e))
      }
    })()

    return () => {
      cancelled = true
      unsubStale()
      unsubHost()
      teardown()
    }
  }, [hostId, executionId, key])

  return { problem }
}
```

- [ ] **Step 4: Run to pass** — `npx vitest run src/hooks/useExecutionSubscription.test.ts`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/hooks/useExecutionSubscription.ts spa/src/hooks/useExecutionSubscription.test.ts && git commit --only spa/src/hooks/useExecutionSubscription.ts spa/src/hooks/useExecutionSubscription.test.ts -m "feat(spa): useExecutionSubscription — summary, history, SSE in contract order"
```

---

### Task 5: `ExecutionView` + header, pane wiring, delete M0 page, i18n

**Files:**
- Create: `spa/src/components/execution/ExecutionHeader.tsx`, `spa/src/components/execution/ExecutionView.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx` (`ExecutionPaneWrapper`, ~line 98)
- Delete: `spa/src/components/ExecutionDetailPage.tsx`, `spa/src/components/ExecutionDetailPage.test.tsx`, `spa/src/lib/execution-api.ts` (+ its test if present)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Test: `spa/src/components/execution/ExecutionView.test.tsx`, `spa/src/components/execution/ExecutionHeader.test.tsx`

**Interfaces:**
- Consumes: Tasks 1–4; `useExecutionStore`; `getNexClientId`; `sendMessage`, `interruptExecution`, `terminateExecution` (nex-api).
- Produces:

```ts
export interface ExecutionHeaderProps {
  summary: ExecutionSummary | null
  costUsd: number
  sse: ExecutionState['sse']
  isMine: (principal: string | undefined) => boolean
  onInterrupt: () => void
  onTerminate: () => void
  busy: boolean
}
export default function ExecutionHeader(p: ExecutionHeaderProps): JSX.Element
export interface ExecutionViewProps { hostId: string; executionId: string; isActive: boolean }
export default function ExecutionView(p: ExecutionViewProps): JSX.Element
```

i18n keys (add to both locales; zh-TW values in Traditional Chinese):

```
execution.title              "Execution"                      執行體
execution.loading            "Loading execution…"             載入執行體…
execution.not_found          "Execution not found on this host." 這台主機上找不到此執行體。
execution.host_removed       "Host removed."                  主機已移除。
execution.nex_unavailable    "Nex is unavailable on this host: {{error}}"  這台主機的 Nex 無法使用：{{error}}
execution.nex_disabled       "Nex is not enabled on this host. Enable it under Hosts → Nex."  這台主機未啟用 Nex，請到「主機 → Nex」啟用。
execution.empty              "No messages yet."               尚無訊息。
execution.observers          "observers"                      觀察者
execution.lease_you          "(you)"                          （你）
execution.lease_none         "no lease"                       無 lease
execution.lease_held         "Held by {{principal}} — try again when released" 由 {{principal}} 持有，等對方釋放後再試
execution.turns              "turns"                          回合
execution.interrupt          "Interrupt"                      中斷
execution.terminate          "Terminate"                      終止
execution.terminate_confirm  "Confirm terminate"              確認終止
execution.queued             "queued"                         已排隊
execution.sse.connecting     "connecting"                     連線中
execution.sse.open           "live"                           即時
execution.sse.reconnecting   "reconnecting"                   重新連線中
execution.sse.closed         "disconnected"                   已斷線
execution.input.archived     "Execution is archived"          執行體已歸檔
execution.input.terminal     "Execution has ended"            執行體已結束
execution.error.generic      "Send failed: {{message}}"         送出失敗：{{message}}
execution.error.invalid_text "Message too long for this host" 訊息超過這台主機的長度上限
execution.error.execution_archived "Execution is archived; unarchive it first" 執行體已歸檔，請先取消歸檔
execution.error.execution_terminal "Execution has ended"      執行體已結束
execution.error.turn_failed_to_launch "The turn never started; check the execution and resend" 這一輪未能啟動，請確認後重送
execution.error.turn_stalled "The turn was withdrawn; resend if still wanted" 這一輪已被撤回，需要的話請重送
execution.error.interrupt_unconfirmed "Interrupt sent but not confirmed; the turn may still be running" 已送出中斷但未確認，這一輪可能仍在執行
```

`t(key, params)` interpolates `{{name}}` from `params` (`spa/src/stores/useI18nStore.ts:49`); use that — never build these strings by concatenation.

- [ ] **Step 1: Failing tests**

```tsx
// spa/src/components/execution/ExecutionView.test.tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { NexApiError } from '../../lib/nex/types'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
const ensureLease = vi.fn(), release = vi.fn(), touch = vi.fn()
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3, ...extra })

beforeEach(() => {
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); release.mockReset(); touch.mockReset()
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease, release, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(api.interruptExecution).mockReset().mockResolvedValue({ turn_id: 't1', state: 'idle' })
  vi.mocked(api.terminateExecution).mockReset().mockResolvedValue(undefined)
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
})

describe('ExecutionView', () => {
  it('renders header facts from the summary', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-me000000', expires_at: 1 } }) as never)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByText(/standard/)).toBeInTheDocument()
    expect(screen.getByText(/repo/)).toBeInTheDocument()
    expect(screen.getByText(/\(you\)/)).toBeInTheDocument()
  })

  it('send: optimistic bubble, lease acquired, message posted, queued tag shown', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't1', delivery: 'queued' })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'hello'))
    expect(ensureLease).toHaveBeenCalledTimes(1)
    expect(touch).toHaveBeenCalled()
    expect(screen.getByText('hello')).toBeInTheDocument()
    expect(screen.getByText(/queued/i)).toBeInTheDocument()
    expect(useExecutionStore.getState().executions[KEY].pendingSend).toBe(true)
  })

  it('send failure withdraws the bubble, re-enables input, restores text, shows the error (I12)', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.pendingSend).toBe(false)
    expect(st.pendingLocal).toBeNull()
    expect(st.sendError?.code).toBe('invalid_text')
    expect(box.value).toBe('hello')
    expect(box.disabled).toBe(false)
  })

  it('lease_held shows the holder notice and keeps the input enabled', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } }) as never)
    // The real hook writes leaseError before rethrowing; the mock must too.
    ensureLease.mockImplementationOnce(async () => {
      useExecutionStore.getState().setLeaseError(H, E, { code: 'lease_held', heldBy: 'pdx:mlab/t-other' })
      throw new NexApiError(409, 'lease_held', 'held')
    })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'x' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByText(/t-other/)).toBeInTheDocument())
    expect(box.disabled).toBe(false)
    expect(api.sendMessage).not.toHaveBeenCalled()
  })

  it('interrupt acquires the lease and posts; no_live_turn is silent', async () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await waitFor(() => expect(api.interruptExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
    vi.mocked(api.interruptExecution).mockRejectedValueOnce(new NexApiError(409, 'no_live_turn', 'nothing'))
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await act(async () => {})
    expect(screen.queryByTestId('send-error')).not.toBeInTheDocument()
  })

  it('terminate needs two clicks, then acquires the lease and posts', async () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
  })

  it('disables input with a reason when archived or ended', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    const { rerender } = render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/archived/i))
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/ended/i))
  })

  it('renders the problem states instead of the conversation', () => {
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'not_found' })
    const { rerender } = render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'host_removed' })
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/host removed/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'nex_disabled' })
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/not enabled/i)).toBeInTheDocument()
  })

  it('shows the loading state until history is loaded', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
  })
})
```

`ExecutionHeader.test.tsx`: three small cases — (1) state dot text + profile + basename(cwd) + observers + turns; (2) lease line "(you)" when `isMine` returns true, raw principal otherwise, "no lease" when absent; (3) SSE badge text follows `sse`.

- [ ] **Step 2: Run to fail** — `npx vitest run src/components/execution`.

- [ ] **Step 3: Implement**

`ExecutionHeader.tsx` (presentational; Phosphor icons `Prohibit` for interrupt, `Power` for terminate; two-click terminate state lives here with a 4 s revert timer):

```tsx
// spa/src/components/execution/ExecutionHeader.tsx — the facts strip above
// an execution conversation (spec §4.3.3): state, provider/profile, cwd,
// observers, lease holder, turns, cost, SSE status, and the two lease-backed
// actions. Pure presentation; ExecutionView owns the network.
import { useEffect, useState } from 'react'
import { Prohibit, Power } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ExecutionSummary } from '../../lib/nex/types'
import type { ExecutionState } from '../../lib/nex/event-reducer'

export interface ExecutionHeaderProps {
  summary: ExecutionSummary | null
  costUsd: number
  sse: ExecutionState['sse']
  isMine: (principal: string | undefined) => boolean
  onInterrupt: () => void
  onTerminate: () => void
  busy: boolean
}

const STATE_DOT: Record<string, string> = {
  running: 'bg-status-success', idle: 'bg-text-muted', queued: 'bg-status-warning',
  failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-status-error',
}

export const TERMINATE_CONFIRM_MS = 4000

export default function ExecutionHeader({ summary, costUsd, sse, isMine, onInterrupt, onTerminate, busy }: ExecutionHeaderProps) {
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState(false)
  useEffect(() => {
    if (!confirming) return
    const id = setTimeout(() => setConfirming(false), TERMINATE_CONFIRM_MS)
    return () => clearTimeout(id)
  }, [confirming])

  const state = summary?.state ?? '…'
  const cwdBase = summary?.cwd ? summary.cwd.split('/').filter(Boolean).pop() ?? summary.cwd : ''
  const lease = summary?.lease
  const leaseText = lease ? `${lease.principal_id}${isMine(lease.principal_id) ? ` ${t('execution.lease_you')}` : ''}` : t('execution.lease_none')

  return (
    <div className="flex flex-col gap-1 px-4 py-2 border-b border-border-default text-xs text-text-muted">
      <div className="flex items-center gap-2">
        <span className={`w-2 h-2 rounded-full ${STATE_DOT[state] ?? 'bg-text-muted'}`} />
        <span data-testid="execution-state" className="text-text-primary font-medium">{state}</span>
        {summary && <span>{summary.provider} · {summary.effective_profile ?? summary.requested_profile ?? '—'}</span>}
        {cwdBase && <span title={summary?.cwd} className="font-mono">{cwdBase}</span>}
        <div className="flex-1" />
        <span data-testid="execution-sse">{t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`)}</span>
      </div>
      <div className="flex items-center gap-3">
        <span>{summary?.observers ?? 0} {t('execution.observers')}</span>
        <span data-testid="execution-lease">{leaseText}</span>
        {summary?.turn_count != null && <span>{summary.turn_count} {t('execution.turns')}</span>}
        <span>${costUsd.toFixed(2)}</span>
        <div className="flex-1" />
        <button type="button" disabled={busy} onClick={onInterrupt}
          className="flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40">
          <Prohibit size={12} /> {t('execution.interrupt')}
        </button>
        <button type="button" disabled={busy}
          onClick={() => { if (confirming) { setConfirming(false); onTerminate() } else setConfirming(true) }}
          className={`flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40 ${confirming ? 'text-status-error' : ''}`}>
          <Power size={12} /> {confirming ? t('execution.terminate_confirm') : t('execution.terminate')}
        </button>
      </div>
    </div>
  )
}
```

`ExecutionView.tsx`:

```tsx
// spa/src/components/execution/ExecutionView.tsx — the {kind:'execution'}
// pane (spec §4.3.3). Composes the observe subscription, the lazy control
// lease, the shared message renderer and StreamInput. Send/interrupt/
// terminate are the only writes; every one goes through ensureLease().
import { useCallback, useMemo, useState } from 'react'
import ConversationMessages from '../ConversationMessages'
import StreamInput from '../StreamInput'
import ExecutionHeader from './ExecutionHeader'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useExecutionSubscription } from '../../hooks/useExecutionSubscription'
import { useExecutionLease } from '../../hooks/useExecutionLease'
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import { interruptExecution, sendMessage, terminateExecution } from '../../lib/nex/nex-api'
import { NexApiError } from '../../lib/nex/types'
import { defaultExecutionState } from '../../lib/nex/event-reducer'
import type { StreamMessage } from '../../lib/stream-ws'

export interface ExecutionViewProps { hostId: string; executionId: string; isActive: boolean }

const EMPTY = defaultExecutionState()
const TERMINAL_STATES = new Set(['rejected', 'failed', 'terminated'])
const KNOWN_ERROR_KEYS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

export default function ExecutionView({ hostId, executionId, isActive }: ExecutionViewProps) {
  const t = useI18nStore((s) => s.t)
  const key = executionKey(hostId, executionId)
  const st = useExecutionStore((s) => s.executions[key] ?? EMPTY)
  const { problem } = useExecutionSubscription(hostId, executionId)
  const { ensureLease, touch } = useExecutionLease(hostId, executionId)
  const [draft, setDraft] = useState<string | null>(null) // restored text after a failed send

  const isMine = useCallback((p: string | undefined) => !!p && p.endsWith(`/${getNexClientId()}`), [])
  const costUsd = useMemo(() => st.messages.reduce((sum, m) => sum + ((m as { total_cost_usd?: number }).total_cost_usd ?? 0), 0), [st.messages])

  const store = () => useExecutionStore.getState()
  const fail = (e: unknown) => {
    if (e instanceof NexApiError) {
      if (e.code === 'no_live_turn') return
      store().setSendError(hostId, executionId, { code: e.code, message: e.message, turnId: e.turnId })
    } else {
      store().setSendError(hostId, executionId, { code: 'network', message: e instanceof Error ? e.message : String(e) })
    }
  }

  const handleSend = useCallback(async (text: string) => {
    store().setSendError(hostId, executionId, null)
    setDraft(null)
    touch()
    try {
      const leaseId = await ensureLease()
      store().setPendingLocal(hostId, executionId, { text, delivery: null })
      store().setPendingSend(hostId, executionId, true)
      const r = await sendMessage(hostId, executionId, leaseId, text)
      store().setPendingLocal(hostId, executionId, { text, delivery: r.delivery })
      store().setLastTurn(hostId, executionId, { turnId: r.turn_id, delivery: r.delivery })
    } catch (e) {
      store().setPendingLocal(hostId, executionId, null)
      store().setPendingSend(hostId, executionId, false)
      setDraft(text)
      fail(e)
    }
  }, [hostId, executionId, ensureLease, touch])

  const handleInterrupt = useCallback(async () => {
    touch()
    try { await interruptExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) }
  }, [hostId, executionId, ensureLease, touch])

  const handleTerminate = useCallback(async () => {
    touch()
    try { await terminateExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) }
  }, [hostId, executionId, ensureLease, touch])

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex items-center justify-center h-full text-sm text-text-muted">{text}</div>
  }

  const ended = !!st.summary && (TERMINAL_STATES.has(st.summary.state) || st.summary.archived)
  const placeholder = st.summary?.archived ? t('execution.input.archived') : ended ? t('execution.input.terminal') : undefined
  const leaseHeld = st.leaseError?.code === 'lease_held'
  const errorText = st.sendError
    ? (KNOWN_ERROR_KEYS.has(st.sendError.code) ? t(`execution.error.${st.sendError.code}`) : t('execution.error.generic', { message: st.sendError.message }))
    : null

  return (
    <div className="flex flex-col h-full">
      <ExecutionHeader summary={st.summary} costUsd={costUsd} sse={st.sse} isMine={isMine}
        onInterrupt={() => void handleInterrupt()} onTerminate={() => void handleTerminate()} busy={ended} />
      {!st.historyLoaded ? (
        <div data-testid="execution-loading" className="flex-1 flex items-center justify-center text-sm text-text-muted">{t('execution.loading')}</div>
      ) : (
        <ConversationMessages messages={st.messages} keyPrefix={executionId} showThinking={st.pendingSend && !st.pendingLocal?.delivery}
          showEmptyHint={st.messages.length === 0 && !st.pendingLocal} emptyText={t('execution.empty')} scrollKey={st.pendingLocal ? 1 : 0}>
          {st.pendingLocal && (
            <div className="flex justify-end">
              <div className="flex items-center gap-2 bg-surface-input rounded-[12px_12px_4px_12px] px-3 py-1.5 text-sm">
                <span>{st.pendingLocal.text}</span>
                {st.pendingLocal.delivery === 'queued' && <span className="text-[10px] uppercase text-text-muted">{t('execution.queued')}</span>}
              </div>
            </div>
          )}
        </ConversationMessages>
      )}
      {leaseHeld && (
        <div data-testid="lease-held" className="mx-2 mb-1 text-xs text-status-warning">
          {t('execution.lease_held', { principal: st.leaseError?.heldBy ?? '' })}
        </div>
      )}
      {errorText && <div data-testid="send-error" className="mx-2 mb-1 text-xs text-status-error">{errorText}</div>}
      <StreamInput key={draft ?? ''} initialValue={draft ?? undefined} onSend={(text) => void handleSend(text)} showAttach={false}
        disabled={st.pendingSend || ended} placeholder={placeholder} focused={isActive} />
    </div>
  )
}
```

> `StreamInput` has no `initialValue` prop today — add one (`initialValue?: string`, used as `useState(initialValue ?? '')`) in this task with a one-line test in `StreamInput.test.tsx`; the `key={draft}` remount is what puts the failed text back. Interpolated strings use `t(key, { … })` with `{{name}}` placeholders.

`register-modules/index.tsx`:

```tsx
function ExecutionPaneWrapper({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'execution') return null
  return <ExecutionView hostId={resolveExecutionHostId(content.host)} executionId={content.executionId} isActive={isActive} />
}
```

(import `ExecutionView` from `../../components/execution/ExecutionView` and `resolveExecutionHostId` from `../nex/resolve-host`; remove the `ExecutionDetailPage` import.) Delete `ExecutionDetailPage.tsx`, its test, `lib/execution-api.ts` (and its test if present): `git rm`. `grep -rn "execution-api\|ExecutionDetailPage" spa/src` must return nothing.

- [ ] **Step 4: Run to pass** — `npx vitest run src/components/execution src/components/StreamInput.test.tsx src/locales` then the full suite.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add -A spa/src/components/execution spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx spa/src/lib/register-modules/index.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git rm -q spa/src/components/ExecutionDetailPage.tsx spa/src/components/ExecutionDetailPage.test.tsx spa/src/lib/execution-api.ts && git commit --only spa/src/components/execution spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx spa/src/lib/register-modules/index.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json spa/src/components/ExecutionDetailPage.tsx spa/src/components/ExecutionDetailPage.test.tsx spa/src/lib/execution-api.ts -m "feat(spa): ExecutionView pane on Nexen; remove M0 execution detail page"
```

(If `lib/execution-api.test.ts` exists, include it in the `git rm` and the `--only` list.)

---

### Task 6: Host removal closes execution tabs (`closeTabs` mode)

**Files:**
- Modify: `spa/src/lib/host-lifecycle.ts` (the `closeTabs` scan ~line 96-110)
- Test: `spa/src/lib/host-lifecycle.test.ts`

**Interfaces:** none new. In `closeTabs` mode a tab is closed when any pane is a `tmux-session` of that host **or** an `execution` pane whose `resolveExecutionHostId(content.host) === hostId`. Keep-tabs mode leaves execution panes alone (the hook renders "Host removed", Task 4).

- [ ] **Step 1: Failing test** (append to the cascade `describe`; `createTab` is already imported):

```ts
  it('closeTabs=true also closes execution tabs of the removed host, not other hosts (I13)', () => {
    const exA = createTab({ kind: 'execution', executionId: 'exc_1', host: HOST_A })
    const exB = createTab({ kind: 'execution', executionId: 'exc_1', host: HOST_B })
    useTabStore.getState().addTab(exA)
    useTabStore.getState().addTab(exB)
    deleteHostCascade(HOST_A, true)
    expect(useTabStore.getState().tabs[exA.id]).toBeUndefined()
    expect(useTabStore.getState().tabs[exB.id]).toBeDefined()
  })

  it('closeTabs=false leaves execution tabs in place', () => {
    const exA = createTab({ kind: 'execution', executionId: 'exc_1', host: HOST_A })
    useTabStore.getState().addTab(exA)
    deleteHostCascade(HOST_A, false)
    expect(useTabStore.getState().tabs[exA.id]).toBeDefined()
  })
```

- [ ] **Step 2: Run to fail** — `npx vitest run src/lib/host-lifecycle.test.ts`.

- [ ] **Step 3: Implement** — in the `closeTabs` scan:

```ts
        if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId) {
          hasHostPane = true
        }
        // Execution panes belong to the host that runs the execution (P-B);
        // an absent host hint resolves like everywhere else.
        if (pane.content.kind === 'execution' && resolveExecutionHostId(pane.content.host) === hostId) {
          hasHostPane = true
        }
```

with `import { resolveExecutionHostId } from './nex/resolve-host'`. Note the undo path already restores `snapshot.closedTabs` wholesale, so nothing else changes.

- [ ] **Step 4: Run to pass** — `npx vitest run src/lib/host-lifecycle.test.ts`.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane && git add spa/src/lib/host-lifecycle.ts spa/src/lib/host-lifecycle.test.ts && git commit --only spa/src/lib/host-lifecycle.ts spa/src/lib/host-lifecycle.test.ts -m "feat(spa): host removal closes that host's execution tabs"
```

---

### Task 7: Full verification

- [ ] `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb-execution-pane/spa && npx vitest run 2>&1 | tail -6 && pnpm run lint 2>&1 | tail -5 && pnpm run build 2>&1 | tail -3` — all green; the I7 snapshot still passes without update.
- [ ] `grep -rn "execution-api\|ExecutionDetailPage\|fetchExecutionView" spa/src` → nothing.
- [ ] Fix anything red in a `fix(spa): …` commit naming the cause.

---

## Self-review notes

- Spec coverage: §4.3.1 → T1; §4.3.2 hooks → T3 (lease) + T4 (subscription); §4.3.3 view/identity/deeplink → T2 + T5; §4.3.4 teardown → T3 (unmount/beforeunload), T4 (keep-tabs host removal), T6 (closeTabs); §4.5 rows → T4 (`not_found`, `nex_unavailable`, host removal), T5 (disabled nex is P-B.3's `/api/info` check — the pane shows `nex_unavailable` text for the 503, and the "not enabled" copy is used by P-B.3), reconnect refetch → T4.
- Invariants: I3 → T3 tests 2-3; I6 → T3 test 6 + T4 test 7; I7 → T1 snapshot; I10 → T2; I11 (hook half) → T4 test 4 + T3 never writing from events; I12 → T5 test 3; I13 → T4 test 8 + T6.
- `summaryStale` (P-B.1 addition) is consumed by T4's subscribe; `setSummary` clears it.
- Types: `ExecutionLeaseApi` used identically in T3 and T5 mocks; `SubscriptionProblem` values identical in T4 and T5.

## Codex plan review disposition (`task-mu2a5qnp-2vejqo`, one round, gpt-5.5)

| # | Finding | Disposition |
|---|---|---|
| P1-1 | `children` placed before `ThinkingIndicator` would break the I7 snapshot (Stream renders prompts after it) | Fixed: `children` (before) + `afterThinking` (after); Stream passes prompts as `afterThinking`; test asserts both orders |
| P1-2 | `useHostStore.subscribe(selector, listener)` — the store has no `subscribeWithSelector` | Fixed: whole-store subscribe comparing `prev.hosts[hostId]` vs `state.hosts[hostId]` (Tasks 3 and 4) |
| P1-3 | Lease hook did not react to host removal (timer kept running, §4.3.4/I13) | Fixed: host-removal subscription in `useExecutionLease` + test |
| P1-4 | Renew/attach resolving after `release()`/unmount could write the lease back | Fixed: `disposed`/`releasing` refs, `writeLease` guard, attach-after-dispose gives the lease back; test added |
| P1-5 | `releaseLease` had no `RequestInit`; `keepalive` never sent | Fixed: `postJson`/`releaseLease` gain `init?`; nex-api test; `onUnload` passes `{ keepalive: true }` |
| P1-6 | `lease_held` UI test would fail — mock `ensureLease` did not write `leaseError` | Fixed: mock writes `leaseError` before throwing |
| P2-1 | `t()` interpolates `{{name}}`, plan used `{name}` + concatenation | Fixed: keys use `{{…}}`, calls pass params |
| P2-2 | Malformed durable frame: §4.5 wants drop + warn once per connection, cursor untouched | Fixed: `warnedMalformed` per connection (reset on `open`) + test |
| P2-3 | `nex_disabled` copy existed but no problem state / detection | Fixed: `SubscriptionProblem` gains `'nex_disabled'` (bare `http_404`); hook + view + tests |
| P3-1 | No `useRouteSync` regression for the host route | Fixed: test added to Task 2 |
