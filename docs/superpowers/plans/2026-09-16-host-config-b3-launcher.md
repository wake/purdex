# Host Config B3 — Session Launcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the "new session" forms (New Tab host block `+`, Host › Sessions `New Session`) with a launcher: session-name input plus a grid of the host's projects, each with its normal command icons; clicking creates a tmux session in the project path and runs the command.

**Architecture:** A pure name generator (`lib/launch-session-name.ts`), a launch helper (`lib/session-launch.ts`) that creates and sends through one `pinHost(hostId)` transport, and a presentational `SessionLauncher` component reading `useHostConfigStore` / `useSessionStore`. Both entry points mount it and keep their own attach / close behaviour.

**Tech Stack:** React 19, Zustand 5, Tailwind 4 (container query variants), Vitest + Testing Library, wouter, Phosphor icons.

**Spec:** `docs/superpowers/specs/2026-09-16-host-projects-commands-launcher-design.md` (§5). Depends on B2 plan `docs/superpowers/plans/2026-09-16-host-config-b2-spa-settings.md` (merged) and on PR #1064.

**Base:** B3 is planned against `main` **after PR #1064 (branch `worktree-agent-a4ed6ef7bde8cd175`) and B2 have merged**. #1064 turns `SessionSection.tsx` into one `HostSessionSection` block per host (`sessions:<hostId>` providers, `spa/src/lib/session-new-tab-providers.tsx`), with the inline `NewTabSessionForm`. Line numbers for `SessionSection.tsx` / `SessionSection.test.tsx` below refer to `git show worktree-agent-a4ed6ef7bde8cd175:spa/src/components/SessionSection.tsx`. Before starting, rebase the B3 branch on `origin/main` and confirm `rg -n "NewTabSessionForm|export function HostSessionSection" spa/src/components/SessionSection.tsx` matches.

## Spec deviations / clarifications

1. **Old daemons without `tmux_instance`.** `PinnedTransport.sendKeys` refuses an empty expected generation (`spa/src/lib/rebuild/transport.ts:131-134`). A daemon that does not return `tmux_instance` on create therefore cannot receive the command; the launcher reports "session created but command failed to send" and still attaches (spec failure path 2). No unguarded fallback — B2 deleted `lib/execute-command.ts`.
2. **Send-failure visibility.** Both callers unmount the launcher in `onLaunched`, so an inline message would never be seen. The launcher reports the send failure through the existing global toast (`useUndoToast.getState().show(message)`, `spa/src/stores/useUndoToast.ts`) before calling `onLaunched`. Create failures stay inline (launcher stays open), as specified.
3. **Retry rule.** "On 409, increment and retry up to 5 times" applies to **generated** names only. A typed name (Enter path or project launch with a typed name) keeps today's behaviour: a 409 is an inline error.
4. **Keyboard.** "Arrows move between items, row-major": `ArrowRight`/`ArrowDown` → next item, `ArrowLeft`/`ArrowUp` → previous item in DOM order (project name, then its icons, card by card); `ArrowUp`/`ArrowLeft` on the first item returns focus to the input. True 2-D grid movement is not attempted because card rows have variable item counts.
5. **Placeholder.** "Placeholder shows the name that would be used": Enter needs a typed name (no default exists), so the placeholder shows the rule for project launches: `Session name — blank uses <slug>-N`.
6. **Dropped inputs.** The old forms' `cwd` input and `terminal/stream` mode select are removed (spec: mode `terminal`; stream mode returns later as Nexen mode).
7. **Liveness guards kept.** #1064's New Tab form re-checks host liveness at submit and after create and ignores results after unmount (`SessionSection.tsx` `isHostLive`, `activeRef`). The launcher keeps the unmount guard; `HostSessionSection.onLaunched` keeps the post-create `isHostLive` check.

## Global Constraints

- Every Bash command in a subagent must be prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && `; Edit/Write paths include `.claude/worktrees/host-launcher/`.
- pnpm; tests `cd spa && npx vitest run <file>`; lint `cd spa && pnpm run lint`; build `cd spa && pnpm run build`.
- Parallel subagents commit with `git commit --only <files>`; commits end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Session name rule (daemon, mirrored in `spa/src/lib/session-name.ts`): `^[a-zA-Z0-9_-]+$`.
- Launch sequence (spec §5.2, verbatim): one `pinHost(hostId)` transport; `pinned.createSession(name, project.path, 'terminal')` with the raw stored path (including `~`); then `pinned.sendKeys(session.code, command.command, session.tmux_instance)`; then `onLaunched(session)`.
- Grid: `@container` on the launcher, `grid-cols-2` default, `@md:grid-cols-3`, `@3xl:grid-cols-4` — never fewer than 2 columns.
- Icons: Phosphor only, plus `CommandIconView` from B2.

---

## File Structure

| File | Responsibility |
|---|---|
| `spa/src/lib/launch-session-name.ts` (new) | Pure: next `{slug}-{N}` name from live names |
| `spa/src/lib/session-launch.ts` (new) | Create (with generated-name retry) + guarded send via one pinned transport |
| `spa/src/components/session-launcher/SessionLauncher.tsx` (new) | Name input, project/command grid, keyboard nav, empty/unsupported states |
| `spa/src/components/SessionSection.tsx` (modify, post-#1064) | New Tab host block `+` mounts the launcher |
| `spa/src/components/hosts/SessionsSection.tsx` (modify, post-B2) | `New Session` panel mounts the launcher |
| `spa/src/locales/en.json`, `zh-TW.json` | `launcher.*` keys |

---

### Task 1: Session-name generation (pure)

**Files:**
- Create: `spa/src/lib/launch-session-name.ts`, `spa/src/lib/launch-session-name.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `nextProjectSessionName(slug: string, liveNames: readonly string[], bump?: number): string` — `N = 1 + count(liveNames matching exactly slug or ^slug-\d+$) + bump`, then incremented while `${slug}-${N}` is in `liveNames`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, it, expect } from 'vitest'
import { nextProjectSessionName } from './launch-session-name'

describe('nextProjectSessionName', () => {
  it.each([
    ['no sessions', 'purdex', [], 'purdex-1'],
    ['bare slug counts', 'purdex', ['purdex'], 'purdex-2'],
    ['numbered ones count', 'purdex', ['purdex-1', 'purdex-2'], 'purdex-3'],
    ['others do not count', 'purdex', ['purdex-x', 'purdexy-1', 'dev', 'purdex-1a'], 'purdex-1'],
    ['a gap is not reused but the count is', 'purdex', ['purdex-5'], 'purdex-2'],
    ['increments past a taken candidate', 'purdex', ['purdex', 'purdex-2', 'purdex-3'], 'purdex-4'],
    ['collision chain', 'p', ['p-2', 'p-3', 'p-1'], 'p-4'],
  ])('%s', (_label, slug, live, want) => {
    expect(nextProjectSessionName(slug, live)).toBe(want)
  })

  it('bump advances past a name the daemon just refused', () => {
    expect(nextProjectSessionName('purdex', [], 1)).toBe('purdex-2')
    expect(nextProjectSessionName('purdex', ['purdex-3'], 2)).toBe('purdex-4')
  })

  it('treats regex metacharacters in a slug literally', () => {
    // Slugs are [a-z0-9-] by validation; this guards the helper anyway.
    expect(nextProjectSessionName('a-b', ['a-b-1', 'axb-2'], 0)).toBe('a-b-2')
  })
})
```

Save as `spa/src/lib/launch-session-name.test.ts`.

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/launch-session-name.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

`spa/src/lib/launch-session-name.ts`:

```ts
// The session name a project launch uses when the user typed none
// (host-launcher spec §5.2): `{slug}-{N}`.

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * N = 1 + the number of live sessions named exactly `slug` or `slug-<digits>`;
 * if that name is taken, N increments until free. `bump` is added to the
 * starting N — the launch helper passes the retry count after a 409, when the
 * daemon knew a session the cached list did not.
 */
export function nextProjectSessionName(slug: string, liveNames: readonly string[], bump = 0): string {
  const own = new RegExp(`^${escapeRegExp(slug)}(-\\d+)?$`)
  const taken = new Set(liveNames)
  let n = 1 + liveNames.filter((name) => own.test(name)).length + bump
  while (taken.has(`${slug}-${n}`)) n++
  return `${slug}-${n}`
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/launch-session-name.test.ts`
Expected: PASS (9 tests). Note the "gap" row: `['purdex-5']` counts 1 → candidate `purdex-2`, which is free.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/launch-session-name.ts spa/src/lib/launch-session-name.test.ts && git commit -m "feat(spa): project session name generator for the launcher

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 2: Launch helper `lib/session-launch.ts`

**Files:**
- Create: `spa/src/lib/session-launch.ts`, `spa/src/lib/session-launch.test.ts`

**Interfaces:**
- Consumes: `pinHost`, `PinnedTransport` (`spa/src/lib/rebuild/transport.ts:22-48,84`); `Session` (`spa/src/lib/host-api.ts:7-23`, `tmux_instance?: string`); `isValidSessionName` (`spa/src/lib/session-name.ts`); `useSessionStore.sessions[hostId]`; `nextProjectSessionName` (Task 1); `HostProject`, `HostCommand` (B2 `lib/host-config-api.ts`)
- Produces:
  - `interface LaunchRequest { name: string; project?: HostProject; command?: HostCommand }`
  - `type LaunchOutcome = { status: 'created'; session: Session; sendError?: string } | { status: 'failed'; reason: 'invalid_name' | 'create_failed' | 'host'; error: string }`
  - `interface LaunchDeps { pin?: (hostId: string) => Pick<PinnedTransport, 'createSession' | 'sendKeys'>; liveNames?: (hostId: string) => readonly string[] }`
  - `launchSession(hostId: string, req: LaunchRequest, deps?: LaunchDeps): Promise<LaunchOutcome>` — never throws
  - `LAUNCH_CREATE_ATTEMPTS = 5`

- [ ] **Step 1: Write the failing test**

`spa/src/lib/session-launch.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { launchSession, LAUNCH_CREATE_ATTEMPTS, type LaunchDeps } from './session-launch'
import { HostApiError, type Session } from './host-api'
import { GenerationConflictError } from './rebuild/transport'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import type { HostCommand, HostProject } from './host-config-api'

const H = 'h1'
const PROJECT: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const COMMAND: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }

function session(over: Partial<Session> = {}): Session {
  return { code: 'abc', name: 'purdex-1', cwd: '~/w/purdex', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '111:1000', ...over }
}

function fakePin() {
  const createSession = vi.fn(async (name: string) => session({ name }))
  const sendKeys = vi.fn(async () => {})
  const pin = vi.fn(() => ({ createSession, sendKeys }))
  return { pin, createSession, sendKeys }
}

beforeEach(() => {
  useSessionStore.setState({ sessions: {} })
})

describe('launchSession', () => {
  it('Enter path: typed name, cwd ~, terminal, no send', async () => {
    const f = fakePin()
    const out = await launchSession(H, { name: ' dev ' }, { pin: f.pin })
    expect(f.pin).toHaveBeenCalledWith(H)
    expect(f.createSession).toHaveBeenCalledWith('dev', '~', 'terminal')
    expect(f.sendKeys).not.toHaveBeenCalled()
    expect(out).toMatchObject({ status: 'created', session: { name: 'dev' } })
  })

  it('empty or invalid typed name without a project creates nothing', async () => {
    const f = fakePin()
    expect(await launchSession(H, { name: '  ' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(await launchSession(H, { name: 'a b' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(await launchSession(H, { name: 'bad/x', project: PROJECT }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(f.createSession).not.toHaveBeenCalled()
  })

  it('project + command: generated name, raw project path, guarded send with the created generation', async () => {
    const f = fakePin()
    const out = await launchSession(H, { name: '', project: PROJECT, command: COMMAND }, {
      pin: f.pin, liveNames: () => ['purdex-1', 'other'],
    })
    expect(f.createSession).toHaveBeenCalledWith('purdex-2', '~/w/purdex', 'terminal')
    expect(f.sendKeys).toHaveBeenCalledWith('abc', 'claude', '111:1000')
    expect(out).toEqual({ status: 'created', session: session({ name: 'purdex-2' }) })
  })

  it('project name only: cwd launch, no send; a typed name wins over generation', async () => {
    const f = fakePin()
    await launchSession(H, { name: 'mine', project: PROJECT }, { pin: f.pin })
    expect(f.createSession).toHaveBeenCalledWith('mine', '~/w/purdex', 'terminal')
    expect(f.sendKeys).not.toHaveBeenCalled()
  })

  it('reads live names from the session store by default', async () => {
    useSessionStore.setState({ sessions: { [H]: [session({ name: 'purdex' })] } })
    const f = fakePin()
    await launchSession(H, { name: '', project: PROJECT }, { pin: f.pin })
    expect(f.createSession).toHaveBeenCalledWith('purdex-2', '~/w/purdex', 'terminal')
  })

  it('409 on a generated name retries with the next N, up to the cap', async () => {
    const f = fakePin()
    f.createSession
      .mockRejectedValueOnce(new HostApiError(409, 'Conflict'))
      .mockRejectedValueOnce(new HostApiError(409, 'Conflict'))
    const out = await launchSession(H, { name: '', project: PROJECT }, { pin: f.pin, liveNames: () => [] })
    expect(f.createSession.mock.calls.map(([n]) => n)).toEqual(['purdex-1', 'purdex-2', 'purdex-3'])
    expect(out).toMatchObject({ status: 'created' })

    const g = fakePin()
    g.createSession.mockRejectedValue(new HostApiError(409, 'Conflict'))
    const capped = await launchSession(H, { name: '', project: PROJECT }, { pin: g.pin, liveNames: () => [] })
    expect(g.createSession).toHaveBeenCalledTimes(LAUNCH_CREATE_ATTEMPTS)
    expect(capped).toMatchObject({ status: 'failed', reason: 'create_failed' })
  })

  it('409 on a typed name is not retried; 400/500 are never retried', async () => {
    const f = fakePin()
    f.createSession.mockRejectedValue(new HostApiError(409, 'Conflict'))
    expect(await launchSession(H, { name: 'dev' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'create_failed', error: '409 Conflict' })
    expect(f.createSession).toHaveBeenCalledTimes(1)

    const g = fakePin()
    g.createSession.mockRejectedValue(new HostApiError(500, 'Internal Server Error'))
    await launchSession(H, { name: '', project: PROJECT }, { pin: g.pin, liveNames: () => [] })
    expect(g.createSession).toHaveBeenCalledTimes(1)
  })

  it('a blank code from create is a failure', async () => {
    const f = fakePin()
    f.createSession.mockResolvedValue(session({ code: '' }))
    expect(await launchSession(H, { name: 'dev' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'create_failed' })
  })

  it('send failure keeps the session and reports sendError', async () => {
    const f = fakePin()
    f.sendKeys.mockRejectedValue(new GenerationConflictError('abc', '111:1000'))
    const out = await launchSession(H, { name: '', project: PROJECT, command: COMMAND }, { pin: f.pin, liveNames: () => [] })
    expect(out.status).toBe('created')
    expect(out.status === 'created' && out.sendError).toMatch(/tmux generation/)
  })

  it('a session with no tmux_instance (old daemon) cannot be sent to: sendError, not a throw', async () => {
    const f = fakePin()
    f.createSession.mockResolvedValue(session({ tmux_instance: undefined }))
    f.sendKeys.mockImplementation(async (_code: string, _cmd: string, expected: string) => {
      if (!expected) throw new Error('refusing to send keys to abc without a tmux generation to assert')
    })
    const out = await launchSession(H, { name: 'dev', project: PROJECT, command: COMMAND }, { pin: f.pin })
    expect(f.sendKeys).toHaveBeenCalledWith('abc', 'claude', '')
    expect(out).toMatchObject({ status: 'created', sendError: expect.stringMatching(/tmux generation/) })
  })

  it('an unknown host fails before any request (real pinHost)', async () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    const out = await launchSession('ghost', { name: 'dev' }, {} as LaunchDeps)
    expect(out).toMatchObject({ status: 'failed', reason: 'host' })
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/session-launch.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `spa/src/lib/session-launch.ts`**

```ts
// spa/src/lib/session-launch.ts — create a session for the launcher and run a
// command in it (host-launcher spec §5.2).
//
// Everything goes through ONE `pinHost(hostId)` transport, the rebuild
// engine's: `hostFetch` would resolve an unknown or re-pointed host id to the
// active host, and a command typed for one machine must never run on another.
// The send is the same generation-guarded send-keys the rebuild uses.
import { pinHost, type PinnedTransport } from './rebuild/transport'
import { isValidSessionName } from './session-name'
import { nextProjectSessionName } from './launch-session-name'
import { useSessionStore } from '../stores/useSessionStore'
import type { Session } from './host-api'
import type { HostCommand, HostProject } from './host-config-api'

export const LAUNCH_CREATE_ATTEMPTS = 5

export interface LaunchRequest {
  /** What the user typed; trimmed here. Empty means "generate from the project". */
  name: string
  project?: HostProject
  command?: HostCommand
}

export type LaunchOutcome =
  | { status: 'created'; session: Session; sendError?: string }
  | { status: 'failed'; reason: 'invalid_name' | 'create_failed' | 'host'; error: string }

export interface LaunchDeps {
  pin?: (hostId: string) => Pick<PinnedTransport, 'createSession' | 'sendKeys'>
  liveNames?: (hostId: string) => readonly string[]
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function isDuplicateName(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { status?: unknown }).status === 409
}

const storeLiveNames = (hostId: string): readonly string[] =>
  (useSessionStore.getState().sessions[hostId] ?? []).map((s) => s.name)

/** Never throws: every failure is an outcome the launcher can render. */
export async function launchSession(hostId: string, req: LaunchRequest, deps: LaunchDeps = {}): Promise<LaunchOutcome> {
  const typed = req.name.trim()
  if (typed ? !isValidSessionName(typed) : !req.project) {
    return { status: 'failed', reason: 'invalid_name', error: typed ? 'invalid session name' : 'session name required' }
  }

  let pinned: Pick<PinnedTransport, 'createSession' | 'sendKeys'>
  try {
    pinned = (deps.pin ?? pinHost)(hostId)
  } catch (err) {
    return { status: 'failed', reason: 'host', error: message(err) }
  }

  const cwd = req.project?.path ?? '~'
  const liveNames = (deps.liveNames ?? storeLiveNames)(hostId)
  // A typed name is the user's: one attempt, a duplicate is reported. A
  // generated name may lose a race with a session the cached list missed.
  const attempts = typed ? 1 : LAUNCH_CREATE_ATTEMPTS

  let session: Session | undefined
  let lastError: unknown
  for (let attempt = 0; attempt < attempts; attempt++) {
    const name = typed || nextProjectSessionName(req.project!.slug, liveNames, attempt)
    try {
      session = await pinned.createSession(name, cwd, 'terminal')
      break
    } catch (err) {
      lastError = err
      if (!isDuplicateName(err)) break
    }
  }
  if (!session) return { status: 'failed', reason: 'create_failed', error: message(lastError) }
  if (!session.code) return { status: 'failed', reason: 'create_failed', error: 'empty session code' }

  if (!req.command) return { status: 'created', session }
  try {
    await pinned.sendKeys(session.code, req.command.command, session.tmux_instance ?? '')
    return { status: 'created', session }
  } catch (err) {
    return { status: 'created', session, sendError: message(err) }
  }
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/session-launch.test.ts && npx eslint src/lib/session-launch.ts src/lib/launch-session-name.ts`
Expected: PASS (11 tests); no lint errors.

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/session-launch.ts spa/src/lib/session-launch.test.ts && git commit -m "feat(spa): pinned launch helper for project sessions and commands

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 3: `SessionLauncher` component

**Files:**
- Create: `spa/src/components/session-launcher/SessionLauncher.tsx`, `spa/src/components/session-launcher/SessionLauncher.test.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `launchSession`, `LaunchOutcome` (Task 2); `nextProjectSessionName` (Task 1); `useHostConfigStore`, `EMPTY_HOST_CONFIG` (B2); `CommandIconView` (B2 `spa/src/components/hosts/CommandIconView.tsx`); `isValidSessionName`; `useUndoToast`; `encodeHostRouteId` (`spa/src/lib/host-routes.ts`); wouter `useLocation`
- Produces:
  - `SessionLauncher({ hostId, onLaunched, onCancel, launch? }: { hostId: string; onLaunched: (session: Session) => void; onCancel: () => void; launch?: typeof launchSession })`
  - testids: `launcher` (root, has `@container`), `launcher-name`, `launcher-name-error`, `launcher-grid`, `launcher-project-<id>` (card), `launcher-project-name-<id>` (button), `launcher-command-<projectId>-<commandId>` (button), `launcher-error`, `launcher-empty`, `launcher-empty-link`, `launcher-unsupported`
  - every launchable button carries `data-launch-item`

- [ ] **Step 1: Write the failing test**

`spa/src/components/session-launcher/SessionLauncher.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(), getIconPath: () => 'M0,0',
}))
const setLocation = vi.fn()
vi.mock('wouter', async (importOriginal) => ({
  ...(await importOriginal<typeof import('wouter')>()),
  useLocation: () => ['/', setLocation],
}))

import { SessionLauncher } from './SessionLauncher'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { HostCommand, HostProject } from '../../lib/host-config-api'
import type { Session } from '../../lib/host-api'

const H = 'h1'
const P1: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const P2: HostProject = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/srv/ploom' }
const C1: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const C2: HostCommand = { id: 'c2', name: 'Codex', command: 'codex', icon: { kind: 'agent', value: 'codex' } }
const made: Session = { code: 'new1', name: 'x', cwd: '~', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '1:1' }

const onLaunched = vi.fn()
const onCancel = vi.fn()
const launch = vi.fn()

function seed(status: 'ready' | 'unsupported' = 'ready', projects = [P1, P2], commands = [C1, C2]) {
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry(status), projects, commands } },
    ensureLoaded: vi.fn(async () => {}),
  })
}

function renderLauncher() {
  return render(<SessionLauncher hostId={H} onLaunched={onLaunched} onCancel={onCancel} launch={launch} />)
}

beforeEach(() => {
  onLaunched.mockReset(); onCancel.mockReset(); setLocation.mockReset()
  launch.mockReset().mockResolvedValue({ status: 'created', session: made })
  useSessionStore.setState({ sessions: { [H]: [] } })
  useUndoToast.setState({ toast: null })
  seed()
})

describe('SessionLauncher', () => {
  it('autofocuses the name input and uses a ≥2-column container-query grid', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-name')).toHaveFocus()
    expect(screen.getByTestId('launcher').className).toContain('@container')
    const grid = screen.getByTestId('launcher-grid').className
    expect(grid).toContain('grid-cols-2')
    expect(grid).toContain('@md:grid-cols-3')
    expect(grid).toContain('@3xl:grid-cols-4')
  })

  it('renders every project with name, truncated path (title) and every command icon', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveTextContent('Purdex')
    expect(screen.getByTitle('/srv/ploom')).toBeInTheDocument()
    expect(screen.getByTestId('launcher-command-p2-c2')).toHaveAttribute('title', 'Codex')
    expect(screen.getAllByTestId(/^launcher-command-/)).toHaveLength(4)
  })

  it('Enter in the name input launches name + ~ (no project, no command)', async () => {
    renderLauncher()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'dev' } })
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    await waitFor(() => expect(onLaunched).toHaveBeenCalledWith(made))
    expect(launch).toHaveBeenCalledWith(H, { name: 'dev' })
  })

  it('Enter with an empty or invalid name shows inline validation and launches nothing', () => {
    renderLauncher()
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(screen.getByTestId('launcher-name-error')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'a b' } })
    expect(screen.getByTestId('launcher-name-error')).toBeInTheDocument()
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(launch).not.toHaveBeenCalled()
  })

  it('clicking a command icon launches {project, command} with the typed name', async () => {
    renderLauncher()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'mine' } })
    fireEvent.click(screen.getByTestId('launcher-command-p1-c2'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalled())
    expect(launch).toHaveBeenCalledWith(H, { name: 'mine', project: P1, command: C2 })
  })

  it('clicking a project name launches {project} only', async () => {
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p2'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalled())
    expect(launch).toHaveBeenCalledWith(H, { name: '', project: P2 })
  })

  it('create failure stays open with an inline error', async () => {
    launch.mockResolvedValue({ status: 'failed', reason: 'create_failed', error: '409 Conflict' })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('409 Conflict')
    expect(onLaunched).not.toHaveBeenCalled()
  })

  it('send failure toasts "created but command failed" and still lands in the session', async () => {
    launch.mockResolvedValue({ status: 'created', session: made, sendError: 'boom' })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalledWith(made))
    expect(useUndoToast.getState().toast?.message).toContain('command failed to send')
  })

  it('busy disables every item and a second click does not launch twice', async () => {
    let resolve!: (v: unknown) => void
    launch.mockReturnValue(new Promise((r) => { resolve = r }))
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    expect(screen.getByTestId('launcher-name')).toBeDisabled()
    expect(screen.getByTestId('launcher-project-name-p2')).toBeDisabled()
    await act(async () => { resolve({ status: 'created', session: made }) })
    expect(launch).toHaveBeenCalledTimes(1)
  })

  it('does not call onLaunched after unmount', async () => {
    let resolve!: (v: unknown) => void
    launch.mockReturnValue(new Promise((r) => { resolve = r }))
    const { unmount } = renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    unmount()
    await act(async () => { resolve({ status: 'created', session: made }) })
    expect(onLaunched).not.toHaveBeenCalled()
  })

  it('keyboard: ArrowDown enters the grid, arrows walk items row-major, Enter launches, ArrowUp at the start returns to input, Escape cancels', async () => {
    renderLauncher()
    const input = screen.getByTestId('launcher-name')
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(screen.getByTestId('launcher-command-p1-c1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect(screen.getByTestId('launcher-command-p1-c2')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(screen.getByTestId('launcher-project-name-p2')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(input).toHaveFocus()
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
    // Native buttons turn Enter/Space into click; assert the click path launches.
    fireEvent.click(screen.getByTestId('launcher-command-p1-c2'))
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P1, command: C2 }))
  })

  it('placeholder names the generated-name rule', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-name')).toHaveAttribute('placeholder', expect.stringContaining('<slug>-N'))
  })

  it('empty host: hint + link to Host › Projects; the name input still works', async () => {
    seed('ready', [], [C1])
    renderLauncher()
    expect(screen.getByTestId('launcher-empty')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('launcher-empty-link'))
    expect(setLocation).toHaveBeenCalledWith('/hosts/h1/projects')
    expect(onCancel).toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'dev' } })
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: 'dev' }))
  })

  it('old daemon: unsupported hint, no grid, Enter still creates', () => {
    seed('unsupported', [], [])
    renderLauncher()
    expect(screen.getByTestId('launcher-unsupported')).toBeInTheDocument()
    expect(screen.queryByTestId('launcher-grid')).toBeNull()
    expect(screen.getByTestId('launcher-name')).toBeEnabled()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/session-launcher/SessionLauncher.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `spa/src/components/session-launcher/SessionLauncher.tsx`**

```tsx
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { FolderSimple } from '@phosphor-icons/react'
import { launchSession, type LaunchRequest } from '../../lib/session-launch'
import { isValidSessionName } from '../../lib/session-name'
import { encodeHostRouteId } from '../../lib/host-routes'
import type { Session } from '../../lib/host-api'
import { EMPTY_HOST_CONFIG, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { CommandIconView } from '../hosts/CommandIconView'

interface Props {
  hostId: string
  onLaunched: (session: Session) => void
  onCancel: () => void
  /** Injection point for tests; production uses the pinned launch helper. */
  launch?: typeof launchSession
}

const NEXT_KEYS = new Set(['ArrowDown', 'ArrowRight'])
const PREV_KEYS = new Set(['ArrowUp', 'ArrowLeft'])

export function SessionLauncher({ hostId, onLaunched, onCancel, launch = launchSession }: Props) {
  const t = useI18nStore((s) => s.t)
  const [, setLocation] = useLocation()
  const config = useHostConfigStore((s) => s.byHost[hostId] ?? EMPTY_HOST_CONFIG)
  const [name, setName] = useState('')
  const [triedEnter, setTriedEnter] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const busyRef = useRef(false)
  const aliveRef = useRef(true)
  const rootRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    aliveRef.current = true
    return () => { aliveRef.current = false }
  }, [])

  useEffect(() => {
    void useHostConfigStore.getState().ensureLoaded(hostId)
  }, [hostId])

  const trimmed = name.trim()
  const nameError = trimmed ? (isValidSessionName(trimmed) ? '' : t('tab.rename_invalid_format')) : (triedEnter ? t('launcher.name_required') : '')

  const run = async (req: LaunchRequest) => {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      const outcome = await launch(hostId, req)
      if (!aliveRef.current) return
      if (outcome.status === 'failed') {
        setError(outcome.error)
        return
      }
      if (outcome.sendError) {
        useUndoToast.getState().show(t('launcher.send_failed', { reason: outcome.sendError }))
      }
      onLaunched(outcome.session)
    } finally {
      busyRef.current = false
      if (aliveRef.current) setBusy(false)
    }
  }

  const items = (): HTMLElement[] =>
    Array.from(rootRef.current?.querySelectorAll<HTMLElement>('[data-launch-item]:not(:disabled)') ?? [])

  const handleInputKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      setTriedEnter(true)
      if (!trimmed || !isValidSessionName(trimmed)) return
      void run({ name: trimmed })
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      items()[0]?.focus()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
  }

  const handleGridKey = (e: React.KeyboardEvent<HTMLElement>) => {
    if (e.key === 'Escape') { e.preventDefault(); onCancel(); return }
    const next = NEXT_KEYS.has(e.key)
    if (!next && !PREV_KEYS.has(e.key)) return
    const list = items()
    const index = list.indexOf(e.target as HTMLElement)
    if (index === -1) return
    e.preventDefault()
    if (next) list[Math.min(index + 1, list.length - 1)]?.focus()
    else if (index === 0) inputRef.current?.focus()
    else list[index - 1]?.focus()
  }

  const itemCls = 'cursor-pointer rounded focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50 disabled:cursor-not-allowed'

  return (
    <div ref={rootRef} data-testid="launcher" className="@container flex flex-col gap-2 p-2 bg-surface-secondary border border-border-default rounded-md">
      <input
        ref={inputRef}
        data-testid="launcher-name"
        autoFocus
        value={name}
        disabled={busy}
        spellCheck={false}
        placeholder={t('launcher.name_placeholder')}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={handleInputKey}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary"
      />
      {nameError && <p data-testid="launcher-name-error" className="text-xs text-red-400">{nameError}</p>}
      {error && <p data-testid="launcher-error" className="text-xs text-red-400">{error}</p>}

      {config.status === 'unsupported' ? (
        <p data-testid="launcher-unsupported" className="text-xs text-text-muted">{t('launcher.unsupported')}</p>
      ) : config.projects.length === 0 ? (
        <p data-testid="launcher-empty" className="text-xs text-text-muted">
          {t('launcher.empty')}{' '}
          <button type="button" data-testid="launcher-empty-link" className="text-accent hover:underline cursor-pointer"
            onClick={() => { setLocation(`/hosts/${encodeHostRouteId(hostId)}/projects`); onCancel() }}>
            {t('launcher.empty_link')}
          </button>
        </p>
      ) : (
        <div data-testid="launcher-grid" className="grid grid-cols-2 @md:grid-cols-3 @3xl:grid-cols-4 gap-2" onKeyDown={handleGridKey}>
          {config.projects.map((project) => (
            <div key={project.id} data-testid={`launcher-project-${project.id}`}
              className="min-w-0 flex flex-col gap-1 p-2 rounded border border-border-subtle bg-surface-primary">
              <button type="button" data-launch-item data-testid={`launcher-project-name-${project.id}`} disabled={busy}
                onClick={() => void run({ name: trimmed, project })}
                className={`flex items-center gap-1.5 text-left text-sm font-bold text-text-primary min-w-0 ${itemCls}`}>
                <FolderSimple size={14} className="shrink-0 text-text-secondary" />
                <span className="truncate">{project.name}</span>
              </button>
              <span className="truncate text-xs text-text-muted font-mono" title={project.path}>{project.path}</span>
              {config.commands.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {config.commands.map((command) => (
                    <button key={command.id} type="button" data-launch-item
                      data-testid={`launcher-command-${project.id}-${command.id}`}
                      title={command.name} aria-label={`${project.name} · ${command.name}`} disabled={busy}
                      onClick={() => void run({ name: trimmed, project, command })}
                      className={`p-1 text-text-secondary hover:text-text-primary hover:bg-surface-hover ${itemCls}`}>
                      <CommandIconView icon={command.icon} size={16} />
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
```

Notes for the implementer:
- `run({ name: trimmed, project })` passes `''` for a blank name — matches the test's `{ name: '', project: P2 }`. The Enter path passes `{ name: trimmed }` with no `project` key.
- A typed but invalid name also blocks project launches inside `launchSession` (`invalid_name` → inline error), so the grid needs no extra check.
- The `Escape` handler sits on both the input and the grid, so Escape works from anywhere in the launcher.

- [ ] **Step 4: Locale keys**

en.json:
```json
  "launcher.name_placeholder": "Session name — blank uses <slug>-N",
  "launcher.name_required": "Type a session name, or pick a project below.",
  "launcher.send_failed": "Session created, but the command failed to send: {{reason}}",
  "launcher.empty": "No projects on this host yet.",
  "launcher.empty_link": "Add one in Host › Projects",
  "launcher.unsupported": "This host's daemon is too old for projects — press Enter to create a plain session.",
```
zh-TW.json:
```json
  "launcher.name_placeholder": "Session 名稱 — 留空則用 <slug>-N",
  "launcher.name_required": "請輸入 session 名稱，或從下方選擇專案。",
  "launcher.send_failed": "Session 已建立，但指令送出失敗：{{reason}}",
  "launcher.empty": "此主機尚未設定專案。",
  "launcher.empty_link": "到 主機 › 專案 新增",
  "launcher.unsupported": "此主機的 daemon 版本過舊，不支援專案——按 Enter 可建立一般 session。",
```
The en `send_failed` text contains "command failed to send" (asserted by the test).

- [ ] **Step 5: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/session-launcher/SessionLauncher.test.tsx src/locales && npx eslint src/components/session-launcher && npx tsc -b`
Expected: PASS (14 tests); no lint errors; tsc exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/components/session-launcher spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): session launcher with project and command grid

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 4: Mount the launcher in New Tab host block and Host › Sessions

**Files:**
- Modify: `spa/src/components/SessionSection.tsx` (post-#1064: delete `NewTabSessionForm`; `HostSessionSection` mounts `SessionLauncher`)
- Modify: `spa/src/components/SessionSection.test.tsx` (post-#1064: replace the create-form cases)
- Modify: `spa/src/components/hosts/SessionsSection.tsx` (post-B2: delete `NewSessionDialog`, mount `SessionLauncher`)
- Modify: `spa/src/components/hosts/SessionsSection.test.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json` (remove keys the old forms alone used)

**Interfaces:**
- Consumes: `SessionLauncher` (Task 3)
- Produces: New Tab `+` (`data-testid="new-session-<hostId>"`) toggles the launcher; `onLaunched` attaches `{ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName, tmuxInstance }` exactly as #1064's form did. Host › Sessions `New Session` toggles the launcher; `onLaunched` closes it.

- [ ] **Step 1: Rewrite the New Tab create tests (failing first)**

In `spa/src/components/SessionSection.test.tsx` (post-#1064):
- extend the `host-api` mock is no longer needed for create; add a launcher mock right after the existing `vi.mock` blocks:
```tsx
const launcherProps = vi.hoisted(() => ({ current: null as null | { hostId: string; onLaunched: (s: unknown) => void; onCancel: () => void } }))
vi.mock('./session-launcher/SessionLauncher', () => ({
  SessionLauncher: (props: { hostId: string; onLaunched: (s: unknown) => void; onCancel: () => void }) => {
    launcherProps.current = props
    return <div data-testid={`launcher-stub-${props.hostId}`} />
  },
}))
```
- delete every case from `it('creates a session and attaches it into the current pane'` through `it('disables submit and does not POST when the host goes offline after the form opens'` (they drive `Session Name` / `Create` / `Cancel` of the removed form), keeping `LIVE` and `made` helpers.
- add:
```tsx
  it('+ opens the launcher for that host and toggles it closed', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toBeInTheDocument()
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('a launched session attaches into the current pane with its generation and closes the launcher', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    act(() => launcherProps.current!.onLaunched({ ...made(), tmux_instance: '222:2000' }))
    expect(mockOnSelect).toHaveBeenCalledWith({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'new001', mode: 'terminal', cachedName: 'built', tmuxInstance: '222:2000' })
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('does not attach when the host went offline or was removed before the launch resolved', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    const { onLaunched } = launcherProps.current!
    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } }) })
    act(() => onLaunched(made()))
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  it('onCancel closes the launcher', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    act(() => launcherProps.current!.onCancel())
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })
```
- the existing case "expands a collapsed host when its create button is clicked so the form is visible": replace `expect(screen.getByPlaceholderText('Session Name')).toBeInTheDocument()` with `expect(screen.getByTestId(\`launcher-stub-${HOST_B}\`)).toBeInTheDocument()`.

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/SessionSection.test.tsx`
Expected: FAIL — no `launcher-stub-*` rendered (old form still mounted).

- [ ] **Step 2: Wire `HostSessionSection`**

In `spa/src/components/SessionSection.tsx` (post-#1064):
- imports: remove `useRef, useEffect` from the React import if unused after the deletion (keep `useState`); remove `import { createSession } from '../lib/host-api'`; add `import { SessionLauncher } from './session-launcher/SessionLauncher'`.
- delete the whole `function NewTabSessionForm(...) { … }` component.
- keep `isHostLive` (used below).
- replace the `{isExpanded && creating && ( <NewTabSessionForm … /> )}` block with:
```tsx
      {isExpanded && creating && (
        <div className="mx-3 my-1">
          <SessionLauncher
            hostId={hostId}
            onCancel={() => setCreating(false)}
            onLaunched={(session) => {
              setCreating(false)
              // The host may have dropped or been removed while the launch was
              // in flight; attaching then would bind the pane to a dead host.
              if (!isHostLive(hostId)) return
              onSelect({
                kind: 'tmux-session',
                hostId,
                sessionCode: session.code,
                mode: 'terminal',
                cachedName: session.name,
                // Generation from the create response itself (spec §4.5 of tab
                // rebuild); '' on old daemons, adopted from the next payload.
                tmuxInstance: session.tmux_instance ?? '',
              })
            }}
          />
        </div>
      )}
```
Run the test from Step 1 → PASS.

- [ ] **Step 3: Host › Sessions (failing test first)**

In `spa/src/components/hosts/SessionsSection.test.tsx` add the same `launcherProps` hoisted mock with path `'../session-launcher/SessionLauncher'` and these cases inside `describe('SessionsSection'`:
```tsx
  it('New Session opens the launcher for this host; launching closes it', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('Working Directory')).toBeNull()
    act(() => launcherProps.current!.onLaunched({ ...SESSIONS[0], code: 'new1' }))
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
    expect(mockOpenSingletonTab).not.toHaveBeenCalled() // same as the old dialog: create only
  })

  it('cancel closes the launcher', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    act(() => launcherProps.current!.onCancel())
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })
```
Remove the now-unused `hostFetch` create expectations if any case asserted a POST to `/api/sessions` from the dialog (the header / delete / rename cases keep `hostFetch`).

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/SessionsSection.test.tsx` → FAIL.

In `spa/src/components/hosts/SessionsSection.tsx`:
- delete `/* ─── New Session Dialog ─── */` and `function NewSessionDialog(...) { … }` (lines 26-104 at plan time);
- add `import { SessionLauncher } from '../session-launcher/SessionLauncher'`;
- replace `{showNew && <NewSessionDialog hostId={hostId} onClose={() => setShowNew(false)} />}` with:
```tsx
      {showNew && (
        <div className="mb-4">
          <SessionLauncher hostId={hostId} onLaunched={() => setShowNew(false)} onCancel={() => setShowNew(false)} />
        </div>
      )}
```
- make the header button a toggle: `onClick={() => setShowNew((v) => !v)}`.

Run the test again → PASS.

- [ ] **Step 4: Locale cleanup**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "hosts\.session_cwd|hosts\.create'|t\('hosts\.create'\)" spa/src --glob '!locales/*'`
If `hosts.session_cwd` has no remaining use, delete it from both locale files. Keep `hosts.create` / `hosts.session_name` if still used anywhere (e.g. the Sessions table header uses `hosts.session_name`); delete only keys with zero hits.

- [ ] **Step 5: Run affected suites, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/SessionSection.test.tsx src/components/hosts/SessionsSection.test.tsx src/components/session-launcher src/lib/session-new-tab-providers.test.ts src/locales && npx eslint src/components/SessionSection.tsx src/components/hosts/SessionsSection.tsx && npx tsc -b`
Expected: PASS; no lint errors; tsc exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/components/SessionSection.tsx spa/src/components/SessionSection.test.tsx spa/src/components/hosts/SessionsSection.tsx spa/src/components/hosts/SessionsSection.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): use the session launcher for New Tab + and Host > Sessions

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Full verification

**Files:** none unless a check fails.

- [ ] **Step 1:** `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run` — Expected: 0 failures.
- [ ] **Step 2:** `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run lint` — Expected: exit 0.
- [ ] **Step 3:** `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run build` — Expected: success; confirm Tailwind emitted the container variants: `rg -c "@container|min-width:\s*28rem|min-width:\s*48rem" dist/assets/*.css` prints a non-zero count (`@md` = 28rem, `@3xl` = 48rem in Tailwind 4).
- [ ] **Step 4:** `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "NewTabSessionForm|NewSessionDialog" spa/src` — Expected: no output.
- [ ] **Step 5: Manual smoke** (dev server, a host on the B1 daemon with ≥3 projects and 2 commands):
  1. New Tab → host `+`: input focused; narrow window shows 2 columns, wide shows 3–4.
  2. Type `dev`, Enter → pane attaches to `dev` in `~`.
  3. Blank name, click a project's Claude icon → session `<slug>-1` in the project path, `claude` runs; again → `<slug>-2`.
  4. Keyboard: ArrowDown, arrows, Enter launches the focused item; Escape closes.
  5. Host › Sessions → New Session → click a project name → launcher closes, session appears in the table with the project cwd.
  6. Host with no projects → hint + link opens Host › Projects. Old-daemon host → unsupported hint, Enter still creates.
  7. Stop tmux on the host between create and send is hard to time; instead use an old daemon without `tmux_instance` and a command icon → toast "command failed to send", pane still attaches.

---

## Self-review

**Spec coverage (§5):**
- §5.1 component path/props, two consumers, layout (autofocus input, placeholder, container-query grid ≥2 cols, card name/path/icons with tooltips, empty + unsupported states) → Task 3; consumers → Task 4.
- §5.2 Enter path identical to old submit (name, `~`, terminal; empty/invalid inline) → Tasks 2-3; click icon / click name → Task 3; keyboard (ArrowDown into grid, arrows, Enter/Space, Escape) → Task 3 (deviation 4); name generation + 409 retry ×5 → Tasks 1-2 (deviation 3); pinned create → guarded send → onLaunched, failure paths → Task 2 + Task 3 (deviations 1-2); busy disables inputs → Task 3.
- §5.3 tests: name table tests (Task 1), helper success/409/send failure (Task 2), component Enter / icon / name / keyboard / empty / unsupported / ≥2-column class (Task 3).

**Type consistency:** `launchSession(hostId, LaunchRequest, LaunchDeps?) → LaunchOutcome` is used identically in Tasks 2-3; `SessionLauncher` props `{ hostId, onLaunched(session: Session), onCancel, launch? }` match both consumers in Task 4; `nextProjectSessionName(slug, liveNames, bump)` matches Task 2's call.

**Open questions:** (1) should a typed name that collides (409) auto-suffix like generated names? Plan keeps today's behaviour (error). (2) Should Host › Sessions `onLaunched` also open the new session in a tab? Spec says "same as today" (close only); kept.

