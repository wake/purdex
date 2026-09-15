# Host Config B2 — SPA Settings Pages, Resume Rewire, Removals — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the SPA a daemon-backed per-host config cache (projects / commands / resume templates), Host › Projects / Commands / Snapshots pages, per-host resume template lookup for the rebuild engine, and remove the quick-command system and the global Commands / Snapshot settings pages.

**Architecture:** `lib/host-config-api.ts` wraps the B1 HTTP contract; `stores/useHostConfigStore.ts` is an unpersisted, unsynced cache keyed by host id, loaded on connect (`lib/host-config-loader.ts`, started from `main.tsx` like `startDeviceStateUploader`) and on host page mount. Resume templates become `lib/resume-templates.ts` (pure defaults + per-host lookups). The global snapshot page is split into a host-scoped section plus a client-scoped block shown on the dev host's page.

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest + Testing Library, Phosphor icons (existing build-time icon pipeline), pnpm.

**Spec:** `docs/superpowers/specs/2026-09-16-host-projects-commands-launcher-design.md` (§4). HTTP contract: `docs/superpowers/plans/2026-09-16-host-config-b1-daemon.md` Task 4 "Produces".

## Spec deviations / clarifications

1. **Phosphor catalog (spec §4.3 icon picker + spike).** The repo already ships a build-time pipeline: `spa/scripts/generate-icon-data.mjs` (run by `predev` / `prebuild`) reads `@phosphor-icons/core` and writes `spa/src/features/workspace/generated/icon-meta.json` (`{n: PascalName, t: tags, c: categories}[]`, committed) plus `spa/public/icons/{weight}.json` (path data, git-ignored, fetched lazily by `features/workspace/lib/icon-path-cache.ts`). `WorkspaceIcon` / `WorkspaceIconPicker` render through it. B2 reuses this instead of `import('@phosphor-icons/react')`: the full icon set never enters any JS chunk (path data is a runtime JSON fetch), so the "not in main bundle" goal is met more strongly. Consequently `@phosphor-icons/core` stays a **devDependency** (only the build script uses it). Task 1 verifies this and records the decision; the spec's fallback path is used only if the pipeline is broken.
2. **Connect-triggered load (§4.1).** There is no single shared "host connected" hook. The loader follows the existing pattern in `lib/device-state/uploader.ts:150` — a module-level `useHostStore.subscribe` watching `runtime[hostId].status` transitions to `'connected'` — started once in `spa/src/main.tsx` next to `startDeviceStateUploader()`.
3. **Unknown host guard.** `hostFetch` falls back to the ACTIVE host for an unknown host id (`lib/rebuild/transport.ts:4-10`). `host-config-api.ts` therefore refuses unknown host ids before fetching, and the engine calls `ensureLoaded` only after `pinHost` succeeded (keeps `engine.test.ts` "never contacts the active host" true).
4. **`publishRefusal` (engine.ts:148-171)** stores a display-only `resumeCommand` for an operation that never started; it uses `resumeLookupFor(hostId)` synchronously (defaults if not yet loaded) rather than awaiting.
5. **`RebuildActionSet`.** `binding` is optional in its props (`RebuildActionSet.tsx:57`); the only production caller (`TerminatedPane.tsx:80-86`) always passes it. Lookup uses `binding?.hostId ?? ''`; an empty host id answers from defaults.
6. **Snapshots split (§4.3).** Spec lists tabs block / restore tab layout / restore all / DeviceStateSection as client-scoped. **Capture** (captures every host) and **Undo** (restores the whole `-prev` world) are client-scoped too and move into that block. The per-tab records "Rebuild all" is also host-scoped: `runBatchRebuild` gains `options.hostId` so host A's page never rebuilds host B's panes.
7. **Task ordering vs. spec §4.2 "delete useResumeTemplateStore".** To keep the app building after every task, Task 3 rewires every consumer except `ResumeTemplateSettings`; Task 4 retargets that component and then deletes the store, its sync registration and the storage key.
8. **`lib/execute-command.ts`** is deleted in Task 9: B3's launcher sends through `pinHost(...).sendKeys` (guarded), not through it.
9. **`resume_template.limit_global` / `resume_template.test_against`** are replaced by `resume_template.limit_host` (templates are now per host; no host picker).

## Global Constraints

- Every Bash command in a subagent must be prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && `. Edit/Write paths must include `.claude/worktrees/host-launcher/`.
- Package manager: pnpm. Tests `cd spa && npx vitest run <file>`; lint `cd spa && pnpm run lint`; build `cd spa && pnpm run build`.
- Parallel subagents in this worktree commit with `git commit --only <files>`.
- Commits end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Icons: Phosphor only (plus existing `lib/agent-icons.tsx` brand SVGs).
- Host config is **not persisted and not synced**; the daemon is SOT. No migration of `purdex-quick-commands` / `purdex-resume-templates`.
- Validation mirrors B1 (verbatim): id `^[A-Za-z0-9_-]{1,64}$` unique; project name trimmed 1–64 runes; slug `^[a-z0-9][a-z0-9-]{0,31}$` unique; path trimmed 1–1024 bytes, starts with `/` or is `~` or starts with `~/`, no NUL; command name trimmed 1–64 runes; command 1–4096 bytes no NUL; icon kind `agent` (value ∈ cc-bot, cc-star, openai, codex, opencode) or `phosphor` (value `^[A-Z][A-Za-z0-9]{0,63}$`); max 200 items per list.
- HTTP contract (B1 Task 4): `GET /api/hostconfig` → `{projects:{items,revision}, commands:{items,revision}, resumeTemplates:{items,revision}}`; `PUT /api/hostconfig/{projects|commands|resume-templates}` body `{items, baseRevision}` → 200 `{items, revision}` | 400 text | 409 `{items, revision}` | 413; `POST /api/hostconfig/check-path` `{path}` → `{status:'dir'|'not_dir'|'missing'|'error', resolved, reason?}` | 400. Old daemon → 404.
- Locale files are flat-key JSON (`spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`); `locale-completeness.test.ts` requires identical key sets and non-empty values.

---

## File Structure

| File | Responsibility |
|---|---|
| `spa/src/lib/host-config-api.ts` (new) | Types + fetch wrappers for the B1 contract, typed errors |
| `spa/src/stores/useHostConfigStore.ts` (new) | Per-host cache: load / ensureLoaded / save* / forget |
| `spa/src/lib/host-config-loader.ts` (new) | Load on connect, forget on host removal / endpoint change |
| `spa/src/lib/host-config-validate.ts` (new) | Client validation mirroring §3.3, `suggestSlug`, `moveItem`, `newConfigId` |
| `spa/src/lib/resume-templates.ts` (new) | Defaults, pair type, pure lookup, `resumeLookupFor`, `useResumeTemplateLookup` |
| `spa/src/lib/command-word.ts` (new) | `commandWordOf` (moved from ResumeTemplateSettings) |
| `spa/src/lib/command-icons.ts` (new) | Agent icon value → component map |
| `spa/src/components/settings/ShellVerdict.tsx` (new) | Shared shell-resolve verdict rendering |
| `spa/src/components/hosts/CommandIconView.tsx` / `CommandIconPicker.tsx` (new) | Render / pick a `CommandIcon` |
| `spa/src/components/hosts/HostConfigNotice.tsx` (new) | `useHostConfigGate(hostId)` + inline notice |
| `spa/src/components/hosts/usePathCheck.ts` (new) | Debounced `check-path` hook |
| `spa/src/components/hosts/ProjectsSection.tsx`, `ProjectEditDialog.tsx` (new) | Host › Projects |
| `spa/src/components/hosts/CommandsSection.tsx`, `CommandEditDialog.tsx` (new) | Host › Commands (Normal / Resume) |
| `spa/src/lib/snapshot/filter.ts` (new) | `filterSnapshotByHost` |
| `spa/src/components/settings/snapshot/{shared,RebuildRecordsBlock,TmuxBlock,TabsBlock,ClientSnapshotBlock}.tsx` (new, split from `SnapshotSettingsSection.tsx`) | Snapshot building blocks |
| `spa/src/components/hosts/SnapshotsSection.tsx` (new) | Host › Snapshots container |
| Deleted | see Tasks 4, 8, 9 |

---

### Task 1: Install + Phosphor icon pipeline spike (decision recorded)

**Files:**
- Modify: `docs/superpowers/plans/2026-09-16-host-config-b2-spa-settings.md` (fill the "Spike result" block at the end of this task)

**Interfaces:**
- Consumes: nothing
- Produces: confirmed facts used by Task 5 — `icon-meta.json` shape `{ n: string; t: string[]; c: string[] }[]`; `prefetchWeight('regular')` / `getIconPath(name, 'regular')` from `spa/src/features/workspace/lib/icon-path-cache.ts`; baseline main-bundle gzip size.

- [ ] **Step 1: Install dependencies in the worktree** (the worktree has no `node_modules`; the pnpm workspace root is the repo root: `pnpm-workspace.yaml` lists `spa`, `electron`)

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && pnpm install --frozen-lockfile`
Expected: exits 0; `ls spa/node_modules/@phosphor-icons` prints `core react`.

- [ ] **Step 2: Confirm the `@phosphor-icons/core` metadata export**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && node --input-type=module -e "const m = await import('./node_modules/@phosphor-icons/core/dist/index.mjs'); console.log(Object.keys(m).join(','), m.icons.length, JSON.stringify(m.icons.find(i => i.pascal_name === 'Terminal')))"
```
Expected: keys include `icons` (plus `IconStyle,IconCategory,FigmaCategory`), a count > 1000, and an entry with `pascal_name:"Terminal"` and a `tags` array.

- [ ] **Step 3: Confirm the generator runs and outputs both artifacts**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run generate:icons && ls -la public/icons/regular.json src/features/workspace/generated/icon-meta.json && git status --short src/features/workspace/generated`
Expected: both files exist; `git status` shows no change to `icon-meta.json` (committed output is current). `public/icons/*.json` is ignored (`spa/.gitignore:16`).

- [ ] **Step 4: Record the baseline main bundle size and whether icon-meta is already in it**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run build >/tmp/b2-build.log 2>&1; tail -5 /tmp/b2-build.log; MAIN=$(ls -S dist/assets/index-*.js | head -1); echo "main=$MAIN gzip=$(gzip -c "$MAIN" | wc -c)"; rg -l '"AddressBookTabs"' dist/assets/*.js || echo "icon-meta not in any chunk"
```
Expected: build succeeds; note the `gzip=` byte count and which chunk (if any) contains icon-meta.

- [ ] **Step 5: Record the decision** — replace the block below with the measured values.

```markdown
**Spike result (Task 1):**
- `@phosphor-icons/core@2.1.1` exports `icons: IconEntry[]` (`name`, `pascal_name`, `tags`, `categories`, …); count = <N>.
- Existing pipeline OK: `generate:icons` writes `icon-meta.json` (committed) and `public/icons/{weight}.json` (runtime fetch).
- Baseline main chunk gzip = <bytes>; icon-meta currently in chunk: <file or "none">.
- Decision: CommandIconPicker lazy-imports `icon-meta.json` via `import()` and CommandIconView renders via `icon-path-cache` (`regular` weight). No `import('@phosphor-icons/react')`. `@phosphor-icons/core` stays in devDependencies. Budget check (< 20 KB gzip growth) re-measured in Task 10.
```

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add docs/superpowers/plans/2026-09-16-host-config-b2-spa-settings.md && git commit -m "docs(plan): record B2 phosphor icon spike result

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 2: host-config API wrappers, `useHostConfigStore`, connect loader

**Files:**
- Create: `spa/src/lib/host-config-api.ts`, `spa/src/lib/host-config-api.test.ts`
- Create: `spa/src/stores/useHostConfigStore.ts`, `spa/src/stores/useHostConfigStore.test.ts`
- Create: `spa/src/lib/host-config-loader.ts`, `spa/src/lib/host-config-loader.test.ts`
- Modify: `spa/src/main.tsx:9,26` (import + `startHostConfigLoader()` after `startDeviceStateUploader()`)

**Interfaces:**
- Consumes: `hostFetch` (`spa/src/lib/host-api.ts:84`), `useHostStore` (`hosts`, `runtime[hostId].status`)
- Produces:
  - `type AgentIconValue = 'cc-bot' | 'cc-star' | 'openai' | 'codex' | 'opencode'`
  - `type CommandIcon = { kind: 'agent'; value: AgentIconValue } | { kind: 'phosphor'; value: string }`
  - `interface HostProject { id: string; name: string; slug: string; path: string }`
  - `interface HostCommand { id: string; name: string; command: string; icon: CommandIcon }`
  - `type ResumeTemplateOverrides = Record<string, { exact: string; fallback: string }>`
  - `interface Versioned<T> { items: T; revision: number }`
  - `interface HostConfigPayload { projects: Versioned<HostProject[]>; commands: Versioned<HostCommand[]>; resumeTemplates: Versioned<ResumeTemplateOverrides> }`
  - `type PathCheckStatus = 'dir' | 'not_dir' | 'missing' | 'error' | 'unverifiable'`; `interface PathCheck { status: PathCheckStatus; resolved: string; reason?: string }`
  - `class HostConfigApiError extends Error { status: number }`, `class HostConfigConflictError extends Error { current: Versioned<unknown> }`
  - `fetchHostConfig(hostId): Promise<HostConfigPayload>` (404 → `HostConfigApiError` status 404)
  - `putHostConfig<C extends 'projects'|'commands'|'resume-templates'>(hostId, collection: C, items, baseRevision): Promise<Versioned<...>>` (409 → `HostConfigConflictError`)
  - `checkHostPath(hostId, path, signal?): Promise<PathCheck>` (404 / network → `{status:'unverifiable', resolved:''}`)
  - `type HostConfigStatus = 'idle'|'loading'|'ready'|'unsupported'|'error'`
  - `interface HostConfigEntry { status; projects: HostProject[]; commands: HostCommand[]; resumeTemplates: ResumeTemplateOverrides; revisions: { projects: number; commands: number; resumeTemplates: number }; error?: string }`
  - `emptyHostConfigEntry(status?: HostConfigStatus): HostConfigEntry`, `EMPTY_HOST_CONFIG: HostConfigEntry` (frozen, status `'idle'`)
  - `useHostConfigStore` state `{ byHost: Record<string, HostConfigEntry>; load(hostId): Promise<void>; ensureLoaded(hostId): Promise<void>; saveProjects(hostId, items): Promise<void>; saveCommands(hostId, items): Promise<void>; saveResumeTemplates(hostId, items): Promise<void>; forget(hostId): void }` — `load` / `ensureLoaded` never throw; `save*` throw `HostConfigConflictError` (after replacing local with server copy) or `HostConfigApiError` / `Error`.
  - `startHostConfigLoader(): () => void`

- [ ] **Step 1: Write the failing API tests**

`spa/src/lib/host-config-api.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  checkHostPath,
  fetchHostConfig,
  HostConfigApiError,
  HostConfigConflictError,
  putHostConfig,
} from './host-config-api'
import { useHostStore } from '../stores/useHostStore'

const H = 'h1'

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  vi.unstubAllGlobals()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0 } },
    hostOrder: [H],
    activeHostId: H,
  })
})

describe('fetchHostConfig', () => {
  it('GETs /api/hostconfig with auth and returns the payload', async () => {
    const payload = {
      projects: { items: [{ id: 'p1', name: 'P', slug: 'p', path: '~/p' }], revision: 2 },
      commands: { items: [], revision: 0 },
      resumeTemplates: { items: {}, revision: 0 },
    }
    const fetchMock = vi.fn(async () => json(payload))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchHostConfig(H)).resolves.toEqual(payload)
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/hostconfig')
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok')
  })

  it('maps 404 (old daemon) to HostConfigApiError status 404', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('404 page not found', { status: 404 })))
    await expect(fetchHostConfig(H)).rejects.toMatchObject({ name: 'HostConfigApiError', status: 404 })
  })

  it('refuses an unknown host without fetching (hostFetch would fall back to the active host)', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchHostConfig('ghost')).rejects.toBeInstanceOf(HostConfigApiError)
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('putHostConfig', () => {
  it('PUTs items + baseRevision and returns the stored copy', async () => {
    const fetchMock = vi.fn(async () => json({ items: [], revision: 4 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(putHostConfig(H, 'commands', [], 3)).resolves.toEqual({ items: [], revision: 4 })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/hostconfig/commands')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(String(init.body))).toEqual({ items: [], baseRevision: 3 })
  })

  it('409 → HostConfigConflictError carrying the server copy', async () => {
    const current = { items: { cc: { exact: 'x {id}', fallback: 'x' } }, revision: 9 }
    vi.stubGlobal('fetch', vi.fn(async () => json(current, 409)))
    const err = await putHostConfig(H, 'resume-templates', {}, 1).catch((e) => e)
    expect(err).toBeInstanceOf(HostConfigConflictError)
    expect((err as HostConfigConflictError).current).toEqual(current)
  })

  it('400 → HostConfigApiError with the daemon reason as message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('slug "a b" invalid', { status: 400 })))
    await expect(putHostConfig(H, 'projects', [], 0)).rejects.toMatchObject({ status: 400, message: 'slug "a b" invalid' })
  })
})

describe('checkHostPath', () => {
  it('POSTs the path and returns the verdict', async () => {
    const fetchMock = vi.fn(async () => json({ status: 'dir', resolved: '/Users/wake/w' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(checkHostPath(H, '~/w')).resolves.toEqual({ status: 'dir', resolved: '/Users/wake/w' })
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(JSON.parse(String(init.body))).toEqual({ path: '~/w' })
  })

  it('404 and network failure are unverifiable, not errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })))
    await expect(checkHostPath(H, '/x')).resolves.toEqual({ status: 'unverifiable', resolved: '' })
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    await expect(checkHostPath(H, '/x')).resolves.toEqual({ status: 'unverifiable', resolved: '' })
  })

  it('400 (relative path) is reported as error with the reason', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('path must be absolute', { status: 400 })))
    await expect(checkHostPath(H, 'rel')).resolves.toEqual({ status: 'error', resolved: '', reason: 'path must be absolute' })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-api.test.ts`
Expected: FAIL — `Failed to resolve import "./host-config-api"`.

- [ ] **Step 3: Implement `spa/src/lib/host-config-api.ts`**

```ts
// spa/src/lib/host-config-api.ts — the daemon `hostconfig` module's HTTP
// contract (B1 Task 4). The daemon is the source of truth; nothing here caches.
import { hostFetch } from './host-api'
import { useHostStore } from '../stores/useHostStore'

export type AgentIconValue = 'cc-bot' | 'cc-star' | 'openai' | 'codex' | 'opencode'

export type CommandIcon =
  | { kind: 'agent'; value: AgentIconValue }
  | { kind: 'phosphor'; value: string }

export interface HostProject { id: string; name: string; slug: string; path: string }
export interface HostCommand { id: string; name: string; command: string; icon: CommandIcon }
export type ResumeTemplateOverrides = Record<string, { exact: string; fallback: string }>

export interface Versioned<T> { items: T; revision: number }

export interface HostConfigPayload {
  projects: Versioned<HostProject[]>
  commands: Versioned<HostCommand[]>
  resumeTemplates: Versioned<ResumeTemplateOverrides>
}

export type PathCheckStatus = 'dir' | 'not_dir' | 'missing' | 'error' | 'unverifiable'
export interface PathCheck { status: PathCheckStatus; resolved: string; reason?: string }

interface CollectionItems {
  projects: HostProject[]
  commands: HostCommand[]
  'resume-templates': ResumeTemplateOverrides
}
export type HostConfigCollection = keyof CollectionItems

/** Non-2xx (status = HTTP status) or a refused request (status = 0). */
export class HostConfigApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'HostConfigApiError'
    this.status = status
  }
}

/** PUT lost the compare-and-set: `current` is the daemon's copy. */
export class HostConfigConflictError extends Error {
  readonly current: Versioned<unknown>
  constructor(current: Versioned<unknown>) {
    super('host config changed elsewhere')
    this.name = 'HostConfigConflictError'
    this.current = current
  }
}

/**
 * `hostFetch` resolves an unknown host id to the ACTIVE host's address
 * (`useHostStore.getDaemonBase`). Host config written to the wrong daemon is
 * silent corruption, so an unknown id never reaches the network.
 */
function assertKnownHost(hostId: string): void {
  if (!useHostStore.getState().hosts[hostId]) {
    throw new HostConfigApiError(0, `host ${hostId} is not configured`)
  }
}

async function failure(res: Response): Promise<HostConfigApiError> {
  let text = ''
  try { text = (await res.text()).trim() } catch { /* body unreadable */ }
  return new HostConfigApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
}

export async function fetchHostConfig(hostId: string): Promise<HostConfigPayload> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig')
  if (!res.ok) throw await failure(res)
  return (await res.json()) as HostConfigPayload
}

export async function putHostConfig<C extends HostConfigCollection>(
  hostId: string,
  collection: C,
  items: CollectionItems[C],
  baseRevision: number,
): Promise<Versioned<CollectionItems[C]>> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, `/api/hostconfig/${collection}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) {
    throw new HostConfigConflictError((await res.json()) as Versioned<unknown>)
  }
  if (!res.ok) throw await failure(res)
  return (await res.json()) as Versioned<CollectionItems[C]>
}

const UNVERIFIABLE: PathCheck = { status: 'unverifiable', resolved: '' }

/** Advice only: never throws, so a check can never block a save. */
export async function checkHostPath(hostId: string, path: string, signal?: AbortSignal): Promise<PathCheck> {
  if (!useHostStore.getState().hosts[hostId]) return UNVERIFIABLE
  let res: Response
  try {
    res = await hostFetch(hostId, '/api/hostconfig/check-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
      signal,
    })
  } catch {
    return UNVERIFIABLE
  }
  if (res.status === 400) {
    const reason = (await res.text().catch(() => '')).trim()
    return { status: 'error', resolved: '', reason }
  }
  if (!res.ok) return UNVERIFIABLE
  return (await res.json()) as PathCheck
}
```

- [ ] **Step 4: Run API tests to verify pass**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-api.test.ts`
Expected: PASS (8 tests).

- [ ] **Step 5: Write the failing store tests**

`spa/src/stores/useHostConfigStore.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { emptyHostConfigEntry, useHostConfigStore } from './useHostConfigStore'
import * as api from '../lib/host-config-api'
import { HostConfigApiError, HostConfigConflictError } from '../lib/host-config-api'

vi.mock('../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-config-api')>()),
  fetchHostConfig: vi.fn(),
  putHostConfig: vi.fn(),
}))

const H = 'h1'
const payload = {
  projects: { items: [{ id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }], revision: 3 },
  commands: { items: [], revision: 0 },
  resumeTemplates: { items: { cc: { exact: 'cld --resume {id}', fallback: 'cld -c' } }, revision: 1 },
}

beforeEach(() => {
  vi.mocked(api.fetchHostConfig).mockReset()
  vi.mocked(api.putHostConfig).mockReset()
  useHostConfigStore.setState({ byHost: {} })
})

describe('load', () => {
  it('stores items and revisions and becomes ready', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.status).toBe('ready')
    expect(e.projects).toEqual(payload.projects.items)
    expect(e.resumeTemplates).toEqual(payload.resumeTemplates.items)
    expect(e.revisions).toEqual({ projects: 3, commands: 0, resumeTemplates: 1 })
  })

  it('404 → unsupported; never throws', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new HostConfigApiError(404, 'nope'))
    await expect(useHostConfigStore.getState().load(H)).resolves.toBeUndefined()
    expect(useHostConfigStore.getState().byHost[H].status).toBe('unsupported')
  })

  it('other failures → error with message; never throws', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new Error('boom'))
    await useHostConfigStore.getState().load(H)
    expect(useHostConfigStore.getState().byHost[H]).toMatchObject({ status: 'error', error: 'boom' })
  })

  it('dedupes concurrent loads for one host', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await Promise.all([useHostConfigStore.getState().load(H), useHostConfigStore.getState().load(H)])
    expect(api.fetchHostConfig).toHaveBeenCalledTimes(1)
  })
})

describe('ensureLoaded', () => {
  it('does nothing when already ready or unsupported', async () => {
    useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('ready'), h2: emptyHostConfigEntry('unsupported') } })
    await useHostConfigStore.getState().ensureLoaded(H)
    await useHostConfigStore.getState().ensureLoaded('h2')
    expect(api.fetchHostConfig).not.toHaveBeenCalled()
  })

  it('loads an idle or errored host and swallows failures', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new Error('down'))
    await expect(useHostConfigStore.getState().ensureLoaded(H)).resolves.toBeUndefined()
    expect(useHostConfigStore.getState().byHost[H].status).toBe('error')
  })
})

describe('save*', () => {
  beforeEach(async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
  })

  it('PUTs with the current revision and stores the returned copy', async () => {
    const next = [{ id: 'p2', name: 'B', slug: 'b', path: '/b' }]
    vi.mocked(api.putHostConfig).mockResolvedValue({ items: next, revision: 4 })
    await useHostConfigStore.getState().saveProjects(H, next)
    expect(api.putHostConfig).toHaveBeenCalledWith(H, 'projects', next, 3)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.projects).toEqual(next)
    expect(e.revisions.projects).toBe(4)
  })

  it('409 replaces local with the server copy and rethrows the conflict', async () => {
    const server = { items: { codex: { exact: 'cx {id}', fallback: 'cx' } }, revision: 7 }
    vi.mocked(api.putHostConfig).mockRejectedValue(new HostConfigConflictError(server))
    await expect(useHostConfigStore.getState().saveResumeTemplates(H, {})).rejects.toBeInstanceOf(HostConfigConflictError)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.resumeTemplates).toEqual(server.items)
    expect(e.revisions.resumeTemplates).toBe(7)
  })

  it('refuses to save a host that is not ready', async () => {
    await expect(useHostConfigStore.getState().saveCommands('h-unloaded', [])).rejects.toThrow(/not loaded/)
    expect(api.putHostConfig).not.toHaveBeenCalled()
  })
})

it('forget drops the host entry', () => {
  useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('ready') } })
  useHostConfigStore.getState().forget(H)
  expect(useHostConfigStore.getState().byHost[H]).toBeUndefined()
})
```

- [ ] **Step 6: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/stores/useHostConfigStore.test.ts`
Expected: FAIL — cannot resolve `./useHostConfigStore`.

- [ ] **Step 7: Implement `spa/src/stores/useHostConfigStore.ts`**

```ts
// spa/src/stores/useHostConfigStore.ts — per-host cache of the daemon's
// projects / commands / resume templates (spec §4.1). Not persisted, not
// synced: the daemon is the source of truth and every write is a CAS.
import { create } from 'zustand'
import {
  fetchHostConfig,
  HostConfigApiError,
  HostConfigConflictError,
  putHostConfig,
  type HostCommand,
  type HostConfigCollection,
  type HostProject,
  type ResumeTemplateOverrides,
} from '../lib/host-config-api'

export type HostConfigStatus = 'idle' | 'loading' | 'ready' | 'unsupported' | 'error'

export interface HostConfigEntry {
  status: HostConfigStatus
  projects: HostProject[]
  commands: HostCommand[]
  resumeTemplates: ResumeTemplateOverrides
  revisions: { projects: number; commands: number; resumeTemplates: number }
  error?: string
}

export function emptyHostConfigEntry(status: HostConfigStatus = 'idle'): HostConfigEntry {
  return { status, projects: [], commands: [], resumeTemplates: {}, revisions: { projects: 0, commands: 0, resumeTemplates: 0 } }
}

/** Stable fallback for selectors — a fresh object per call would loop useSyncExternalStore. */
export const EMPTY_HOST_CONFIG: HostConfigEntry = Object.freeze(emptyHostConfigEntry()) as HostConfigEntry

type RevisionKey = keyof HostConfigEntry['revisions']

interface HostConfigState {
  byHost: Record<string, HostConfigEntry>
  load: (hostId: string) => Promise<void>
  ensureLoaded: (hostId: string) => Promise<void>
  saveProjects: (hostId: string, items: HostProject[]) => Promise<void>
  saveCommands: (hostId: string, items: HostCommand[]) => Promise<void>
  saveResumeTemplates: (hostId: string, items: ResumeTemplateOverrides) => Promise<void>
  forget: (hostId: string) => void
}

const inflight = new Map<string, Promise<void>>()

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export const useHostConfigStore = create<HostConfigState>()((set, get) => {
  const patch = (hostId: string, update: Partial<HostConfigEntry>) =>
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? emptyHostConfigEntry()), ...update } } }))

  async function save<C extends HostConfigCollection>(
    hostId: string,
    collection: C,
    field: 'projects' | 'commands' | 'resumeTemplates',
    items: Parameters<typeof putHostConfig<C>>[2],
  ): Promise<void> {
    const entry = get().byHost[hostId]
    if (!entry || entry.status !== 'ready') throw new Error(`host config for ${hostId} is not loaded`)
    const revKey: RevisionKey = field
    try {
      const stored = await putHostConfig(hostId, collection, items, entry.revisions[revKey])
      const now = get().byHost[hostId] ?? entry
      patch(hostId, { [field]: stored.items, revisions: { ...now.revisions, [revKey]: stored.revision } })
    } catch (err) {
      if (err instanceof HostConfigConflictError) {
        const now = get().byHost[hostId] ?? entry
        patch(hostId, { [field]: err.current.items, revisions: { ...now.revisions, [revKey]: err.current.revision } })
      }
      throw err
    }
  }

  return {
    byHost: {},

    load: (hostId) => {
      const running = inflight.get(hostId)
      if (running) return running
      const run = (async () => {
        // A refresh of a ready host keeps showing its data while it loads.
        if (get().byHost[hostId]?.status !== 'ready') patch(hostId, { status: 'loading', error: undefined })
        try {
          const p = await fetchHostConfig(hostId)
          patch(hostId, {
            status: 'ready',
            error: undefined,
            projects: p.projects.items ?? [],
            commands: p.commands.items ?? [],
            resumeTemplates: p.resumeTemplates.items ?? {},
            revisions: { projects: p.projects.revision, commands: p.commands.revision, resumeTemplates: p.resumeTemplates.revision },
          })
        } catch (err) {
          if (err instanceof HostConfigApiError && err.status === 404) {
            patch(hostId, { ...emptyHostConfigEntry('unsupported') })
          } else {
            patch(hostId, { status: 'error', error: errorText(err) })
          }
        } finally {
          inflight.delete(hostId)
        }
      })()
      inflight.set(hostId, run)
      return run
    },

    ensureLoaded: async (hostId) => {
      const status = get().byHost[hostId]?.status
      if (status === 'ready' || status === 'unsupported') return
      try { await get().load(hostId) } catch { /* load never throws; belt and braces */ }
    },

    saveProjects: (hostId, items) => save(hostId, 'projects', 'projects', items),
    saveCommands: (hostId, items) => save(hostId, 'commands', 'commands', items),
    saveResumeTemplates: (hostId, items) => save(hostId, 'resume-templates', 'resumeTemplates', items),

    forget: (hostId) => set((s) => {
      if (!(hostId in s.byHost)) return s
      const { [hostId]: _dropped, ...rest } = s.byHost
      return { byHost: rest }
    }),
  }
})
```

If `Parameters<typeof putHostConfig<C>>` does not type-check under the repo's TS version, replace the `items` parameter type with `unknown` and cast at the `putHostConfig` call (`items as never`); the public `save*` signatures stay typed.

- [ ] **Step 8: Run store tests to verify pass**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/stores/useHostConfigStore.test.ts`
Expected: PASS (10 tests).

- [ ] **Step 9: Write the failing loader test**

`spa/src/lib/host-config-loader.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startHostConfigLoader } from './host-config-loader'
import { useHostStore } from '../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'

const host = (id: string, ip = '100.64.0.2') => ({ id, name: id, ip, port: 7860, token: null, order: 0 })
let stop: () => void = () => {}
const load = vi.fn(async () => {})

beforeEach(() => {
  load.mockClear()
  useHostConfigStore.setState({ byHost: {}, load })
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: { h2: { status: 'connected' } } })
})
afterEach(() => stop())

describe('startHostConfigLoader', () => {
  it('loads hosts already connected at start', () => {
    stop = startHostConfigLoader()
    expect(load).toHaveBeenCalledWith('h2')
    expect(load).not.toHaveBeenCalledWith('h1')
  })

  it('loads a host when it transitions to connected, once', () => {
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().setRuntime('h1', { latency: 3 })
    expect(load).toHaveBeenCalledTimes(1)
    expect(load).toHaveBeenCalledWith('h1')
  })

  it('forgets a removed host', () => {
    useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(useHostConfigStore.getState().byHost.h1).toBeUndefined()
  })

  it('an endpoint change forgets the host and reloads it if connected', () => {
    useHostConfigStore.setState({ byHost: { h2: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') } })
    expect(useHostConfigStore.getState().byHost.h2).toBeUndefined()
    expect(load).toHaveBeenCalledWith('h2')
  })
})
```

- [ ] **Step 10: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-loader.test.ts`
Expected: FAIL — cannot resolve `./host-config-loader`.

- [ ] **Step 11: Implement `spa/src/lib/host-config-loader.ts`**

```ts
// spa/src/lib/host-config-loader.ts — load host config when a host connects
// (spec §4.1). Same shape as `device-state/uploader.ts`: one module-level
// subscription for the app's lifetime, started from main.tsx.
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'

function endpoint(h: HostConfig | undefined): string {
  return h ? `${h.ip}:${h.port}:${h.token ?? ''}` : ''
}

export function startHostConfigLoader(): () => void {
  const load = (hostId: string) => { void useHostConfigStore.getState().load(hostId) }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) load(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    const config = useHostConfigStore.getState()
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) config.forget(hostId)
    }
    for (const hostId of Object.keys(next.hosts)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      const moved = !!prev.hosts[hostId] && endpoint(prev.hosts[hostId]) !== endpoint(next.hosts[hostId])
      if (moved) {
        // The cached copy (and its revisions) belong to the old daemon.
        config.forget(hostId)
        if (connected) load(hostId)
        continue
      }
      if (connected && !wasConnected) load(hostId)
    }
  })
}
```

`HostConfig` is already exported from `spa/src/stores/useHostStore.ts:12`.

- [ ] **Step 12: Wire into `spa/src/main.tsx`**

After line 9 add `import { startHostConfigLoader } from './lib/host-config-loader'`; after line 26 (`startDeviceStateUploader()`) add:

```ts
// Host config (projects / commands / resume templates): fetch each host's copy when it connects.
startHostConfigLoader()
```

- [ ] **Step 13: Run all three test files, lint the new files**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-api.test.ts src/stores/useHostConfigStore.test.ts src/lib/host-config-loader.test.ts && npx eslint src/lib/host-config-api.ts src/stores/useHostConfigStore.ts src/lib/host-config-loader.ts src/main.tsx && npx tsc -b`
Expected: all PASS, no lint errors, tsc exits 0.

- [ ] **Step 14: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/host-config-api.ts spa/src/lib/host-config-api.test.ts spa/src/stores/useHostConfigStore.ts spa/src/stores/useHostConfigStore.test.ts spa/src/lib/host-config-loader.ts spa/src/lib/host-config-loader.test.ts spa/src/main.tsx && git commit -m "feat(spa): host config api, cache store and connect loader

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 3: `lib/resume-templates.ts` + per-host lookup; rewire engine, batch (two-pass), composer, components

**Files:**
- Create: `spa/src/lib/resume-templates.ts`, `spa/src/lib/resume-templates.test.ts`
- Modify: `spa/src/stores/useResumeTemplateStore.ts:17-35` (re-export types/defaults from the lib; store itself survives until Task 4)
- Modify: `spa/src/lib/rebuild/composer.ts:2`
- Modify: `spa/src/lib/rebuild/engine.ts:25,166,491-492,520`
- Modify: `spa/src/lib/rebuild/batch.ts:24,30-37,74-83,104-140,148-166`
- Modify: `spa/src/components/RebuildActionSet.tsx:13,220`
- Modify: `spa/src/components/RenamePopover.tsx:4,76`
- Modify: `spa/src/components/settings/SnapshotSettingsSection.tsx:27,31-37,447-457,620,629-631,677`
- Test (modify): `spa/src/lib/rebuild/composer.test.ts:3`, `engine.test.ts:9,414,430,455-456`, `batch.test.ts` (groupForBatch plan cases lines 66-103 + new tests), `provenance-probe.test.ts:14-18`, `spa/src/stores/useTabStore.rebuild.test.ts:11-15`, `spa/src/stores/useAgentStore.provenance.test.ts:9-13`, `spa/src/components/RebuildActionSet.test.tsx:5,16,36-50,79,111,146`, `spa/src/components/RenamePopover.rebuild.test.tsx:11,141,283`, `spa/src/hooks/useMultiHostEventWs.revive.test.ts:150`

**Interfaces:**
- Consumes: `useHostConfigStore`, `emptyHostConfigEntry` (Task 2)
- Produces:
  - `interface ResumeTemplatePair { exact: string; fallback: string }`
  - `type ResumeTemplateLookup = (agentType: string) => ResumeTemplatePair | undefined`
  - `DEFAULT_RESUME_TEMPLATES: Readonly<Record<string, ResumeTemplatePair>>`
  - `lookupResumeTemplate(overrides: Record<string, ResumeTemplatePair>, agentType: string): ResumeTemplatePair | undefined`
  - `buildResumeLookup(overrides): ResumeTemplateLookup`; `defaultResumeLookup: ResumeTemplateLookup`
  - `resumeLookupFor(hostId: string): ResumeTemplateLookup` (live read; defaults unless `ready`)
  - `useResumeTemplateLookup(hostId: string): ResumeTemplateLookup` (subscribed)
  - batch: `interface BatchGroupDraft extends PaneRef { paneIds: string[]; sourcePaneId: string; record: PaneRebuildRecord }`, `interface BatchGroup extends BatchGroupDraft { plan: RebuildPlan }`, `groupForBatch(panes): { groups: BatchGroupDraft[]; excluded: BatchCandidate[] }`, `planForRecord(record, templates: ResumeTemplateLookup): RebuildPlan`, `planGroups(groups: BatchGroupDraft[], lookupFor: (hostId: string) => ResumeTemplateLookup): BatchGroup[]`, `planBatch(groups: BatchGroupDraft[], ensure?: (hostId: string) => Promise<void>): Promise<BatchGroup[]>`, `runBatchRebuild(deps?: RebuildDeps, options?: { hostId?: string }): Promise<BatchReport>`

- [ ] **Step 1: Write the failing lib tests**

`spa/src/lib/resume-templates.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  buildResumeLookup,
  DEFAULT_RESUME_TEMPLATES,
  defaultResumeLookup,
  resumeLookupFor,
  useResumeTemplateLookup,
} from './resume-templates'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'

function ready(resumeTemplates: Record<string, { exact: string; fallback: string }>) {
  return { ...emptyHostConfigEntry('ready'), resumeTemplates }
}

beforeEach(() => useHostConfigStore.setState({ byHost: {} }))

describe('resume template lookups', () => {
  it('defaults are frozen and match the shipped shapes', () => {
    expect(DEFAULT_RESUME_TEMPLATES.cc).toEqual({ exact: 'claude --resume {id}', fallback: 'claude -c' })
    expect(Object.isFrozen(DEFAULT_RESUME_TEMPLATES)).toBe(true)
    expect(Object.isFrozen(DEFAULT_RESUME_TEMPLATES.cc)).toBe(true)
  })

  it('a sparse override wins for its agent only; own properties only', () => {
    const lookup = buildResumeLookup({ cc: { exact: 'cld --resume {id}', fallback: 'cld -c' } })
    expect(lookup('cc')?.exact).toBe('cld --resume {id}')
    expect(lookup('codex')).toEqual(DEFAULT_RESUME_TEMPLATES.codex)
    expect(lookup('constructor')).toBeUndefined()
    expect(defaultResumeLookup('aider')).toBeUndefined()
  })

  it('resumeLookupFor answers from defaults until the host is ready', () => {
    useHostConfigStore.setState({ byHost: { h1: { ...ready({ cc: { exact: 'a {id}', fallback: 'a' } }), status: 'loading' } } })
    expect(resumeLookupFor('h1')('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
    useHostConfigStore.setState({ byHost: { h1: ready({ cc: { exact: 'a {id}', fallback: 'a' } }) } })
    expect(resumeLookupFor('h1')('cc')?.exact).toBe('a {id}')
    expect(resumeLookupFor('h2')('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
  })

  it('two hosts with different overrides for the same agent stay apart', () => {
    useHostConfigStore.setState({ byHost: {
      h1: ready({ cc: { exact: 'one {id}', fallback: 'one' } }),
      h2: ready({ cc: { exact: 'two {id}', fallback: 'two' } }),
    } })
    expect(resumeLookupFor('h1')('cc')?.exact).toBe('one {id}')
    expect(resumeLookupFor('h2')('cc')?.exact).toBe('two {id}')
  })

  it('useResumeTemplateLookup re-renders when that host\'s templates change', () => {
    const { result } = renderHook(() => useResumeTemplateLookup('h1'))
    expect(result.current('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
    act(() => useHostConfigStore.setState({ byHost: { h1: ready({ cc: { exact: 'x {id}', fallback: 'x' } }) } }))
    expect(result.current('cc')?.exact).toBe('x {id}')
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/resume-templates.test.ts`
Expected: FAIL — cannot resolve `./resume-templates`.

- [ ] **Step 3: Implement `spa/src/lib/resume-templates.ts`**

```ts
// spa/src/lib/resume-templates.ts — per-agent resume command templates (spec
// §4.2 of the host-launcher design). Defaults and lookup semantics are carried
// over verbatim from the retired `useResumeTemplateStore`; the overrides now
// live on each host's daemon (`useHostConfigStore`), so every lookup is built
// FOR a host.
import { useMemo } from 'react'
import { useHostConfigStore } from '../stores/useHostConfigStore'

export interface ResumeTemplatePair {
  /** Used when the record has a usable session id. Should contain `{id}`. */
  exact: string
  /** Used when it does not — taken verbatim, `{id}` included if present. */
  fallback: string
}

/** How a consumer asks for an agent's pair; `undefined` means "no template". */
export type ResumeTemplateLookup = (agentType: string) => ResumeTemplatePair | undefined

/** The shapes that shipped hardcoded: a host with no overrides sees exactly these. */
export const DEFAULT_RESUME_TEMPLATES: Readonly<Record<string, ResumeTemplatePair>> = Object.freeze({
  cc: Object.freeze({ exact: 'claude --resume {id}', fallback: 'claude -c' }),
  codex: Object.freeze({ exact: 'codex resume {id}', fallback: 'codex resume --last' }),
  opencode: Object.freeze({ exact: 'opencode -s {id}', fallback: 'opencode -c' }),
})

/**
 * Own properties only: an agent type is an open string from a daemon payload,
 * so `overrides['constructor']` must not resolve up the prototype chain.
 */
function own<T>(map: Readonly<Record<string, T>>, key: string): T | undefined {
  return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : undefined
}

export function lookupResumeTemplate(
  overrides: Readonly<Record<string, ResumeTemplatePair>>,
  agentType: string,
): ResumeTemplatePair | undefined {
  return own(overrides, agentType) ?? own(DEFAULT_RESUME_TEMPLATES, agentType)
}

export function buildResumeLookup(overrides: Readonly<Record<string, ResumeTemplatePair>>): ResumeTemplateLookup {
  return (agentType) => lookupResumeTemplate(overrides, agentType)
}

const NO_OVERRIDES: Readonly<Record<string, ResumeTemplatePair>> = Object.freeze({})

export const defaultResumeLookup: ResumeTemplateLookup = buildResumeLookup(NO_OVERRIDES)

function overridesOf(hostId: string): Readonly<Record<string, ResumeTemplatePair>> {
  const entry = useHostConfigStore.getState().byHost[hostId]
  return entry?.status === 'ready' ? entry.resumeTemplates : NO_OVERRIDES
}

/**
 * Live lookup for code outside React (engine, batch planner). Reads the store
 * on every call; callers that must not see later edits resolve once and pin
 * the resulting string, as the engine does.
 */
export function resumeLookupFor(hostId: string): ResumeTemplateLookup {
  return (agentType) => lookupResumeTemplate(overridesOf(hostId), agentType)
}

/**
 * The lookup for code that RENDERS a composed command. Subscribed to that
 * host's overrides so an edit in Host › Commands › Resume repaints without a
 * remount. Not-yet-loaded hosts answer from defaults (display only).
 */
export function useResumeTemplateLookup(hostId: string): ResumeTemplateLookup {
  const overrides = useHostConfigStore((s) => {
    const entry = s.byHost[hostId]
    return entry?.status === 'ready' ? entry.resumeTemplates : NO_OVERRIDES
  })
  return useMemo(() => buildResumeLookup(overrides), [overrides])
}
```

- [ ] **Step 4: Run lib tests to verify pass**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/resume-templates.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Point the legacy store at the lib (kept alive only for `ResumeTemplateSettings` until Task 4)**

In `spa/src/stores/useResumeTemplateStore.ts` replace lines 17-35 (the `ResumeTemplatePair` interface, `ResumeTemplateLookup` type and `DEFAULT_RESUME_TEMPLATES` const) with:

```ts
import { DEFAULT_RESUME_TEMPLATES, type ResumeTemplateLookup, type ResumeTemplatePair } from '../lib/resume-templates'
export { DEFAULT_RESUME_TEMPLATES, type ResumeTemplateLookup, type ResumeTemplatePair }
```

(Move the `import` line up with the other imports at lines 12-15 if lint's `import/first` complains.)

- [ ] **Step 6: Write the failing batch / engine tests**

In `spa/src/lib/rebuild/batch.test.ts`:

1. Extend the import at lines 3-8 to `import { BATCH_LOCK_OWNER, groupForBatch, planBatch, planGroups, recordsDisagree, runBatchRebuild } from './batch'` and add:
```ts
import { defaultResumeLookup } from '../resume-templates'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
```
2. Add at file top level (after the `pane` fixture, before `describe('groupForBatch'`):
```ts
/** Host config loaded (no overrides) for every host the fixtures use, so the
 *  planner's ensureLoaded never reaches the network. */
beforeEach(() => {
  useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready'), h2: emptyHostConfigEntry('ready') } })
})
const planned = (groups: Parameters<typeof planGroups>[0]) => planGroups(groups, () => defaultResumeLookup)
```
3. In the four `groupForBatch` cases that read `.plan` (currently lines 66-69, 71-81, 83-92, 101-104), replace `groups[0].plan` with `planned(groups)[0].plan`.
4. Append to the `groupForBatch` describe:
```ts
  it('groups carry no plan until planned (pass 1 is pure grouping)', () => {
    const { groups } = groupForBatch([pane('p1')])
    expect('plan' in groups[0]).toBe(false)
  })
```
5. Append a new describe:
```ts
describe('planBatch — per-host templates', () => {
  it('loads every involved host once, in parallel, then plans each group with ITS host\'s lookup', async () => {
    useHostConfigStore.setState({ byHost: {} })
    const ensure = vi.fn(async (hostId: string) => {
      // h1 blanks the cc template (resume off); h2 keeps the default (resume on).
      const resumeTemplates = hostId === 'h1' ? { cc: { exact: '', fallback: '' } } : {}
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...emptyHostConfigEntry('ready'), resumeTemplates } } }))
    })
    const { groups } = groupForBatch([pane('p1'), pane('p2', { hostId: 'h2' }), pane('p3', { hostId: 'h2', sessionCode: 'other' })])
    const plannedGroups = await planBatch(groups, ensure)
    expect(ensure.mock.calls.map(([h]) => h).sort()).toEqual(['h1', 'h2'])
    expect(plannedGroups.map((g) => [g.hostId, g.plan.runResume])).toEqual([['h1', false], ['h2', true], ['h2', true]])
  })
})
```
6. In `describe('runBatchRebuild'`, add:
```ts
  it('sends each host\'s own resume template for the same agent type', async () => {
    useHostConfigStore.setState({ byHost: {
      h1: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'one --resume {id}', fallback: 'one -c' } } },
      h2: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'two --resume {id}', fallback: 'two -c' } } },
    } })
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, h2: { id: 'h2', name: 'h2', ip: '127.0.0.2', port: 7860, token: null, order: 1 } },
      hostOrder: ['h1', 'h2'],
      runtime: { ...s.runtime, h2: { status: 'connected', attachReady: true } },
    }))
    seedPane('h1', 't1', 'p1')
    seedPane('h2', 't2', 'p2')
    const sendKeys = vi.fn()
    await runBatchRebuild({
      createSession: vi.fn(async (hostId: string) => session({ code: `new-${hostId}`, name: 'dev', tmux_instance: '222:2000' })),
      sendKeys,
    })
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new-h1', 'one --resume S1', '222:2000')
    expect(sendKeys).toHaveBeenCalledWith('h2', 'new-h2', 'two --resume S1', '222:2000')
  })

  it('options.hostId restricts the batch to that host', async () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, h2: { id: 'h2', name: 'h2', ip: '127.0.0.2', port: 7860, token: null, order: 1 } },
      hostOrder: ['h1', 'h2'],
      runtime: { ...s.runtime, h2: { status: 'connected', attachReady: true } },
    }))
    seedPane('h1', 't1', 'p1')
    seedPane('h2', 't2', 'p2')
    const create = vi.fn(async (hostId: string) => session({ code: `new-${hostId}`, name: 'dev', tmux_instance: '222:2000' }))
    const report = await runBatchRebuild({ createSession: create, sendKeys: vi.fn() }, { hostId: 'h1' })
    expect(create.mock.calls.map(([h]) => h)).toEqual(['h1'])
    expect(report.groups.map((g) => g.hostId)).toEqual(['h1'])
  })
```

In `spa/src/lib/rebuild/engine.test.ts`:

1. Replace line 9 (`import { useResumeTemplateStore } ...`) with:
```ts
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { defaultResumeLookup } from '../resume-templates'
```
2. Add at top level after the imports block:
```ts
// Every host the fixtures use starts with host config loaded and no overrides,
// so `ensureLoaded` is a no-op and fetch-count assertions stay exact.
beforeEach(() => {
  useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready'), h2: emptyHostConfigEntry('ready'), other: emptyHostConfigEntry('ready') } })
})
```
3. Delete line 414 (`useResumeTemplateStore.setState({ agents: {} })`).
4. Replace line 430 with:
```ts
    useHostConfigStore.setState({ byHost: { h1: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'cld-yolo --resume {id}', fallback: 'claude -c' } } } } })
```
5. Lines 455-456: `planForRecord(record)` → `planForRecord(record, defaultResumeLookup)` (both occurrences).
6. Append to `describe('rebuildPane — the command it sends is resolved, not stored'`:
```ts
  it('waits for the host config before resolving, and uses that host\'s override', async () => {
    useHostConfigStore.setState({ byHost: {} })
    const ensureLoaded = vi.fn(async (hostId: string) => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: {
        ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'host-cld --resume {id}', fallback: 'host-cld -c' } },
      } } }))
    })
    useHostConfigStore.setState({ ensureLoaded })
    seedPane('h1', 't1', 'p1', { cwd: '/w', agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })
    const sendKeys = vi.fn()
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: created(), sendKeys })
    expect(ensureLoaded).toHaveBeenCalledWith('h1')
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'host-cld --resume S1', '222:2000')
  })

  it('a host whose config cannot load rebuilds with the default templates', async () => {
    useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('error') }, ensureLoaded: vi.fn(async () => {}) })
    seedPane('h1', 't1', 'p1', { cwd: '/w', agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })
    const sendKeys = vi.fn()
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: created(), sendKeys })
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'claude --resume S1', '222:2000')
  })
```
Because step 6 overrides the `ensureLoaded` action, restore it in the top-level `beforeEach` of engine.test.ts by capturing the original once:
```ts
const realEnsureLoaded = useHostConfigStore.getState().ensureLoaded
// inside the top-level beforeEach, first line:
useHostConfigStore.setState({ ensureLoaded: realEnsureLoaded })
```

In `spa/src/hooks/useMultiHostEventWs.revive.test.ts` add to the top-level `beforeEach` at line 150:
```ts
  useHostConfigStore.setState({ byHost: { [HOST]: emptyHostConfigEntry('ready'), [H2]: emptyHostConfigEntry('ready') } })
```
with `import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'` (if `H2` is declared below line 150, move that `const` above the `beforeEach` or inline its literal).

- [ ] **Step 7: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/rebuild/batch.test.ts src/lib/rebuild/engine.test.ts`
Expected: FAIL — `planGroups` / `planBatch` not exported; engine does not call `ensureLoaded`.

- [ ] **Step 8: Implement composer / engine / batch changes**

`spa/src/lib/rebuild/composer.ts:2` → `import type { ResumeTemplateLookup } from '../resume-templates'`

`spa/src/lib/rebuild/engine.ts`:
- line 25 → 
```ts
import { resumeLookupFor } from '../resume-templates'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
```
- line 166 → `    resumeCommand: resolveResumeCommand(content?.rebuild, resumeLookupFor(hostId)),`
- insert immediately after the pin `try/catch` that ends at line 491:
```ts

  // The host's resume templates live on its daemon (host-launcher spec §4.2).
  // Loaded AFTER the pin — an unknown host was refused above, so this never
  // reaches another machine — and BEFORE the pane read below, because
  // everything from the binding check to the create must stay one
  // synchronous run. Never throws; a failed load answers from defaults.
  await useHostConfigStore.getState().ensureLoaded(hostId)
```
- line 520 → `  const resumeCommand = resolveResumeCommand(record, resumeLookupFor(hostId))`

`spa/src/lib/rebuild/batch.ts`:
- line 24 → 
```ts
import { resumeLookupFor, type ResumeTemplateLookup } from '../resume-templates'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
```
- replace lines 30-37 (`BatchGroup`) with:
```ts
/** Pass 1 of planning: a group, without the plan that needs its host's templates. */
export interface BatchGroupDraft extends PaneRef {
  /** Every pane re-pointed to this group's result, in collection order. */
  paneIds: string[]
  /** Whose record won the conflict resolution — the pane the engine runs on. */
  sourcePaneId: string
  record: PaneRebuildRecord
}

export interface BatchGroup extends BatchGroupDraft {
  plan: RebuildPlan
}
```
- replace lines 74-83 (`planForRecord`) signature: drop the default:
```ts
export function planForRecord(
  record: PaneRebuildRecord,
  templates: ResumeTemplateLookup,
): RebuildPlan {
```
(body unchanged)
- in `groupForBatch` (lines 104-140): return type `{ groups: BatchGroupDraft[]; excluded: BatchCandidate[] }`; `byKey` is `Map<string, BatchGroupDraft>`; delete the `plan: planForRecord(pane.record),` line (125) and the `group.plan = planForRecord(pane.record)` line (135).
- add after `groupForBatch`:
```ts
/** Pass 2, pure: attach each group's plan using ITS host's lookup. */
export function planGroups(
  groups: BatchGroupDraft[],
  lookupFor: (hostId: string) => ResumeTemplateLookup,
): BatchGroup[] {
  return groups.map((group) => ({ ...group, plan: planForRecord(group.record, lookupFor(group.hostId)) }))
}

/**
 * Pass 2 for execution: load every involved host's config (in parallel,
 * never throwing), then plan. Display code must NOT use this — it plans with
 * `useResumeTemplateLookup(hostId)` per row instead.
 */
export async function planBatch(
  groups: BatchGroupDraft[],
  ensure: (hostId: string) => Promise<void> = (hostId) => useHostConfigStore.getState().ensureLoaded(hostId),
): Promise<BatchGroup[]> {
  const hostIds = Array.from(new Set(groups.map((g) => g.hostId)))
  await Promise.all(hostIds.map((hostId) => ensure(hostId)))
  return planGroups(groups, resumeLookupFor)
}
```
- replace `runBatchRebuild` lines 148-166 head with:
```ts
export async function runBatchRebuild(
  deps: RebuildDeps = {},
  options: { hostId?: string } = {},
): Promise<BatchReport> {
  const rows = collectRecordRows(useTabStore.getState().tabs)
  const candidates = batchCandidates(options.hostId ? rows.filter((r) => r.hostId === options.hostId) : rows)
  const drafts = groupForBatch(candidates)
  const { excluded } = drafts
  const tabOfPane = new Map(candidates.map((c) => [c.paneId, c.tabId]))
  // Planned before the lock: loading host config is a network wait, and the
  // engine re-verifies every group's binding at create time anyway.
  const groups = await planBatch(drafts.groups)

  const grant = useRebuildStore.getState().acquireOperationLock(BATCH_LOCK_OWNER)
```
(the rest of the function body from `if (!grant) {` stays unchanged).

- [ ] **Step 9: Rewire components**

`spa/src/components/RebuildActionSet.tsx`:
- line 13 → `import { useResumeTemplateLookup } from '../lib/resume-templates'`
- line 220 → 
```ts
  // Per host: the command a Rebuild would send is composed from THIS pane's
  // host's templates. `binding` is always passed by TerminatedPane; without it
  // the defaults answer (display only).
  const templates = useResumeTemplateLookup(binding?.hostId ?? '')
```

`spa/src/components/RenamePopover.tsx`:
- line 4 → `import { useResumeTemplateLookup } from '../lib/resume-templates'`
- line 76 → `  const templates = useResumeTemplateLookup(target.hostId)`

`spa/src/components/settings/SnapshotSettingsSection.tsx`:
- line 27 → `import { resumeLookupFor, useResumeTemplateLookup } from '../../lib/resume-templates'` and add `import { useHostConfigStore } from '../../stores/useHostConfigStore'`
- lines 31-37 import: replace `type BatchGroup` with `type BatchGroupDraft`
- `handleRebuildOne` (447-457) body first line becomes:
```ts
    runRebuildAction(`rebuild:${pane.paneId}`, async () => {
      await useHostConfigStore.getState().ensureLoaded(pane.hostId)
      const report = await rebuildPane(pane.hostId, pane.tabId, pane.paneId, planForRecord(pane.record, resumeLookupFor(pane.hostId)))
```
- `RebuildRecordsBlock` prop type line 620: `groups: BatchGroupDraft[]`
- delete lines 629-631 (the block-level `templates` lookup and its comment)
- line 677 → `                  <RecordCommandCell hostId={row.hostId} record={row.record} />`
- add below `RebuildRecordsBlock`:
```tsx
/** One row's command, composed with that row's host's templates (subscribed). */
function RecordCommandCell({ hostId, record }: { hostId: string; record: RecordRow['record'] }) {
  const templates = useResumeTemplateLookup(hostId)
  return <td className="py-1 pr-3 font-mono">{resolveResumeCommand(record, templates) || '—'}</td>
}
```

- [ ] **Step 10: Update remaining tests that referenced the global store**

- `spa/src/lib/rebuild/composer.test.ts:3` → `import { DEFAULT_RESUME_TEMPLATES, type ResumeTemplateLookup } from '../resume-templates'`
- `spa/src/lib/rebuild/provenance-probe.test.ts:14-18` → `import { defaultResumeLookup as defaultTemplates } from '../resume-templates'` (delete the local `defaultTemplates` const and its comment)
- `spa/src/stores/useTabStore.rebuild.test.ts:11-15` → `import { defaultResumeLookup as defaultTemplates } from '../lib/resume-templates'` (same deletion)
- `spa/src/stores/useAgentStore.provenance.test.ts:9-13` → `import { defaultResumeLookup as defaultTemplates } from '../lib/resume-templates'` (same deletion)
- `spa/src/components/RebuildActionSet.test.tsx`:
  - line 5 → `import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'`
  - line 16 → `  useHostConfigStore.setState({ byHost: {} })`
  - add below the imports:
```ts
const BINDING = { hostId: 'h1', sessionCode: 'old1', tmuxInstance: '111:1000' }
function setHostTemplate(agentType: string, exact: string) {
  useHostConfigStore.setState({ byHost: { h1: { ...emptyHostConfigEntry('ready'), resumeTemplates: { [agentType]: { exact, fallback: '' } } } } })
}
```
  - every `useResumeTemplateStore.getState().setTemplate(X, 'exact', Y)` (lines 39, 47, 79, 111, 146) → `setHostTemplate(X, Y)`
  - every `render(<RebuildActionSet ... />)` in the tests that call `setHostTemplate` (the describes at lines 24-50, and the cases around 79, 111, 146) gets `binding={BINDING}` if it does not already pass a `binding` (the one at line 90 already passes the same value). Add one new case to the first describe:
```ts
  it('composes with the binding host\'s template, not another host\'s', () => {
    useHostConfigStore.setState({ byHost: { h2: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'other --resume {id}', fallback: '' } } } } })
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} binding={BINDING} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume S1')
  })
```
- `spa/src/components/RenamePopover.rebuild.test.tsx`:
  - line 11 → `import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'`
  - line 141 → `  useHostConfigStore.setState({ byHost: {} })`
  - line 283 → `    act(() => { useHostConfigStore.setState({ byHost: { h1: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'cld-yolo --resume {id}', fallback: '' } } } } }) })` (the `terminal()` fixture's `hostId` is `'h1'`, line 78)

- [ ] **Step 11: Verify no consumer outside ResumeTemplateSettings still imports the old store**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "useResumeTemplateStore|liveResumeTemplates" spa/src`
Expected: only `spa/src/stores/useResumeTemplateStore.ts`, `spa/src/stores/useResumeTemplateStore.test.ts`, `spa/src/components/settings/ResumeTemplateSettings.tsx`, `spa/src/components/settings/ResumeTemplateSettings.test.tsx`, `spa/src/components/settings/SnapshotSettingsSection.records.test.tsx`.

In `SnapshotSettingsSection.records.test.tsx` replace the import at line 13 with `import { useHostConfigStore } from '../../stores/useHostConfigStore'` and the `useResumeTemplateStore.setState({ agents: {} })` in `beforeEach` with `useHostConfigStore.setState({ byHost: {} })`. Re-run the rg: only the three store/settings files remain.

- [ ] **Step 12: Run the affected suites + typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/resume-templates.test.ts src/lib/rebuild src/stores/useTabStore.rebuild.test.ts src/stores/useAgentStore.provenance.test.ts src/components/RebuildActionSet.test.tsx src/components/RenamePopover.rebuild.test.tsx src/components/settings/SnapshotSettingsSection.test.tsx src/components/settings/SnapshotSettingsSection.records.test.tsx src/hooks/useMultiHostEventWs.revive.test.ts src/components/settings/ResumeTemplateSettings.test.tsx && npx tsc -b`
Expected: all PASS; tsc exits 0.

- [ ] **Step 13: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/resume-templates.ts spa/src/lib/resume-templates.test.ts spa/src/stores/useResumeTemplateStore.ts spa/src/lib/rebuild spa/src/components/RebuildActionSet.tsx spa/src/components/RebuildActionSet.test.tsx spa/src/components/RenamePopover.tsx spa/src/components/RenamePopover.rebuild.test.tsx spa/src/components/settings/SnapshotSettingsSection.tsx spa/src/components/settings/SnapshotSettingsSection.records.test.tsx spa/src/stores/useTabStore.rebuild.test.ts spa/src/stores/useAgentStore.provenance.test.ts spa/src/hooks/useMultiHostEventWs.revive.test.ts && git commit -m "feat(spa): resolve resume templates per host from host config

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 4: `ResumeTemplateSettings` → `{ hostId, busy }`; delete `useResumeTemplateStore`

**Files:**
- Create: `spa/src/lib/command-word.ts`, `spa/src/lib/command-word.test.ts`
- Create: `spa/src/components/settings/ShellVerdict.tsx`
- Modify: `spa/src/components/settings/ResumeTemplateSettings.tsx` (header comment 1-38, imports 39-45, `commandWordOf` 82-107, component 109-332, `Verdict` 445-495)
- Modify: `spa/src/components/settings/ResumeTemplateSettings.test.tsx` (whole file migrated, rules below)
- Modify: `spa/src/components/settings/SnapshotSettingsSection.tsx:14,484-486`
- Modify: `spa/src/components/settings/SnapshotSettingsSection.records.test.tsx:251-259` (delete the "mounts the resume template editor" case)
- Modify: `spa/src/lib/storage/keys.ts:16` (delete `RESUME_TEMPLATES`)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`
- Delete: `spa/src/stores/useResumeTemplateStore.ts`, `spa/src/stores/useResumeTemplateStore.test.ts`

**Interfaces:**
- Consumes: `useHostConfigStore.saveResumeTemplates`, `HostConfigConflictError` (Task 2); `useResumeTemplateLookup(hostId)`, `lookupResumeTemplate` (Task 3)
- Produces:
  - `commandWordOf(template: string): string` (`spa/src/lib/command-word.ts`)
  - `ShellVerdict({ testId, verdict, t }: { testId: string; verdict: ShellResolveVerdict | 'pending'; t: TFn })` (same DOM as today's `Verdict`: `data-testid`, `data-status`, `data-reason`)
  - `ResumeTemplateSettings({ hostId, busy }: { hostId: string; busy?: boolean })`

The component is not rendered anywhere between this task and Task 7 (Commands › Resume); the app still builds.

- [ ] **Step 1: Move `commandWordOf` with a test**

`spa/src/lib/command-word.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { commandWordOf } from './command-word'

describe('commandWordOf', () => {
  it.each([
    ['cld-yolo --resume {id}', 'cld-yolo'],
    ['  claude -c', 'claude'],
    ['OPENCODE_YOLO=true opencode -s {id}', 'opencode'],
    ['A=1 B=x=y codex resume', 'codex'],
    ['--flag value', '--flag'],
    ['./bin/run', './bin/run'],
    ['A=1 B=2', ''],
    ['', ''],
  ])('%j → %j', (template, word) => {
    expect(commandWordOf(template)).toBe(word)
  })
})
```

`spa/src/lib/command-word.ts` — move lines 86-107 of `ResumeTemplateSettings.tsx` verbatim (the doc comment, `ASSIGNMENT`, `commandWordOf`) and prefix `export` on the function. Delete those lines from the component and import `{ commandWordOf } from '../../lib/command-word'`.

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/command-word.test.ts`
Expected: PASS (8 cases).

- [ ] **Step 2: Extract `ShellVerdict`**

`spa/src/components/settings/ShellVerdict.tsx`:

```tsx
import { CheckCircle, CircleNotch, Question, XCircle } from '@phosphor-icons/react'
import type { ShellResolveVerdict } from '../../lib/host-api'

type TFn = (key: string, params?: Record<string, string | number>) => string

/**
 * Every `reason` the daemon can return. An unknown one is a newer daemon than
 * this build; it still renders as "did not resolve" rather than a raw token.
 */
const REASON_LABEL: Record<string, string> = {
  not_found: 'resume_template.verdict.not_found',
  shell_metacharacters: 'resume_template.verdict.shell_metacharacters',
  too_long: 'resume_template.verdict.too_long',
  timeout: 'resume_template.verdict.timeout',
  shell_failed: 'resume_template.verdict.shell_failed',
}

/** The shell resolve-command verdict, shared by resume templates and normal commands. */
export function ShellVerdict({ testId, verdict, t }: { testId: string; verdict: ShellResolveVerdict | 'pending'; t: TFn }) {
  const cls = 'flex items-center gap-1'
  if (verdict === 'pending') {
    return (
      <span data-testid={testId} data-status="pending" className={`${cls} text-text-secondary`}>
        <CircleNotch size={14} className="animate-spin" />
        {t('resume_template.verdict.pending')}
      </span>
    )
  }
  if (verdict.status === 'resolved') {
    return (
      <span data-testid={testId} data-status="resolved" className={`${cls} text-status-success`}>
        <CheckCircle size={14} />
        {t('resume_template.verdict.resolved', { detail: verdict.detail })}
      </span>
    )
  }
  if (verdict.status === 'unverifiable') {
    return (
      <span data-testid={testId} data-status="unverifiable" className={`${cls} text-text-secondary`}>
        <Question size={14} />
        {t('resume_template.verdict.unverifiable')}
      </span>
    )
  }
  return (
    <span data-testid={testId} data-status="unresolved" data-reason={verdict.reason} className={`${cls} text-status-warning`}>
      <XCircle size={14} />
      {t(REASON_LABEL[verdict.reason] ?? 'resume_template.verdict.unresolved')}
    </span>
  )
}
```

In `ResumeTemplateSettings.tsx` delete `REASON_LABEL` (lines 57-68) and the `Verdict` function (445-495); where `<Verdict agentType={agentType} field={field} result={result} t={t} />` is rendered (line 419) use:

```tsx
      {result ? <ShellVerdict testId={`resume-template-verdict-${agentType}-${field}`} verdict={result.verdict} t={t} /> : null}
```

Remove now-unused icon imports (`CheckCircle`, `CircleNotch`, `Question`, `XCircle`) from line 40.

- [ ] **Step 3: Migrate the component test (failing first)**

Rewrite `spa/src/components/settings/ResumeTemplateSettings.test.tsx` by these rules — every `it` not named below keeps its body:

1. Imports: replace line 17 with
```ts
import { DEFAULT_RESUME_TEMPLATES } from '../../lib/resume-templates'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { HostConfigConflictError, type ResumeTemplateOverrides } from '../../lib/host-config-api'
```
2. Harness, replacing the `useResumeTemplateStore.setState({ agents: {} })` line in `beforeEach` (line 59) with `seedTemplates({})`, and adding above `beforeEach`:
```ts
/** H1's host config, ready, with a save that applies locally like a successful PUT. */
function seedTemplates(resumeTemplates: ResumeTemplateOverrides) {
  useHostConfigStore.setState({
    byHost: { [H1]: { ...emptyHostConfigEntry('ready'), resumeTemplates } },
    saveResumeTemplates: vi.fn(async (hostId: string, items: ResumeTemplateOverrides) => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], resumeTemplates: items } } }))
    }),
  })
}
const overrides = () => useHostConfigStore.getState().byHost[H1].resumeTemplates
const saveMock = () => vi.mocked(useHostConfigStore.getState().saveResumeTemplates)
```
3. Every `render(<ResumeTemplateSettings />)` → `render(<ResumeTemplateSettings hostId={H1} />)`; `render(<ResumeTemplateSettings busy />)` → `render(<ResumeTemplateSettings hostId={H1} busy />)`.
4. Every `useResumeTemplateStore.getState().agents` → `overrides()` (e.g. `overrides().cc?.exact`).
5. The "Enter commits … does not commit twice" case: drop the `vi.spyOn(useResumeTemplateStore.getState(), 'setTemplate')` line; assert `expect(saveMock()).toHaveBeenCalledTimes(1)` and `await waitFor(() => expect(overrides().cc?.exact).toBe('cld-yolo --resume {id}'))`. Apply the same `await waitFor(...)` wrapping to every other assertion that reads `overrides()` right after a commit (the save is async); make those `it` callbacks `async`.
6. `describe('ResumeTemplateSettings — the host picker'`: delete "defaults to the active host and sends the probe there", "switching host clears a verdict already on screen" and "a response that lands after the host changed is discarded". In "a verdict from a superseded request never overwrites a newer one" replace the two host `fireEvent.change(... 'resume-template-host' ...)` lines with a second `fireEvent.click(testButton('cc', 'exact'))` (pressing Test again abandons the first request). Rename the describe to `'ResumeTemplateSettings — request revisions'`.
7. "a committed row follows a reset performed elsewhere": replace `useResumeTemplateStore.getState().resetAgent('cc')` with `seedTemplates({})`. "a committed row follows a later store change": replace the store write with `seedTemplates({ cc: { exact: '<value the test used>', fallback: DEFAULT_RESUME_TEMPLATES.cc.fallback } })`.
8. Add a new describe:
```ts
describe('ResumeTemplateSettings — host scoped', () => {
  it('shows this host\'s overrides, not another host\'s', () => {
    useHostConfigStore.setState({ byHost: {
      [H1]: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'one --resume {id}', fallback: 'one -c' } } },
      [H2]: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'two --resume {id}', fallback: 'two -c' } } },
    } })
    render(<ResumeTemplateSettings hostId={H2} />)
    expect(input('cc', 'exact').value).toBe('two --resume {id}')
    expect(screen.queryByTestId('resume-template-host')).toBeNull()
  })

  it('a commit saves the whole sparse map with the edited field merged onto the current pair', async () => {
    seedTemplates({ codex: { exact: 'cx {id}', fallback: 'cx' } })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'fallback'), { target: { value: 'cld -c' } })
    fireEvent.blur(input('cc', 'fallback'))
    await waitFor(() => expect(saveMock()).toHaveBeenCalledWith(H1, {
      codex: { exact: 'cx {id}', fallback: 'cx' },
      cc: { exact: DEFAULT_RESUME_TEMPLATES.cc.exact, fallback: 'cld -c' },
    }))
  })

  it('Test probes THIS host', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H2} />)
    fireEvent.click(testButton('cc', 'exact'))
    await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
    expect(String(fetchSpy.mock.calls[0][0])).toContain(':7861/api/shell/resolve-command')
  })

  it('Reset all saves an empty map', async () => {
    seedTemplates({ cc: { exact: 'x {id}', fallback: 'x' } })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.click(screen.getByTestId('resume-template-reset'))
    await waitFor(() => expect(saveMock()).toHaveBeenCalledWith(H1, {}))
  })

  it('a conflict shows the changed-elsewhere notice and the reloaded values', async () => {
    seedTemplates({})
    useHostConfigStore.setState({ saveResumeTemplates: vi.fn(async () => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [H1]: { ...s.byHost[H1], resumeTemplates: { cc: { exact: 'server {id}', fallback: 'server' } } } } }))
      throw new HostConfigConflictError({ items: {}, revision: 5 })
    }) })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'mine {id}' } })
    fireEvent.blur(input('cc', 'exact'))
    expect(await screen.findByTestId('resume-template-save-error')).toHaveTextContent('Changed elsewhere')
    expect(input('cc', 'exact').value).toBe('server {id}')
  })

  it('a host whose config is not ready renders defaults read-only', () => {
    useHostConfigStore.setState({ byHost: { [H1]: emptyHostConfigEntry('unsupported') } })
    render(<ResumeTemplateSettings hostId={H1} />)
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
    expect(input('cc', 'exact')).toBeDisabled()
  })
})
```
(`H2`'s fixture port is `7861`: `host(H2, 'air', 1)` gives `7860 + 1`.)

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/settings/ResumeTemplateSettings.test.tsx`
Expected: FAIL — component still uses the global store and the host picker.

- [ ] **Step 4: Retarget the component**

In `ResumeTemplateSettings.tsx`:

- Header comment: replace contract 3 ("The host picker defaults to the active host.") with "3. **Per host.** Templates are this host's daemon copy (host-launcher spec §4.2); the Test runs against the same host." and contract 5 with "5. **The limits are on screen**: the test approximates the pane's shell rather than reproducing it." Drop the paragraph about host changes abandoning requests (keep the revision paragraph, minus "a host change").
- Imports (39-45):
```ts
import { useRef, useState } from 'react'
import { ArrowCounterClockwise, Warning } from '@phosphor-icons/react'
import { AGENT_NAMES } from '../../lib/agent-metadata'
import { resolveShellCommand, type ShellResolveVerdict } from '../../lib/host-api'
import { commandWordOf } from '../../lib/command-word'
import { HostConfigConflictError } from '../../lib/host-config-api'
import { useResumeTemplateLookup, type ResumeTemplatePair } from '../../lib/resume-templates'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ShellVerdict } from './ShellVerdict'
```
- Replace the component head (lines 109-123) with:
```tsx
const BLANK: ResumeTemplatePair = { exact: '', fallback: '' }

export function ResumeTemplateSettings({ hostId, busy = false }: { hostId: string; busy?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const lookup = useResumeTemplateLookup(hostId)
  const ready = useHostConfigStore((s) => s.byHost[hostId]?.status === 'ready')
  const [saveError, setSaveError] = useState<string | null>(null)
  // Editing is only meaningful against a loaded copy: its revision is what the
  // PUT is compared against.
  const locked = busy || !ready
```
- Replace every other use of `busy` inside the returned JSX of `ResumeTemplateSettings` and the `busy={busy}` passed to `TemplateRow` with `locked`.
- Replace `handleCommit` (177-185):
```tsx
  const persist = async (next: Record<string, ResumeTemplatePair>) => {
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveResumeTemplates(hostId, next)
    } catch (err) {
      setSaveError(err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
    }
  }

  const handleCommit = (agentType: string, field: Field, value: string) => {
    const current = useHostConfigStore.getState().byHost[hostId]?.resumeTemplates ?? {}
    // The edit lands on top of whatever currently answers for this agent, so
    // editing one field never silently blanks the other.
    const base = lookup(agentType) ?? BLANK
    dropDraft(rowKey(agentType, field))
    void persist({ ...current, [agentType]: { ...base, [field]: value } })
  }
```
- Delete `handleHostChange` (194-201) and the `<label>…<select data-testid="resume-template-host">…</label>` block (277-290); delete `hosts`, `hostOrder`, `activeHostId`, `pickedHostId` state and the `setTemplate` / `resetAgent` selectors.
- Replace `handleResetAll` (203-208):
```tsx
  const handleResetAll = () => {
    setDrafts({})
    setResults({})
    abandonAllRequests()
    void persist({})
  }
```
- Limits paragraph (271-275):
```tsx
      <p data-testid="resume-template-limits" className="mt-1 text-xs text-text-secondary">
        {t('resume_template.limit_host')}
        {' '}
        {t('resume_template.limit_probe')}
      </p>
      {saveError ? (
        <p data-testid="resume-template-save-error" className="mt-2 text-xs text-status-warning">{saveError}</p>
      ) : null}
```
- `liveRef`, `settle`, `runTest`, `shownResult` keep using `hostId` (now the prop).

- [ ] **Step 5: Locale keys**

`spa/src/locales/en.json`: delete `resume_template.limit_global` and `resume_template.test_against`; add
```json
  "resume_template.limit_host": "These templates apply to this host only; the test below also runs on this host.",
  "host_config.conflict": "Changed elsewhere — the latest version was reloaded. Re-apply your edit.",
  "host_config.save_failed": "Save failed: {{reason}}",
```
and change `resume_template.limit_probe` to `"The test approximates the pane's shell, it does not reproduce it: it runs an interactive login shell on this host, with no tty and no tmux default-command. A bash function defined only in .bashrc will fail the test — and will equally be missing from the rebuilt pane."`.

`spa/src/locales/zh-TW.json`: delete the same two keys; add
```json
  "resume_template.limit_host": "這組範本只套用在此主機；下方的測試也在此主機執行。",
  "host_config.conflict": "已在其他地方變更，已重新載入最新版本，請重新套用你的修改。",
  "host_config.save_failed": "儲存失敗：{{reason}}",
```
and set `resume_template.limit_probe` to `"測試只是近似 pane 的 shell，並非完整重現：它在此主機以互動式 login shell 執行，沒有 tty，也不套用 tmux default-command。只定義在 .bashrc 的 bash function 會測試失敗——重建後的 pane 裡也同樣找不到。"`.

- [ ] **Step 6: Unmount from Snapshot, delete the store**

- `SnapshotSettingsSection.tsx`: delete line 14 (`import { ResumeTemplateSettings } …`) and lines 484-486 (the comment + `<ResumeTemplateSettings busy={busy} />`).
- `SnapshotSettingsSection.records.test.tsx`: delete the `it('mounts the resume template editor above the records table'` case (lines 251-259) and its comment.
- `git rm spa/src/stores/useResumeTemplateStore.ts spa/src/stores/useResumeTemplateStore.test.ts` (this removes its `syncManager.register(STORAGE_KEYS.RESUME_TEMPLATES, …)`).
- `spa/src/lib/storage/keys.ts`: delete line 16 `RESUME_TEMPLATES: 'purdex-resume-templates',`.

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "useResumeTemplateStore|RESUME_TEMPLATES|resume_template\.(limit_global|test_against)" spa/src`
Expected: no output.

- [ ] **Step 7: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/settings src/lib/command-word.test.ts src/locales && npx eslint src/components/settings/ResumeTemplateSettings.tsx src/components/settings/ShellVerdict.tsx src/lib/command-word.ts && npx tsc -b`
Expected: PASS; no lint errors; tsc exits 0.

- [ ] **Step 8: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add -A spa/src/lib/command-word.ts spa/src/lib/command-word.test.ts spa/src/components/settings/ShellVerdict.tsx spa/src/components/settings/ResumeTemplateSettings.tsx spa/src/components/settings/ResumeTemplateSettings.test.tsx spa/src/components/settings/SnapshotSettingsSection.tsx spa/src/components/settings/SnapshotSettingsSection.records.test.tsx spa/src/stores/useResumeTemplateStore.ts spa/src/stores/useResumeTemplateStore.test.ts spa/src/lib/storage/keys.ts spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "refactor(spa): resume template editor is per host; drop global template store

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 5: `CommandIconView` + `CommandIconPicker`

**Files:**
- Create: `spa/src/lib/command-icons.ts`
- Create: `spa/src/components/hosts/CommandIconView.tsx`, `spa/src/components/hosts/CommandIconView.test.tsx`
- Create: `spa/src/components/hosts/CommandIconPicker.tsx`, `spa/src/components/hosts/CommandIconPicker.test.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `CC_ICON_VARIANTS`, `CODEX_ICON_VARIANTS`, `getAgentIcon`, `AgentIconComponent` (`spa/src/lib/agent-icons.tsx`); `prefetchWeight`, `getIconPath`, `isWeightLoaded` (`spa/src/features/workspace/lib/icon-path-cache.ts`); `renderPaths` (`spa/src/features/workspace/lib/render-paths.tsx`); `icon-meta.json` (Task 1); `CommandIcon`, `AgentIconValue` (Task 2)
- Produces:
  - `AGENT_ICON_VALUES: readonly AgentIconValue[]` (order `cc-bot, cc-star, openai, codex, opencode`), `agentIconComponent(value: AgentIconValue): AgentIconComponent`
  - `CommandIconView({ icon, size = 16, className }: { icon: CommandIcon; size?: number; className?: string })` — root has `data-testid="command-icon"`, `data-kind`, `data-value`, `data-fallback="true"` when rendering the `Terminal` fallback
  - `CommandIconPicker({ value, onChange, disabled }: { value: CommandIcon; onChange: (icon: CommandIcon) => void; disabled?: boolean })` — testids `command-icon-agent-<value>`, `command-icon-search`, `command-icon-phosphor-<Name>`, `command-icon-more`, `command-icon-loading`, `command-icon-empty`
  - `DEFAULT_COMMAND_ICON: CommandIcon = { kind: 'phosphor', value: 'Terminal' }` (exported from `lib/command-icons.ts`)

- [ ] **Step 1: Write the failing tests**

`spa/src/components/hosts/CommandIconView.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

const cache = vi.hoisted(() => ({ loaded: false, paths: { Rocket: 'M1,1L2,2' } as Record<string, string> }))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => cache.loaded,
  prefetchWeight: vi.fn(async () => { cache.loaded = true }),
  getIconPath: (name: string) => (cache.loaded ? cache.paths[name] ?? null : null),
}))

import { CommandIconView } from './CommandIconView'

beforeEach(() => { cache.loaded = false })

describe('CommandIconView', () => {
  it.each(['cc-bot', 'cc-star', 'openai', 'codex', 'opencode'] as const)('renders agent icon %s regardless of global variant settings', (value) => {
    render(<CommandIconView icon={{ kind: 'agent', value }} />)
    const el = screen.getByTestId('command-icon')
    expect(el).toHaveAttribute('data-kind', 'agent')
    expect(el).toHaveAttribute('data-value', value)
    expect(el.querySelector('svg')).not.toBeNull()
    expect(el).not.toHaveAttribute('data-fallback')
  })

  it('shows the Terminal fallback while path data loads, then the stored icon', async () => {
    render(<CommandIconView icon={{ kind: 'phosphor', value: 'Rocket' }} />)
    expect(screen.getByTestId('command-icon')).toHaveAttribute('data-fallback', 'true')
    await waitFor(() => expect(screen.getByTestId('command-icon')).not.toHaveAttribute('data-fallback'))
    expect(screen.getByTestId('command-icon').querySelector('path')?.getAttribute('d')).toBe('M1,1L2,2')
  })

  it('an unknown phosphor name stays on the fallback', async () => {
    cache.loaded = true
    render(<CommandIconView icon={{ kind: 'phosphor', value: 'NoSuchIcon' }} />)
    expect(screen.getByTestId('command-icon')).toHaveAttribute('data-fallback', 'true')
  })
})
```

`spa/src/components/hosts/CommandIconPicker.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
  getIconPath: () => 'M0,0L1,1',
}))

vi.mock('../../features/workspace/generated/icon-meta.json', () => ({
  default: [
    { n: 'Terminal', t: ['console', 'cli'], c: ['development'] },
    { n: 'Rocket', t: ['launch', 'spaceship'], c: ['objects'] },
    { n: 'House', t: ['home'], c: ['general'] },
    ...Array.from({ length: 150 }, (_, i) => ({ n: `Filler${i}`, t: ['filler'], c: [] })),
  ],
}))

import { CommandIconPicker } from './CommandIconPicker'

describe('CommandIconPicker', () => {
  it('offers the five agent icons first and selects one', () => {
    const onChange = vi.fn()
    render(<CommandIconPicker value={{ kind: 'phosphor', value: 'Terminal' }} onChange={onChange} />)
    for (const v of ['cc-bot', 'cc-star', 'openai', 'codex', 'opencode']) {
      expect(screen.getByTestId(`command-icon-agent-${v}`)).toBeInTheDocument()
    }
    fireEvent.click(screen.getByTestId('command-icon-agent-codex'))
    expect(onChange).toHaveBeenCalledWith({ kind: 'agent', value: 'codex' })
  })

  it('marks the current value as pressed', async () => {
    render(<CommandIconPicker value={{ kind: 'phosphor', value: 'Rocket' }} onChange={vi.fn()} />)
    expect(await screen.findByTestId('command-icon-phosphor-Rocket')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('command-icon-agent-cc-bot')).toHaveAttribute('aria-pressed', 'false')
  })

  it('lazily loads the catalog, paginates, and searches names and tags', async () => {
    const onChange = vi.fn()
    render(<CommandIconPicker value={{ kind: 'agent', value: 'cc-bot' }} onChange={onChange} />)
    await screen.findByTestId('command-icon-phosphor-Terminal')
    expect(screen.queryByTestId('command-icon-phosphor-Filler140')).toBeNull()
    fireEvent.click(screen.getByTestId('command-icon-more'))
    expect(screen.getByTestId('command-icon-phosphor-Filler140')).toBeInTheDocument()

    fireEvent.change(screen.getByTestId('command-icon-search'), { target: { value: 'launch' } })
    await waitFor(() => expect(screen.queryByTestId('command-icon-phosphor-Terminal')).toBeNull())
    fireEvent.click(screen.getByTestId('command-icon-phosphor-Rocket'))
    expect(onChange).toHaveBeenCalledWith({ kind: 'phosphor', value: 'Rocket' })

    fireEvent.change(screen.getByTestId('command-icon-search'), { target: { value: 'zzz' } })
    expect(await screen.findByTestId('command-icon-empty')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/CommandIconView.test.tsx src/components/hosts/CommandIconPicker.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement `spa/src/lib/command-icons.ts`**

```ts
// Agent icons a command may carry, rendered by explicit variant — independent
// of the user's global cc/codex icon-variant setting (spec §4.3).
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS, getAgentIcon, type AgentIconComponent } from './agent-icons'
import type { AgentIconValue, CommandIcon } from './host-config-api'

export const AGENT_ICON_VALUES: readonly AgentIconValue[] = ['cc-bot', 'cc-star', 'openai', 'codex', 'opencode']

export const DEFAULT_COMMAND_ICON: CommandIcon = { kind: 'phosphor', value: 'Terminal' }

const OPENCODE = getAgentIcon('opencode', { ccVariant: 'bot', codexVariant: 'openai' }) as AgentIconComponent

const AGENT_COMPONENTS: Record<AgentIconValue, AgentIconComponent> = {
  'cc-bot': CC_ICON_VARIANTS.bot,
  'cc-star': CC_ICON_VARIANTS.star,
  openai: CODEX_ICON_VARIANTS.openai,
  codex: CODEX_ICON_VARIANTS.codex,
  opencode: OPENCODE,
}

export function agentIconComponent(value: AgentIconValue): AgentIconComponent {
  return AGENT_COMPONENTS[value]
}
```

- [ ] **Step 4: Implement `spa/src/components/hosts/CommandIconView.tsx`**

```tsx
import { useEffect, useState } from 'react'
import { Terminal } from '@phosphor-icons/react'
import { agentIconComponent } from '../../lib/command-icons'
import type { CommandIcon } from '../../lib/host-config-api'
import { getIconPath, isWeightLoaded, prefetchWeight } from '../../features/workspace/lib/icon-path-cache'
import { renderPaths } from '../../features/workspace/lib/render-paths'

const WEIGHT = 'regular'

/**
 * A stored command icon. Phosphor icons render from the lazily fetched
 * `/icons/regular.json` path data (same pipeline as workspace icons), so no
 * icon component set enters a JS chunk. Unknown names or data still loading
 * render the `Terminal` fallback.
 */
export function CommandIconView({ icon, size = 16, className }: { icon: CommandIcon; size?: number; className?: string }) {
  const [, setTick] = useState(0)
  const needsPaths = icon.kind === 'phosphor' && !isWeightLoaded(WEIGHT)

  useEffect(() => {
    if (!needsPaths) return
    let cancelled = false
    prefetchWeight(WEIGHT).then(() => { if (!cancelled) setTick((n) => n + 1) }).catch(() => {})
    return () => { cancelled = true }
  }, [needsPaths])

  if (icon.kind === 'agent') {
    const Agent = agentIconComponent(icon.value)
    return (
      <span data-testid="command-icon" data-kind="agent" data-value={icon.value} className={`inline-flex ${className ?? ''}`}>
        <Agent size={size} />
      </span>
    )
  }

  const path = getIconPath(icon.value, WEIGHT)
  return (
    <span
      data-testid="command-icon"
      data-kind="phosphor"
      data-value={icon.value}
      {...(path ? {} : { 'data-fallback': 'true' })}
      className={`inline-flex ${className ?? ''}`}
    >
      {path ? (
        <svg width={size} height={size} viewBox="0 0 256 256" fill="currentColor" aria-hidden="true">
          {renderPaths(path)}
        </svg>
      ) : (
        <Terminal size={size} aria-hidden="true" />
      )}
    </span>
  )
}
```

- [ ] **Step 5: Implement `spa/src/components/hosts/CommandIconPicker.tsx`**

```tsx
import { useEffect, useMemo, useState } from 'react'
import { MagnifyingGlass } from '@phosphor-icons/react'
import { AGENT_ICON_VALUES } from '../../lib/command-icons'
import type { CommandIcon } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import { CommandIconView } from './CommandIconView'

interface IconMeta { n: string; t: string[]; c: string[] }

const PAGE = 120

// Module-level so reopening the picker does not re-import.
let catalogPromise: Promise<IconMeta[]> | null = null
function loadCatalog(): Promise<IconMeta[]> {
  catalogPromise ??= import('../../features/workspace/generated/icon-meta.json').then((m) => m.default as IconMeta[])
  return catalogPromise
}

function matches(meta: IconMeta, q: string): boolean {
  return meta.n.toLowerCase().includes(q) || meta.t.some((tag) => tag.toLowerCase().includes(q))
}

export function CommandIconPicker({ value, onChange, disabled = false }: {
  value: CommandIcon
  onChange: (icon: CommandIcon) => void
  disabled?: boolean
}) {
  const t = useI18nStore((s) => s.t)
  const [catalog, setCatalog] = useState<IconMeta[] | null>(null)
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(PAGE)

  useEffect(() => {
    let cancelled = false
    loadCatalog().then((list) => { if (!cancelled) setCatalog(list) }).catch(() => { if (!cancelled) setCatalog([]) })
    return () => { cancelled = true }
  }, [])

  const filtered = useMemo(() => {
    if (!catalog) return []
    const q = query.trim().toLowerCase()
    return q ? catalog.filter((m) => matches(m, q)) : catalog
  }, [catalog, query])

  const cell = (selected: boolean) =>
    `w-8 h-8 rounded-md flex items-center justify-center cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 ${
      selected ? 'bg-accent/20 ring-2 ring-accent text-text-primary' : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
    }`

  return (
    <div className="flex flex-col gap-2">
      <div className="text-[11px] text-text-secondary">{t('command_icon.agents')}</div>
      <div className="flex flex-wrap gap-1.5">
        {AGENT_ICON_VALUES.map((v) => {
          const selected = value.kind === 'agent' && value.value === v
          return (
            <button key={v} type="button" data-testid={`command-icon-agent-${v}`} aria-pressed={selected} title={v}
              disabled={disabled} onClick={() => onChange({ kind: 'agent', value: v })} className={cell(selected)}>
              <CommandIconView icon={{ kind: 'agent', value: v }} size={18} />
            </button>
          )
        })}
      </div>

      <div className="text-[11px] text-text-secondary">{t('command_icon.all')}</div>
      <div className="relative">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-tertiary" />
        <input data-testid="command-icon-search" value={query} disabled={disabled}
          onChange={(e) => { setQuery(e.target.value); setLimit(PAGE) }}
          placeholder={t('command_icon.search')}
          className="w-full pl-8 pr-3 py-1.5 bg-surface-tertiary border border-border-subtle rounded-md text-xs text-text-primary focus:outline-none focus:ring-1 focus:ring-accent" />
      </div>

      {!catalog ? (
        <div data-testid="command-icon-loading" className="text-xs text-text-muted">{t('command_icon.loading')}</div>
      ) : filtered.length === 0 ? (
        <div data-testid="command-icon-empty" className="text-xs text-text-muted">{t('command_icon.no_results')}</div>
      ) : (
        <div className="max-h-48 overflow-y-auto p-0.5">
          <div className="flex flex-wrap gap-1.5">
            {filtered.slice(0, limit).map((m) => {
              const selected = value.kind === 'phosphor' && value.value === m.n
              return (
                <button key={m.n} type="button" data-testid={`command-icon-phosphor-${m.n}`} aria-pressed={selected} title={m.n}
                  disabled={disabled} onClick={() => onChange({ kind: 'phosphor', value: m.n })} className={cell(selected)}>
                  <CommandIconView icon={{ kind: 'phosphor', value: m.n }} size={18} />
                </button>
              )
            })}
          </div>
          {filtered.length > limit && (
            <button type="button" data-testid="command-icon-more" onClick={() => setLimit((n) => n + PAGE)}
              className="mt-2 text-xs text-text-secondary hover:text-text-primary cursor-pointer">
              {t('command_icon.more', { n: filtered.length - limit })}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 6: Locale keys**

en.json:
```json
  "command_icon.agents": "Agents",
  "command_icon.all": "All icons",
  "command_icon.search": "Search icons",
  "command_icon.loading": "Loading icons…",
  "command_icon.no_results": "No icons match",
  "command_icon.more": "Show {{n}} more",
```
zh-TW.json:
```json
  "command_icon.agents": "Agent",
  "command_icon.all": "全部圖示",
  "command_icon.search": "搜尋圖示",
  "command_icon.loading": "載入圖示中…",
  "command_icon.no_results": "沒有符合的圖示",
  "command_icon.more": "再顯示 {{n}} 個",
```

- [ ] **Step 7: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/CommandIconView.test.tsx src/components/hosts/CommandIconPicker.test.tsx src/locales && npx eslint src/lib/command-icons.ts src/components/hosts/CommandIconView.tsx src/components/hosts/CommandIconPicker.tsx && npx tsc -b`
Expected: PASS (7 + 3 tests); no lint errors; tsc exits 0. If tsc rejects the JSON dynamic import type, cast: `.then((m) => (m as { default: unknown }).default as IconMeta[])`.

- [ ] **Step 8: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/command-icons.ts spa/src/components/hosts/CommandIconView.tsx spa/src/components/hosts/CommandIconView.test.tsx spa/src/components/hosts/CommandIconPicker.tsx spa/src/components/hosts/CommandIconPicker.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): command icon view and picker

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 6: Validation helpers, host-config gate, path check, Host › Projects

**Files:**
- Create: `spa/src/lib/host-config-validate.ts`, `spa/src/lib/host-config-validate.test.ts`
- Create: `spa/src/components/hosts/HostConfigNotice.tsx`
- Create: `spa/src/components/hosts/usePathCheck.ts`, `spa/src/components/hosts/usePathCheck.test.ts`
- Create: `spa/src/components/hosts/ProjectEditDialog.tsx`, `spa/src/components/hosts/ProjectsSection.tsx`, `spa/src/components/hosts/ProjectsSection.test.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx:57,462-470` (import + `projects` at order 7)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: Task 2 store/api (`useHostConfigStore`, `checkHostPath`, `HostConfigConflictError`, `HostProject`), `generateId` (`spa/src/lib/id.ts`), `useHostStore.runtime`
- Produces:
  - `suggestSlug(name: string): string`
  - `type FieldErrors<K extends string> = Partial<Record<K, string>>` (values are i18n keys)
  - `validateProject(draft: HostProject, others: HostProject[]): FieldErrors<'name'|'slug'|'path'>`
  - `validateCommand(draft: HostCommand): FieldErrors<'name'|'command'>`
  - `moveItem<T>(list: readonly T[], index: number, delta: -1 | 1): T[]`
  - `newConfigId(): string`
  - `MAX_CONFIG_ITEMS = 200`
  - `useHostConfigGate(hostId): { entry: HostConfigEntry; editable: boolean; notice: { key: string; params?: Record<string, string> } | null }` and `<HostConfigNotice notice={...} />` (`data-testid="host-config-notice"`)
  - `usePathCheck(hostId: string, path: string, delayMs?: number): PathCheckStatus | 'checking' | 'idle'`
  - `ProjectsSection({ hostId })`

- [ ] **Step 1: Failing validation tests**

`spa/src/lib/host-config-validate.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { moveItem, newConfigId, suggestSlug, validateCommand, validateProject } from './host-config-validate'

const p = (over: Partial<{ id: string; name: string; slug: string; path: string }> = {}) =>
  ({ id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex', ...over })

describe('suggestSlug', () => {
  it.each([
    ['Purdex', 'purdex'],
    ['  My Cool_App!! ', 'my-cool-app'],
    ['中文 Project 2', 'project-2'],
    ['---', ''],
    ['a'.repeat(40), 'a'.repeat(32)],
    ['ab-'.repeat(20), ('ab-'.repeat(11)).slice(0, 32).replace(/-+$/, '')],
  ])('%j → %j', (name, slug) => expect(suggestSlug(name)).toBe(slug))
})

describe('validateProject', () => {
  it('accepts a valid project', () => expect(validateProject(p(), [])).toEqual({}))
  it('name: trimmed 1-64 runes', () => {
    expect(validateProject(p({ name: '   ' }), []).name).toBe('projects.invalid.name')
    expect(validateProject(p({ name: '字'.repeat(64) }), [])).toEqual({})
    expect(validateProject(p({ name: '字'.repeat(65) }), []).name).toBe('projects.invalid.name')
  })
  it('slug: pattern and uniqueness among other projects', () => {
    expect(validateProject(p({ slug: 'Bad' }), []).slug).toBe('projects.invalid.slug')
    expect(validateProject(p({ slug: '-x' }), []).slug).toBe('projects.invalid.slug')
    expect(validateProject(p({ slug: 'a'.repeat(33) }), []).slug).toBe('projects.invalid.slug')
    expect(validateProject(p(), [p({ id: 'p2' })]).slug).toBe('projects.invalid.slug_taken')
    expect(validateProject(p(), [p()])).toEqual({}) // same id is itself
  })
  it('path: /, ~ or ~/ prefix, 1-1024 bytes, no NUL', () => {
    for (const ok of ['/', '/srv/app', '~', '~/w']) expect(validateProject(p({ path: ok }), [])).toEqual({})
    for (const bad of ['', 'rel/x', '~user/x', '/a\u0000b', '/' + 'a'.repeat(1024)]) {
      expect(validateProject(p({ path: bad }), []).path).toBe('projects.invalid.path')
    }
  })
})

describe('validateCommand', () => {
  const c = (over = {}) => ({ id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent' as const, value: 'cc-bot' as const }, ...over })
  it('accepts a valid command', () => expect(validateCommand(c())).toEqual({}))
  it('name trimmed 1-64 runes; command 1-4096 bytes, no NUL', () => {
    expect(validateCommand(c({ name: ' ' })).name).toBe('commands.invalid.name')
    expect(validateCommand(c({ command: '' })).command).toBe('commands.invalid.command')
    expect(validateCommand(c({ command: 'a\u0000' })).command).toBe('commands.invalid.command')
    expect(validateCommand(c({ command: 'é'.repeat(2049) })).command).toBe('commands.invalid.command') // 4098 bytes
  })
})

describe('moveItem / newConfigId', () => {
  it('moves within bounds and is a no-op at the edges', () => {
    expect(moveItem(['a', 'b', 'c'], 0, 1)).toEqual(['b', 'a', 'c'])
    expect(moveItem(['a', 'b', 'c'], 2, 1)).toEqual(['a', 'b', 'c'])
    expect(moveItem(['a', 'b', 'c'], 0, -1)).toEqual(['a', 'b', 'c'])
  })
  it('ids satisfy the daemon id rule', () => expect(newConfigId()).toMatch(/^[A-Za-z0-9_-]{1,64}$/))
})
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-validate.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 2: Implement `spa/src/lib/host-config-validate.ts`**

```ts
// Client-side mirror of the daemon's hostconfig validation (spec §3.3). The
// daemon still validates; this exists so the dialog can say what is wrong
// before a round trip.
import { generateId } from './id'
import type { HostCommand, HostProject } from './host-config-api'

export type FieldErrors<K extends string> = Partial<Record<K, string>>

export const MAX_CONFIG_ITEMS = 200
const SLUG = /^[a-z0-9][a-z0-9-]{0,31}$/
const encoder = new TextEncoder()
const runes = (s: string) => Array.from(s).length
const bytes = (s: string) => encoder.encode(s).length

export function suggestSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32)
    .replace(/-+$/, '')
}

function validName(name: string): boolean {
  const n = name.trim()
  return runes(n) >= 1 && runes(n) <= 64
}

export function validateProject(draft: HostProject, others: HostProject[]): FieldErrors<'name' | 'slug' | 'path'> {
  const errors: FieldErrors<'name' | 'slug' | 'path'> = {}
  if (!validName(draft.name)) errors.name = 'projects.invalid.name'
  if (!SLUG.test(draft.slug)) errors.slug = 'projects.invalid.slug'
  else if (others.some((o) => o.id !== draft.id && o.slug === draft.slug)) errors.slug = 'projects.invalid.slug_taken'
  const path = draft.path.trim()
  const shapeOk = path.startsWith('/') || path === '~' || path.startsWith('~/')
  if (!shapeOk || bytes(path) < 1 || bytes(path) > 1024 || path.includes('\u0000')) errors.path = 'projects.invalid.path'
  return errors
}

export function validateCommand(draft: HostCommand): FieldErrors<'name' | 'command'> {
  const errors: FieldErrors<'name' | 'command'> = {}
  if (!validName(draft.name)) errors.name = 'commands.invalid.name'
  const size = bytes(draft.command)
  if (size < 1 || size > 4096 || draft.command.includes('\u0000')) errors.command = 'commands.invalid.command'
  return errors
}

export function moveItem<T>(list: readonly T[], index: number, delta: -1 | 1): T[] {
  const target = index + delta
  const next = list.slice()
  if (index < 0 || index >= list.length || target < 0 || target >= list.length) return next
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

export function newConfigId(): string {
  return generateId()
}
```

Run the test again. Expected: PASS. (If the `'ab-'` slug case disagrees, fix the implementation, not the test: collapse → trim → cut at 32 → trim trailing `-`.)

- [ ] **Step 3: Failing `usePathCheck` test**

`spa/src/components/hosts/usePathCheck.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { usePathCheck } from './usePathCheck'
import * as api from '../../lib/host-config-api'

vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  checkHostPath: vi.fn(),
}))

beforeEach(() => { vi.useFakeTimers(); vi.mocked(api.checkHostPath).mockReset() })
afterEach(() => vi.useRealTimers())

describe('usePathCheck', () => {
  it('debounces 400 ms and reports the daemon verdict', async () => {
    vi.mocked(api.checkHostPath).mockResolvedValue({ status: 'dir', resolved: '/w' })
    const { result, rerender } = renderHook(({ path }) => usePathCheck('h1', path), { initialProps: { path: '~/a' } })
    expect(result.current).toBe('checking')
    rerender({ path: '~/w' })
    await act(async () => { vi.advanceTimersByTime(399) })
    expect(api.checkHostPath).not.toHaveBeenCalled()
    await act(async () => { vi.advanceTimersByTime(1); await Promise.resolve() })
    expect(api.checkHostPath).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.checkHostPath).mock.calls[0].slice(0, 2)).toEqual(['h1', '~/w'])
    expect(result.current).toBe('dir')
  })

  it('an empty path is idle and never checked', async () => {
    const { result } = renderHook(() => usePathCheck('h1', '  '))
    await act(async () => { vi.advanceTimersByTime(1000) })
    expect(result.current).toBe('idle')
    expect(api.checkHostPath).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 4: Implement `spa/src/components/hosts/usePathCheck.ts`**

```ts
import { useEffect, useState } from 'react'
import { checkHostPath, type PathCheckStatus } from '../../lib/host-config-api'

export type PathVerdict = PathCheckStatus | 'checking' | 'idle'

/** Debounced "does this path exist on the host" advice. Never blocks anything. */
export function usePathCheck(hostId: string, path: string, delayMs = 400): PathVerdict {
  const trimmed = path.trim()
  const [verdict, setVerdict] = useState<{ key: string; status: PathVerdict }>({ key: '', status: 'idle' })
  const key = `${hostId}\u0000${trimmed}`

  useEffect(() => {
    if (!trimmed) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      checkHostPath(hostId, trimmed, controller.signal).then((r) => {
        if (!controller.signal.aborted) setVerdict({ key, status: r.status })
      })
    }, delayMs)
    return () => { clearTimeout(timer); controller.abort() }
  }, [hostId, trimmed, delayMs, key])

  if (!trimmed) return 'idle'
  return verdict.key === key ? verdict.status : 'checking'
}
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/usePathCheck.test.ts`
Expected: PASS.

- [ ] **Step 5: Implement the gate `spa/src/components/hosts/HostConfigNotice.tsx`**

```tsx
import { useEffect } from 'react'
import { WarningCircle } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { EMPTY_HOST_CONFIG, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'

export interface GateNotice { key: string; params?: Record<string, string> }

/**
 * Shared by Projects / Commands: loads on mount (spec §4.1) and decides
 * whether editing is allowed. Offline or an old daemon → notice, read-only.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useHostConfigGate(hostId: string): { entry: HostConfigEntry; editable: boolean; notice: GateNotice | null } {
  const entry = useHostConfigStore((s) => s.byHost[hostId] ?? EMPTY_HOST_CONFIG)
  const online = useHostStore((s) => s.runtime[hostId]?.status === 'connected')

  useEffect(() => {
    if (online) void useHostConfigStore.getState().load(hostId)
  }, [hostId, online])

  let notice: GateNotice | null = null
  if (!online) notice = { key: 'host_config.offline' }
  else if (entry.status === 'unsupported') notice = { key: 'host_config.unsupported' }
  else if (entry.status === 'error') notice = { key: 'host_config.load_failed', params: { reason: entry.error ?? '' } }
  else if (entry.status !== 'ready') notice = { key: 'host_config.loading' }

  return { entry, editable: online && entry.status === 'ready', notice }
}

export function HostConfigNotice({ notice }: { notice: GateNotice | null }) {
  const t = useI18nStore((s) => s.t)
  if (!notice) return null
  return (
    <div data-testid="host-config-notice" data-notice={notice.key} className="mb-3 flex items-center gap-1.5 text-xs text-text-secondary">
      <WarningCircle size={14} />
      {t(notice.key, notice.params)}
    </div>
  )
}
```

- [ ] **Step 6: Failing ProjectsSection test**

`spa/src/components/hosts/ProjectsSection.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { ProjectsSection } from './ProjectsSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import * as api from '../../lib/host-config-api'
import { HostConfigConflictError, type HostProject } from '../../lib/host-config-api'

vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  checkHostPath: vi.fn(async () => ({ status: 'dir', resolved: '/x' })),
}))

const H = 'h1'
const P1: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const P2: HostProject = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/missing' }
const saveProjects = vi.fn()

function seed(projects: HostProject[], status: 'ready' | 'unsupported' = 'ready') {
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry(status), projects } },
    load: vi.fn(async () => {}),
    saveProjects,
  })
}

beforeEach(() => {
  saveProjects.mockReset().mockImplementation(async (hostId: string, items: HostProject[]) => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], projects: items } } }))
  })
  vi.mocked(api.checkHostPath).mockImplementation(async (_h, path) =>
    ({ status: path === '/missing' ? 'missing' : 'dir', resolved: path }))
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
  seed([P1, P2])
})

describe('ProjectsSection', () => {
  it('lists projects in order with name, slug, path and a per-row path status', async () => {
    render(<ProjectsSection hostId={H} />)
    const rows = screen.getAllByTestId(/^project-row-/)
    expect(rows.map((r) => r.getAttribute('data-testid'))).toEqual(['project-row-p1', 'project-row-p2'])
    expect(within(rows[0]).getByText('purdex')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('project-path-status-p2')).toHaveAttribute('data-status', 'missing'))
    expect(screen.getByTestId('project-path-status-p1')).toHaveAttribute('data-status', 'dir')
  })

  it('reorders with the down button and saves the new order', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-down-p1'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [P2, P1]))
    expect(screen.getByTestId('project-up-p1')).toBeEnabled()
    expect(screen.getByTestId('project-down-p1')).toBeDisabled()
  })

  it('adds a project; slug follows the name until edited; client validation blocks bad input', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-add'))
    fireEvent.change(screen.getByTestId('project-field-name'), { target: { value: 'My App' } })
    expect(screen.getByTestId('project-field-slug')).toHaveValue('my-app')
    fireEvent.change(screen.getByTestId('project-field-slug'), { target: { value: 'purdex' } })
    fireEvent.change(screen.getByTestId('project-field-name'), { target: { value: 'My App 2' } })
    expect(screen.getByTestId('project-field-slug')).toHaveValue('purdex') // user-edited: no longer follows
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: 'relative' } })
    fireEvent.click(screen.getByTestId('project-save'))
    expect(screen.getByTestId('project-error-slug')).toHaveTextContent('already used')
    expect(screen.getByTestId('project-error-path')).toBeInTheDocument()
    expect(saveProjects).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('project-field-slug'), { target: { value: 'my-app' } })
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: '~/w/app' } })
    fireEvent.click(screen.getByTestId('project-save'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledTimes(1))
    const saved = saveProjects.mock.calls[0][1] as HostProject[]
    expect(saved).toHaveLength(3)
    expect(saved[2]).toMatchObject({ name: 'My App 2', slug: 'my-app', path: '~/w/app' })
    expect(saved[2].id).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
  })

  it('the dialog shows the live path check but never blocks saving on it', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-edit-p1'))
    fireEvent.change(screen.getByTestId('project-field-path'), { target: { value: '/missing' } })
    await waitFor(() => expect(screen.getByTestId('project-dialog-path-status')).toHaveAttribute('data-status', 'missing'))
    fireEvent.click(screen.getByTestId('project-save'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [{ ...P1, path: '/missing' }, P2]))
  })

  it('deletes after confirmation', async () => {
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-delete-p2'))
    fireEvent.click(screen.getByTestId('project-delete-confirm-p2'))
    await waitFor(() => expect(saveProjects).toHaveBeenCalledWith(H, [P1]))
  })

  it('a conflict shows the reloaded notice', async () => {
    saveProjects.mockRejectedValue(new HostConfigConflictError({ items: [P1], revision: 9 }))
    render(<ProjectsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('project-delete-p2'))
    fireEvent.click(screen.getByTestId('project-delete-confirm-p2'))
    expect(await screen.findByTestId('projects-save-error')).toHaveTextContent('Changed elsewhere')
  })

  it('offline or old daemon → notice, editing disabled', () => {
    seed([P1], 'unsupported')
    const { unmount } = render(<ProjectsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.unsupported')
    expect(screen.getByTestId('project-add')).toBeDisabled()
    unmount()
    seed([P1])
    useHostStore.setState({ runtime: { [H]: { status: 'disconnected' } } })
    render(<ProjectsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.offline')
    expect(screen.getByTestId('project-edit-p1')).toBeDisabled()
  })
})
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/ProjectsSection.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 7: Implement `spa/src/components/hosts/ProjectEditDialog.tsx`**

```tsx
import { useState } from 'react'
import { CheckCircle, CircleNotch, Question, Warning, XCircle } from '@phosphor-icons/react'
import { suggestSlug, validateProject, type FieldErrors } from '../../lib/host-config-validate'
import type { HostProject } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import { usePathCheck, type PathVerdict } from './usePathCheck'

// eslint-disable-next-line react-refresh/only-export-components
export const PATH_STATUS_ICON: Record<PathVerdict, { Icon: typeof CheckCircle; cls: string; key: string }> = {
  dir: { Icon: CheckCircle, cls: 'text-status-success', key: 'projects.path.dir' },
  not_dir: { Icon: Warning, cls: 'text-status-warning', key: 'projects.path.not_dir' },
  missing: { Icon: XCircle, cls: 'text-red-400', key: 'projects.path.missing' },
  error: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
  unverifiable: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
  checking: { Icon: CircleNotch, cls: 'text-text-muted animate-spin', key: 'projects.path.checking' },
  idle: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
}

export function PathStatusIcon({ status, testId }: { status: PathVerdict; testId: string }) {
  const t = useI18nStore((s) => s.t)
  const { Icon, cls, key } = PATH_STATUS_ICON[status]
  return (
    <span data-testid={testId} data-status={status} title={`${t(key)} · ${t('projects.path_hint')}`} className="inline-flex">
      <Icon size={14} className={cls} />
    </span>
  )
}

export function ProjectEditDialog({ hostId, initial, others, onSave, onCancel }: {
  hostId: string
  initial: HostProject
  others: HostProject[]
  onSave: (project: HostProject) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initial)
  // A new project's slug follows its name until the user types in the slug.
  const [slugTouched, setSlugTouched] = useState(initial.slug !== '')
  const [errors, setErrors] = useState<FieldErrors<'name' | 'slug' | 'path'>>({})
  const pathStatus = usePathCheck(hostId, draft.path)
  const isNew = !others.some((o) => o.id === initial.id)

  const submit = () => {
    const next = { ...draft, name: draft.name.trim(), path: draft.path.trim() }
    const found = validateProject(next, others)
    setErrors(found)
    if (Object.keys(found).length === 0) onSave(next)
  }

  const input = 'w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'
  const err = (field: 'name' | 'slug' | 'path') => errors[field]
    ? <p data-testid={`project-error-${field}`} className="mt-1 text-xs text-red-400">{t(errors[field]!)}</p>
    : null

  return (
    <div role="dialog" aria-label={t(isNew ? 'projects.add_title' : 'projects.edit_title')}
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <h3 className="text-sm font-semibold mb-3">{t(isNew ? 'projects.add_title' : 'projects.edit_title')}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.name')}
        <input data-testid="project-field-name" autoFocus className={input} value={draft.name}
          onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value, slug: slugTouched ? d.slug : suggestSlug(e.target.value) }))} />
        {err('name')}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.slug')}
        <input data-testid="project-field-slug" className={`${input} font-mono`} value={draft.slug}
          onChange={(e) => { setSlugTouched(true); setDraft((d) => ({ ...d, slug: e.target.value })) }} />
        {err('slug')}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.path')}
        <span className="flex items-center gap-2">
          <input data-testid="project-field-path" className={`${input} font-mono`} value={draft.path}
            onChange={(e) => setDraft((d) => ({ ...d, path: e.target.value }))}
            onKeyDown={(e) => { if (e.key === 'Enter') submit() }} />
          <PathStatusIcon status={pathStatus} testId="project-dialog-path-status" />
        </span>
        {err('path')}
      </label>
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="project-save" onClick={submit}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer">{t('common.save')}</button>
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 8: Implement `spa/src/components/hosts/ProjectsSection.tsx`**

```tsx
import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { HostConfigConflictError, type HostProject } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, moveItem, newConfigId } from '../../lib/host-config-validate'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'
import { PathStatusIcon, ProjectEditDialog } from './ProjectEditDialog'
import { usePathCheck } from './usePathCheck'

function RowPathStatus({ hostId, project }: { hostId: string; project: HostProject }) {
  const status = usePathCheck(hostId, project.path, 0)
  return <PathStatusIcon status={status} testId={`project-path-status-${project.id}`} />
}

export function ProjectsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const projects = entry.projects
  const [editing, setEditing] = useState<HostProject | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const locked = !editable || saving

  const persist = async (next: HostProject[]) => {
    setSaving(true)
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveProjects(hostId, next)
      return true
    } catch (err) {
      setSaveError(err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      return false
    } finally {
      setSaving(false)
    }
  }

  const handleSave = async (project: HostProject) => {
    const exists = projects.some((p) => p.id === project.id)
    const next = exists ? projects.map((p) => (p.id === project.id ? project : p)) : [...projects, project]
    if (await persist(next)) setEditing(null)
  }

  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.projects')}</h2>
        <button type="button" data-testid="project-add" disabled={locked || projects.length >= MAX_CONFIG_ITEMS}
          onClick={() => setEditing({ id: newConfigId(), name: '', slug: '', path: '' })}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('projects.add')}
        </button>
      </div>

      <HostConfigNotice notice={notice} />
      {saveError && <p data-testid="projects-save-error" className="mb-3 text-xs text-status-warning">{saveError}</p>}

      {editing && (
        <ProjectEditDialog hostId={hostId} initial={editing} others={projects}
          onSave={(p) => { void handleSave(p) }} onCancel={() => setEditing(null)} />
      )}

      {projects.length === 0 ? (
        <p className="text-sm text-text-muted">{t('projects.empty')}</p>
      ) : (
        <div className="border border-border-subtle rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-surface-tertiary text-text-secondary text-xs">
                <th className="text-left px-3 py-2">{t('projects.col.name')}</th>
                <th className="text-left px-3 py-2">{t('projects.col.slug')}</th>
                <th className="text-left px-3 py-2">{t('projects.col.path')}</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody>
              {projects.map((project, index) => (
                <tr key={project.id} data-testid={`project-row-${project.id}`} className="border-t border-border-subtle">
                  <td className="px-3 py-2 text-text-primary">{project.name}</td>
                  <td className="px-3 py-2 font-mono text-xs text-text-secondary">{project.slug}</td>
                  <td className="px-3 py-2 font-mono text-xs text-text-muted">
                    <span className="inline-flex items-center gap-1.5 max-w-[260px]">
                      <RowPathStatus hostId={hostId} project={project} />
                      <span className="truncate" title={project.path}>{project.path}</span>
                    </span>
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex items-center justify-end gap-1">
                      <button type="button" data-testid={`project-up-${project.id}`} title={t('host_config.move_up')}
                        disabled={locked || index === 0} onClick={() => void persist(moveItem(projects, index, -1))} className={iconBtn}><ArrowUp size={14} /></button>
                      <button type="button" data-testid={`project-down-${project.id}`} title={t('host_config.move_down')}
                        disabled={locked || index === projects.length - 1} onClick={() => void persist(moveItem(projects, index, 1))} className={iconBtn}><ArrowDown size={14} /></button>
                      <button type="button" data-testid={`project-edit-${project.id}`} title={t('common.edit')}
                        disabled={locked} onClick={() => setEditing(project)} className={iconBtn}><PencilSimple size={14} /></button>
                      {deleting === project.id ? (
                        <>
                          <button type="button" data-testid={`project-delete-confirm-${project.id}`} disabled={locked}
                            onClick={() => { setDeleting(null); void persist(projects.filter((p) => p.id !== project.id)) }}
                            className="p-1 text-red-400 cursor-pointer"><Check size={14} /></button>
                          <button type="button" onClick={() => setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                        </>
                      ) : (
                        <button type="button" data-testid={`project-delete-${project.id}`} title={t('common.delete')}
                          disabled={locked} onClick={() => setDeleting(project.id)} className={iconBtn}><Trash size={14} /></button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 9: Register the section (order 7) and add locale keys**

`spa/src/lib/register-modules/index.tsx`: after line 57 add `import { ProjectsSection } from '../../components/hosts/ProjectsSection'`; after the `nex` entry (line 469) add:
```tsx
    { localId: 'projects',  labelKey: 'hosts.projects',  order: 7, component: ProjectsSection },
```

en.json:
```json
  "hosts.projects": "Projects",
  "host_config.offline": "Host is offline — editing is disabled.",
  "host_config.unsupported": "This host's daemon is too old for projects and commands. Update pdx on this host.",
  "host_config.loading": "Loading host config…",
  "host_config.load_failed": "Could not load host config: {{reason}}",
  "host_config.move_up": "Move up",
  "host_config.move_down": "Move down",
  "projects.add": "Add project",
  "projects.add_title": "Add project",
  "projects.edit_title": "Edit project",
  "projects.empty": "No projects on this host yet.",
  "projects.col.name": "Name",
  "projects.col.slug": "Slug",
  "projects.col.path": "Path",
  "projects.field.name": "Name",
  "projects.field.slug": "Slug (used for session names)",
  "projects.field.path": "Path on this host",
  "projects.invalid.name": "Name must be 1–64 characters.",
  "projects.invalid.slug": "Slug: lowercase letters, digits and -, starting with a letter or digit, up to 32.",
  "projects.invalid.slug_taken": "This slug is already used by another project on this host.",
  "projects.invalid.path": "Path must start with /, ~ or ~/ (up to 1024 bytes).",
  "projects.path.dir": "Directory exists",
  "projects.path.not_dir": "Exists but is not a directory",
  "projects.path.missing": "Does not exist",
  "projects.path.unknown": "Could not check",
  "projects.path.checking": "Checking…",
  "projects.path_hint": "checked on this host",
```
zh-TW.json:
```json
  "hosts.projects": "專案",
  "host_config.offline": "主機離線，暫停編輯。",
  "host_config.unsupported": "此主機的 daemon 版本過舊，不支援專案與指令，請更新該主機的 pdx。",
  "host_config.loading": "載入主機設定中…",
  "host_config.load_failed": "無法載入主機設定：{{reason}}",
  "host_config.move_up": "上移",
  "host_config.move_down": "下移",
  "projects.add": "新增專案",
  "projects.add_title": "新增專案",
  "projects.edit_title": "編輯專案",
  "projects.empty": "此主機尚未設定專案。",
  "projects.col.name": "名稱",
  "projects.col.slug": "Slug",
  "projects.col.path": "路徑",
  "projects.field.name": "名稱",
  "projects.field.slug": "Slug（用於 session 名稱）",
  "projects.field.path": "此主機上的路徑",
  "projects.invalid.name": "名稱需為 1–64 個字元。",
  "projects.invalid.slug": "Slug 只能用小寫英數與 -，以英數開頭，最多 32 字。",
  "projects.invalid.slug_taken": "此主機已有其他專案使用這個 slug。",
  "projects.invalid.path": "路徑需以 /、~ 或 ~/ 開頭（最多 1024 bytes）。",
  "projects.path.dir": "目錄存在",
  "projects.path.not_dir": "存在但不是目錄",
  "projects.path.missing": "不存在",
  "projects.path.unknown": "無法檢查",
  "projects.path.checking": "檢查中…",
  "projects.path_hint": "於此主機檢查",
```
(The en "already used" substring is what the test asserts.)

- [ ] **Step 10: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/host-config-validate.test.ts src/components/hosts/usePathCheck.test.ts src/components/hosts/ProjectsSection.test.tsx src/locales src/lib/register-modules.test.ts src/lib/host-builtin-sections.test.tsx && npx eslint src/lib/host-config-validate.ts src/components/hosts/HostConfigNotice.tsx src/components/hosts/usePathCheck.ts src/components/hosts/ProjectEditDialog.tsx src/components/hosts/ProjectsSection.tsx src/lib/register-modules/index.tsx && npx tsc -b`
Expected: PASS; no lint errors; tsc exits 0.

- [ ] **Step 11: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/lib/host-config-validate.ts spa/src/lib/host-config-validate.test.ts spa/src/components/hosts/HostConfigNotice.tsx spa/src/components/hosts/usePathCheck.ts spa/src/components/hosts/usePathCheck.test.ts spa/src/components/hosts/ProjectEditDialog.tsx spa/src/components/hosts/ProjectsSection.tsx spa/src/components/hosts/ProjectsSection.test.tsx spa/src/lib/register-modules/index.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): Host > Projects page with path check

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 7: Host › Commands (Normal / Resume tabs)

**Files:**
- Create: `spa/src/components/hosts/CommandEditDialog.tsx`, `spa/src/components/hosts/CommandsSection.tsx`, `spa/src/components/hosts/CommandsSection.test.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx` (import + `commands` at order 8)
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `useHostConfigGate`, `HostConfigNotice` (Task 6); `validateCommand`, `moveItem`, `newConfigId`, `MAX_CONFIG_ITEMS` (Task 6); `CommandIconPicker`, `CommandIconView`, `DEFAULT_COMMAND_ICON` (Task 5); `ResumeTemplateSettings`, `ShellVerdict`, `commandWordOf` (Task 4); `resolveShellCommand` (`spa/src/lib/host-api.ts:321`)
- Produces: `CommandsSection({ hostId })` — tab buttons `commands-tab-normal` / `commands-tab-resume`; rows `command-row-<id>`

- [ ] **Step 1: Failing test**

`spa/src/components/hosts/CommandsSection.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(), getIconPath: () => 'M0,0',
}))
vi.mock('../../features/workspace/generated/icon-meta.json', () => ({
  default: [{ n: 'Terminal', t: ['cli'], c: [] }, { n: 'Rocket', t: ['launch'], c: [] }],
}))
vi.mock('../../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-api')>()),
  resolveShellCommand: vi.fn(async () => ({ status: 'resolved', detail: '/usr/local/bin/claude' })),
}))

import { CommandsSection } from './CommandsSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { resolveShellCommand } from '../../lib/host-api'
import type { HostCommand } from '../../lib/host-config-api'

const H = 'h1'
const C1: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const C2: HostCommand = { id: 'c2', name: 'Logs', command: 'tail -f log', icon: { kind: 'phosphor', value: 'Rocket' } }
const saveCommands = vi.fn()

beforeEach(() => {
  saveCommands.mockReset().mockImplementation(async (hostId: string, items: HostCommand[]) => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], commands: items } } }))
  })
  vi.mocked(resolveShellCommand).mockClear()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry('ready'), commands: [C1, C2] } },
    load: vi.fn(async () => {}),
    saveCommands,
  })
})

describe('CommandsSection', () => {
  it('Normal tab lists commands with icon, name and mono command', () => {
    render(<CommandsSection hostId={H} />)
    expect(screen.getAllByTestId(/^command-row-/).map((r) => r.dataset.testid)).toEqual(['command-row-c1', 'command-row-c2'])
    expect(screen.getByTestId('command-row-c2').querySelector('[data-testid="command-icon"]')).toHaveAttribute('data-value', 'Rocket')
    expect(screen.getByText('tail -f log')).toHaveClass('font-mono')
  })

  it('adds a command with a picked icon; the command-word check runs on this host and never blocks', async () => {
    vi.mocked(resolveShellCommand).mockResolvedValueOnce({ status: 'unresolved', reason: 'not_found' })
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-add'))
    fireEvent.change(screen.getByTestId('command-field-name'), { target: { value: 'Codex' } })
    fireEvent.change(screen.getByTestId('command-field-command'), { target: { value: 'FOO=1 codex --yolo' } })
    fireEvent.click(screen.getByTestId('command-word-test'))
    await waitFor(() => expect(resolveShellCommand).toHaveBeenCalledWith(H, 'codex'))
    expect(await screen.findByTestId('command-word-verdict')).toHaveAttribute('data-status', 'unresolved')
    fireEvent.click(screen.getByTestId('command-icon-agent-codex'))
    fireEvent.click(screen.getByTestId('command-save'))
    await waitFor(() => expect(saveCommands).toHaveBeenCalledTimes(1))
    const saved = saveCommands.mock.calls[0][1] as HostCommand[]
    expect(saved[2]).toMatchObject({ name: 'Codex', command: 'FOO=1 codex --yolo', icon: { kind: 'agent', value: 'codex' } })
  })

  it('a new command defaults to the Terminal icon; empty fields are rejected', () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-add'))
    expect(screen.getByTestId('command-dialog').querySelector('[data-testid="command-icon"]')).toHaveAttribute('data-value', 'Terminal')
    fireEvent.click(screen.getByTestId('command-save'))
    expect(screen.getByTestId('command-error-name')).toBeInTheDocument()
    expect(screen.getByTestId('command-error-command')).toBeInTheDocument()
    expect(saveCommands).not.toHaveBeenCalled()
  })

  it('reorders and deletes', async () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-up-c2'))
    await waitFor(() => expect(saveCommands).toHaveBeenLastCalledWith(H, [C2, C1]))
    fireEvent.click(screen.getByTestId('command-delete-c1'))
    fireEvent.click(screen.getByTestId('command-delete-confirm-c1'))
    await waitFor(() => expect(saveCommands).toHaveBeenLastCalledWith(H, [C2]))
  })

  it('Resume tab renders the per-host template editor without a host picker', () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('commands-tab-resume'))
    expect(screen.getByTestId('resume-templates')).toBeInTheDocument()
    expect(screen.queryByTestId('resume-template-host')).toBeNull()
    expect(screen.queryByTestId('command-add')).toBeNull()
  })

  it('old daemon → notice and disabled editing on both tabs', () => {
    useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('unsupported') } })
    render(<CommandsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.unsupported')
    expect(screen.getByTestId('command-add')).toBeDisabled()
    fireEvent.click(screen.getByTestId('commands-tab-resume'))
    expect(screen.getByTestId('resume-template-input-cc-exact')).toBeDisabled()
  })
})
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/CommandsSection.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 2: Implement `spa/src/components/hosts/CommandEditDialog.tsx`**

```tsx
import { useRef, useState } from 'react'
import { commandWordOf } from '../../lib/command-word'
import { resolveShellCommand, type ShellResolveVerdict } from '../../lib/host-api'
import { validateCommand, type FieldErrors } from '../../lib/host-config-validate'
import type { HostCommand } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import { ShellVerdict } from '../settings/ShellVerdict'
import { CommandIconPicker } from './CommandIconPicker'

export function CommandEditDialog({ hostId, initial, isNew, onSave, onCancel }: {
  hostId: string
  initial: HostCommand
  isNew: boolean
  onSave: (command: HostCommand) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<FieldErrors<'name' | 'command'>>({})
  // Verdict is only shown while the word it judged is still the word on screen.
  const [check, setCheck] = useState<{ word: string; verdict: ShellResolveVerdict | 'pending' } | null>(null)
  const seq = useRef(0)
  const word = commandWordOf(draft.command)

  const runCheck = async () => {
    if (!word) return
    const mine = ++seq.current
    setCheck({ word, verdict: 'pending' })
    let verdict: ShellResolveVerdict
    try { verdict = await resolveShellCommand(hostId, word) } catch { verdict = { status: 'unverifiable' } }
    if (seq.current === mine) setCheck({ word, verdict })
  }

  const submit = () => {
    const next = { ...draft, name: draft.name.trim() }
    const found = validateCommand(next)
    setErrors(found)
    if (Object.keys(found).length === 0) onSave(next)
  }

  const input = 'w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'
  return (
    <div role="dialog" data-testid="command-dialog" aria-label={t(isNew ? 'commands.add_title' : 'commands.edit_title')}
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <h3 className="text-sm font-semibold mb-3">{t(isNew ? 'commands.add_title' : 'commands.edit_title')}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('commands.field.name')}
        <input data-testid="command-field-name" autoFocus className={input} value={draft.name}
          onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
        {errors.name && <p data-testid="command-error-name" className="mt-1 text-xs text-red-400">{t(errors.name)}</p>}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('commands.field.command')}
        <input data-testid="command-field-command" className={`${input} font-mono`} value={draft.command} spellCheck={false}
          onChange={(e) => setDraft((d) => ({ ...d, command: e.target.value }))} />
        {errors.command && <p data-testid="command-error-command" className="mt-1 text-xs text-red-400">{t(errors.command)}</p>}
      </label>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
        <button type="button" data-testid="command-word-test" disabled={!word || check?.verdict === 'pending'} onClick={() => void runCheck()}
          className="rounded-md border border-border-default px-2 py-1 text-text-secondary hover:border-border-active hover:text-text-primary disabled:opacity-50 disabled:cursor-not-allowed">
          {t('resume_template.test')}
        </button>
        {check && check.word === word
          ? <ShellVerdict testId="command-word-verdict" verdict={check.verdict} t={t} />
          : null}
      </div>
      <div className="text-xs text-text-secondary mb-1">{t('commands.field.icon')}</div>
      <CommandIconPicker value={draft.icon} onChange={(icon) => setDraft((d) => ({ ...d, icon }))} />
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="command-save" onClick={submit}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer">{t('common.save')}</button>
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
```

The test reads the first `[data-testid="command-icon"]` inside `command-dialog`. Without a preview that would be the picker's first agent cell, so render a preview of the current icon above the picker:

```tsx
      <div className="text-xs text-text-secondary mb-1 flex items-center gap-2">
        {t('commands.field.icon')}
        <CommandIconView icon={draft.icon} size={16} />
      </div>
```
(replace the plain `<div className="text-xs text-text-secondary mb-1">{t('commands.field.icon')}</div>` with it and import `CommandIconView` from `./CommandIconView`). The preview is the first `command-icon` in DOM order.

- [ ] **Step 3: Implement `spa/src/components/hosts/CommandsSection.tsx`**

```tsx
import { useState } from 'react'
import { ArrowDown, ArrowUp, Check, PencilSimple, Plus, Trash, X } from '@phosphor-icons/react'
import { DEFAULT_COMMAND_ICON } from '../../lib/command-icons'
import { HostConfigConflictError, type HostCommand } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS, moveItem, newConfigId } from '../../lib/host-config-validate'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ResumeTemplateSettings } from '../settings/ResumeTemplateSettings'
import { CommandEditDialog } from './CommandEditDialog'
import { CommandIconView } from './CommandIconView'
import { HostConfigNotice, useHostConfigGate } from './HostConfigNotice'

type Tab = 'normal' | 'resume'

export function CommandsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const commands = entry.commands
  const [tab, setTab] = useState<Tab>('normal')
  const [editing, setEditing] = useState<{ command: HostCommand; isNew: boolean } | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const locked = !editable || saving

  const persist = async (next: HostCommand[]) => {
    setSaving(true)
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveCommands(hostId, next)
      return true
    } catch (err) {
      setSaveError(err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      return false
    } finally {
      setSaving(false)
    }
  }

  const handleSave = async (command: HostCommand) => {
    const exists = commands.some((c) => c.id === command.id)
    const next = exists ? commands.map((c) => (c.id === command.id ? command : c)) : [...commands, command]
    if (await persist(next)) setEditing(null)
  }

  const tabBtn = (id: Tab, key: string) => (
    <button type="button" data-testid={`commands-tab-${id}`} aria-pressed={tab === id} onClick={() => setTab(id)}
      className={`px-3 py-1 rounded text-xs cursor-pointer ${tab === id ? 'bg-accent/20 text-accent font-semibold' : 'text-text-secondary hover:text-text-primary'}`}>
      {t(key)}
    </button>
  )
  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-lg font-semibold">{t('hosts.commands')}</h2>
        {tab === 'normal' && (
          <button type="button" data-testid="command-add" disabled={locked || commands.length >= MAX_CONFIG_ITEMS}
            onClick={() => setEditing({ command: { id: newConfigId(), name: '', command: '', icon: DEFAULT_COMMAND_ICON }, isNew: true })}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
            <Plus size={14} />{t('commands.add')}
          </button>
        )}
      </div>
      <div className="flex gap-1 mb-4">
        {tabBtn('normal', 'commands.tab.normal')}
        {tabBtn('resume', 'commands.tab.resume')}
      </div>

      <HostConfigNotice notice={notice} />
      {saveError && <p data-testid="commands-save-error" className="mb-3 text-xs text-status-warning">{saveError}</p>}

      {tab === 'resume' ? (
        <ResumeTemplateSettings hostId={hostId} busy={!editable} />
      ) : (
        <>
          {editing && (
            <CommandEditDialog hostId={hostId} initial={editing.command} isNew={editing.isNew}
              onSave={(c) => { void handleSave(c) }} onCancel={() => setEditing(null)} />
          )}
          {commands.length === 0 ? (
            <p className="text-sm text-text-muted">{t('commands.empty')}</p>
          ) : (
            <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
              {commands.map((command, index) => (
                <li key={command.id} data-testid={`command-row-${command.id}`} className="flex items-center gap-3 px-3 py-2">
                  <CommandIconView icon={command.icon} size={16} className="text-text-secondary" />
                  <span className="w-40 shrink-0 truncate text-sm text-text-primary">{command.name}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-text-muted" title={command.command}>{command.command}</span>
                  <div className="flex items-center gap-1">
                    <button type="button" data-testid={`command-up-${command.id}`} title={t('host_config.move_up')}
                      disabled={locked || index === 0} onClick={() => void persist(moveItem(commands, index, -1))} className={iconBtn}><ArrowUp size={14} /></button>
                    <button type="button" data-testid={`command-down-${command.id}`} title={t('host_config.move_down')}
                      disabled={locked || index === commands.length - 1} onClick={() => void persist(moveItem(commands, index, 1))} className={iconBtn}><ArrowDown size={14} /></button>
                    <button type="button" data-testid={`command-edit-${command.id}`} title={t('common.edit')}
                      disabled={locked} onClick={() => setEditing({ command, isNew: false })} className={iconBtn}><PencilSimple size={14} /></button>
                    {deleting === command.id ? (
                      <>
                        <button type="button" data-testid={`command-delete-confirm-${command.id}`} disabled={locked}
                          onClick={() => { setDeleting(null); void persist(commands.filter((c) => c.id !== command.id)) }}
                          className="p-1 text-red-400 cursor-pointer"><Check size={14} /></button>
                        <button type="button" onClick={() => setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                      </>
                    ) : (
                      <button type="button" data-testid={`command-delete-${command.id}`} title={t('common.delete')}
                        disabled={locked} onClick={() => setDeleting(command.id)} className={iconBtn}><Trash size={14} /></button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Register (order 8) and locale keys**

`spa/src/lib/register-modules/index.tsx`: add `import { CommandsSection } from '../../components/hosts/CommandsSection'`; after the `projects` entry add:
```tsx
    { localId: 'commands',  labelKey: 'hosts.commands',  order: 8, component: CommandsSection },
```

en.json:
```json
  "hosts.commands": "Commands",
  "commands.tab.normal": "Commands",
  "commands.tab.resume": "Resume",
  "commands.add": "Add command",
  "commands.add_title": "Add command",
  "commands.edit_title": "Edit command",
  "commands.empty": "No commands on this host yet.",
  "commands.field.name": "Name",
  "commands.field.command": "Command",
  "commands.field.icon": "Icon",
  "commands.invalid.name": "Name must be 1–64 characters.",
  "commands.invalid.command": "Command must be 1–4096 bytes.",
```
zh-TW.json:
```json
  "hosts.commands": "指令",
  "commands.tab.normal": "指令",
  "commands.tab.resume": "Resume",
  "commands.add": "新增指令",
  "commands.add_title": "新增指令",
  "commands.edit_title": "編輯指令",
  "commands.empty": "此主機尚未設定指令。",
  "commands.field.name": "名稱",
  "commands.field.command": "指令",
  "commands.field.icon": "圖示",
  "commands.invalid.name": "名稱需為 1–64 個字元。",
  "commands.invalid.command": "指令需為 1–4096 bytes。",
```

- [ ] **Step 5: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/CommandsSection.test.tsx src/locales && npx eslint src/components/hosts/CommandEditDialog.tsx src/components/hosts/CommandsSection.tsx src/lib/register-modules/index.tsx && npx tsc -b`
Expected: PASS (6 tests); no lint errors; tsc exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add spa/src/components/hosts/CommandEditDialog.tsx spa/src/components/hosts/CommandsSection.tsx spa/src/components/hosts/CommandsSection.test.tsx spa/src/lib/register-modules/index.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): Host > Commands page with normal commands and resume templates

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 8: Snapshots split — host-scoped section + client-scoped block; unregister global page

**Files:**
- Create: `spa/src/lib/snapshot/filter.ts`, `spa/src/lib/snapshot/filter.test.ts`
- Create: `spa/src/components/settings/snapshot/shared.tsx`, `RebuildRecordsBlock.tsx`, `TmuxBlock.tsx`, `TabsBlock.tsx`, `ClientSnapshotBlock.tsx` (all under `spa/src/components/settings/snapshot/`)
- Create: `spa/src/components/hosts/SnapshotsSection.tsx`
- Move + modify tests: `git mv spa/src/components/settings/SnapshotSettingsSection.test.tsx spa/src/components/hosts/SnapshotsSection.test.tsx`; `git mv spa/src/components/settings/SnapshotSettingsSection.records.test.tsx spa/src/components/hosts/SnapshotsSection.records.test.tsx`
- Delete: `spa/src/components/settings/SnapshotSettingsSection.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx:32,418-425` (drop global section) and host sections (add `snapshots` order 9)
- Modify: `spa/src/lib/settings-order.ts:71` (delete `SNAPSHOT: 22`)
- Modify: `spa/src/lib/__tests__/settings-order-pr2.test.ts:31,75,83`
- Modify: `spa/src/lib/register-modules.test.ts:154-168`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `selectDevHostId` (`spa/src/stores/useHostStore.ts:110`), `runBatchRebuild(deps, { hostId })`, `groupForBatch`, `planForRecord`, `resumeLookupFor`, `useResumeTemplateLookup` (Task 3), snapshot restore APIs (`spa/src/lib/snapshot/restore.ts`)
- Produces:
  - `filterSnapshotByHost(snap: WorkspaceSnapshot, hostId: string): WorkspaceSnapshot` — new object, `sessionMeta` only `{ [hostId]: … }` (or `{}`), every other field shared by reference
  - `selectSnapshotClientHostId(s: { devHostId: string | null; hosts: Record<string, unknown>; hostOrder: string[] }): string | null`
  - `shared.tsx`: `type Tone`, `interface Status`, `IDLE`, `CAPTURE_OWNER`, `errMessage`, `type HostLive`, `livenessOf`, `computeHealth`, `HealthBadge`, `StatusLine`, `leafLabel`, `formatRelativeTime`, `statusForRestore(t, report, failed): Status`, `useSnapshotActions(t): { busy: boolean; status: Status; setStatus; lockedOut(owner): boolean; run(owner: string, busyKey: string, action: () => Promise<Status>, after?: () => void): Promise<void> }`
  - `ClientSnapshotBlock({ snap, onRefresh, showNoDevHint })`
  - `SnapshotsSection({ hostId })`

- [ ] **Step 1: Failing filter test**

`spa/src/lib/snapshot/filter.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { filterSnapshotByHost, selectSnapshotClientHostId } from './filter'
import type { WorkspaceSnapshot } from './types'

const snap: WorkspaceSnapshot = {
  version: 1, capturedAt: 5, tabs: {}, tabOrder: [], activeTabId: null, workspaces: [], activeWorkspaceId: null,
  sessionMeta: {
    h1: { s1: { hostId: 'h1', sessionCode: 's1', name: 'a', mode: 'terminal', restorable: true, cwd: '/a' } },
    h2: { s2: { hostId: 'h2', sessionCode: 's2', name: 'b', mode: 'terminal', restorable: true, cwd: '/b' } },
  },
}

describe('filterSnapshotByHost', () => {
  it('keeps only that host\'s session meta and never mutates the original', () => {
    const out = filterSnapshotByHost(snap, 'h1')
    expect(Object.keys(out.sessionMeta)).toEqual(['h1'])
    expect(out.sessionMeta.h1).toBe(snap.sessionMeta.h1)
    expect(out.tabs).toBe(snap.tabs)
    expect(Object.keys(snap.sessionMeta)).toEqual(['h1', 'h2'])
    expect(out).not.toBe(snap)
  })

  it('a host with nothing captured yields an empty map', () => {
    expect(filterSnapshotByHost(snap, 'h9').sessionMeta).toEqual({})
  })
})

describe('selectSnapshotClientHostId', () => {
  const hosts = { h1: {}, h2: {} }
  it('prefers the dev host', () => {
    expect(selectSnapshotClientHostId({ devHostId: 'h2', hosts, hostOrder: ['h1', 'h2'] })).toBe('h2')
  })
  it('falls back to the first existing host in order when no (valid) dev host', () => {
    expect(selectSnapshotClientHostId({ devHostId: null, hosts, hostOrder: ['gone', 'h1', 'h2'] })).toBe('h1')
    expect(selectSnapshotClientHostId({ devHostId: 'gone', hosts, hostOrder: ['h2', 'h1'] })).toBe('h2')
    expect(selectSnapshotClientHostId({ devHostId: null, hosts: {}, hostOrder: [] })).toBeNull()
  })
})
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/snapshot/filter.test.ts` → FAIL (module missing).

- [ ] **Step 2: Implement `spa/src/lib/snapshot/filter.ts`**

```ts
import type { WorkspaceSnapshot } from './types'

/**
 * A derived, host-scoped view of the captured snapshot (host-launcher spec
 * §4.3). Used only as the INPUT of a host page's "rebuild all sessions", so
 * that action can never create a session on another host. Never written back:
 * edits go through `setSessionMetaCwd` on the full snapshot.
 */
export function filterSnapshotByHost(snap: WorkspaceSnapshot, hostId: string): WorkspaceSnapshot {
  const own = Object.prototype.hasOwnProperty.call(snap.sessionMeta, hostId) ? snap.sessionMeta[hostId] : undefined
  return { ...snap, sessionMeta: own ? { [hostId]: own } : {} }
}

/** Which host page shows the client-scoped (whole-device) snapshot block. */
export function selectSnapshotClientHostId(s: {
  devHostId: string | null
  hosts: Record<string, unknown>
  hostOrder: string[]
}): string | null {
  if (s.devHostId !== null && s.hosts[s.devHostId]) return s.devHostId
  return s.hostOrder.find((id) => !!s.hosts[id]) ?? null
}
```

Run the test → PASS.

- [ ] **Step 3: Split the building blocks out of `SnapshotSettingsSection.tsx` (pure moves unless stated)**

`spa/src/components/settings/snapshot/shared.tsx` — move from `SnapshotSettingsSection.tsx`: `Tone`/`Status`/`IDLE` (55-66), `CAPTURE_OWNER` (68-73), `errMessage` (75-77), `HostLive`/`Health`/`livenessOf`/`computeHealth` (79-118), `HEALTH_ICON`/`HEALTH_COLOR`/`HealthBadge` (120-161), `leafLabel` (163-177), `formatRelativeTime` (179-188), `StatusLine` (564-600). Add `export` to each and the file-top `/* eslint-disable react-refresh/only-export-components */`. Then append the two new helpers (replacing the component-local `reportStatus`, `refuseWhileLocked`, `runRebuildAction` logic at 272-277, 327-364, 411-425):

```tsx
type TFn = ReturnType<typeof useI18nStore.getState>['t']

/** A resolved or failed RestoreReport as a status (was `reportStatus`). */
export function statusForRestore(t: TFn, report: RestoreReport, failed: boolean): Status {
  const attrs = { 'data-reattached': report.reattached, 'data-rebuilt': report.rebuilt, 'data-failed': report.failed }
  const unattached = report.rebuiltButUnattached
  if (unattached.length > 0) console.warn('[snapshot] sessions rebuilt but could not be reattached', unattached)
  if (failed) {
    return { tone: 'error', message: t('settings.snapshot.toast.restoreError'), attrs, unattached: unattached.length > 0 ? unattached : undefined }
  }
  if (unattached.length > 0) {
    return { tone: 'warn', message: t('settings.snapshot.toast.rebuiltUnattached'), attrs, unattached }
  }
  return {
    tone: 'success',
    message: t('settings.snapshot.toast.restoreReport', { reattached: report.reattached, rebuilt: report.rebuilt, failed: report.failed }),
    attrs,
  }
}

/**
 * The single-flight guard + global operation lock + status line every
 * snapshot action shares (spec §4.11 of the tab-rebuild work). One instance per
 * block; the lock is global, so two blocks still never interleave a restore.
 */
export function useSnapshotActions(t: TFn) {
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const lockedBy = useRebuildStore((s) => s.lockedBy)
  const [status, setStatus] = useState<Status>(IDLE)
  const lockedOut = (owner: string) => lockedBy !== null && lockedBy !== owner

  const run = async (owner: string, busyKey: string, action: () => Promise<Status>, after?: () => void) => {
    if (busyRef.current) return
    const holder = useRebuildStore.getState().lockedBy
    if (holder !== null && holder !== owner) {
      setStatus({ tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: holder }) })
      return
    }
    busyRef.current = true
    setBusy(true)
    setStatus({ tone: 'busy', message: t(busyKey) })
    try {
      setStatus(await action())
    } catch (e) {
      setStatus({ tone: 'error', message: t('settings.snapshot.toast.restoreFailed', { reason: errMessage(e) }) })
    } finally {
      busyRef.current = false
      setBusy(false)
      after?.()
    }
  }

  return { busy, busyRef, status, setStatus, lockedOut, run }
}

/** Restore-style action body: null → "nothing to undo", RestoreError → error status. */
export async function restoreAction(t: TFn, action: () => Promise<RestoreReport | null>): Promise<Status> {
  try {
    const report = await action()
    if (report === null) return { tone: 'warn', message: t('settings.snapshot.toast.undoNothing') }
    return statusForRestore(t, report, false)
  } catch (e) {
    if (e instanceof RestoreError) return statusForRestore(t, e.report, true)
    throw e
  }
}
```
(imports needed at the top of `shared.tsx`: `useRef, useState` from react; the Phosphor icons used by `HEALTH_ICON`/`StatusLine`; `useI18nStore`; `useRebuildStore`; `RestoreError`, `type RestoreReport`, `type SessionMeta` from `../../../lib/snapshot/types`; `type Session` from `../../../lib/host-api`; `type HostLiveness, type RecordHealth` from `../../../lib/rebuild/eligibility`; `type PaneContent` from `../../../types/tab`.) `useSnapshotActions` also exports `busyRef` for the cwd-edit guard.

`spa/src/components/settings/snapshot/RebuildRecordsBlock.tsx` — move `RebuildRecordsBlock` (602-736) plus the `RecordCommandCell` added in Task 3, `export` the block, and **drop the host column**: delete the `<th>` for `settings.snapshot.col.host` (664) and the `<td … >{row.hostId}</td>` (674). In the "needs attention" list replace `{pane.record.sessionName} · {pane.hostId}` (718) with `{pane.record.sessionName}`.

`spa/src/components/settings/snapshot/TmuxBlock.tsx` — move `TmuxBlock` (738-819), `export` it, drop the host `<th>` (781) and `<td>{meta.hostId}</td>` (793). It receives the host-filtered snapshot, so its row loop is unchanged.

`spa/src/components/settings/snapshot/TabsBlock.tsx` — move `TabsBlock` (821-874), `export` it, import `leafLabel` from `./shared`.

- [ ] **Step 4: Implement `spa/src/components/settings/snapshot/ClientSnapshotBlock.tsx`**

```tsx
import { ArrowCounterClockwise, ArrowsClockwise, Camera } from '@phosphor-icons/react'
import { SettingItem } from '../SettingItem'
import { DeviceStateSection } from '../device-state/DeviceStateSection'
import { useI18nStore } from '../../../stores/useI18nStore'
import { readPrevSnapshot } from '../../../lib/snapshot/storage'
import { captureSnapshot } from '../../../lib/snapshot/capture'
import { restoreAll, restoreTabLayout, undoLastRestore, SNAPSHOT_LOCK_OWNER } from '../../../lib/snapshot/restore'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'
import { CAPTURE_OWNER, StatusLine, formatRelativeTime, restoreAction, useSnapshotActions } from './shared'
import { TabsBlock } from './TabsBlock'

const BTN = 'flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed'

/**
 * Everything that acts on THIS DEVICE's whole workspace across all hosts:
 * capture, restore all / tab layout, undo, and device-state backup (which
 * lives on the dev host's daemon). Rendered on one host page only.
 */
export function ClientSnapshotBlock({ snap, onRefresh, showNoDevHint }: {
  snap: WorkspaceSnapshot | null
  onRefresh: () => void
  showNoDevHint: boolean
}) {
  const t = useI18nStore((s) => s.t)
  const { busy, status, lockedOut, run } = useSnapshotActions(t)
  // Read fresh each render: every action re-renders, so Undo re-enables once a
  // restore writes the `-prev` backup.
  const hasPrev = readPrevSnapshot() !== null

  const handleCapture = () => run(CAPTURE_OWNER, 'settings.snapshot.toast.capturing', async () => {
    const res = await captureSnapshot(Date.now())
    return {
      tone: 'success',
      message: t('settings.snapshot.toast.captured', { total: res.total, unresolved: res.unresolved }),
      attrs: { 'data-total': res.total, 'data-unresolved': res.unresolved },
    }
  }, onRefresh).catch(() => {})
  const restore = (owner: string, action: () => Promise<Awaited<ReturnType<typeof restoreAll>> | null>) =>
    run(owner, 'settings.snapshot.toast.restoring', () => restoreAction(t, action), onRefresh)

  return (
    <section data-testid="snapshot-client-block" className="mt-8 border-t border-border-default pt-6">
      <h3 className="text-sm text-text-primary">{t('hosts.snapshots.client_title')}</h3>
      <p className="text-xs text-text-secondary mb-2">{t('hosts.snapshots.client_desc')}</p>
      {showNoDevHint && (
        <p data-testid="snapshot-client-no-dev-hint" className="text-xs text-status-warning mb-2">{t('hosts.snapshots.client_no_dev')}</p>
      )}

      <SettingItem
        label={t('settings.snapshot.capture')}
        description={snap ? t('settings.snapshot.capturedAt', { time: formatRelativeTime(t, snap.capturedAt) }) : t('settings.snapshot.neverCaptured')}
      >
        <button type="button" data-testid="snapshot-capture-btn" onClick={() => void handleCapture()}
          disabled={busy || lockedOut(CAPTURE_OWNER)} className={BTN}>
          <Camera size={14} className={busy ? 'animate-pulse' : ''} />
          {t('settings.snapshot.capture')}
        </button>
      </SettingItem>

      {snap && (
        <>
          <SettingItem label={t('settings.snapshot.restore.label')} description={t('settings.snapshot.restore.description')}>
            <div className="flex items-center gap-2">
              <button type="button" data-testid="snapshot-restore-all-btn"
                onClick={() => void restore(SNAPSHOT_LOCK_OWNER.restoreAll, () => restoreAll(snap))}
                disabled={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreAll)} className={BTN}>
                <ArrowsClockwise size={14} />{t('settings.snapshot.restore.all')}
              </button>
              <button type="button" data-testid="snapshot-undo-btn"
                onClick={() => void restore(SNAPSHOT_LOCK_OWNER.undo, () => undoLastRestore())}
                disabled={busy || !hasPrev || lockedOut(SNAPSHOT_LOCK_OWNER.undo)} className={BTN}>
                <ArrowCounterClockwise size={14} />{t('settings.snapshot.restore.undo')}
              </button>
            </div>
          </SettingItem>
          <TabsBlock snap={snap} busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreLayout)}
            onRestoreLayout={() => void restore(SNAPSHOT_LOCK_OWNER.restoreLayout, () => restoreTabLayout(snap))} t={t} />
        </>
      )}

      <StatusLine status={status} />
      <DeviceStateSection onRestored={onRefresh} />
    </section>
  )
}
```

(`run` already swallows errors into the status; the `.catch(() => {})` on capture only silences the promise for lint `no-floating-promises` if enabled — drop it if lint does not require it.)

- [ ] **Step 5: Implement `spa/src/components/hosts/SnapshotsSection.tsx`**

```tsx
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectDevHostId, useHostStore } from '../../stores/useHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { readSnapshot, setSessionMetaCwd, writeSnapshot } from '../../lib/snapshot/storage'
import { rebuildAllSessions, SNAPSHOT_LOCK_OWNER } from '../../lib/snapshot/restore'
import { filterSnapshotByHost, selectSnapshotClientHostId } from '../../lib/snapshot/filter'
import type { WorkspaceSnapshot } from '../../lib/snapshot/types'
import { listSessions } from '../../lib/host-api'
import { BATCH_LOCK_OWNER, groupForBatch, planForRecord, runBatchRebuild } from '../../lib/rebuild/batch'
import { batchCandidates, collectRecordRows, type BatchCandidate } from '../../lib/rebuild/eligibility'
import { rebuildPane } from '../../lib/rebuild/engine'
import { resumeLookupFor } from '../../lib/resume-templates'
import { restoreAction, StatusLine, useSnapshotActions, type HostLive } from '../settings/snapshot/shared'
import { RebuildRecordsBlock } from '../settings/snapshot/RebuildRecordsBlock'
import { TmuxBlock } from '../settings/snapshot/TmuxBlock'
import { ClientSnapshotBlock } from '../settings/snapshot/ClientSnapshotBlock'

/**
 * Host › Snapshots (host-launcher spec §4.3). The host-scoped part — per-tab
 * rebuild records and captured tmux sessions — shows and acts on THIS host
 * only. The client-scoped block (whole device, all hosts) is rendered on the
 * dev host's page, or the first host when no dev host is set.
 */
export function SnapshotsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const [snap, setSnap] = useState<WorkspaceSnapshot | null>(() => readSnapshot())
  const refresh = useCallback(() => setSnap(readSnapshot()), [])
  const clientHostId = useHostStore(selectSnapshotClientHostId)
  const devHostId = useHostStore(selectDevHostId)
  const { busy, busyRef, status, lockedOut, run } = useSnapshotActions(t)

  const hostSnap = useMemo(() => (snap ? filterSnapshotByHost(snap, hostId) : null), [snap, hostId])
  const tabs = useTabStore((s) => s.tabs)
  const rows = useMemo(() => collectRecordRows(tabs).filter((r) => r.hostId === hostId), [tabs, hostId])
  const { groups, excluded } = useMemo(() => groupForBatch(batchCandidates(rows)), [rows])
  const hasHostData = rows.length > 0 || Object.keys(hostSnap?.sessionMeta ?? {}).length > 0

  const [live, setLive] = useState<HostLive>('loading')
  useEffect(() => {
    if (!hasHostData) return
    setLive('loading')
    let cancelled = false
    listSessions(hostId)
      .then((sessions) => { if (!cancelled) setLive(sessions) })
      .catch(() => { if (!cancelled) setLive('offline') })
    return () => { cancelled = true }
  }, [hostId, snap, hasHostData])
  const liveByHost = useMemo(() => ({ [hostId]: live }), [hostId, live])

  const handleCommitCwd = (h: string, code: string, value: string) => {
    if (busyRef.current || useRebuildStore.getState().lockedBy !== null) return
    const cur = readSnapshot()
    if (!cur) return
    // Written to the FULL snapshot; the filtered copy is never persisted.
    writeSnapshot(setSessionMetaCwd(cur, h, code, value))
    refresh()
  }

  const handleRebuildSessions = () => {
    if (!snap) return
    void run(SNAPSHOT_LOCK_OWNER.rebuildAll, 'settings.snapshot.toast.restoring',
      () => restoreAction(t, () => rebuildAllSessions(filterSnapshotByHost(snap, hostId))), refresh)
  }

  const handleRebuildAll = () => void run(BATCH_LOCK_OWNER, 'rebuild.batch_running', async () => {
    const report = await runBatchRebuild({}, { hostId })
    if (report.status === 'blocked') {
      return { tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: report.blockedBy ?? '' }) }
    }
    const created = report.groups.filter((g) => g.report.created).length
    const repointed = report.groups.reduce((n, g) => n + (g.report.repointed ? 1 : 0) + g.members.filter((m) => m.repointed).length, 0)
    return {
      tone: created === report.groups.length ? 'success' : 'warn',
      message: t('rebuild.batch_report', { created, total: report.groups.length, repointed }),
      attrs: { 'data-groups': report.groups.length, 'data-created': created, 'data-repointed': repointed },
    }
  })

  const handleRebuildOne = (pane: BatchCandidate) => void run(`rebuild:${pane.paneId}`, 'rebuild.batch_running', async () => {
    await useHostConfigStore.getState().ensureLoaded(pane.hostId)
    const report = await rebuildPane(pane.hostId, pane.tabId, pane.paneId, planForRecord(pane.record, resumeLookupFor(pane.hostId)))
    if (report.steps.create.status === 'failed') {
      return { tone: 'error', message: report.steps.create.error ?? t('settings.snapshot.toast.restoreError') }
    }
    return {
      tone: report.repointed ? 'success' : 'warn',
      message: t('rebuild.batch_report', { created: 1, total: 1, repointed: report.repointed ? 1 : 0 }),
    }
  })

  return (
    <div className="max-w-3xl">
      <h2 className="text-lg text-text-primary">{t('hosts.snapshots')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('hosts.snapshots.host_desc')}</p>

      <RebuildRecordsBlock rows={rows} groups={groups} excluded={excluded} liveByHost={liveByHost}
        busy={busy || lockedOut(BATCH_LOCK_OWNER)} lockedOut={lockedOut}
        onRebuildAll={handleRebuildAll} onRebuildOne={handleRebuildOne} t={t} />

      {!hostSnap ? (
        <p data-testid="snapshot-empty" className="text-xs text-text-muted mt-4">{t('settings.snapshot.empty')}</p>
      ) : (
        <>
          <p data-testid="snapshot-legacy-shell-only" className="mt-6 text-xs text-status-warning">{t('rebuild.legacy_shell_only')}</p>
          <TmuxBlock snap={hostSnap} liveByHost={liveByHost} busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.rebuildAll)}
            onRebuild={handleRebuildSessions} onCommitCwd={handleCommitCwd} t={t} />
        </>
      )}

      <StatusLine status={status} />

      {clientHostId === hostId && (
        <ClientSnapshotBlock snap={snap} onRefresh={refresh} showNoDevHint={devHostId === null} />
      )}
    </div>
  )
}
```

`git rm spa/src/components/settings/SnapshotSettingsSection.tsx`.

- [ ] **Step 6: Migrate the two test files (failing first against the old import)**

After the `git mv`s, in **both** files:
- `import { SnapshotSettingsSection } from './SnapshotSettingsSection'` → `import { SnapshotsSection } from './SnapshotsSection'` (relative `../../` paths stay valid: `components/hosts` has the same depth as `components/settings`).
- Add `import { useHostStore } from '../../stores/useHostStore'` and, before the first `describe`:
```tsx
vi.mock('../settings/device-state/DeviceStateSection', () => ({
  DeviceStateSection: () => <div data-testid="device-state-section" />,
}))
```
- In the top-level (or first) `beforeEach` add:
```ts
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: ['h1', 'h2'], devHostId: 'h1', runtime: {},
  })
```
- Every `render(<SnapshotSettingsSection />)` → `render(<SnapshotsSection hostId="h1" />)`; rename describe prefixes `SnapshotSettingsSection —` → `SnapshotsSection —`.

In `SnapshotsSection.test.tsx` replace the case "calls listSessions exactly once per captured host" (lines 222-237) with:
```tsx
  it('lists sessions for THIS host only and hides other hosts\' rows', async () => {
    mockedReadSnapshot.mockReturnValue(makeSnapshot({ sessionMeta: {
      h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/x' }) },
      h2: { s2: meta({ hostId: 'h2', sessionCode: 's2', name: 'b', cwd: '/y' }) },
    } }))
    render(<SnapshotsSection hostId="h1" />)
    await waitFor(() => expect(mockedListSessions).toHaveBeenCalledTimes(1))
    expect(mockedListSessions).toHaveBeenCalledWith('h1')
    expect(screen.getByTestId('snapshot-health-h1-s1')).toBeInTheDocument()
    expect(screen.queryByTestId('snapshot-health-h2-s2')).toBeNull()
  })
```
Replace the body of "rebuild button → rebuildAllSessions(snap)" with an assertion on the derived copy, and add the host-scoping describe:
```tsx
  it('rebuild button → rebuildAllSessions with the host-filtered snapshot', async () => {
    const snap = snapWithData()
    snap.sessionMeta.h2 = { s2: meta({ hostId: 'h2', sessionCode: 's2', name: 'b', cwd: '/y' }) }
    mockedReadSnapshot.mockReturnValue(snap)
    render(<SnapshotsSection hostId="h1" />)
    fireEvent.click(screen.getByTestId('snapshot-rebuild-btn'))
    await waitFor(() => expect(mockedRebuildAll).toHaveBeenCalledTimes(1))
    expect(Object.keys(mockedRebuildAll.mock.calls[0][0].sessionMeta)).toEqual(['h1'])
    expect(mockedWriteSnapshot).not.toHaveBeenCalled()
  })
```
```tsx
describe('SnapshotsSection — host vs client scope', () => {
  it('the client block (capture / restore / undo / tabs / device state) renders only on the dev host page', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    const { unmount } = render(<SnapshotsSection hostId="h2" />)
    expect(screen.queryByTestId('snapshot-client-block')).toBeNull()
    expect(screen.queryByTestId('snapshot-capture-btn')).toBeNull()
    unmount()
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.getByTestId('snapshot-client-block')).toBeInTheDocument()
    expect(screen.getByTestId('snapshot-restore-all-btn')).toBeInTheDocument()
    expect(screen.getByTestId('device-state-section')).toBeInTheDocument()
    expect(screen.queryByTestId('snapshot-client-no-dev-hint')).toBeNull()
  })

  it('with no dev host the block goes to the first host in order, with a hint', () => {
    useHostStore.setState({ devHostId: null })
    mockedReadSnapshot.mockReturnValue(snapWithData())
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.getByTestId('snapshot-client-no-dev-hint')).toBeInTheDocument()
  })

  it('the host column is gone from both tables', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    render(<SnapshotsSection hostId="h1" />)
    expect(within(screen.getByTestId('snapshot-tmux-block')).queryByText('h1')).toBeNull()
  })
})
```
(add `within` to the `@testing-library/react` import).

In `SnapshotsSection.records.test.tsx`, in "\"Rebuild all\" runs the batch once and reports what it did" add after the `toHaveBeenCalledTimes(1)` wait:
```ts
    expect(mockedRunBatch).toHaveBeenCalledWith({}, { hostId: 'h1' })
```
and append:
```tsx
  it('shows only this host\'s record rows', () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { hostId: 'h2' }))
    render(<SnapshotsSection hostId="h1" />)
    expect(screen.getByTestId('record-health-p1')).toBeInTheDocument()
    expect(screen.queryByTestId('record-health-p2')).toBeNull()
  })
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts/SnapshotsSection.test.tsx src/components/hosts/SnapshotsSection.records.test.tsx src/lib/snapshot/filter.test.ts`
Expected: PASS once Steps 3-5 are in (run before Step 5 to see the import FAIL).

- [ ] **Step 7: Registration, order constant, locale keys**

`spa/src/lib/register-modules/index.tsx`:
- delete line 32 (`import { SnapshotSettingsSection } …`) and the block at 418-425 (the comment + `registerSettingsSection({ id: 'snapshot', … })`);
- add `import { SnapshotsSection } from '../../components/hosts/SnapshotsSection'` and after the `commands` host entry:
```tsx
    { localId: 'snapshots', labelKey: 'hosts.snapshots', order: 9, component: SnapshotsSection },
```

`spa/src/lib/settings-order.ts:71`: delete `  SNAPSHOT: 22,`.

`spa/src/lib/__tests__/settings-order-pr2.test.ts`: delete comment line 31, the `{ id: 'snapshot', … }` entry (75) and `'snapshot'` from the Set at 83.

`spa/src/lib/register-modules.test.ts:154-168`: replace both cases with
```ts
  it('no longer registers a global Snapshot settings section', () => {
    registerBuiltinModules()
    expect(getSettingsSections().find((s) => s.id === 'snapshot')).toBeUndefined()
  })

  it('registers host sub-pages projects / commands / snapshots after nex (7/8/9)', () => {
    registerBuiltinModules()
    const host = listContributions('host').filter((c) => ['nex', 'projects', 'commands', 'snapshots'].includes(c.localId))
    expect(host.map((c) => [c.localId, c.order])).toEqual([['nex', 6], ['projects', 7], ['commands', 8], ['snapshots', 9]])
  })
```
(import `listContributions` from `./settings-contribution-registry` if not already imported; if `listContributions` does not return in order, sort by `order` before mapping.)

en.json: delete `settings.section.snapshot`, `settings.snapshot.description`; add
```json
  "hosts.snapshots": "Snapshots",
  "hosts.snapshots.host_desc": "Rebuild records and captured tmux sessions for this host.",
  "hosts.snapshots.client_title": "This device's workspace (all hosts)",
  "hosts.snapshots.client_desc": "Capture, restore and device-state backup cover this computer's whole workspace across every host.",
  "hosts.snapshots.client_no_dev": "No Development host is set, so this is shown on the first host. Pick one in Settings › Development.",
```
zh-TW.json: delete the same two keys; add
```json
  "hosts.snapshots": "快照",
  "hosts.snapshots.host_desc": "此主機的重建紀錄與已擷取的 tmux session。",
  "hosts.snapshots.client_title": "這台裝置的工作區（所有主機）",
  "hosts.snapshots.client_desc": "擷取、還原與裝置狀態備份涵蓋這台電腦橫跨所有主機的整個工作區。",
  "hosts.snapshots.client_no_dev": "尚未設定 Development 主機，因此顯示在第一台主機上。請到 設定 › Development 選擇。",
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "SnapshotSettingsSection|SETTINGS_ORDER.SNAPSHOT|settings\.section\.snapshot|settings\.snapshot\.description" spa/src`
Expected: no output.

- [ ] **Step 8: Run tests, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/components/hosts src/components/settings src/lib/snapshot src/lib/register-modules.test.ts src/lib/__tests__/settings-order-pr2.test.ts src/locales && npx eslint src/components/settings/snapshot src/components/hosts/SnapshotsSection.tsx src/lib/snapshot/filter.ts src/lib/register-modules/index.tsx src/lib/settings-order.ts && npx tsc -b`
Expected: PASS; no lint errors; tsc exits 0.

- [ ] **Step 9: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add -A spa/src/lib/snapshot/filter.ts spa/src/lib/snapshot/filter.test.ts spa/src/components/settings/snapshot spa/src/components/hosts/SnapshotsSection.tsx spa/src/components/hosts/SnapshotsSection.test.tsx spa/src/components/hosts/SnapshotsSection.records.test.tsx spa/src/components/settings/SnapshotSettingsSection.tsx spa/src/components/settings/SnapshotSettingsSection.test.tsx spa/src/components/settings/SnapshotSettingsSection.records.test.tsx spa/src/lib/register-modules/index.tsx spa/src/lib/settings-order.ts spa/src/lib/__tests__/settings-order-pr2.test.ts spa/src/lib/register-modules.test.ts spa/src/locales/en.json spa/src/locales/zh-TW.json && git commit -m "feat(spa): Host > Snapshots, split host-scoped and device-scoped snapshot actions

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 9: Remove the quick-command system

**Files (from `rg -l` run at plan time — re-run in Step 1; the list must match):**

Delete (source + tests):
- `spa/src/stores/useQuickCommandStore.ts`, `useQuickCommandStore.bindings.test.ts`, `useQuickCommandStore.crud.test.ts`
- `spa/src/lib/quick-command-bindings.ts` (+ `.test.ts`), `spa/src/lib/quick-command-slots.ts` (+ `.test.ts`)
- `spa/src/lib/slot-executor.ts` (+ `.test.ts`), `spa/src/lib/execute-command.ts` (+ `.test.ts`)
- `spa/src/components/CommandSlot.tsx` (+ `.test.tsx`), `spa/src/components/QuickCommandMenu.tsx`
- `spa/src/components/HostPickerPopover.tsx` (+ `.test.tsx`) — only quick-command code uses it
- `spa/src/hooks/useCommands.ts` (+ `.test.ts`)
- `spa/src/components/settings/QuickCommandsSettingsSection.tsx` (+ `.test.tsx`)
- `spa/src/lib/sync/contributors/quick-commands.ts` (+ `.test.ts`)
- `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx` (+ `.test.tsx`)
- `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx` (+ `.test.tsx`)
- `spa/src/lib/register-modules.quick-commands.test.tsx`

Modify:
- `spa/src/components/PaneLayoutRenderer.tsx:8-9,157-164` (drop `QuickCommandMenu` + `executeCommand`; `extraActions` removed)
- `spa/src/components/hosts/SessionsSection.tsx:1,12-15,164-224,238-244,310-313` (+ `SessionsSection.test.tsx:7,11,13,54-56,76-77,85-91,188-end`)
- `spa/src/features/workspace/components/WorkspaceContextMenu.tsx:4-8,11-22,43-58,89-99` (+ test `4,6-7,202-267`)
- `spa/src/features/workspace/components/WorkspaceRow.tsx:9,12,49-134,199-253` (+ test `11-12`, quick fixtures and the two popover/touch describes from line 256)
- `spa/src/App.tsx:44,334-347` (context menu no longer needs `workspaceId`/`hostId`)
- `spa/src/lib/register-modules/index.tsx:67,236-259` (module `quick-commands`)
- `spa/src/lib/module-registry.ts:55-67,79,210-212` (+ `module-registry.test.ts:12,303-327`)
- `spa/src/lib/settings-order.ts:11-26 (doc table row), 63` (+ `__tests__/settings-order-pr2.test.ts:24,70,82,104`)
- `spa/src/lib/register-modules.test.ts:830-835,838-845`
- `spa/src/lib/sync/register-sync.ts:6,18`
- `spa/src/lib/storage/keys.ts` (`QUICK_COMMANDS`)
- `spa/src/lib/rebuild/transport.ts:8-10,42-44` (comments only: drop the `executeCommand` / Quick Commands references)
- `spa/src/components/SettingsPage.tsx:139` (comment only)
- `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

Keep: `spa/src/lib/infer-workspace-host-id.ts` (still used by `lib/host-color.ts:3` via `collectTmuxSessionHostIds`; `inferWorkspaceHostId` becomes unused by production code — DELETE that export and its tests (YAGNI; decided by orchestrator), keep `collectTmuxSessionHostIds`).

**Interfaces:**
- Consumes: nothing new
- Produces: no quick-command API remains; `ModuleDefinition` has no `commands`; `WorkspaceContextMenu` props `{ position, onSettings, onTearOff?, onMergeTo?, onClose }`; `PaneHeader` in `PaneLayoutRenderer` gets no `extraActions`. B3 relies on `SessionsSection` having no `CommandSlot` in its header.

- [ ] **Step 1: Fresh importer inventory**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -l "useQuickCommandStore|quick-command-bindings|quick-command-slots|slot-executor|CommandSlot|QuickCommandMenu|useCommands|QuickCommandsSettingsSection|contributors/quick-commands|QUICK_COMMANDS|execute-command|executeCommand|getModulesWithCommands|CommandContribution|CommandContext|MODULE_QUICK_COMMANDS|quick_commands|QuickAction|HostPickerPopover|quick-commands" spa/src electron | sort
```
Expected (44 paths at plan time): exactly the Delete + Modify lists above plus `spa/src/components/SettingsPage.test.tsx` (uses `'quick-commands'` only as an arbitrary fixture localId with its own component — **leave unchanged**) and `spa/src/lib/infer-workspace-host-id.ts` (comment only — update the comment at line 28 to drop the HostPickerPopover reference). If the list differs, stop and surface. Also confirm no module still contributes `commands`:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "commands:\s*\[" spa/src --glob '!*.test.*'
```
Expected: no output (spec §4.4: otherwise stop and surface).

- [ ] **Step 2: Write the regression tests first (they fail while the system exists)**

Add to `spa/src/lib/module-registry.test.ts` (replacing the `describe('module-registry commands'` block at 303-327, and dropping `getModulesWithCommands` from the import at line 12):
```ts
describe('module-registry — no command contributions', () => {
  it('ModuleDefinition no longer exposes a commands API', async () => {
    const mod = await import('./module-registry')
    expect('getModulesWithCommands' in mod).toBe(false)
  })
})
```
Add to `spa/src/lib/register-modules.test.ts` (replacing T7 at 830-835):
```ts
  it('no quick-commands module or purdex Commands section is registered', () => {
    registerBuiltinModules()
    expect(getModule('quick-commands')).toBeUndefined()
    expect(listContributions('purdex').map((c) => c.localId)).not.toContain('quick-commands')
  })
```
and in T9 (838-845) remove `'settings.section.commands',` from `required`.

Add to `spa/src/components/hosts/SessionsSection.test.tsx` (inside `describe('SessionsSection'`):
```tsx
  it('the header carries only the New Session button (no quick-command slot)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.queryByRole('toolbar')).toBeNull()
    expect(screen.getByText('New Session')).toBeInTheDocument()
  })
```

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/module-registry.test.ts src/lib/register-modules.test.ts`
Expected: FAIL (`getModulesWithCommands` still exported; `quick-commands` module still registered).

- [ ] **Step 3: Delete the files**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git rm \
  spa/src/stores/useQuickCommandStore.ts spa/src/stores/useQuickCommandStore.bindings.test.ts spa/src/stores/useQuickCommandStore.crud.test.ts \
  spa/src/lib/quick-command-bindings.ts spa/src/lib/quick-command-bindings.test.ts \
  spa/src/lib/quick-command-slots.ts spa/src/lib/quick-command-slots.test.ts \
  spa/src/lib/slot-executor.ts spa/src/lib/slot-executor.test.ts \
  spa/src/lib/execute-command.ts spa/src/lib/execute-command.test.ts \
  spa/src/components/CommandSlot.tsx spa/src/components/CommandSlot.test.tsx spa/src/components/QuickCommandMenu.tsx \
  spa/src/components/HostPickerPopover.tsx spa/src/components/HostPickerPopover.test.tsx \
  spa/src/hooks/useCommands.ts spa/src/hooks/useCommands.test.ts \
  spa/src/components/settings/QuickCommandsSettingsSection.tsx spa/src/components/settings/QuickCommandsSettingsSection.test.tsx \
  spa/src/lib/sync/contributors/quick-commands.ts spa/src/lib/sync/contributors/quick-commands.test.ts \
  spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx spa/src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx \
  spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx \
  spa/src/lib/register-modules.quick-commands.test.tsx
```

- [ ] **Step 4: Edit the entry points**

`spa/src/components/PaneLayoutRenderer.tsx`: delete lines 8-9 (imports) and the `extraActions={…}` prop (157-164, from `extraActions={content.kind === 'tmux-session' ? (` through `) : undefined}`). If `content` is now only used for `title={content.kind}`, keep the `const content = layout.pane.content` line.

`spa/src/components/hosts/SessionsSection.tsx`:
- line 1 → `import { useState } from 'react'`
- delete imports 12-15 (`CommandSlot`, `runHostSlot`, `QUICK_COMMAND_SLOTS`, `QuickCommand`)
- delete the executor block 164-224 (comment through the `}, [hostId])` closing `runHostExecutor`)
- in the header replace 237-253 with:
```tsx
        <button
          onClick={() => setShowNew(true)}
          disabled={isOffline}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
        >
          <Plus size={14} />
          {t('hosts.new_session')}
        </button>
```
- delete the stale comment inside the row actions (310-313).

`SessionsSection.test.tsx`: delete imports at lines 7 (`useQuickCommandStore`), 11 (`QUICK_COMMAND_SLOTS`), 13 (`executeCommand`); delete the `vi.mock('../../lib/execute-command', …)` block (54-56); delete `vi.mocked(executeCommand)…` lines (76-77) and the quick-command store reset + module registry lines (85-91; keep `useModuleEnabledStore.setState(...)` only if still imported elsewhere, otherwise drop its import too); delete everything from `describe('SessionsSection — v1 QuickCommandMenu removal` (line 188) to end of file. Remove now-unused `useTabStore`/`useWorkspaceStore` mock members only if lint reports them unused (`findWorkspaceByTab`, `mockActiveTabId` belong to the deleted cases — delete them and their resets if unused).

`spa/src/features/workspace/components/WorkspaceContextMenu.tsx`:
- imports 1-8 → 
```tsx
import { useEffect, useState } from 'react'
import { Sliders, ArrowSquareOut, ArrowSquareIn } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
```
- `Props`: delete `workspaceId` and `hostId` with their doc comments (11-22); function params drop them.
- delete the quick-commands selector block (43-58).
- delete the `{showQuickCommandsSection && (…)}` fragment (89-99).

`WorkspaceContextMenu.test.tsx`: delete imports 4, 6, 7 (and `useModuleEnabledStore` if only used by the deleted cases); delete the three quick-command cases 202-267; remove any `workspaceId=`/`hostId=` props still passed in remaining cases.

`spa/src/App.tsx`: delete line 44 import; replace 334-347 with:
```tsx
        {wsContextMenu && workspaces.some((w) => w.id === wsContextMenu.wsId) && (
          <WorkspaceContextMenu
            position={wsContextMenu.position}
            onSettings={() => openWsSettings(wsContextMenu.wsId)}
            onTearOff={window.electronAPI ? () => handleWsTearOff(wsContextMenu.wsId) : undefined}
            onMergeTo={window.electronAPI ? (targetWindowId) => handleWsMergeTo(wsContextMenu.wsId, targetWindowId) : undefined}
            onClose={handleCloseWsContextMenu}
          />
        )}
```

`spa/src/features/workspace/components/WorkspaceRow.tsx`:
- delete imports line 9 (`inferWorkspaceHostId`) and 12 (`WorkspaceQuickActionsPopover`); line 1 → `import { useCallback, useEffect, useRef, useState } from 'react'` becomes `import type React from 'react'` only if no hooks remain — after the deletions below no hook from React is used, so use no React value import (keep the `React.CSSProperties` type via `import type React from 'react'`).
- delete lines 49-134 (popover state, hub refs, long-press, picker, pointerdown effects).
- replace the hub `<div ref={hubRef} …>…</div>` (199-253, from `<div\n            ref={hubRef}` through its closing `</div>` after `WorkspaceQuickActionsPopover`) with just the Plus button:
```tsx
          <button
            type="button"
            aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
            title={t('nav.add_tab_to_workspace', { name: workspace.name })}
            onClick={(e) => {
              e.stopPropagation()
              onAddTabToWorkspace(workspace.id)
            }}
            className="p-0.5 rounded hover:bg-surface-secondary hover:text-text-primary cursor-pointer opacity-0 group-hover/ws-header:opacity-100 focus:opacity-100 transition-opacity focus:outline-none"
          >
            <Plus size={12} />
          </button>
```
(keep it inside the existing `{showTabs && (…)}`).
- `WorkspaceRow.test.tsx`: delete imports 11-12 and the `useQuickCommandStore` / `useModuleEnabledStore` / `useHostStore` imports if only used by fixtures being removed; delete from the `// Phase 1b' — Plus hover popover` banner (line 256) through the end of `describe('WorkspaceRow — touch fallback (codex round-1 C17)'`, including `setupHoverPopoverFixtures`. The "calls onAddTabToWorkspace when header plus is clicked" case (230) keeps covering the Plus click.

`spa/src/lib/register-modules/index.tsx`: delete line 67 (`QuickCommandsSettingsSection` import) and the comment + `registerModule({ id: 'quick-commands', … })` block (236-259).

`spa/src/lib/module-registry.ts`: delete `CommandContribution` + `CommandContext` (55-67), `commands?: CommandContribution[]` (79) and `getModulesWithCommands` (210-212).

`spa/src/lib/settings-order.ts`: delete `MODULE_QUICK_COMMANDS: 12,` (63) and renumber nothing (gaps are allowed); update the doc table row "Browser / Commands / Editor / Files / Monitor / Sync" (19-20) and the comment at 58-61 to "Browser / Editor / Files / Monitor / Sync", and the `MODULE_QUICK_COMMANDS` example at 24 to `MODULE_PERFORMANCE_MONITOR` (sidebar "Monitor").

`spa/src/lib/__tests__/settings-order-pr2.test.ts`: delete comment line 24, the `quick-commands` entry at 70, `'quick-commands'` in the Set at 82 and in the module band list at 104; update the header comment's label list to drop "Commands".

`spa/src/lib/sync/register-sync.ts`: delete line 6 import and line 18 `syncEngine.register(createQuickCommandsContributor())`.

`spa/src/lib/storage/keys.ts`: delete `QUICK_COMMANDS: 'purdex-quick-commands',`.

`spa/src/lib/rebuild/transport.ts`: lines 8-10 → "`createSession` (`host-api.ts`) goes through `hostFetch`, so the engine cannot reuse it: it needs the requests themselves, pinned."; lines 42-44 → "Required and non-empty on THIS transport: a rebuild or launch always states the generation it expects, so 'no expectation' is not representable here — an empty value throws rather than sending."

`spa/src/components/SettingsPage.tsx:139`: replace `quick-commands` in the comment with `a module section`.

`spa/src/lib/infer-workspace-host-id.ts:28`: drop the `/ §4.4 HostPickerPopover` reference.

- [ ] **Step 5: Locale keys**

Delete from **both** `en.json` and `zh-TW.json`: `modules.quick_commands.description`, `settings.section.commands`, `settings.section.quick_commands`, every `settings.quick_commands.*` (`title, desc, new, edit, empty, name, command, icon, category, mount, slot.workspace, slot.host`), every `quick_commands.*` (`host_picker.label, host_picker.empty, host_picker.online, host_picker.offline, host_picker.close, toast.create_failed, toast.send_keys_failed, toast.switch_failed, toast.retry, aria.toolbar, aria.workspace_actions`).

Check for other now-unused keys introduced only for these components:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && node -e 'const j=require("./src/locales/en.json"); for (const k of Object.keys(j)) if (/quick|slot/i.test(k)) console.log(k)'
```
Expected: no output.

- [ ] **Step 6: Verify nothing references the removed system**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "useQuickCommandStore|quick-command|slot-executor|CommandSlot|QuickCommandMenu|useCommands|QuickCommandsSettingsSection|QUICK_COMMANDS|execute-command|executeCommand|getModulesWithCommands|CommandContribution|CommandContext|MODULE_QUICK_COMMANDS|quick_commands|WorkspaceQuick|HostPickerPopover" spa/src electron --glob '!spa/src/components/SettingsPage.test.tsx'
```
Expected: no output.

- [ ] **Step 7: Run the affected suites, lint, typecheck**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run src/lib/module-registry.test.ts src/lib/register-modules.test.ts src/lib/__tests__ src/components/hosts/SessionsSection.test.tsx src/features/workspace src/components/SettingsPage.test.tsx src/lib/sync src/locales src/components/PaneLayoutRenderer && npx tsc -b && pnpm run lint`
Expected: PASS; tsc 0; lint 0 errors.

- [ ] **Step 8: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && git add -A spa/src electron && git commit -m "refactor(spa): remove the quick-command system

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```
(`git add -A spa/src` is safe here only if no parallel subagent is working in this worktree; otherwise list the paths from Steps 3-5 with `git commit --only`.)

### Task 10: Full verification

**Files:** none modified unless a check fails (fix in the task that owns the file, as a follow-up commit).

- [ ] **Step 1: Whole test suite**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && npx vitest run`
Expected: all test files pass, 0 failures.

- [ ] **Step 2: Lint**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run lint`
Expected: exit 0.

- [ ] **Step 3: Build + bundle budget**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher/spa && pnpm run build && MAIN=$(ls -S dist/assets/index-*.js | head -1) && echo "main gzip=$(gzip -c "$MAIN" | wc -c)" && rg -l '"AddressBookTabs"' dist/assets/*.js
```
Expected: build succeeds; main gzip grows < 20 KB (20480 bytes) versus the Task 1 baseline; `icon-meta` is either in its own chunk or in the same chunk it already was in at baseline. Record both numbers in the PR description.

- [ ] **Step 4: Leftover scan**

Run:
```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/host-launcher && rg -n "useResumeTemplateStore|RESUME_TEMPLATES|SnapshotSettingsSection|QuickCommand|quick_commands|execute-command|liveResumeTemplates" spa/src electron --glob '!spa/src/components/SettingsPage.test.tsx'
```
Expected: no output.

- [ ] **Step 5: Manual smoke (dev server, HMR)** — against a host running the B1 daemon and one running an old daemon:
  1. Host › Projects: add / edit / reorder / delete; path icons show ✅ / ❌; a relative path is refused client-side.
  2. Host › Commands › Normal: add with an agent icon and a Phosphor icon (search "rocket"); command-word Test shows a verdict; reload the page — icons render.
  3. Host › Commands › Resume: edit `cc` exact; open a terminated pane's Rebuild panel on that host — the command shows the edit; a pane on another host does not.
  4. Edit the same collection from two browser windows → second save shows "Changed elsewhere" and the reloaded values.
  5. Old-daemon host: Projects / Commands show the "daemon too old" notice; rebuild still sends default templates.
  6. Host › Snapshots on the dev host shows the device block; another host's page does not; "Rebuild all sessions" on host A never creates on host B.
  7. Workspace row `+` only adds a tab (no hover popover); workspace right-click has no quick-commands section; pane header has no quick-command menu; Settings sidebar has no Commands / Snapshot entries.

---

## Self-review

**Spec coverage (§4):**
- §4.1 store (status set, load/save/409/ensureLoaded, not persisted/synced, API file) → Task 2. Connect trigger → Task 2 loader; page-mount load → Task 6 `useHostConfigGate`.
- §4.2 lib move, `resumeLookupFor` / `useResumeTemplateLookup`, engine `ensureLoaded`, batch two-pass + multi-host test, RebuildActionSet / RenamePopover / provenance tests, store + sync + key deletion → Tasks 3-4.
- §4.3 host sections 7/8/9 → Tasks 6, 7, 8; offline/unsupported notice → Task 6 gate (used by 6 and 7; Snapshots works offline as before, showing ⚪ health). Projects list/edit/slug suggestion/debounced path check/never blocks → Task 6. Commands tabs, icon picker, command-word check → Tasks 5, 7. Spike → Task 1 (deviation 1). Snapshots split, `filterSnapshotByHost`, dev-host client block + no-dev fallback, ResumeTemplateSettings not rendered there, global section unregistered → Tasks 4, 8.
- §4.4 removals incl. execute-command decision, module command API, settings-order constant, sync contributor, storage key, workspace menus, PaneLayoutRenderer, Sessions header slot, locales → Task 9.

**Known gaps / open questions:** see "Spec deviations" 1-9. Additionally: (a) the Snapshots page is not gated on host config (it does not read it except for rebuild templates, which fall back to defaults); (b) `inferWorkspaceHostId` is deleted with its tests (only `collectTmuxSessionHostIds` remains).

