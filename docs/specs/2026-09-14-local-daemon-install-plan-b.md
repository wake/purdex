# Local Daemon Install — Plan B (Electron + SPA) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the Purdex app install, start, restart and update a `pdx` daemon
on the machine it runs on, fetching a cross-compiled binary from the source
daemon (Plan A's `GET /api/dev/daemon/download`), and register the new
daemon as a host automatically.

**Architecture:** All file/process work lives in Electron main under
`electron/local-daemon/` — a pure parser for `lsof -F0`, a login-shell PATH
resolver, a TOML config helper, and `createLocalDaemon(deps)` which owns one
promise queue and exposes `status / install / start / restart /
ensureRunning / withLock`. Side effects are injected through `deps`, so
vitest covers the logic without mocking modules. The SPA gets a
`LocalDaemonSection` rendered under the existing *Daemon* block and an
idempotent `registerLocalHost` store helper.

**Tech Stack:** Electron 41 (main + contextBridge preload), Node 24, vitest
4 (`cd electron && pnpm test`), React 19 / Zustand 5 / Tailwind 4
(`cd spa && npx vitest run`), `smol-toml`.

**Spec:** `docs/specs/2026-09-14-local-daemon-install-spec.md` (v4) — §3 and
§4 are this plan's contract; §1 decisions are fixed.

**Plan review:** codex `task-mu01nnur-hom7fq` (6 Blocker, 10 Important, 2 Minor) — all applied in this revision. Plan A must be merged
(or at least its endpoint contract in Plan A Task 7 honoured) before Task 8's
manual acceptance; unit tasks do not depend on it.

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again,
  commit. Tasks 1–9 are one commit each; Task 10 is a verification gate and
  creates no commit.
- **Commit messages in English**; every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01JYDubMeVHmpGkgjyRo5bFN
  ```
- **Worktree:** everything runs from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/local-daemon-install`
  (prefix every Bash call with `cd <that path> &&`; absolute paths in
  Edit/Write must include `.claude/worktrees/local-daemon-install/`).
- **Verification commands** (exact forms):
  ```
  cd electron && pnpm test                 # electron unit tests
  cd spa && npx vitest run                  # SPA tests
  cd spa && pnpm run lint && pnpm run build # SPA lint + build
  pnpm exec electron-vite build             # main/preload bundle compiles (from repo root)
  ```
- **pnpm, never npm.** New dependency `smol-toml` goes into the **root**
  `package.json` **`devDependencies`**: `pnpm add -Dw smol-toml@^1.3.0`.
  electron-vite 5 externalises `dependencies` by default and bundles
  `devDependencies` into `out/main`; the dev-update flow ships only `out/`,
  so a runtime dep must be bundled. After Task 4:
  `pnpm exec electron-vite build && ! rg -n "smol-toml" out/main/index.js`
  must show no external import (the parser code is inlined).
- **Dev-mode semantics (spec D6):** in Electron, `PDX_DEV_MODE` is enabled
  unless it equals `'0'`. `main.ts` sets the default at startup; every gate
  reads `!== '0'`.
- **No NUL bytes in any `execFile` argv** (Node rejects them).
- **i18n:** every user-visible string in BOTH `spa/src/locales/en.json`
  and `spa/src/locales/zh-TW.json`, flat keys under `settings.dev.local.*`.
  Placeholders use the store's `{{name}}` form and `t(key, { name })`
  (`makeT` interpolates double braces only; a missing key falls back to en,
  then to the key itself).
- **Never widen `useHostStore` semantics.** `registerLocalHost` is a helper
  built on the existing `addHost`/`updateHost`; it does not change them.
- **electron vitest include** currently is `['*.test.ts']` (root only).
  Task 2 widens it to `['**/*.test.ts']` with `exclude: ['node_modules/**']`.

---

## File Structure

| File | Responsibility |
|---|---|
| `electron/local-daemon/lsof.ts` *(new)* + `lsof.test.ts` | `parseLsofF0`, `decideOwnership` — pure |
| `electron/local-daemon/launch-env.ts` *(new)* + `launch-env.test.ts` | `resolveShellPath`, `buildLaunchEnv` |
| `electron/local-daemon/config.ts` *(new)* + `config.test.ts` | `readDaemonConfig`, `pickBindAddress`, `renderInitialConfig` |
| `electron/local-daemon/types.ts` *(new)* | `LocalDaemonStatus`, `LocalDaemonResult`, `LocalDaemonDeps` |
| `electron/local-daemon/index.ts` *(new)* + `index.test.ts` | `createLocalDaemon(deps)` — queue, status, install, start, restart, ensureRunning |
| `electron/local-daemon/node-deps.ts` *(new)* | real `deps` built from `fs/promises`, `child_process`, `os`, `crypto`, global `fetch` |
| `electron/devmode.test.ts` *(new)* | static assertions on the three `!== '0'` gates |
| `electron/main.ts` *(modify)* | default env, IPC, `ensureRunning`, `applyUpdate` under the lock |
| `electron/preload.ts` *(modify)* | four new bridges + progress listener |
| `electron/updater.ts:34` *(modify)* | `devUpdateEnabled` |
| `electron/vitest.config.ts` *(modify)* | include subfolders |
| `spa/src/types/electron.d.ts` *(modify)* | new types + methods |
| `spa/src/stores/useHostStore.ts` *(modify)* + test | `registerLocalHost` action |
| `spa/src/components/settings/LocalDaemonSection.tsx` *(new)* + test | the UI block |
| `spa/src/components/settings/DevEnvironmentSection.tsx` *(modify)* | render `<LocalDaemonSection>` |
| `spa/src/locales/{en,zh-TW}.json` *(modify)* | strings |

---

### Task 1: Dev mode on by default in Electron

**Files:**
- Modify: `electron/main.ts` (top of file + `:216`), `electron/preload.ts:135`, `electron/updater.ts:34`
- Modify: `electron/signing.test.ts:43-57, 63-69` (two static tests assert `=== '1'`; flip them)
- Create: `electron/devmode.test.ts`

- [ ] **Step 1: Failing static test** (pattern: `electron/signing.test.ts`)

```ts
// electron/devmode.test.ts
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = (p: string) => readFileSync(resolve(__dirname, p), 'utf8')

describe('PDX_DEV_MODE is on by default (spec D6)', () => {
  it('main sets the default before any gate reads it', () => {
    const main = src('main.ts')
    const idx = main.indexOf("if (process.env.PDX_DEV_MODE === undefined) process.env.PDX_DEV_MODE = '1'")
    expect(idx).toBeGreaterThan(-1)
    // The default must precede the IPC gate.
    expect(idx).toBeLessThan(main.indexOf("process.env.PDX_DEV_MODE !== '0'"))
  })
  it('no gate still requires === "1"', () => {
    for (const f of ['main.ts', 'preload.ts', 'updater.ts']) {
      expect(src(f), f).not.toContain("PDX_DEV_MODE === '1'")
    }
  })
  it('preload and updater gate on !== "0"', () => {
    expect(src('preload.ts')).toContain("process.env.PDX_DEV_MODE !== '0'")
    expect(src('updater.ts')).toContain("devUpdateEnabled: process.env.PDX_DEV_MODE !== '0'")
  })
})
```

- [ ] **Step 2: Flip the two existing static tests in `electron/signing.test.ts`**

`'preload gates dev update API behind strict PDX_DEV_MODE === "1"'` →
title `'preload gates dev update API on PDX_DEV_MODE !== "0"'`; replace both
regexes `/process\.env\.PDX_DEV_MODE\s*===\s*['"]1['"]/` with
`/process\.env\.PDX_DEV_MODE\s*!==\s*['"]0['"]/` and rewrite the comment
to "Dev features are on by default; only PDX_DEV_MODE=0 disables (spec
2026-09-14 D6). Must match main.ts and the daemon's devmode.Enabled()."
`'main.ts gates dev:* IPC handler registration behind strict PDX_DEV_MODE === "1"'` →
same title/regex change. Leave the Go-side test alone (Plan A owns it).

Run: `cd electron && pnpm test` → the devmode tests and the two flipped
signing tests FAIL (source still says `=== '1'`).

- [ ] **Step 3: Implement**

`electron/main.ts`, immediately after the imports (before
`protocol.registerSchemesAsPrivileged`):

```ts
// Dev features (dev update, local daemon management) are on by default —
// Purdex is single-user. Only an explicit PDX_DEV_MODE=0 turns them off
// (spec 2026-09-14 D6). Set here so preload and updater see the same value.
if (process.env.PDX_DEV_MODE === undefined) process.env.PDX_DEV_MODE = '1'
```

`main.ts:214-216`: comment → `// Dev Update — on unless PDX_DEV_MODE === '0' (spec D6), matching the daemon's devmode.Enabled() and preload.` and the condition → `if (process.env.PDX_DEV_MODE !== '0') {`.

`preload.ts:134-135`: comment → `// Dev Update (exposed unless PDX_DEV_MODE=0)`; spread condition → `...(process.env.PDX_DEV_MODE !== '0' ? {`.

`updater.ts:34`: `devUpdateEnabled: process.env.PDX_DEV_MODE !== '0',`.

- [ ] **Step 4: Verify**

`cd electron && pnpm test` → PASS. `pnpm exec electron-vite build` (repo root) → compiles.

- [ ] **Step 5: Commit**

```bash
git add electron/main.ts electron/preload.ts electron/updater.ts electron/devmode.test.ts electron/signing.test.ts
git commit -m "feat(electron): dev features on by default, PDX_DEV_MODE=0 disables"
```

---

### Task 2: `lsof -F0` parser and ownership decision (pure)

**Files:**
- Create: `electron/local-daemon/lsof.ts`, `electron/local-daemon/lsof.test.ts`
- Modify: `electron/vitest.config.ts`

**Interfaces (produced):**

```ts
export interface LsofFile { fd: string; name: string }
export interface LsofProcess { pid: number; files: LsofFile[] }
export function parseLsofF0(output: string): LsofProcess[]
export function txtPaths(procs: LsofProcess[], pid: number): string[]        // names of fd === 'txt'
export function listenersOn(procs: LsofProcess[], bind: string, port: number): number[]  // pids whose some name is `${bind}:${port}` or `*:${port}`
export type Ownership =
  | { managed: 'managed'; alive: { pid: number } | null }
  | { managed: 'external'; reason: string; alive: { pid: number } | null }
export function decideOwnership(input: {
  candidatePid: number | null
  candidateIsOurs: boolean          // txt realpath matched binPath
  listenerPids: number[]            // from listenersOn
  listenerBinaries: Record<number, string | undefined>  // pid → resolved txt path if known
  binExists: boolean
}): Ownership | { managed: 'none'; alive: null }
```

- [ ] **Step 1: vitest config**

```ts
// electron/vitest.config.ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['**/*.test.ts'],
    exclude: ['node_modules/**'],
  },
})
```

- [ ] **Step 2: Failing tests**

```ts
// electron/local-daemon/lsof.test.ts
import { describe, expect, it } from 'vitest'
import { parseLsofF0, txtPaths, listenersOn, decideOwnership } from './lsof'

// lsof -F0pfn: each field is <letter><value>\0; a process set ends with \n.
const NUL = '\0'
const txtOut =
  `p7520${NUL}\n` +
  `ftxt${NUL}n/Users/wake/Workspace/wake/purdex/bin/pdx${NUL}\n` +
  `ftxt${NUL}n/usr/lib/dyld${NUL}\n`
const listenOut =
  `p7520${NUL}\n` +
  `f8${NUL}n100.64.0.2:7860${NUL}\n` +
  `p9999${NUL}\n` +
  `f5${NUL}n*:8080${NUL}\n`

describe('parseLsofF0', () => {
  it('groups NUL-terminated fields into processes and files', () => {
    const procs = parseLsofF0(txtOut)
    expect(procs).toEqual([{ pid: 7520, files: [
      { fd: 'txt', name: '/Users/wake/Workspace/wake/purdex/bin/pdx' },
      { fd: 'txt', name: '/usr/lib/dyld' },
    ] }])
  })
  it('handles several processes and empty output', () => {
    expect(parseLsofF0('')).toEqual([])
    expect(parseLsofF0(listenOut).map((p) => p.pid)).toEqual([7520, 9999])
  })
  it('tolerates a trailing set without newline', () => {
    expect(parseLsofF0(`p1${NUL}\nftxt${NUL}n/a${NUL}`)).toEqual([{ pid: 1, files: [{ fd: 'txt', name: '/a' }] }])
  })
})

describe('txtPaths / listenersOn', () => {
  it('returns every txt name for the pid', () => {
    expect(txtPaths(parseLsofF0(txtOut), 7520)).toEqual(['/Users/wake/Workspace/wake/purdex/bin/pdx', '/usr/lib/dyld'])
    expect(txtPaths(parseLsofF0(txtOut), 1)).toEqual([])
  })
  it('matches bind:port and *:port only', () => {
    const procs = parseLsofF0(listenOut)
    expect(listenersOn(procs, '100.64.0.2', 7860)).toEqual([7520])
    expect(listenersOn(procs, '127.0.0.1', 7860)).toEqual([])
    expect(listenersOn(procs, '127.0.0.1', 8080)).toEqual([9999])
  })
})

describe('decideOwnership (spec §3.1 step 3)', () => {
  const base = { candidatePid: 7520, candidateIsOurs: true, listenerPids: [7520], listenerBinaries: {}, binExists: true }
  it('ours + listener ours → managed, alive', () => {
    expect(decideOwnership(base)).toEqual({ managed: 'managed', alive: { pid: 7520 } })
  })
  it('ours + no listener → managed, alive (unhealthy, restart offered)', () => {
    expect(decideOwnership({ ...base, listenerPids: [] })).toEqual({ managed: 'managed', alive: { pid: 7520 } })
  })
  it('stale pid (not ours), no listener → managed, stopped', () => {
    expect(decideOwnership({ ...base, candidateIsOurs: false, listenerPids: [] })).toEqual({ managed: 'managed', alive: null })
  })
  it('listener owned by another pid → external with the pid', () => {
    const r = decideOwnership({ ...base, listenerPids: [4242] })
    expect(r.managed).toBe('external')
    expect((r as { reason: string }).reason).toBe('port is served by pid 4242')
  })
  it('foreign listener with a known binary names it', () => {
    const r = decideOwnership({ ...base, candidatePid: null, candidateIsOurs: false, listenerPids: [4242], listenerBinaries: { 4242: '/repo/bin/pdx' } })
    expect((r as { reason: string }).reason).toBe('running daemon is /repo/bin/pdx')
  })
  it('nothing alive, no binary → none', () => {
    expect(decideOwnership({ ...base, candidatePid: null, candidateIsOurs: false, listenerPids: [], binExists: false })).toEqual({ managed: 'none', alive: null })
  })
})
```

- [ ] **Step 3: Run to fail**

`cd electron && pnpm test -- lsof` → FAIL (module missing).

- [ ] **Step 4: Implement**

```ts
// electron/local-daemon/lsof.ts
// Pure helpers for the `lsof -F0pfn` machine format: every field is a
// single-letter tag followed by its value and a NUL; a process set is
// terminated by a newline. We never fall back to parsing the human table.

export interface LsofFile { fd: string; name: string }
export interface LsofProcess { pid: number; files: LsofFile[] }

export function parseLsofF0(output: string): LsofProcess[] {
  const procs: LsofProcess[] = []
  let cur: LsofProcess | null = null
  let file: LsofFile | null = null
  for (const set of output.split('\n')) {
    if (set === '') continue
    for (const field of set.split('\0')) {
      if (field === '') continue
      const tag = field[0]
      const value = field.slice(1)
      if (tag === 'p') {
        cur = { pid: Number(value), files: [] }
        procs.push(cur)
        file = null
      } else if (tag === 'f' && cur) {
        file = { fd: value, name: '' }
        cur.files.push(file)
      } else if (tag === 'n' && file) {
        file.name = value
      }
    }
  }
  return procs
}

export function txtPaths(procs: LsofProcess[], pid: number): string[] {
  return procs.filter((p) => p.pid === pid).flatMap((p) => p.files.filter((f) => f.fd === 'txt').map((f) => f.name))
}

export function listenersOn(procs: LsofProcess[], bind: string, port: number): number[] {
  const exact = `${bind}:${port}`
  const any = `*:${port}`
  return procs.filter((p) => p.files.some((f) => f.name === exact || f.name === any)).map((p) => p.pid)
}

export type Ownership =
  | { managed: 'managed'; alive: { pid: number } | null }
  | { managed: 'external'; reason: string; alive: { pid: number } | null }
  | { managed: 'none'; alive: null }

export interface OwnershipInput {
  candidatePid: number | null
  candidateIsOurs: boolean
  listenerPids: number[]
  listenerBinaries: Record<number, string | undefined>
  binExists: boolean
}

// Spec §3.1 "Decision". The pid-file number is only a candidate: it is
// ours iff its executable resolves to binPath. A foreign listener on our
// endpoint always wins — we must never stop or replace someone else's daemon.
export function decideOwnership(i: OwnershipInput): Ownership {
  const alive = i.candidatePid !== null && i.candidateIsOurs ? { pid: i.candidatePid } : null
  const foreign = i.listenerPids.find((p) => alive === null || p !== alive.pid)
  if (foreign !== undefined) {
    const bin = i.listenerBinaries[foreign]
    return { managed: 'external', reason: bin ? `running daemon is ${bin}` : `port is served by pid ${foreign}`, alive }
  }
  if (alive) return { managed: 'managed', alive }
  return i.binExists ? { managed: 'managed', alive: null } : { managed: 'none', alive: null }
}
```

- [ ] **Step 5: Verify** `cd electron && pnpm test -- lsof` → PASS.

- [ ] **Step 6: Commit**

```bash
git add electron/vitest.config.ts electron/local-daemon/lsof.ts electron/local-daemon/lsof.test.ts
git commit -m "feat(electron): lsof -F0 parser and daemon ownership decision"
```

---

### Task 3: Login-shell PATH and launch env

**Files:**
- Create: `electron/local-daemon/launch-env.ts`, `electron/local-daemon/launch-env.test.ts`

**Interfaces (produced):**

```ts
export interface ExecResult { code: number | null; stdout: string; stderr: string; timedOut: boolean }
export type ExecFn = (file: string, args: string[], opts: { env?: NodeJS.ProcessEnv; cwd?: string; timeoutMs: number }) => Promise<ExecResult>
export function buildShellProbeScript(sentinel: string): string   // printf '\n%s%s%s\n' '<S>' "$PATH" '<S>'
export function extractBetweenSentinels(stdout: string, sentinel: string): string | null
export async function resolveShellPath(exec: ExecFn, shell: string, sentinel: string): Promise<string | null>  // tries -ilc then -lc
export function fallbackPath(basePath: string | undefined, home: string): string
export async function buildLaunchEnv(deps: { exec: ExecFn; shell: string | undefined; baseEnv: NodeJS.ProcessEnv; home: string; sentinel: () => string }): Promise<NodeJS.ProcessEnv>
```

- [ ] **Step 1: Failing tests**

```ts
// electron/local-daemon/launch-env.test.ts
import { describe, expect, it, vi } from 'vitest'
import { buildShellProbeScript, extractBetweenSentinels, resolveShellPath, fallbackPath, buildLaunchEnv, type ExecFn } from './launch-env'

const S = 'PDX_PATH_0123456789abcdef0123456789abcdef'

describe('buildShellProbeScript', () => {
  it('contains no NUL and frames PATH with the sentinel', () => {
    const script = buildShellProbeScript(S)
    expect(script).not.toContain('\0')
    expect(script).toBe(`printf '\\n%s%s%s\\n' '${S}' "$PATH" '${S}'`)
  })
})

describe('extractBetweenSentinels', () => {
  it('ignores banners before and after', () => {
    const out = `Welcome!\nsome plugin noise\n${S}/opt/homebrew/bin:/usr/bin${S}\nbye\n`
    expect(extractBetweenSentinels(out, S)).toBe('/opt/homebrew/bin:/usr/bin')
  })
  it('returns null when a sentinel is missing or the value is empty', () => {
    expect(extractBetweenSentinels(`${S}/x`, S)).toBeNull()
    expect(extractBetweenSentinels(`${S}${S}`, S)).toBeNull()
    expect(extractBetweenSentinels('nothing', S)).toBeNull()
  })
})

describe('resolveShellPath', () => {
  it('uses -ilc first and never puts NUL in argv', async () => {
    const exec: ExecFn = vi.fn(async (_f, args) => {
      for (const a of args) expect(a).not.toContain('\0')
      return { code: 0, stdout: `${S}/a:/b${S}\n`, stderr: '', timedOut: false }
    })
    expect(await resolveShellPath(exec, '/bin/zsh', S)).toBe('/a:/b')
    expect(vi.mocked(exec).mock.calls[0][1][0]).toBe('-ilc')
  })
  it('falls back to -lc when the interactive probe yields nothing', async () => {
    const exec: ExecFn = vi.fn(async (_f, args) => {
      if (args[0] === '-ilc') return { code: 0, stdout: 'banner only\n', stderr: '', timedOut: false }
      return { code: 0, stdout: `${S}/login/bin${S}\n`, stderr: '', timedOut: false }
    })
    expect(await resolveShellPath(exec, '/bin/zsh', S)).toBe('/login/bin')
    expect(vi.mocked(exec).mock.calls.map((c) => c[1][0])).toEqual(['-ilc', '-lc'])
  })
  it('returns null when both probes fail or time out', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: '', timedOut: true })
    expect(await resolveShellPath(exec, '/bin/zsh', S)).toBeNull()
  })
})

describe('fallbackPath', () => {
  it('prefixes brew and ~/.local/bin without a literal tilde', () => {
    expect(fallbackPath('/usr/bin:/bin', '/Users/x')).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin:/usr/bin:/bin')
    expect(fallbackPath(undefined, '/Users/x')).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin')
  })
})

describe('buildLaunchEnv', () => {
  it('sets PATH from the shell and PDX_DEV_MODE=1', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: `${S}/shell/bin${S}\n`, stderr: '', timedOut: false })
    const env = await buildLaunchEnv({ exec, shell: '/bin/zsh', baseEnv: { HOME: '/Users/x', PATH: '/usr/bin' }, home: '/Users/x', sentinel: () => S })
    expect(env.PATH).toBe('/shell/bin')
    expect(env.PDX_DEV_MODE).toBe('1')
    expect(env.HOME).toBe('/Users/x')
  })
  it('uses the fallback when the shell probe fails', async () => {
    const exec: ExecFn = async () => ({ code: 1, stdout: '', stderr: 'boom', timedOut: false })
    const env = await buildLaunchEnv({ exec, shell: undefined, baseEnv: { PATH: '/usr/bin' }, home: '/Users/x', sentinel: () => S })
    expect(env.PATH).toBe('/opt/homebrew/bin:/usr/local/bin:/Users/x/.local/bin:/usr/bin')
  })
})
```

- [ ] **Step 2: Run to fail** `cd electron && pnpm test -- launch-env` → FAIL.

- [ ] **Step 3: Implement**

```ts
// electron/local-daemon/launch-env.ts
// A Finder-launched app inherits /usr/bin:/bin:/usr/sbin:/sbin. The daemon
// execs `tmux` (and agents exec `claude`, `codex`…) from PATH, so we launch
// it with the user's *shell* PATH. Spec §3.1 "Launch PATH".
import { join } from 'node:path'

export interface ExecResult { code: number | null; stdout: string; stderr: string; timedOut: boolean }
export type ExecFn = (
  file: string,
  args: string[],
  opts: { env?: NodeJS.ProcessEnv; cwd?: string; timeoutMs: number },
) => Promise<ExecResult>

export const SHELL_PROBE_TIMEOUT_MS = 5000

export function buildShellProbeScript(sentinel: string): string {
  // Newline framing + a per-call random sentinel: no NUL anywhere in argv,
  // and banner/plugin output on either side cannot leak into the value.
  return `printf '\\n%s%s%s\\n' '${sentinel}' "$PATH" '${sentinel}'`
}

export function extractBetweenSentinels(stdout: string, sentinel: string): string | null {
  const start = stdout.indexOf(sentinel)
  if (start < 0) return null
  const from = start + sentinel.length
  const end = stdout.indexOf(sentinel, from)
  if (end < 0) return null
  const value = stdout.slice(from, end)
  return value.length > 0 ? value : null
}

export async function resolveShellPath(exec: ExecFn, shell: string, sentinel: string): Promise<string | null> {
  const script = buildShellProbeScript(sentinel)
  for (const flag of ['-ilc', '-lc']) {
    try {
      const r = await exec(shell, [flag, script], { timeoutMs: SHELL_PROBE_TIMEOUT_MS })
      if (r.timedOut) continue
      const value = extractBetweenSentinels(r.stdout, sentinel)
      if (value) return value
    } catch {
      // try the next form
    }
  }
  return null
}

export function fallbackPath(basePath: string | undefined, home: string): string {
  const prefix = ['/opt/homebrew/bin', '/usr/local/bin', join(home, '.local/bin')]
  return basePath ? [...prefix, basePath].join(':') : prefix.join(':')
}

export async function buildLaunchEnv(deps: {
  exec: ExecFn
  shell: string | undefined
  baseEnv: NodeJS.ProcessEnv
  home: string
  sentinel: () => string
}): Promise<NodeJS.ProcessEnv> {
  const fromShell = await resolveShellPath(deps.exec, deps.shell || '/bin/zsh', deps.sentinel())
  const PATH = fromShell ?? fallbackPath(deps.baseEnv.PATH, deps.home)
  return { ...deps.baseEnv, PATH, PDX_DEV_MODE: '1' }
}
```

- [ ] **Step 4: Verify** `cd electron && pnpm test -- launch-env` → PASS.

- [ ] **Step 5: Commit**

```bash
git add electron/local-daemon/launch-env.ts electron/local-daemon/launch-env.test.ts
git commit -m "feat(electron): resolve the user's shell PATH for daemon launches"
```

---

### Task 4: Daemon config helpers (TOML read, bind pick, initial write)

**Files:**
- Create: `electron/local-daemon/config.ts`, `electron/local-daemon/config.test.ts`
- Modify: root `package.json` (`smol-toml`)

**Interfaces (produced):**

```ts
export interface DaemonConfig { bind: string; port: number; token: string | null; dataDir: string }
export function parseDaemonConfig(toml: string, home: string): DaemonConfig     // Go defaults applied
export const DEFAULT_DATA_DIR = (home: string) => join(home, '.config', 'pdx')
export interface Iface { name: string; address: string; family: string; internal: boolean }
export function pickBindAddress(ifaces: Iface[], platform: NodeJS.Platform): { bind: string; note?: string }
export function renderInitialConfig(bind: string, token: string): string
export function generateToken(random: (n: number) => Buffer): string           // 'purdex_' + 40 hex
```

- [ ] **Step 1: Add the dependency**

Run: `cd <worktree> && pnpm add -Dw smol-toml@^1.3.0`. Confirm it lands in
root `package.json` **`devDependencies`** (bundled by electron-vite; see
Global Constraints).

- [ ] **Step 2: Failing tests**

```ts
// electron/local-daemon/config.test.ts
import { describe, expect, it } from 'vitest'
import { parseDaemonConfig, pickBindAddress, renderInitialConfig, generateToken, DEFAULT_DATA_DIR } from './config'

const home = '/Users/x'

describe('parseDaemonConfig', () => {
  it('applies Go defaults for a missing key', () => {
    expect(parseDaemonConfig('', home)).toEqual({ bind: '127.0.0.1', port: 7860, token: null, dataDir: `${home}/.config/pdx` })
  })
  it('reads the keys the daemon rewrites (full TOML from EnsureHostID)', () => {
    const toml = `host_id = "mini:abc123"\nbind = "100.64.0.2"\nport = 7861\ntoken = "purdex_ff"\ndata_dir = "/Users/x/.config/pdx"\n\n[dev]\nupdate = false\n`
    expect(parseDaemonConfig(toml, home)).toEqual({ bind: '100.64.0.2', port: 7861, token: 'purdex_ff', dataDir: '/Users/x/.config/pdx' })
  })
  it('surfaces a custom data_dir verbatim', () => {
    expect(parseDaemonConfig('data_dir = "/Volumes/X/pdx"\n', home).dataDir).toBe('/Volumes/X/pdx')
  })
  it('throws on invalid TOML', () => {
    expect(() => parseDaemonConfig('bind = ', home)).toThrow()
  })
})

describe('pickBindAddress (spec D4 + §3.1 configure)', () => {
  const ts = { name: 'utun4', address: '100.64.0.9', family: 'IPv4', internal: false }
  it('one utun in 100.64/10 on darwin → that address', () => {
    expect(pickBindAddress([{ name: 'en0', address: '192.168.1.5', family: 'IPv4', internal: false }, ts], 'darwin')).toEqual({ bind: '100.64.0.9' })
  })
  it('none → loopback with a note', () => {
    const r = pickBindAddress([{ name: 'en0', address: '192.168.1.5', family: 'IPv4', internal: false }], 'darwin')
    expect(r.bind).toBe('127.0.0.1')
    expect(r.note).toMatch(/no tailscale/i)
  })
  it('two candidates → loopback with a note listing them', () => {
    const r = pickBindAddress([ts, { ...ts, name: 'utun5', address: '100.100.1.1' }], 'darwin')
    expect(r.bind).toBe('127.0.0.1')
    expect(r.note).toContain('100.64.0.9')
    expect(r.note).toContain('100.100.1.1')
  })
  it('rejects CGNAT addresses on non-utun interfaces on darwin, accepts them on linux', () => {
    const isp = { name: 'en0', address: '100.70.0.1', family: 'IPv4', internal: false }
    expect(pickBindAddress([isp], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([{ ...isp, name: 'tailscale0' }], 'linux').bind).toBe('100.70.0.1')
  })
  it('CIDR edges: 100.63.255.255 and 100.128.0.0 are out, 100.127.255.255 is in', () => {
    const mk = (a: string) => ({ name: 'utun1', address: a, family: 'IPv4', internal: false })
    expect(pickBindAddress([mk('100.63.255.255')], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([mk('100.128.0.0')], 'darwin').bind).toBe('127.0.0.1')
    expect(pickBindAddress([mk('100.127.255.255')], 'darwin').bind).toBe('100.127.255.255')
  })
})

describe('renderInitialConfig / generateToken', () => {
  it('renders the exact initial file', () => {
    expect(renderInitialConfig('100.64.0.9', 'purdex_abc')).toBe(
      'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_abc"\n\n[dev]\nupdate = false\n',
    )
  })
  it('token is purdex_ + 40 hex', () => {
    const t = generateToken((n) => Buffer.alloc(n, 0xab))
    expect(t).toBe('purdex_' + 'ab'.repeat(20))
  })
  it('DEFAULT_DATA_DIR', () => {
    expect(DEFAULT_DATA_DIR(home)).toBe('/Users/x/.config/pdx')
  })
})
```

- [ ] **Step 3: Run to fail** `cd electron && pnpm test -- config` → FAIL.

- [ ] **Step 4: Implement**

```ts
// electron/local-daemon/config.ts
// Reading and seeding ~/.config/pdx/config.toml from the app. The daemon
// rewrites the file with every key on first start (EnsureHostID), so it is
// parsed with a real TOML parser and Go's defaults are applied here.
import { join } from 'node:path'
import { parse } from 'smol-toml'

export interface DaemonConfig { bind: string; port: number; token: string | null; dataDir: string }

export const DEFAULT_DATA_DIR = (home: string) => join(home, '.config', 'pdx')

export function parseDaemonConfig(toml: string, home: string): DaemonConfig {
  const doc = toml.trim() === '' ? {} : (parse(toml) as Record<string, unknown>)
  const str = (k: string) => (typeof doc[k] === 'string' ? (doc[k] as string) : undefined)
  const num = (k: string) => (typeof doc[k] === 'number' ? (doc[k] as number) : undefined)
  return {
    bind: str('bind') ?? '127.0.0.1',
    port: num('port') ?? 7860,
    token: str('token') ?? null,
    dataDir: str('data_dir') ?? DEFAULT_DATA_DIR(home),
  }
}

export interface Iface { name: string; address: string; family: string; internal: boolean }

// 100.64.0.0/10 → first octet 100, second octet 64..127.
function inCGNAT(addr: string): boolean {
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(addr)
  if (!m) return false
  return Number(m[1]) === 100 && Number(m[2]) >= 64 && Number(m[2]) <= 127
}

export function pickBindAddress(ifaces: Iface[], platform: NodeJS.Platform): { bind: string; note?: string } {
  const candidates = ifaces
    .filter((i) => i.family === 'IPv4' && !i.internal && inCGNAT(i.address))
    .filter((i) => platform !== 'darwin' || i.name.startsWith('utun'))
    .map((i) => i.address)
  if (candidates.length === 1) return { bind: candidates[0] }
  if (candidates.length === 0) {
    return { bind: '127.0.0.1', note: 'No Tailscale interface found; bound to 127.0.0.1. Edit ~/.config/pdx/config.toml and restart to change.' }
  }
  return { bind: '127.0.0.1', note: `Several Tailscale-like addresses (${candidates.join(', ')}); bound to 127.0.0.1. Edit ~/.config/pdx/config.toml and restart to choose one.` }
}

export function renderInitialConfig(bind: string, token: string): string {
  return `bind = "${bind}"\nport = 7860\ntoken = "${token}"\n\n[dev]\nupdate = false\n`
}

export function generateToken(random: (n: number) => Buffer): string {
  return 'purdex_' + random(20).toString('hex')
}
```

- [ ] **Step 5: Verify** `cd electron && pnpm test -- config` → PASS.
`pnpm exec electron-vite build` (repo root) succeeds — the bundling check
itself runs in Task 7 once `index.ts` is imported by `main.ts`.

- [ ] **Step 6: Commit**

```bash
git add package.json pnpm-lock.yaml electron/local-daemon/config.ts electron/local-daemon/config.test.ts
git commit -m "feat(electron): daemon config parsing, bind selection and initial config rendering"
```

---

### Task 5: `createLocalDaemon` — types, deps, `status()`

**Files:**
- Create: `electron/local-daemon/types.ts`, `electron/local-daemon/index.ts`, `electron/local-daemon/index.test.ts` (status part)

**Interfaces (produced):**

```ts
// types.ts
export interface LocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; hasToken: boolean } | null
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }
}
export interface LocalDaemonResult { url: string; token: string; hash: string; version: string; hostname: string; bindNote?: string }
export interface WriteHandle { write(chunk: Uint8Array): Promise<void>; close(): Promise<void> }
export interface LocalDaemonDeps {
  home: string
  hostname: () => string
  platform: NodeJS.Platform
  arch: string
  shell: string | undefined
  baseEnv: NodeJS.ProcessEnv
  exec: ExecFn                                    // from launch-env.ts
  fetch: (url: string, init: { headers: Record<string, string>; signal: AbortSignal }) => Promise<Response>
  fs: {
    exists(p: string): Promise<boolean>
    readFile(p: string): Promise<string>
    writeFile(p: string, data: string, mode: number): Promise<void>
    rename(a: string, b: string): Promise<void>
    unlink(p: string): Promise<void>
    mkdir(p: string): Promise<void>               // recursive; the fake records the path
    chmod(p: string, mode: number): Promise<void>
    realpath(p: string): Promise<string>
    openWrite(p: string): Promise<WriteHandle>
    sha256(p: string): Promise<string>
  }
  kill0: (pid: number) => boolean
  portOpen: (host: string, port: number) => Promise<boolean>   // TCP connect succeeds within 500 ms
  networkInterfaces: () => Iface[]
  randomBytes: (n: number) => Buffer
  sleep: (ms: number) => Promise<void>
  now: () => number
  log: (msg: string) => void
}
export interface LocalDaemon {
  status(): Promise<LocalDaemonStatus>
  install(daemonUrl: string, token: string | undefined, onProgress: (step: string) => void): Promise<LocalDaemonResult>
  start(): Promise<LocalDaemonResult>
  restart(): Promise<LocalDaemonResult>
  ensureRunning(): Promise<'started' | 'already-running' | 'not-installed' | 'external' | 'failed'>
  withLock<T>(fn: () => Promise<T>): Promise<T>
}
// index.ts exports: export function createLocalDaemon(deps: LocalDaemonDeps): LocalDaemon
```

- [ ] **Step 1: Write `types.ts`** with the interfaces above **only** (no
`createLocalDaemon` declaration — the factory lives in `index.ts`), plus
`import type { ExecFn } from './launch-env'` and `import type { Iface } from './config'`.

- [ ] **Step 2: Failing tests — a fake deps harness + status matrix**

```ts
// electron/local-daemon/index.test.ts
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { createLocalDaemon } from './index'
import type { LocalDaemonDeps, WriteHandle } from './types'
import type { ExecResult } from './launch-env'

const NUL = '\0'
const HOME = '/Users/t'
const BIN = `${HOME}/.config/pdx/bin/pdx`
const CFG = `${HOME}/.config/pdx/config.toml`
const PID = `${HOME}/.config/pdx/pdx.pid`
// Matches deps.randomBytes below (0xcd × 16 → 'cd' × 16).
const S = 'PDX_PATH_' + 'cd'.repeat(16)

interface Fake {
  deps: LocalDaemonDeps
  files: Map<string, string | Uint8Array>
  modes: Map<string, number>
  dirs: string[]
  execLog: Array<{ file: string; args: string[]; env?: NodeJS.ProcessEnv }>
  health: null | { ok: boolean; hash?: string; version?: string }
  lsofTxt: Record<number, string>      // pid → lsof -d txt output
  lsofListen: string                   // lsof -iTCP output
  alivePids: Set<number>
  portIsOpen: boolean
  onExec: (file: string, args: string[]) => ExecResult | undefined
  downloads: Array<{ status: number; headers: Record<string, string>; body: Uint8Array }>
  clock: number
}

function makeFake(overrides: Partial<Fake> = {}): Fake {
  const fake: Fake = {
    files: new Map(),
    modes: new Map(),
    dirs: [],
    execLog: [],
    health: null,
    lsofTxt: {},
    lsofListen: '',
    alivePids: new Set(),
    portIsOpen: false,
    onExec: () => undefined,
    downloads: [],
    clock: 0,
    deps: undefined as unknown as LocalDaemonDeps,
    ...overrides,
  }
  const text = (p: string) => {
    const v = fake.files.get(p)
    if (v === undefined) throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    return typeof v === 'string' ? v : Buffer.from(v).toString('utf8')
  }
  fake.deps = {
    home: HOME,
    hostname: () => 'air-2026',
    platform: 'darwin',
    arch: 'arm64',
    shell: '/bin/zsh',
    baseEnv: { HOME, PATH: '/usr/bin:/bin' },
    exec: async (file, args, opts) => {
      fake.execLog.push({ file, args, env: opts.env })
      const custom = fake.onExec(file, args)
      if (custom) return custom
      if (file === '/bin/zsh') return { code: 0, stdout: `${S}/opt/homebrew/bin:/usr/bin${S}\n`, stderr: '', timedOut: false }
      if (file === '/usr/bin/which') return { code: 0, stdout: '/opt/homebrew/bin/tmux\n', stderr: '', timedOut: false }
      if (file === '/usr/sbin/lsof') {
        if (args.includes('-d')) {
          const pid = Number(args[args.indexOf('-p') + 1])
          return { code: 0, stdout: fake.lsofTxt[pid] ?? '', stderr: '', timedOut: false }
        }
        return { code: 0, stdout: fake.lsofListen, stderr: '', timedOut: false }
      }
      if (args[0] === 'version') {
        if (!fake.files.has(file)) return { code: 127, stdout: '', stderr: 'not found', timedOut: false }
        const body = text(file)  // fake binaries are JSON identity strings
        return { code: 0, stdout: body + '\n', stderr: '', timedOut: false }
      }
      if (args[0] === 'start') {
        // A real `pdx start` returns only after /api/health answers, with the
        // daemon holding the pid file and listening on bind:port.
        const bindLine = (() => { const c = fake.files.get(CFG); const m = typeof c === 'string' ? /bind = "([^"]+)"/.exec(c) : null; return m ? m[1] : '127.0.0.1' })()
        fake.health = { ok: true, hash: JSON.parse(text(file)).hash, version: '9' }
        fake.alivePids.add(4242); fake.files.set(PID, '4242'); fake.portIsOpen = true
        fake.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
        fake.lsofListen = `p4242${NUL}\nf8${NUL}n${bindLine}:7860${NUL}\n`
        return { code: 0, stdout: 'started', stderr: '', timedOut: false }
      }
      if (args[0] === 'stop') { fake.health = null; fake.alivePids.clear(); fake.lsofListen = ''; fake.portIsOpen = false; return { code: 0, stdout: 'stopped', stderr: '', timedOut: false } }
      return { code: 0, stdout: '', stderr: '', timedOut: false }
    },
    fetch: async (url) => {
      if (url.endsWith('/api/health')) {
        if (!fake.health) throw new Error('ECONNREFUSED')
        return new Response(JSON.stringify(fake.health), { status: 200 })
      }
      // Only the download route consumes the scripted queue.
      const next = fake.downloads.shift()
      if (!next) throw new Error('no scripted download for ' + url)
      return new Response(next.body, { status: next.status, headers: next.headers })
    },
    fs: {
      exists: async (p) => fake.files.has(p),
      readFile: async (p) => text(p),
      writeFile: async (p, d, mode) => { fake.files.set(p, d); fake.modes.set(p, mode) },
      rename: async (a, b) => { const v = fake.files.get(a); if (v === undefined) throw new Error('ENOENT'); fake.files.set(b, v); fake.files.delete(a); const m = fake.modes.get(a); if (m !== undefined) { fake.modes.set(b, m); fake.modes.delete(a) } },
      unlink: async (p) => { fake.files.delete(p) },
      mkdir: async (p) => { fake.dirs.push(p) },
      chmod: async (p, mode) => { fake.modes.set(p, mode) },
      realpath: async (p) => (p === `${HOME}/link-to-pdx` ? BIN : p),
      openWrite: async (p): Promise<WriteHandle> => { const chunks: Uint8Array[] = []; return { write: async (c) => { chunks.push(c) }, close: async () => { fake.files.set(p, Buffer.concat(chunks)) } } },
      sha256: async (p) => { const { createHash } = await import('node:crypto'); const v = fake.files.get(p); return createHash('sha256').update(typeof v === 'string' ? Buffer.from(v) : Buffer.from(v ?? new Uint8Array())).digest('hex') },
    },
    kill0: (pid) => fake.alivePids.has(pid),
    portOpen: async () => fake.portIsOpen,
    networkInterfaces: () => [{ name: 'utun4', address: '100.64.0.9', family: 'IPv4', internal: false }],
    randomBytes: (n) => Buffer.alloc(n, 0xcd),
    sleep: async (ms) => { fake.clock += ms },
    now: () => fake.clock,
    log: () => {},
  }
  return fake
}

const identity = (hash: string) => JSON.stringify({ version: '9', hash, goos: 'darwin', goarch: 'arm64' })

describe('status()', () => {
  let f: Fake
  beforeEach(() => { f = makeFake() })

  it('none when nothing is installed', async () => {
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('none')
    expect(st.installed).toBeNull()
    expect(st.alive).toBeNull()
    expect(st.target).toEqual({ goos: 'darwin', goarch: 'arm64' })
    expect(st.tools.tmux).toBe('/opt/homebrew/bin/tmux')
  })

  it('managed + stopped when the binary exists and nothing listens; stale pid is ignored', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_x"\n')
    f.files.set(PID, '777')
    f.alivePids.add(777)
    f.lsofTxt[777] = `p777${NUL}\nftxt${NUL}n/usr/bin/some-other${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toBeNull()
    expect(st.installed).toEqual({ version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' })
    expect(st.config).toEqual({ bind: '100.64.0.9', port: 7860, hasToken: true })
  })

  it('managed + alive + running when our pid owns the listener', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    const st = await createLocalDaemon(f.deps).status()
    expect(st).toMatchObject({ managed: 'managed', alive: { pid: 4242 }, running: { hash: 'aaa', version: '9', url: 'http://100.64.0.9:7860' } })
  })

  it('managed + alive but not running (no listener yet)', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('managed')
    expect(st.alive).toEqual({ pid: 4242 })
    expect(st.running).toBeNull()
  })

  it('external when a foreign pid serves the endpoint (repo daemon on the Mini)', async () => {
    f.files.set(CFG, 'bind = "100.64.0.2"\n')
    f.lsofListen = `p7520${NUL}\nf8${NUL}n100.64.0.2:7860${NUL}\n`
    f.lsofTxt[7520] = `p7520${NUL}\nftxt${NUL}n/repo/bin/pdx${NUL}\n`
    f.health = { ok: true }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('running daemon is /repo/bin/pdx')
    expect(st.running?.hash).toBe('unknown')
  })

  it('external when health answers but nothing is found listening (no binary either)', async () => {
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    f.health = { ok: true, hash: 'zzz' }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('health answered but no listener found')
  })

  it('ownership compares realpaths (symlinked txt entry still ours)', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${HOME}/link-to-pdx${NUL}\n`
    const st = await createLocalDaemon(f.deps).status()
    expect(st.alive).toEqual({ pid: 4242 })
  })

  it('foreign listener whose txt entries are all dylibs is reported by pid, not by a dylib path', async () => {
    f.files.set(CFG, 'bind = "100.64.0.2"\n')
    f.lsofListen = `p7520${NUL}\nf8${NUL}n100.64.0.2:7860${NUL}\n`
    f.lsofTxt[7520] = `p7520${NUL}\nftxt${NUL}n/usr/lib/dyld${NUL}\n`
    f.health = { ok: true }
    const st = await createLocalDaemon(f.deps).status()
    expect(st.reason).toBe('port is served by pid 7520')
  })

  it('external with custom data_dir', async () => {
    f.files.set(CFG, 'data_dir = "/Volumes/X/pdx"\n')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('custom data_dir')
  })

  it('external when lsof times out', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.onExec = (file) => (file === '/usr/sbin/lsof' ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    const st = await createLocalDaemon(f.deps).status()
    expect(st.managed).toBe('external')
    expect(st.reason).toBe('ownership check timed out')
  })

  it('maps x64 → amd64 and unknown identity on parse failure', async () => {
    f.deps.arch = 'x64'
    f.files.set(BIN, 'garbage')
    const st = await createLocalDaemon(f.deps).status()
    expect(st.target.goarch).toBe('amd64')
    expect(st.installed).toEqual({ version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' })
  })
})
```

- [ ] **Step 3: Run to fail** `cd electron && pnpm test -- index` → FAIL.

- [ ] **Step 4: Implement `index.ts` (status only — install/start come in Task 6, but define the queue now)**

```ts
// electron/local-daemon/index.ts
// The app manages a pdx daemon on the machine it runs on (spec §3.1).
// Every side effect comes through `deps` so the logic is unit-tested with
// an in-memory harness. All public operations run through one promise
// queue; private helpers (suffix `Unlocked`) never enqueue.
import { join } from 'node:path'
import type { LocalDaemon, LocalDaemonDeps, LocalDaemonResult, LocalDaemonStatus } from './types'
import { parseLsofF0, txtPaths, listenersOn, decideOwnership, type Ownership } from './lsof'
import { buildLaunchEnv } from './launch-env'
import { parseDaemonConfig, pickBindAddress, renderInitialConfig, generateToken, DEFAULT_DATA_DIR, type DaemonConfig } from './config'

const LSOF = '/usr/sbin/lsof'
const LSOF_TIMEOUT_MS = 5000
const OWNERSHIP_BUDGET_MS = 10_000
const STOP_SETTLE_MS = 5000
const HEALTH_TIMEOUT_MS = 1500
const VERSION_TIMEOUT_MS = 2000
const STOP_TIMEOUT_MS = 35_000
const START_TIMEOUT_MS = 70_000
const DOWNLOAD_TIMEOUT_MS = 6 * 60_000

class OwnershipTimeout extends Error {}

export function createLocalDaemon(deps: LocalDaemonDeps): LocalDaemon {
  const dataDir = DEFAULT_DATA_DIR(deps.home)
  const binDir = join(dataDir, 'bin')
  const binPath = join(binDir, 'pdx')
  const newPath = join(binDir, 'pdx.new')
  const cfgPath = join(dataDir, 'config.toml')

  // ---- queue -------------------------------------------------------------
  let tail: Promise<unknown> = Promise.resolve()
  function withLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = tail.then(fn, fn)
    tail = run.catch(() => {})
    return run
  }

  // ---- env ---------------------------------------------------------------
  let envPromise: Promise<NodeJS.ProcessEnv> | null = null
  function launchEnv(): Promise<NodeJS.ProcessEnv> {
    envPromise ??= buildLaunchEnv({
      exec: deps.exec, shell: deps.shell, baseEnv: deps.baseEnv, home: deps.home,
      sentinel: () => 'PDX_PATH_' + deps.randomBytes(16).toString('hex'),
    })
    return envPromise
  }

  // ---- primitives --------------------------------------------------------
  function target(): LocalDaemonStatus['target'] {
    const goos = deps.platform === 'darwin' ? 'darwin' : deps.platform === 'linux' ? 'linux' : null
    const goarch = deps.arch === 'arm64' ? 'arm64' : deps.arch === 'x64' ? 'amd64' : null
    if (!goos || !goarch) throw new Error(`unsupported platform ${deps.platform}/${deps.arch}`)
    return { goos, goarch }
  }

  async function readConfig(): Promise<DaemonConfig | null> {
    if (!(await deps.fs.exists(cfgPath))) return null
    return parseDaemonConfig(await deps.fs.readFile(cfgPath), deps.home)
  }

  async function readIdentity(bin: string): Promise<LocalDaemonStatus['installed']> {
    const unknown = { version: 'unknown', hash: 'unknown', goos: 'unknown', goarch: 'unknown' }
    try {
      const r = await deps.exec(bin, ['version', '--json'], { env: await launchEnv(), timeoutMs: VERSION_TIMEOUT_MS })
      if (r.code !== 0) return unknown
      const j = JSON.parse(r.stdout.trim()) as Record<string, unknown>
      const s = (k: string) => (typeof j[k] === 'string' ? (j[k] as string) : 'unknown')
      return { version: s('version'), hash: s('hash'), goos: s('goos'), goarch: s('goarch') }
    } catch {
      return unknown
    }
  }

  async function health(bind: string, port: number): Promise<LocalDaemonStatus['running']> {
    const url = `http://${bind}:${port}`
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), HEALTH_TIMEOUT_MS)
    try {
      const r = await deps.fetch(`${url}/api/health`, { headers: {}, signal: ctl.signal })
      if (!r.ok) return null
      const j = (await r.json()) as Record<string, unknown>
      if (j.ok !== true) return null
      const s = (k: string) => (typeof j[k] === 'string' ? (j[k] as string) : 'unknown')
      return { version: s('version'), hash: s('hash'), url }
    } catch {
      return null
    } finally {
      clearTimeout(timer)
    }
  }

  // Every lsof in one ownership pass shares a 10 s budget (5 s per call).
  async function lsof(args: string[], deadline: number): Promise<string> {
    const remaining = deadline - deps.now()
    if (remaining <= 0) throw new OwnershipTimeout()
    const r = await deps.exec(LSOF, args, { timeoutMs: Math.min(LSOF_TIMEOUT_MS, remaining) })
    if (r.timedOut) throw new OwnershipTimeout()
    return r.stdout // lsof exits 1 when nothing matched; empty output is fine
  }

  async function readCandidatePid(pidPath: string): Promise<number | null> {
    if (!(await deps.fs.exists(pidPath))) return null
    const n = Number.parseInt((await deps.fs.readFile(pidPath)).trim(), 10)
    return Number.isFinite(n) && n > 0 ? n : null
  }

  async function sameBinary(path: string): Promise<boolean> {
    try {
      return (await deps.fs.realpath(path)) === (await deps.fs.realpath(binPath))
    } catch {
      return false
    }
  }

  // Spec §3.1 "Ownership / liveness": resolveOwner.
  async function resolveOwner(cfg: DaemonConfig, binExists: boolean): Promise<Ownership> {
    const deadline = deps.now() + OWNERSHIP_BUDGET_MS
    const candidatePid = await readCandidatePid(join(cfg.dataDir, 'pdx.pid'))
    let candidateIsOurs = false
    if (candidatePid !== null && deps.kill0(candidatePid)) {
      const procs = parseLsofF0(await lsof(['-nP', '-a', '-p', String(candidatePid), '-d', 'txt', '-F0pfn'], deadline))
      for (const p of txtPaths(procs, candidatePid)) {
        if (await sameBinary(p)) { candidateIsOurs = true; break }
      }
    }
    const listenProcs = parseLsofF0(await lsof(['-nP', '-a', `-iTCP:${cfg.port}`, '-sTCP:LISTEN', '-F0pfn'], deadline))
    const listenerPids = listenersOn(listenProcs, cfg.bind, cfg.port)
    const listenerBinaries: Record<number, string | undefined> = {}
    for (const pid of listenerPids) {
      if (candidateIsOurs && pid === candidatePid) continue
      const procs = parseLsofF0(await lsof(['-nP', '-a', '-p', String(pid), '-d', 'txt', '-F0pfn'], deadline))
      // Only name a foreign binary when a txt entry is clearly a pdx
      // executable; the first txt entry can be a dylib, and a wrong path
      // in the reason is worse than the pid alone.
      const pdxLike = txtPaths(procs, pid).find((p) => p.split('/').pop() === 'pdx')
      listenerBinaries[pid] = pdxLike ? await deps.fs.realpath(pdxLike).catch(() => pdxLike) : undefined
    }
    return decideOwnership({ candidatePid, candidateIsOurs, listenerPids, listenerBinaries, binExists })
  }

  async function statusUnlocked(): Promise<LocalDaemonStatus> {
    const tgt = target()
    const env = await launchEnv()
    const which = await deps.exec('/usr/bin/which', ['tmux'], { env, timeoutMs: VERSION_TIMEOUT_MS }).catch(() => null)
    const tmux = which && which.code === 0 ? which.stdout.trim() || null : null
    const cfgFile = await readConfig()
    const cfg = cfgFile ?? parseDaemonConfig('', deps.home)
    const binExists = await deps.fs.exists(binPath)
    const installed = binExists ? await readIdentity(binPath) : null
    const running = await health(cfg.bind, cfg.port)
    const base = {
      binPath, installed, running, target: tgt, tools: { tmux },
      config: cfgFile ? { bind: cfgFile.bind, port: cfgFile.port, hasToken: !!cfgFile.token } : null,
    }
    if (cfgFile && cfgFile.dataDir !== dataDir) {
      return { ...base, managed: 'external', reason: 'custom data_dir', alive: null }
    }
    let own: Ownership
    try {
      own = await resolveOwner(cfg, binExists)
    } catch (e) {
      if (e instanceof OwnershipTimeout) return { ...base, managed: 'external', reason: 'ownership check timed out', alive: null }
      throw e
    }
    if (own.managed === 'external') return { ...base, managed: 'external', reason: own.reason, alive: own.alive }
    if (running && own.alive === null) {
      // Something answered /api/health on our endpoint yet lsof found no
      // listener we could attribute — never treat that as installable.
      return { ...base, managed: 'external', reason: 'health answered but no listener found', alive: null }
    }
    return { ...base, managed: own.managed, alive: own.alive }
  }

  // install/start/restart/ensureRunning are added in Task 6.
  const notYet = async (): Promise<never> => { throw new Error('not implemented') }

  return {
    status: () => withLock(statusUnlocked),
    install: notYet,
    start: notYet,
    restart: notYet,
    ensureRunning: notYet as unknown as LocalDaemon['ensureRunning'],
    withLock,
  }
}
```

(Keep the unused imports — `renderInitialConfig`, `generateToken`,
`pickBindAddress`, `LocalDaemonResult` and the timeout constants — they are
used in Task 6. `index.ts` is not imported by `main.ts` until Task 7, so the
bundle is unaffected; vitest does not fail on unused imports.)

- [ ] **Step 5: Verify** `cd electron && pnpm test -- index` → the `status()` describe passes.

- [ ] **Step 6: Commit**

```bash
git add electron/local-daemon/types.ts electron/local-daemon/index.ts electron/local-daemon/index.test.ts
git commit -m "feat(electron): local daemon status with lsof-based ownership"
```

---

### Task 6: `install`, `start`, `restart`, `ensureRunning`

**Files:**
- Modify: `electron/local-daemon/index.ts`, `electron/local-daemon/index.test.ts`

**Interfaces:** as declared in Task 5's `LocalDaemon`.

- [ ] **Step 1: Failing tests** (append to `index.test.ts`; uses the same `makeFake`/`identity`)

```ts
import { createHash } from 'node:crypto'

function scriptedDownload(f: Fake, hash: string, opts: { status?: number; truncate?: boolean; badSha?: boolean; wrongArch?: boolean; bodyHash?: string; dropHeaders?: boolean } = {}) {
  const bodyHash = opts.bodyHash ?? hash
  const body = Buffer.from(opts.wrongArch ? JSON.stringify({ version: '9', hash: bodyHash, goos: 'darwin', goarch: 'amd64' }) : identity(bodyHash))
  const sha = createHash('sha256').update(body).digest('hex')
  const headers: Record<string, string> = opts.dropHeaders ? {} : {
    'content-length': String(body.length + (opts.truncate ? 5 : 0)),
    'x-pdx-hash': hash, 'x-pdx-version': '9',
    'x-pdx-sha256': opts.badSha ? 'deadbeef' : sha,
  }
  f.downloads.push({ status: opts.status ?? 200, headers, body })
}

describe('install()', () => {
  let f: Fake
  beforeEach(() => { f = makeFake() })

  it('fresh machine: mkdir, download, verify, configure, start, register', async () => {
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    const res = await createLocalDaemon(f.deps).install('http://100.64.0.2:7860', 'tok', (s) => steps.push(s))
    expect(steps).toEqual(['prepare', 'download', 'verify', 'configure', 'swap', 'start', 'register'])
    expect(f.dirs).toContain(`${HOME}/.config/pdx/bin`)
    expect(f.files.get(CFG)).toBe('bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_' + 'cd'.repeat(20) + '"\n\n[dev]\nupdate = false\n')
    expect(f.modes.get(CFG)).toBe(0o600)
    expect(f.modes.get(BIN)).toBe(0o755)
    expect(f.files.has(BIN)).toBe(true)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(res).toEqual({ url: 'http://100.64.0.9:7860', token: 'purdex_' + 'cd'.repeat(20), hash: 'bbb', version: '9', hostname: 'air-2026' })
    const start = f.execLog.find((e) => e.args[0] === 'start')!
    expect(start.file).toBe(BIN)
    expect(start.env?.PDX_DEV_MODE).toBe('1')
    expect(start.env?.PATH).toBe('/opt/homebrew/bin:/usr/bin')
    const dl = f.execLog.findIndex((e) => e.args[0] === 'version' && e.file === `${BIN}.new`)
    expect(dl).toBeGreaterThan(-1)
  })

  it('bindNote when no tailscale interface', async () => {
    f.deps.networkInterfaces = () => []
    scriptedDownload(f, 'bbb')
    const res = await createLocalDaemon(f.deps).install('http://src', 'tok', () => {})
    expect(res.url).toBe('http://127.0.0.1:7860')
    expect(res.bindNote).toMatch(/no tailscale/i)
  })

  it('refuses when external', async () => {
    f.files.set(CFG, 'data_dir = "/elsewhere"\n')
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/external/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
  })

  it('short body vs Content-Length → throws, pdx.new removed, nothing stopped', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    scriptedDownload(f, 'bbb', { truncate: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/length/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('sha256 mismatch → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { badSha: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/sha256/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('verify: wrong arch → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { wrongArch: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/identity mismatch/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('verify: binary hash ≠ X-Pdx-Hash → throws and cleans up', async () => {
    scriptedDownload(f, 'bbb', { bodyHash: 'ccc' })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/identity mismatch/)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('missing integrity headers → throws before writing anything durable', async () => {
    scriptedDownload(f, 'bbb', { dropHeaders: true })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/missing.*header/i)
    expect(f.files.has(`${BIN}.new`)).toBe(false)
  })

  it('ownership changing between status and stop aborts before stop', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\n')
    scriptedDownload(f, 'bbb')
    // After the download, a foreign daemon appears on our endpoint.
    const origFetch = f.deps.fetch
    f.deps.fetch = async (url, init) => { const r = await origFetch(url, init); if (!url.endsWith('/api/health')) { f.lsofListen = `p9${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n` }; return r }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/refusing to stop/)
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(false)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('two concurrent installs run one after the other', async () => {
    scriptedDownload(f, 'bbb'); scriptedDownload(f, 'bbb')
    const d = createLocalDaemon(f.deps)
    const steps: string[] = []
    await Promise.all([
      d.install('http://src', 'tok', (s) => steps.push('1:' + s)),
      d.install('http://src', 'tok', (s) => steps.push('2:' + s)),
    ])
    const firstTwo = steps.findIndex((s) => s.startsWith('2:'))
    expect(steps.slice(0, firstTwo).every((s) => s.startsWith('1:'))).toBe(true)
  })

  it('an install arriving during a withLock-wrapped app update waits for it', async () => {
    scriptedDownload(f, 'bbb')
    const d = createLocalDaemon(f.deps)
    const order: string[] = []
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const upd = d.withLock(async () => { order.push('update-start'); await gate; order.push('update-end') })
    const inst = d.install('http://src', 'tok', (s) => { if (s === 'prepare') order.push('install-start') })
    await new Promise((r) => setTimeout(r, 5))
    expect(order).toEqual(['update-start'])
    release()
    await Promise.all([upd, inst])
    expect(order).toEqual(['update-start', 'update-end', 'install-start'])
  })

  it('non-200 surfaces the daemon error body', async () => {
    f.downloads.push({ status: 500, headers: { 'content-type': 'application/json' }, body: Buffer.from(JSON.stringify({ error: 'build failed', detail: 'boom' })) })
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/build failed.*boom/s)
  })

  it('update: stops our alive daemon, swaps, starts; keeps existing config and token', async () => {
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_old"\n\n[dev]\nupdate = false\n')
    f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.lsofListen = `p4242${NUL}\nf8${NUL}n100.64.0.9:7860${NUL}\n`
    f.health = { ok: true, hash: 'aaa', version: '9' }
    scriptedDownload(f, 'bbb')
    const steps: string[] = []
    const res = await createLocalDaemon(f.deps).install('http://src', 'tok', (s) => steps.push(s))
    expect(steps).toEqual(['prepare', 'download', 'verify', 'stop', 'swap', 'start', 'register'])
    expect(res.token).toBe('purdex_old')
    expect(res.hash).toBe('bbb')
    const order = f.execLog.filter((e) => ['stop', 'start'].includes(e.args[0])).map((e) => e.args[0])
    expect(order).toEqual(['stop', 'start'])
  })

  it('alive but unhealthy daemon is still stopped before swap', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    scriptedDownload(f, 'bbb')
    await createLocalDaemon(f.deps).install('http://src', 'tok', () => {})
    expect(f.execLog.some((e) => e.args[0] === 'stop')).toBe(true)
  })

  it('pdx stop timing out aborts before swap', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => (args[0] === 'stop' ? { code: null, stdout: '', stderr: '', timedOut: true } : undefined)
    scriptedDownload(f, 'bbb')
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/stop/)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
  })

  it('post-start health hash mismatch → throws "served by something else"', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => { if (args[0] === 'start') { f.health = { ok: true, hash: 'zzz' }; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/served by something else/)
  })

  it('post-start health without a hash is a failure, not a pass', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => { if (args[0] === 'start') { f.health = { ok: true }; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/no build hash/)
  })

  it('stop returning while the port stays open → throws, old binary unreplaced', async () => {
    f.files.set(BIN, identity('aaa')); f.files.set(PID, '4242'); f.alivePids.add(4242)
    f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    f.onExec = (_file, args) => { if (args[0] === 'stop') { f.alivePids.clear(); f.portIsOpen = true; return { code: 0, stdout: '', stderr: '', timedOut: false } } return undefined }
    f.portIsOpen = true
    scriptedDownload(f, 'bbb')
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/old binary was not replaced/)
    expect(f.files.get(BIN)).toBe(identity('aaa'))
    expect(f.clock).toBeLessThanOrEqual(5000)
  })

  it('pdx start failure surfaces its stderr', async () => {
    scriptedDownload(f, 'bbb')
    f.onExec = (_file, args) => (args[0] === 'start' ? { code: 1, stdout: '', stderr: 'pdx: bind: address not available', timedOut: false } : undefined)
    await expect(createLocalDaemon(f.deps).install('http://src', 'tok', () => {})).rejects.toThrow(/address not available/)
    expect(f.files.has(BIN)).toBe(true) // swapped; UI offers Start
  })
})

describe('start() / restart() / ensureRunning()', () => {
  let f: Fake
  beforeEach(() => {
    f = makeFake()
    f.files.set(BIN, identity('aaa'))
    f.files.set(CFG, 'bind = "100.64.0.9"\nport = 7860\ntoken = "purdex_t"\n')
  })

  it('start refuses when not managed or already alive', async () => {
    f.files.set(PID, '4242'); f.alivePids.add(4242); f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    await expect(createLocalDaemon(f.deps).start()).rejects.toThrow(/already running/)
  })

  it('start returns the registration payload', async () => {
    const res = await createLocalDaemon(f.deps).start()
    expect(res).toMatchObject({ url: 'http://100.64.0.9:7860', token: 'purdex_t', hash: 'aaa', hostname: 'air-2026' })
  })

  it('restart stops then starts without downloading', async () => {
    f.files.set(PID, '4242'); f.alivePids.add(4242); f.lsofTxt[4242] = `p4242${NUL}\nftxt${NUL}n${BIN}${NUL}\n`
    await createLocalDaemon(f.deps).restart()
    expect(f.execLog.filter((e) => ['stop', 'start'].includes(e.args[0])).map((e) => e.args[0])).toEqual(['stop', 'start'])
    expect(f.downloads).toHaveLength(0)
  })

  it('ensureRunning outcomes', async () => {
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('started')
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('already-running')
    f.files.delete(BIN); f.alivePids.clear(); f.files.delete(PID); f.health = null
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('not-installed')
    f.files.set(CFG, 'data_dir = "/x"\n')
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('external')
  })

  it('ensureRunning retries start 3× then reports failed', async () => {
    let n = 0
    f.onExec = (_file, args) => (args[0] === 'start' ? (n++, { code: 1, stdout: '', stderr: 'no', timedOut: false }) : undefined)
    expect(await createLocalDaemon(f.deps).ensureRunning()).toBe('failed')
    expect(n).toBe(3)
  })

  it('ensureRunning never rejects — a broken config reads as failed', async () => {
    f.files.set(CFG, 'bind = ')
    await expect(createLocalDaemon(f.deps).ensureRunning()).resolves.toBe('failed')
  })

  it('never touches pdx.new', async () => {
    f.files.set(`${BIN}.new`, 'partial')
    await createLocalDaemon(f.deps).ensureRunning()
    expect(f.files.get(`${BIN}.new`)).toBe('partial')
  })
})

describe('withLock', () => {
  it('serialises public calls and callers of withLock', async () => {
    const f = makeFake()
    const order: string[] = []
    const d = createLocalDaemon(f.deps)
    const a = d.withLock(async () => { order.push('a-start'); await new Promise((r) => setTimeout(r, 10)); order.push('a-end') })
    const b = d.status().then(() => order.push('b'))
    await Promise.all([a, b])
    expect(order).toEqual(['a-start', 'a-end', 'b'])
  })
})
```

- [ ] **Step 2: Run to fail** `cd electron && pnpm test -- index` → new tests FAIL (`not implemented`).

- [ ] **Step 3: Implement** — replace the `notYet` block in `index.ts` with:

```ts
  // ---- download + verify -------------------------------------------------
  async function download(daemonUrl: string, token: string | undefined, tgt: LocalDaemonStatus['target']): Promise<{ hash: string; version: string }> {
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), DOWNLOAD_TIMEOUT_MS)
    try {
      const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {}
      const url = `${daemonUrl}/api/dev/daemon/download?goos=${tgt.goos}&goarch=${tgt.goarch}`
      const resp = await deps.fetch(url, { headers, signal: ctl.signal })
      if (!resp.ok) {
        let msg = `download failed: HTTP ${resp.status}`
        try {
          const j = (await resp.json()) as { error?: string; detail?: string }
          if (j.error) msg = `download failed: ${j.error}${j.detail ? `\n${j.detail}` : ''}`
        } catch { /* non-JSON body */ }
        throw new Error(msg)
      }
      const expectLen = Number(resp.headers.get('content-length'))
      const expectSha = resp.headers.get('x-pdx-sha256') ?? ''
      const hash = resp.headers.get('x-pdx-hash') ?? ''
      const version = resp.headers.get('x-pdx-version') ?? 'unknown'
      if (!Number.isFinite(expectLen) || expectLen <= 0 || !/^[0-9a-f]{64}$/.test(expectSha) || hash === '') {
        throw new Error('download failed: missing integrity header (Content-Length, X-Pdx-Sha256, X-Pdx-Hash are required)')
      }
      const out = await deps.fs.openWrite(newPath)
      let written = 0
      try {
        if (!resp.body) throw new Error('download failed: empty body')
        const reader = resp.body.getReader()
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          await out.write(value)
          written += value.byteLength
        }
      } finally {
        await out.close()
      }
      if (written !== expectLen) {
        await deps.fs.unlink(newPath)
        throw new Error(`download failed: content-length ${expectLen}, received ${written}`)
      }
      if ((await deps.fs.sha256(newPath)) !== expectSha) {
        await deps.fs.unlink(newPath)
        throw new Error('download failed: sha256 mismatch')
      }
      await deps.fs.chmod(newPath, 0o755)
      return { hash, version }
    } finally {
      clearTimeout(timer)
    }
  }

  async function verifyNew(tgt: LocalDaemonStatus['target'], expectedHash: string): Promise<void> {
    const r = await deps.exec(newPath, ['version', '--json'], { env: await launchEnv(), timeoutMs: VERSION_TIMEOUT_MS }).catch((e: Error) => ({ code: 1, stdout: '', stderr: e.message, timedOut: false }))
    if (r.code !== 0) {
      await deps.fs.unlink(newPath)
      throw new Error(`downloaded binary does not run: ${r.stderr.trim() || `exit ${r.code}`}`)
    }
    let id: { goos?: string; goarch?: string; hash?: string } = {}
    try { id = JSON.parse(r.stdout.trim()) } catch { /* handled below */ }
    if (id.goos !== tgt.goos || id.goarch !== tgt.goarch || id.hash !== expectedHash) {
      await deps.fs.unlink(newPath)
      throw new Error(`identity mismatch: got ${id.goos}/${id.goarch} ${id.hash}, want ${tgt.goos}/${tgt.goarch} ${expectedHash}`)
    }
  }

  // ---- stop / start ------------------------------------------------------
  // After `pdx stop` returns, wait (≤ 5 s) until the pid is gone AND the
  // port refuses TCP connections. A plain health probe cannot tell
  // "refused" from "500/timeout", hence the dedicated portOpen dep.
  async function stopUnlocked(cfg: DaemonConfig, pid: number): Promise<void> {
    const r = await deps.exec(binPath, ['stop'], { env: await launchEnv(), cwd: deps.home, timeoutMs: STOP_TIMEOUT_MS })
    if (r.timedOut) throw new Error('pdx stop did not finish within 35s — the old binary was not replaced (the old process may or may not still be running)')
    const deadline = deps.now() + STOP_SETTLE_MS
    for (;;) {
      if (!deps.kill0(pid) && !(await deps.portOpen(cfg.bind, cfg.port))) return
      if (deps.now() >= deadline) break
      await deps.sleep(500)
    }
    throw new Error('pdx stop returned but the daemon is still alive or the port is still open — the old binary was not replaced')
  }

  async function startDaemon(cfg: DaemonConfig): Promise<void> {
    const r = await deps.exec(binPath, ['start'], { env: await launchEnv(), cwd: deps.home, timeoutMs: START_TIMEOUT_MS })
    if (r.timedOut) throw new Error('pdx start did not finish within 70s')
    if (r.code !== 0) throw new Error(`pdx start failed: ${(r.stderr || r.stdout).trim()}`)
    const onDisk = await readIdentity(binPath)
    const h = await health(cfg.bind, cfg.port)
    if (!h) throw new Error('pdx start returned but /api/health is not answering')
    if (!onDisk || onDisk.hash === 'unknown') throw new Error('installed binary reports no build hash; refusing to trust the start')
    if (h.hash === 'unknown') throw new Error(`port ${cfg.port} answered health with no build hash — not the binary we started`)
    if (h.hash !== onDisk.hash) {
      throw new Error(`port ${cfg.port} is served by something else (health hash ${h.hash}, binary ${onDisk.hash})`)
    }
  }

  async function register(bindNote?: string): Promise<LocalDaemonResult> {
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    const id = await readIdentity(binPath)
    return {
      url: `http://${cfg.bind}:${cfg.port}`, token: cfg.token ?? '', hash: id?.hash ?? 'unknown', version: id?.version ?? 'unknown',
      hostname: deps.hostname(), ...(bindNote ? { bindNote } : {}),
    }
  }

  // ---- public operations -------------------------------------------------
  async function installUnlocked(daemonUrl: string, token: string | undefined, progress: (s: string) => void): Promise<LocalDaemonResult> {
    const st = await statusUnlocked()
    if (st.managed === 'external') throw new Error(`refusing to install: external daemon (${st.reason})`)
    const tgt = st.target
    progress('prepare')
    await deps.fs.mkdir(binDir)
    progress('download')
    const { hash } = await download(daemonUrl, token, tgt)
    progress('verify')
    await verifyNew(tgt, hash)
    let bindNote: string | undefined
    if (!(await deps.fs.exists(cfgPath))) {
      progress('configure')
      const pick = pickBindAddress(deps.networkInterfaces(), deps.platform)
      bindNote = pick.note
      const tmp = cfgPath + '.tmp'
      await deps.fs.writeFile(tmp, renderInitialConfig(pick.bind, generateToken(deps.randomBytes)), 0o600)
      await deps.fs.rename(tmp, cfgPath)
    }
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    // Re-resolve ownership immediately before the destructive step.
    const own = await resolveOwner(cfg, await deps.fs.exists(binPath))
    if (own.managed === 'external') throw new Error(`refusing to stop: external daemon (${own.reason})`)
    if (own.alive) {
      progress('stop')
      await stopUnlocked(cfg, own.alive.pid)
    }
    progress('swap')
    await deps.fs.rename(newPath, binPath)
    progress('start')
    await startDaemon(cfg)
    progress('register')
    return register(bindNote)
  }

  async function startUnlocked(): Promise<LocalDaemonResult> {
    const st = await statusUnlocked()
    if (st.managed !== 'managed') throw new Error(`cannot start: ${st.managed}${st.reason ? ` (${st.reason})` : ''}`)
    if (st.alive) throw new Error(`already running (pid ${st.alive.pid})`)
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    await startDaemon(cfg)
    return register()
  }

  async function restartUnlocked(): Promise<LocalDaemonResult> {
    const st = await statusUnlocked()
    if (st.managed !== 'managed') throw new Error(`cannot restart: ${st.managed}${st.reason ? ` (${st.reason})` : ''}`)
    const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
    const own = await resolveOwner(cfg, true)
    if (own.managed === 'external') throw new Error(`refusing to stop: external daemon (${own.reason})`)
    if (own.alive) await stopUnlocked(cfg, own.alive.pid)
    await startDaemon(cfg)
    return register()
  }

  // Never rejects (spec §3.1): every failure, including a broken config,
  // is logged and reported as 'failed'.
  async function ensureRunningUnlocked(): Promise<'started' | 'already-running' | 'not-installed' | 'external' | 'failed'> {
    try {
      const st = await statusUnlocked()
      if (st.managed === 'none') return 'not-installed'
      if (st.managed === 'external') return 'external'
      if (st.alive) return 'already-running'
      const cfg = (await readConfig()) ?? parseDaemonConfig('', deps.home)
      for (let attempt = 1; attempt <= 3; attempt++) {
        try {
          await startDaemon(cfg)
          return 'started'
        } catch (e) {
          deps.log(`[local-daemon] start attempt ${attempt} failed: ${e instanceof Error ? e.message : String(e)}`)
          if (attempt < 3) await deps.sleep(5000)
        }
      }
      return 'failed'
    } catch (e) {
      deps.log(`[local-daemon] ensureRunning: ${e instanceof Error ? e.message : String(e)}`)
      return 'failed'
    }
  }

  return {
    status: () => withLock(statusUnlocked),
    install: (u, t, p) => withLock(() => installUnlocked(u, t, p)),
    start: () => withLock(startUnlocked),
    restart: () => withLock(restartUnlocked),
    ensureRunning: () => withLock(ensureRunningUnlocked),
    withLock,
  }
```

- [ ] **Step 4: Verify** `cd electron && pnpm test` → all PASS.

- [ ] **Step 5: Commit**

```bash
git add electron/local-daemon/index.ts electron/local-daemon/index.test.ts
git commit -m "feat(electron): install, start, restart and ensureRunning for the local daemon"
```

---

### Task 7: Node deps, IPC, preload, `ensureRunning` on ready

**Files:**
- Create: `electron/local-daemon/node-deps.ts`
- Modify: `electron/main.ts`, `electron/preload.ts`, `spa/src/types/electron.d.ts`
- Modify: `electron/devmode.test.ts` (extend with static IPC assertions)

- [ ] **Step 1: Failing static test** (append to `electron/devmode.test.ts`)

```ts
describe('local daemon wiring', () => {
  it('main registers the four IPC handlers inside the dev gate and calls ensureRunning on ready', () => {
    const main = src('main.ts')
    for (const ch of ['dev:local-daemon-status', 'dev:local-daemon-install', 'dev:local-daemon-start', 'dev:local-daemon-restart']) {
      expect(main).toContain(`ipcMain.handle('${ch}'`)
    }
    expect(main).toContain('localDaemon.ensureRunning()')
    // applyUpdate must run inside the lock — the exact wrapping form.
    expect(main).toContain('await localDaemon.withLock(() => applyUpdate(')
    // Handlers and ensureRunning sit inside the dev gate.
    const gate = main.indexOf("if (process.env.PDX_DEV_MODE !== '0') {")
    expect(gate).toBeGreaterThan(-1)
    for (const ch of ['dev:local-daemon-status', 'dev:local-daemon-install', 'dev:local-daemon-start', 'dev:local-daemon-restart']) {
      expect(main.indexOf(`ipcMain.handle('${ch}'`)).toBeGreaterThan(gate)
    }
    const ready = main.indexOf('localDaemon.ensureRunning()')
    expect(main.lastIndexOf("process.env.PDX_DEV_MODE !== '0'", ready)).toBeGreaterThan(-1)
  })
  it('preload exposes the bridges', () => {
    const preload = src('preload.ts')
    for (const name of ['localDaemonStatus', 'localDaemonInstall', 'localDaemonStart', 'localDaemonRestart', 'onLocalDaemonProgress']) {
      expect(preload).toContain(name)
    }
  })
})
```

- [ ] **Step 2: Run to fail** `cd electron && pnpm test -- devmode` → FAIL.

- [ ] **Step 3: `node-deps.ts`**

```ts
// electron/local-daemon/node-deps.ts
// The real-world LocalDaemonDeps. Kept apart from index.ts so the logic
// never imports node:fs / child_process directly and stays unit-testable.
import { execFile } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import { createWriteStream } from 'node:fs'
import { access, chmod, mkdir, readFile, realpath, rename, unlink, writeFile } from 'node:fs/promises'
import { connect } from 'node:net'
import { homedir, hostname, networkInterfaces } from 'node:os'
import type { ExecFn } from './launch-env'
import type { LocalDaemonDeps, WriteHandle } from './types'

const exec: ExecFn = (file, args, opts) =>
  new Promise((resolve) => {
    const child = execFile(
      file,
      args,
      { env: opts.env, cwd: opts.cwd, timeout: opts.timeoutMs, maxBuffer: 8 * 1024 * 1024, killSignal: 'SIGKILL', encoding: 'utf8' },
      (err, stdout, stderr) => {
        const timedOut = !!(err && (err as NodeJS.ErrnoException & { killed?: boolean }).killed && child.signalCode === 'SIGKILL')
        const code = err ? ((err as NodeJS.ErrnoException & { code?: number | string }).code as number | null) ?? null : 0
        resolve({ code: typeof code === 'number' ? code : err ? null : 0, stdout: String(stdout), stderr: String(stderr), timedOut })
      },
    )
    // stdin closed: an interactive shell probe must never wait on a tty.
    child.stdin?.end()
  })

async function exists(p: string): Promise<boolean> {
  try { await access(p); return true } catch { return false }
}

async function openWrite(p: string): Promise<WriteHandle> {
  const ws = createWriteStream(p, { mode: 0o755 })
  await new Promise<void>((res, rej) => { ws.once('open', () => res()); ws.once('error', rej) })
  return {
    write: (chunk) => new Promise((res, rej) => { ws.write(chunk, (e) => (e ? rej(e) : res())) }),
    close: () => new Promise((res, rej) => { ws.once('error', rej); ws.end(() => res()) }),
  }
}

async function sha256(p: string): Promise<string> {
  const { createReadStream } = await import('node:fs')
  return new Promise((res, rej) => {
    const h = createHash('sha256')
    createReadStream(p).on('data', (d) => h.update(d)).on('end', () => res(h.digest('hex'))).on('error', rej)
  })
}

export function nodeDeps(log: (msg: string) => void = console.log): LocalDaemonDeps {
  return {
    home: homedir(),
    hostname: () => hostname(),
    platform: process.platform,
    arch: process.arch,
    shell: process.env.SHELL,
    baseEnv: process.env,
    exec,
    fetch: (url, init) => fetch(url, init),
    fs: {
      exists,
      readFile: (p) => readFile(p, 'utf8'),
      writeFile: (p, d, mode) => writeFile(p, d, { mode }),
      rename, unlink, chmod, realpath,
      mkdir: async (p) => { await mkdir(p, { recursive: true }) },
      openWrite,
      sha256,
    },
    kill0: (pid) => { try { process.kill(pid, 0); return true } catch { return false } },
    portOpen: (host, port) => new Promise((res) => {
      const sock = connect({ host, port })
      const done = (v: boolean) => { sock.destroy(); res(v) }
      sock.setTimeout(500, () => done(false))
      sock.once('connect', () => done(true))
      sock.once('error', () => done(false))
    }),
    networkInterfaces: () => Object.entries(networkInterfaces()).flatMap(([name, list]) => (list ?? []).map((i) => ({ name, address: i.address, family: String(i.family), internal: i.internal }))),
    randomBytes: (n) => randomBytes(n),
    sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
    now: () => Date.now(),
    log,
  }
}
```

Note on `exec` exit codes: Node's `execFile` callback error has `code`
(number exit status, or a string like `'ENOENT'`) and `killed` when the
timeout fired. The mapping above yields `{code: null, timedOut: true}` on
timeout, `{code: <n>}` on non-zero exit and `{code: null, timedOut: false}`
for spawn errors (ENOENT) — callers treat non-zero as failure either way.

- [ ] **Step 4: main.ts**

Imports: `import { createLocalDaemon } from './local-daemon/index'` and
`import { nodeDeps } from './local-daemon/node-deps'`. After the
`windowManager`/`browserViewManager` construction add:

```ts
const localDaemon = createLocalDaemon(nodeDeps((m) => console.log(m)))
```

Inside the `if (process.env.PDX_DEV_MODE !== '0') {` block, replace the
`dev:apply-update` handler with:

```ts
    ipcMain.handle('dev:apply-update', async (event, daemonUrl: string, token?: string) => {
      if (updateInProgress) throw 'Update already in progress'
      updateInProgress = true
      const win = BrowserWindow.fromWebContents(event.sender)
      try {
        // Whole update under the local-daemon lock so it cannot app.exit(0)
        // in the middle of a daemon stop → swap → start (spec §3.1).
        return await localDaemon.withLock(() => applyUpdate(daemonUrl, (step) => {
          if (win && !win.isDestroyed()) {
            win.webContents.send('dev:update-progress', step)
          }
        }, token))
      } catch (err) {
        updateInProgress = false
        throw String(err instanceof Error ? err.message : err)
      }
    })

    // Local daemon (spec 2026-09-14 §3.2)
    const asString = (err: unknown) => String(err instanceof Error ? err.message : err)
    ipcMain.handle('dev:local-daemon-status', async () => {
      try { return await localDaemon.status() } catch (err) { throw asString(err) }
    })
    ipcMain.handle('dev:local-daemon-install', async (event, daemonUrl: string, token?: string) => {
      const win = BrowserWindow.fromWebContents(event.sender)
      try {
        return await localDaemon.install(daemonUrl, token, (step) => {
          if (win && !win.isDestroyed()) win.webContents.send('dev:local-daemon-progress', step)
        })
      } catch (err) { throw asString(err) }
    })
    ipcMain.handle('dev:local-daemon-start', async () => {
      try { return await localDaemon.start() } catch (err) { throw asString(err) }
    })
    ipcMain.handle('dev:local-daemon-restart', async () => {
      try { return await localDaemon.restart() } catch (err) { throw asString(err) }
    })
```

In `app.whenReady().then(() => { … })`, right after `registerIpcHandlers()`:

```ts
    // Spec D5: the app is the launcher on machines without booter/launchd.
    if (process.env.PDX_DEV_MODE !== '0') {
      void localDaemon.ensureRunning().then((r) => console.log(`[local-daemon] ensureRunning: ${r}`))
    }
```

- [ ] **Step 5: preload.ts** — inside the dev spread, after `streamCheck`:

```ts
    localDaemonStatus: () => ipcRenderer.invoke('dev:local-daemon-status'),
    localDaemonInstall: (daemonUrl: string, token?: string) => ipcRenderer.invoke('dev:local-daemon-install', daemonUrl, token),
    localDaemonStart: () => ipcRenderer.invoke('dev:local-daemon-start'),
    localDaemonRestart: () => ipcRenderer.invoke('dev:local-daemon-restart'),
    onLocalDaemonProgress: (callback: (step: string) => void) => {
      const handler = (_event: Electron.IpcRendererEvent, step: string) => callback(step)
      ipcRenderer.on('dev:local-daemon-progress', handler)
      return () => ipcRenderer.removeListener('dev:local-daemon-progress', handler)
    },
```

- [ ] **Step 6: `spa/src/types/electron.d.ts`** — add before `interface Window`:

```ts
interface ElectronLocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; hasToken: boolean } | null
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }
}

interface ElectronLocalDaemonResult {
  url: string
  token: string
  hash: string
  version: string
  hostname: string
  bindNote?: string
}
```

and inside `electronAPI` after `streamCheck`:

```ts
    // Local daemon (Electron only; absent in the web build)
    localDaemonStatus?: () => Promise<ElectronLocalDaemonStatus>
    localDaemonInstall?: (daemonUrl: string, token?: string) => Promise<ElectronLocalDaemonResult>
    localDaemonStart?: () => Promise<ElectronLocalDaemonResult>
    localDaemonRestart?: () => Promise<ElectronLocalDaemonResult>
    onLocalDaemonProgress?: (callback: (step: string) => void) => () => void
```

- [ ] **Step 7: Verify** `cd electron && pnpm test` → PASS; `pnpm exec electron-vite build` (repo root) → compiles **and** `rg -n "smol-toml" out/main/index.js` prints nothing (the parser is inlined, not required at runtime); `cd spa && pnpm run lint` clean.

- [ ] **Step 8: Commit**

```bash
git add electron/local-daemon/node-deps.ts electron/main.ts electron/preload.ts electron/devmode.test.ts spa/src/types/electron.d.ts
git commit -m "feat(electron): local daemon IPC, preload bridge and ensureRunning on launch"
```

---

### Task 8: `registerLocalHost` store action

**Files:**
- Modify: `spa/src/stores/useHostStore.ts` (interface + implementation)
- Create: `spa/src/stores/useHostStore.registerLocalHost.test.ts` (store tests live flat in `spa/src/stores/`, e.g. `useAgentStore.test.ts`; import with `./useHostStore`)

**Interfaces (produced):**

```ts
registerLocalHost: (result: { url: string; token: string; hostname: string }) => string  // returns the host id
```

Semantics (spec §3.4): parse `ip`/`port` from `url`; if a host with the
same `ip:port` exists → `updateHost(id, { token })` **only when** that host's
token is empty/`null`/`undefined`, return its id; otherwise
`addHost({ name: hostname, ip, port, token })`.

- [ ] **Step 1: Failing test**

```ts
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from './useHostStore'

// reset() keeps the default 'mlab' host (100.64.0.2), so assertions filter
// by the endpoint under test instead of counting all hosts.
const at = (ip: string, port: number) => Object.values(useHostStore.getState().hosts).filter((h) => h.ip === ip && h.port === port)

describe('registerLocalHost', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('adds a host named after the machine', () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_a', hostname: 'air-2026' })
    const h = useHostStore.getState().hosts[id]
    expect(h).toMatchObject({ name: 'air-2026', ip: '100.64.0.9', port: 7860, token: 'purdex_a' })
    expect(useHostStore.getState().hostOrder).toContain(id)
    expect(at('100.64.0.9', 7860)).toHaveLength(1)
  })

  it('is idempotent on the same ip:port and fills only an empty token', () => {
    const s = useHostStore.getState()
    const existing = s.addHost({ name: 'x', ip: '100.64.0.9', port: 7860, token: null })
    const id = s.registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_b', hostname: 'air-2026' })
    expect(id).toBe(existing)
    expect(useHostStore.getState().hosts[existing].token).toBe('purdex_b')
    expect(useHostStore.getState().hosts[existing].name).toBe('x')
    expect(at('100.64.0.9', 7860)).toHaveLength(1)
  })

  it('an explicit :80 is normalised away by URL and must still register as 80', () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:80', token: 'purdex_c', hostname: 'air-2026' })
    expect(useHostStore.getState().hosts[id].port).toBe(80)
  })

  it('never overwrites a live token', () => {
    const s = useHostStore.getState()
    const existing = s.addHost({ name: 'x', ip: '100.64.0.9', port: 7860, token: 'purdex_live' })
    s.registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'purdex_new', hostname: 'air-2026' })
    expect(useHostStore.getState().hosts[existing].token).toBe('purdex_live')
  })
})
```

`reset()` exists (`useHostStore.ts`, restores `createDefaultState()` with the
default `mlab` host); do not change its semantics.

- [ ] **Step 2: Run to fail** `cd spa && npx vitest run src/stores` → FAIL.

- [ ] **Step 3: Implement** — in `HostState` add
`registerLocalHost: (result: { url: string; token: string; hostname: string }) => string`
and after `updateHost`:

```ts
      // Idempotent registration used by the local-daemon installer
      // (spec 2026-09-14 §3.4): one host per endpoint, and a token is only
      // filled in when the existing one is empty — never overwritten.
      registerLocalHost: ({ url, token, hostname }) => {
        const u = new URL(url)
        const ip = u.hostname
        // URL drops a default port (":80" / ":443") — restore it by scheme.
        const port = u.port ? Number(u.port) : (u.protocol === 'https:' ? 443 : 80)
        const existing = Object.values(get().hosts).find((h) => h.ip === ip && h.port === port)
        if (existing) {
          if (!existing.token) get().updateHost(existing.id, { token })
          return existing.id
        }
        return get().addHost({ name: hostname, ip, port, token })
      },
```

- [ ] **Step 4: Verify** `cd spa && npx vitest run src/stores && pnpm run lint` → PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.registerLocalHost.test.ts
git commit -m "feat(spa): idempotent registerLocalHost store action"
```

---

### Task 9: `LocalDaemonSection` UI + i18n + mount

**Files:**
- Create: `spa/src/components/settings/LocalDaemonSection.tsx`, `spa/src/components/settings/LocalDaemonSection.test.tsx`
- Modify: `spa/src/components/settings/DevEnvironmentSection.tsx` (render after the Daemon block), `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `window.electronAPI.localDaemon*` (Task 7), `useHostStore.registerLocalHost` (Task 8).
- Props: `{ daemonBase: string; token?: string; latestHash: string | null; refreshKey: unknown }` —
  `refreshKey` is the parent's `daemonCheck` object; a new reference (even
  with the same hash) re-runs `status()` (spec §3.4 "when the parent's
  `daemonCheck` changes").

Order: Step 1 (failing tests) → Step 2 (run) → Step 3 (i18n keys) → Step 4
(component) → Step 5 (mount) — the locale strings are part of GREEN.

- [ ] **Step 3 (do after Step 2): i18n keys** — add to `en.json` (and the zh-TW equivalents):

```json
"settings.dev.local.heading": "Local daemon",
"settings.dev.local.none": "No daemon installed on this machine",
"settings.dev.local.target": "Target",
"settings.dev.local.installed": "Installed",
"settings.dev.local.running": "Running",
"settings.dev.local.stopped": "Stopped",
"settings.dev.local.alive_unhealthy": "Daemon process {{pid}} is alive but not answering",
"settings.dev.local.external": "A daemon is running at {{url}} but is not managed by this app",
"settings.dev.local.external_reason": "Reason: {{reason}}",
"settings.dev.local.restart_pending": "On-disk {{hash}} is not running yet",
"settings.dev.local.up_to_date": "Up to date",
"settings.dev.local.update_available": "Update available",
"settings.dev.local.tmux_missing": "tmux not found on the daemon's PATH — install it with Homebrew",
"settings.dev.local.btn.install": "Install",
"settings.dev.local.btn.update": "Update",
"settings.dev.local.btn.start": "Start",
"settings.dev.local.btn.restart": "Restart",
"settings.dev.local.btn.refresh": "Refresh",
"settings.dev.local.step.prepare": "Preparing…",
"settings.dev.local.step.download": "Downloading binary…",
"settings.dev.local.step.verify": "Verifying…",
"settings.dev.local.step.configure": "Writing config…",
"settings.dev.local.step.stop": "Stopping daemon…",
"settings.dev.local.step.swap": "Installing…",
"settings.dev.local.step.start": "Starting daemon…",
"settings.dev.local.step.register": "Registering host…",
"settings.dev.local.registered": "Host registered: {{name}}"
```

zh-TW: 「本機 Daemon」「此機器尚未安裝 daemon」「目標」「已安裝」「執行中」「已停止」「Daemon 行程 {{pid}} 存活但沒有回應」「{{url}} 有 daemon 在執行，但不是由本 App 管理」「原因：{{reason}}」「磁碟上的 {{hash}} 尚未執行」「已是最新」「有可用更新」「daemon 的 PATH 找不到 tmux — 請用 Homebrew 安裝」「安裝」「更新」「啟動」「重新啟動」「重新整理」「準備中…」「下載 binary…」「驗證中…」「寫入設定…」「停止 daemon…」「安裝中…」「啟動 daemon…」「登錄主機…」「已登錄主機：{{name}}」.

`makeT` interpolates `{{name}}` from `t(key, { name })`
(`useI18nStore.ts:49`); never use `.replace()` on translated strings.

- [ ] **Step 1: Failing tests**

```tsx
// spa/src/components/settings/LocalDaemonSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { LocalDaemonSection } from './LocalDaemonSection'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

const status = (o: Partial<ElectronLocalDaemonStatus> = {}): ElectronLocalDaemonStatus => ({
  managed: 'none', binPath: '/Users/t/.config/pdx/bin/pdx', installed: null, alive: null, running: null, config: null,
  target: { goos: 'darwin', goarch: 'arm64' }, tools: { tmux: '/opt/homebrew/bin/tmux' }, ...o,
})
const result: ElectronLocalDaemonResult = { url: 'http://100.64.0.9:7860', token: 'purdex_t', hash: 'bbb', version: '9', hostname: 'air-2026' }

const mockStatus = vi.fn()
const mockInstall = vi.fn()
const mockStart = vi.fn()
const mockRestart = vi.fn()
let progressCb: ((s: string) => void) | null = null

beforeEach(() => {
  vi.clearAllMocks()
  useI18nStore.getState().setLocale('en')
  useHostStore.getState().reset()
  window.electronAPI = {
    ...window.electronAPI!,
    localDaemonStatus: mockStatus,
    localDaemonInstall: mockInstall,
    localDaemonStart: mockStart,
    localDaemonRestart: mockRestart,
    onLocalDaemonProgress: (cb: (s: string) => void) => { progressCb = cb; return () => { progressCb = null } },
  } as typeof window.electronAPI
})

const renderIt = (latestHash: string | null = 'bbb', refreshKey: unknown = { latest_hash: latestHash }) =>
  act(async () => { render(<LocalDaemonSection daemonBase="http://100.64.0.2:7860" token="tok" latestHash={latestHash} refreshKey={refreshKey} />) })

describe('LocalDaemonSection', () => {
  it('none → Install button and target', async () => {
    mockStatus.mockResolvedValue(status())
    await renderIt()
    expect(screen.getByText('No daemon installed on this machine')).toBeTruthy()
    expect(screen.getByText('darwin/arm64')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Install' })).toBeTruthy()
  })

  it('managed+stopped → Start, and Update when hash differs', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt('bbb')
    expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Update' })).toBeTruthy()
  })

  it('managed+running, same hash → Up to date, no Update', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'bbb', url: 'http://100.64.0.9:7860' } }))
    await renderIt('bbb')
    expect(screen.getByText('Up to date')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
  })

  it('managed+running with on-disk ≠ running → Restart', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'aaa', url: 'http://100.64.0.9:7860' } }))
    await renderIt('bbb')
    expect(screen.getByRole('button', { name: 'Restart' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
  })

  it('Update wins over Restart when both would apply', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'aaa', url: 'http://100.64.0.9:7860' } }))
    await renderIt('ccc')
    expect(screen.getByRole('button', { name: 'Update' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Restart' })).toBeNull()
  })

  it('re-queries status when refreshKey changes even with the same hash', async () => {
    mockStatus.mockResolvedValue(status())
    const view = render(<LocalDaemonSection daemonBase="x" latestHash="bbb" refreshKey={{ latest_hash: 'bbb' }} />)
    await waitFor(() => expect(mockStatus).toHaveBeenCalledTimes(1))
    await act(async () => { view.rerender(<LocalDaemonSection daemonBase="x" latestHash="bbb" refreshKey={{ latest_hash: 'bbb' }} />) })
    await waitFor(() => expect(mockStatus).toHaveBeenCalledTimes(2))
  })

  it('alive but unhealthy → Restart with pid', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 4242 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt()
    expect(screen.getByText(/4242/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Restart' })).toBeTruthy()
  })

  it('external → full URL in the message, reason, no buttons', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'running daemon is /repo/bin/pdx', running: { version: 'unknown', hash: 'unknown', url: 'http://100.64.0.2:7860' } }))
    await renderIt()
    expect(screen.getByText('A daemon is running at http://100.64.0.2:7860 but is not managed by this app')).toBeTruthy()
    expect(screen.getByText(/\/repo\/bin\/pdx/)).toBeTruthy()
    for (const n of ['Install', 'Update', 'Start', 'Restart']) expect(screen.queryByRole('button', { name: n })).toBeNull()
  })

  it('external without running info falls back to the config endpoint', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'custom data_dir', config: { bind: '100.64.0.9', port: 7860, hasToken: true } }))
    await renderIt()
    expect(screen.getByText('A daemon is running at http://100.64.0.9:7860 but is not managed by this app')).toBeTruthy()
  })

  it('tmux missing → warning', async () => {
    mockStatus.mockResolvedValue(status({ tools: { tmux: null } }))
    await renderIt()
    expect(screen.getByText(/tmux not found/)).toBeTruthy()
  })

  it('install shows progress, registers the host once, refreshes status', async () => {
    mockStatus.mockResolvedValue(status())
    mockInstall.mockImplementation(async () => { progressCb?.('download'); return result })
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith('http://100.64.0.2:7860', 'tok'))
    const hosts = Object.values(useHostStore.getState().hosts).filter((h) => h.ip === '100.64.0.9')
    expect(hosts).toHaveLength(1)
    expect(hosts[0].token).toBe('purdex_t')
    expect(mockStatus.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('start also registers the host', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    mockStart.mockResolvedValue(result)
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Start' })) })
    await waitFor(() => expect(Object.values(useHostStore.getState().hosts).some((h) => h.ip === '100.64.0.9')).toBe(true))
  })

  it('install error renders inline and re-enables', async () => {
    mockStatus.mockResolvedValue(status())
    mockInstall.mockRejectedValue('download failed: sha256 mismatch')
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    expect(await screen.findByText(/sha256 mismatch/)).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Install' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('a post-swap start failure leaves the error and offers Start on the refreshed status', async () => {
    mockStatus
      .mockResolvedValueOnce(status())
      .mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    mockInstall.mockRejectedValue('pdx start failed: bind: address not available')
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    expect(await screen.findByText(/address not available/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy()
  })

  it('renders nothing when the bridge is absent (web build)', async () => {
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: undefined } as typeof window.electronAPI
    const { container } = render(<LocalDaemonSection daemonBase="x" latestHash={null} />)
    expect(container.innerHTML).toBe('')
  })
})
```

- [ ] **Step 2: Run to fail** `cd spa && npx vitest run LocalDaemonSection` → FAIL.

- [ ] **Step 4: Implement the component** (after Step 3's i18n keys)

```tsx
// spa/src/components/settings/LocalDaemonSection.tsx
import { useCallback, useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

interface Props {
  daemonBase: string
  token?: string
  latestHash: string | null
  /** The parent's latest daemonCheck object; a new reference re-queries status. */
  refreshKey: unknown
}

type Busy = null | 'install' | 'start' | 'restart'

const btnSecondary = 'px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default'
const btnPrimary = 'px-3 py-1.5 text-xs rounded-md bg-accent text-text-inverse hover:bg-accent-hover disabled:opacity-50 cursor-pointer disabled:cursor-default'

// Settings → Development → "Local daemon": install / update / start /
// restart the daemon on the machine the app runs on (spec 2026-09-14 §3.4).
export function LocalDaemonSection({ daemonBase, token, latestHash, refreshKey }: Props) {
  const t = useI18nStore((s) => s.t)
  const registerLocalHost = useHostStore((s) => s.registerLocalHost)
  const api = window.electronAPI
  const [status, setStatus] = useState<ElectronLocalDaemonStatus | null>(null)
  const [busy, setBusy] = useState<Busy>(null)
  const [step, setStep] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    if (!api?.localDaemonStatus) return
    try {
      setStatus(await api.localDaemonStatus())
    } catch (err) {
      setError(String(err))
    }
  }, [api])

  useEffect(() => { void refresh() }, [refresh, refreshKey])
  useEffect(() => api?.onLocalDaemonProgress?.((s) => setStep(s)), [api])

  const run = useCallback(async (kind: Exclude<Busy, null>, op: () => Promise<ElectronLocalDaemonResult> | undefined) => {
    setBusy(kind); setError(null); setNotice(null); setStep(null)
    try {
      const res = await op()
      if (res) {
        registerLocalHost({ url: res.url, token: res.token, hostname: res.hostname })
        setNotice([t('settings.dev.local.registered', { name: res.hostname }), res.bindNote].filter(Boolean).join(' — '))
      }
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(null); setStep(null)
      void refresh()
    }
  }, [refresh, registerLocalHost, t])

  if (!api?.localDaemonStatus) return null

  const installed = status?.installed ?? null
  const running = status?.running ?? null
  const alive = status?.alive ?? null
  const updateAvailable = !!installed && !!latestHash && installed.hash !== latestHash
  const restartPending = !!installed && !!running && running.hash !== installed.hash
  // Spec §3.4: Update, else Restart — never both.
  const showRestart = !!alive && !updateAvailable && (!running || restartPending)
  const disabled = busy !== null
  const externalUrl = running?.url ?? (status?.config ? `http://${status.config.bind}:${status.config.port}` : '')

  return (
    <div className="pt-6 border-t border-border-default">
      <h3 className="text-sm font-semibold text-text-primary mb-3">{t('settings.dev.local.heading')}</h3>

      {status && (
        <div className="space-y-1 mb-3 text-xs text-text-secondary">
          <div className="flex items-center justify-between">
            <span>{t('settings.dev.local.target')}</span>
            <span className="font-mono text-text-primary">{status.target.goos}/{status.target.goarch}</span>
          </div>
          {status.managed === 'none' && <div>{t('settings.dev.local.none')}</div>}
          {status.managed === 'external' && (
            <div className="text-status-warning">
              {t('settings.dev.local.external', { url: externalUrl })}
              {status.reason && <div>{t('settings.dev.local.external_reason', { reason: status.reason })}</div>}
            </div>
          )}
          {status.managed === 'managed' && installed && (
            <>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.installed')}</span>
                <span className="font-mono text-text-primary">{installed.version} ({installed.hash})</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{running ? t('settings.dev.local.running') : t('settings.dev.local.stopped')}</span>
                <span className="font-mono">{running ? `${running.version} (${running.hash}) ${running.url}` : '-'}</span>
              </div>
              {alive && !running && <div className="text-status-warning">{t('settings.dev.local.alive_unhealthy', { pid: alive.pid })}</div>}
              {restartPending && <div className="text-status-warning">{t('settings.dev.local.restart_pending', { hash: installed.hash })}</div>}
              {updateAvailable
                ? <div className="text-status-warning">{t('settings.dev.local.update_available')}</div>
                : (running && !restartPending && <div>{t('settings.dev.local.up_to_date')}</div>)}
            </>
          )}
          {status.tools.tmux === null && <div className="text-status-warning">{t('settings.dev.local.tmux_missing')}</div>}
        </div>
      )}

      {error && <div className="text-xs text-status-error mb-3 whitespace-pre-wrap">{error}</div>}
      {notice && <div className="text-xs text-text-secondary mb-3">{notice}</div>}
      {busy && <div className="text-xs text-accent font-mono mb-3">{step ? t(`settings.dev.local.step.${step}`) : '…'}</div>}

      <div className="flex gap-2">
        <button onClick={() => void refresh()} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.refresh')}</button>
        {status?.managed === 'none' && (
          <button onClick={() => void run('install', () => api.localDaemonInstall?.(daemonBase, token))} disabled={disabled} className={btnPrimary}>{t('settings.dev.local.btn.install')}</button>
        )}
        {status?.managed === 'managed' && (
          <>
            {!alive && (
              <button onClick={() => void run('start', () => api.localDaemonStart?.())} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.start')}</button>
            )}
            {showRestart && (
              <button onClick={() => void run('restart', () => api.localDaemonRestart?.())} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.restart')}</button>
            )}
            {updateAvailable && (
              <button onClick={() => void run('install', () => api.localDaemonInstall?.(daemonBase, token))} disabled={disabled} className={btnPrimary}>{t('settings.dev.local.btn.update')}</button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Mount it** — in `DevEnvironmentSection.tsx`, import
`{ LocalDaemonSection } from './LocalDaemonSection'` and, right after the
closing `</div>` of the Daemon block (before the component's final
`</div>`), add:

```tsx
      <LocalDaemonSection daemonBase={daemonBase} token={token} latestHash={daemonCheck?.latest_hash ?? null} refreshKey={daemonCheck} />
```

- [ ] **Step 6: Verify**

`cd spa && npx vitest run` → all PASS (including the existing
`DevEnvironmentSection.test.tsx` — its `window.electronAPI` spread has no
`localDaemonStatus`, so the new block renders nothing there).
`cd spa && pnpm run lint && pnpm run build` → clean.

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/settings/LocalDaemonSection.tsx spa/src/components/settings/LocalDaemonSection.test.tsx spa/src/components/settings/DevEnvironmentSection.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): Local daemon block in Settings → Development"
```

---

### Task 10: Final sweep and manual acceptance list (verification gate — no commit)

- [ ] `cd electron && pnpm test` · `cd spa && npx vitest run` · `cd spa && pnpm run lint && pnpm run build` · `pnpm exec electron-vite build` — all green; `rg -n "smol-toml" out/main/index.js` → empty.
- [ ] `rg -n "PDX_DEV_MODE === '1'" electron spa/src --glob '!*.test.ts' --glob '!*.test.tsx'` → empty.
- [ ] `rg -n "\\\\0" electron/local-daemon --glob '!*.test.ts'` → only the `split('\0')` in `lsof.ts`; nothing in argv construction.
- [ ] PR-B description lists the manual acceptance from spec §4 (to be run on the arm64 Air after Plan A is deployed on the Mini): Install from `none` → host appears → tmux session attaches; quit app → `/api/health` still answers; relaunch → `ensureRunning: already-running`; `pdx stop` in a shell → relaunch → started; push a commit on the Mini → *Update available* → Update → running hash changes, tmux sessions survive; on the Mini itself the block shows `external`.

## Self-review against spec §3–§4

| Spec | Task |
|---|---|
| §3.1 deps injection, paths, queue, `withLock`, `applyUpdate` under lock | 5, 6, 7 |
| §3.1 status: config via smol-toml + defaults, custom data_dir external, installed via `version --json`, health, candidate pid, `resolveOwner` (lsof txt + listener, decision table, 5 s timeouts), target mapping, `tools.tmux` | 2, 4, 5 |
| §3.1 Launch PATH (sentinel, `-ilc` → `-lc` → fallback, no NUL, `PDX_DEV_MODE=1`) | 3 |
| §3.1 install steps 0–7, integrity (length + sha256), verify identity, config 0600 tmp+rename, bind rule + `bindNote`, stop 35 s + liveness poll, swap, start await + health hash check, register; failure contract | 6 |
| §3.1 `start`, `restart`, `ensureRunning` (retry 3×, never touches `pdx.new`) | 6 |
| §3.2 IPC + preload + types | 7 |
| §3.3 dev-mode default | 1 |
| §3.4 UI rows, Restart, warnings, `registerLocalHost` idempotent | 8, 9 |
| §3.5 tests | 2–9 |
| §4 manual acceptance | 10 |
