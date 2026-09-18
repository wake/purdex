import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useSessionCwdStore } from './useSessionCwdStore'
import { useSessionStore } from './useSessionStore'
import * as api from '../lib/host-api'
import type { Session, SessionCwd } from '../lib/host-api'

vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  fetchSessionCwd: vi.fn(),
}))

const H = 'h1'
const CODE = 'z141yl'

/** `fetchSessionCwd` resolves to `{ cwd, tmuxInstance }` — never a bare string. */
function answer(cwd: string, tmuxInstance = '6901:1789205013'): SessionCwd {
  return { cwd, tmuxInstance }
}

function session(over: Partial<Session> = {}): Session {
  return {
    code: CODE, name: 'ai-chat4', cwd: '/start/dir', mode: 'terminal',
    ...over,
  }
}

/** A fetch nobody has answered yet, plus the switches that answer it. */
function pendingFetch() {
  let settle!: (a: SessionCwd) => void
  let fail!: (err: Error) => void
  vi.mocked(api.fetchSessionCwd).mockReturnValueOnce(new Promise<SessionCwd>((resolve, reject) => {
    settle = resolve
    fail = reject
  }))
  return { settle, fail }
}

beforeEach(() => {
  vi.mocked(api.fetchSessionCwd).mockReset()
  useSessionCwdStore.getState().forgetHost(H)
  useSessionCwdStore.getState().forgetHost('h2')
  useSessionCwdStore.setState({ byHost: {} })
  useSessionStore.setState({ sessions: {} })
})

describe('refresh', () => {
  it('stores the cwd and drops tmuxInstance — this store only displays', async () => {
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/somewhere/else'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    const entry = useSessionCwdStore.getState().byHost[H][CODE]
    expect(entry.cwd).toBe('/somewhere/else')
    expect(entry).not.toHaveProperty('tmuxInstance')
    expect(entry.error).toBeNull()
    expect(entry.loading).toBe(false)
  })

  it('dedupes concurrent refreshes of one (host, code) into a single request', async () => {
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/x'))
    await Promise.all(Array.from({ length: 5 }, () => useSessionCwdStore.getState().refresh(H, CODE)))
    expect(api.fetchSessionCwd).toHaveBeenCalledTimes(1)
  })

  it('a refresh after the first settles issues a second request', async () => {
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/x'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    await useSessionCwdStore.getState().refresh(H, CODE)
    expect(api.fetchSessionCwd).toHaveBeenCalledTimes(2)
  })

  it('keeps two sessions on one host independent', async () => {
    vi.mocked(api.fetchSessionCwd).mockImplementation(async (_hostId: string, code: string) =>
      answer(code === CODE ? '/one' : '/two'))
    await Promise.all([
      useSessionCwdStore.getState().refresh(H, CODE),
      useSessionCwdStore.getState().refresh(H, 'other1'),
    ])
    expect(api.fetchSessionCwd).toHaveBeenCalledTimes(2)
    expect(useSessionCwdStore.getState().byHost[H][CODE].cwd).toBe('/one')
    expect(useSessionCwdStore.getState().byHost[H].other1.cwd).toBe('/two')
  })

  it('keeps the same code on two hosts independent', async () => {
    vi.mocked(api.fetchSessionCwd).mockImplementation(async (hostId: string) =>
      answer(hostId === H ? '/one' : '/two'))
    await Promise.all([
      useSessionCwdStore.getState().refresh(H, CODE),
      useSessionCwdStore.getState().refresh('h2', CODE),
    ])
    expect(useSessionCwdStore.getState().byHost[H][CODE].cwd).toBe('/one')
    expect(useSessionCwdStore.getState().byHost.h2[CODE].cwd).toBe('/two')
  })

  it('marks the entry loading while in flight and clears it afterwards', async () => {
    const { settle } = pendingFetch()
    const p = useSessionCwdStore.getState().refresh(H, CODE)
    expect(useSessionCwdStore.getState().byHost[H][CODE].loading).toBe(true)
    settle(answer('/x'))
    await p
    expect(useSessionCwdStore.getState().byHost[H][CODE].loading).toBe(false)
  })

  it('records fetchedAt on success', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(1_700_000_000_000)
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/x'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    expect(useSessionCwdStore.getState().byHost[H][CODE].fetchedAt).toBe(1_700_000_000_000)
    vi.useRealTimers()
  })

  it('a failed refresh keeps the previous value and records the error', async () => {
    vi.mocked(api.fetchSessionCwd).mockResolvedValueOnce(answer('/somewhere/else'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    const before = useSessionCwdStore.getState().byHost[H][CODE]
    vi.mocked(api.fetchSessionCwd).mockRejectedValueOnce(new Error('boom'))
    await expect(useSessionCwdStore.getState().refresh(H, CODE)).resolves.toBeUndefined()
    const after = useSessionCwdStore.getState().byHost[H][CODE]
    expect(after.cwd).toBe('/somewhere/else')
    expect(after.fetchedAt).toBe(before.fetchedAt)
    expect(after.error).toBe('boom')
    expect(after.loading).toBe(false)
  })

  it('a successful refresh clears a previous error', async () => {
    vi.mocked(api.fetchSessionCwd).mockRejectedValueOnce(new Error('boom'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    expect(useSessionCwdStore.getState().byHost[H][CODE].error).toBe('boom')
    vi.mocked(api.fetchSessionCwd).mockResolvedValueOnce(answer('/x'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    expect(useSessionCwdStore.getState().byHost[H][CODE].error).toBeNull()
  })
})

describe('why this store exists at all', () => {
  // `Session.cwd` is `#{session_path}` — the directory the session was *created*
  // in. It does not follow a `cd`. The endpoint this store calls serves
  // `#{pane_current_path}`. If that distinction is ever lost, the store is
  // pointless and this test is what says so.
  it('yields the pane current path, not Session.cwd, after the session has cd-ed', async () => {
    useSessionStore.getState().replaceHost(H, [session({ cwd: '/start/dir' })])
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/somewhere/else'))

    await useSessionCwdStore.getState().refresh(H, CODE)

    expect(useSessionStore.getState().sessions[H][0].cwd).toBe('/start/dir')
    expect(useSessionCwdStore.getState().byHost[H][CODE].cwd).toBe('/somewhere/else')
    expect(useSessionCwdStore.getState().byHost[H][CODE].cwd)
      .not.toBe(useSessionStore.getState().sessions[H][0].cwd)
  })
})

describe('forgetHost', () => {
  it('clears one host and leaves the others intact', async () => {
    vi.mocked(api.fetchSessionCwd).mockResolvedValue(answer('/x'))
    await useSessionCwdStore.getState().refresh(H, CODE)
    await useSessionCwdStore.getState().refresh('h2', CODE)
    useSessionCwdStore.getState().forgetHost(H)
    expect(useSessionCwdStore.getState().byHost[H]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost.h2).toBeDefined()
  })

  it('an answer already in flight when the host is forgotten never lands', async () => {
    const { settle } = pendingFetch()
    const p = useSessionCwdStore.getState().refresh(H, CODE)
    useSessionCwdStore.getState().forgetHost(H)
    settle(answer('/x'))
    await p
    expect(useSessionCwdStore.getState().byHost[H]).toBeUndefined()
  })

  it('a failure already in flight when the host is forgotten never lands either', async () => {
    const { fail } = pendingFetch()
    const p = useSessionCwdStore.getState().refresh(H, CODE)
    useSessionCwdStore.getState().forgetHost(H)
    fail(new Error('boom'))
    await p
    expect(useSessionCwdStore.getState().byHost[H]).toBeUndefined()
  })

  it('is a no-op for a host that was never fetched', () => {
    expect(() => useSessionCwdStore.getState().forgetHost('nobody')).not.toThrow()
  })
})

// This file's own subject once carried a literal NUL byte as the key separator,
// and git classifies any file containing one as binary: `git diff` prints
// "Binary files ... differ", so the file silently drops out of review, out of
// `git log -p`, and out of every text-based merge. The separator still has to
// be a byte no hostId can contain — it is spelled as an escape instead of being
// embedded raw. The guard is tree-wide because the next one will not be here.
describe('source files stay text', () => {
  it('no .ts/.tsx file embeds a literal NUL byte', () => {
    // `?raw` rather than `node:fs`: this project's tsconfig exposes only
    // `vite/client`, and the glob is resolved at build time, so the test needs
    // no filesystem types and no path arithmetic.
    const sources = import.meta.glob('../**/*.{ts,tsx}', { query: '?raw', eager: true, import: 'default' }) as Record<string, string>
    const offenders = Object.entries(sources)
      .filter(([, text]) => text.includes(' '))
      .map(([path]) => path)
    expect(offenders, 'files git would treat as binary').toEqual([])
  })
})
