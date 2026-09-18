// spa/src/lib/nex/handoff.test.ts — P-C.3b task 2: the imperative "hand to
// nex" / "take back" orchestration (spec §4.4 SPA): readiness gate, resume
// template, checked pane swap, client single-flight, and the error-code →
// locale-key map over the daemon's whole error table (plan baseline).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { NexCapabilities } from './types'
import type { NexInfo } from '../host-api'
import type { NexHostEntry } from './nex-host-reducer'
import type { PaneContent } from '../../types/tab'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostConfigStore, emptyHostConfigEntry } from '../../stores/useHostConfigStore'
import { useSessionStore } from '../../stores/useSessionStore'
import type { HostProject } from '../host-config-api'
import { HandoffApiError, nexHandoff, nexTakeback, nexTakeToTerminal } from './handoff-api'
import { checkHostPath } from '../host-config-api'
import {
  handToNex,
  takeBack,
  takeToTerminal,
  handoffErrorMessage,
  manualResumeHint,
  HANDOFF_ERROR_CODES,
  SESSION_ID_CODES,
} from './handoff'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

vi.mock('../host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-config-api')>()),
  checkHostPath: vi.fn(),
}))
vi.mock('./handoff-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./handoff-api')>()),
  nexHandoff: vi.fn(),
  nexTakeback: vi.fn(),
  nexTakeToTerminal: vi.fn(),
}))

const mockedHandoff = vi.mocked(nexHandoff)
const mockedTakeback = vi.mocked(nexTakeback)
const mockedToTerminal = vi.mocked(nexTakeToTerminal)

const H = 'host-mlab'
const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }

const info = (): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: null })
const caps = (over: Partial<NexCapabilities> = {}): NexCapabilities => ({
  phase: 'ga', host_id: H, verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
  sandbox_profiles: ['default', 'handoff'], sandbox_default_profile: 'default', sandbox_max_profile: 'handoff',
  roots: [], lease: { ttl_seconds: 30, scope: 'x', renew: { method: 'POST', path: '/r' }, release: { method: 'DELETE', path: '/r' } },
  send: { delivery: ['text'], max_text_bytes: 1 },
  delegate: { resume_session_id: true },
  ...over,
})

function seedNexHost(over: Partial<NexHostEntry> = {}) {
  useNexHostStore.setState({
    byHost: { [H]: { info: info(), capabilities: caps(), phase: 'ready', error: null, fetchedAt: Date.now(), generation: 1, fingerprint: 'f', ...over } },
  })
}

function sessionTab(): { tabId: string; paneId: string } {
  const tab = createTab({ kind: 'tmux-session', hostId: H, sessionCode: from.sessionCode, mode: 'terminal', cachedName: from.cachedName, tmuxInstance: from.tmuxInstance })
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
}

function executionTab(withFrom = true): { tabId: string; paneId: string } {
  const tab = createTab(withFrom ? { kind: 'execution', executionId: 'exc_1', host: H, from } : { kind: 'execution', executionId: 'exc_1', host: H })
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
}

function paneContent(tabId: string): PaneContent {
  return getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
}

async function rejection(p: Promise<unknown>): Promise<HandoffApiError> {
  try {
    await p
  } catch (e) {
    expect(e).toBeInstanceOf(HandoffApiError)
    return e as HandoffApiError
  }
  throw new Error('expected rejection')
}

/** Never-resolving promise + its resolver, to hold a request in flight. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const handoffOk = { execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w', session_kept: true }
const takebackOk = { session_id: 'sid-1', archived: true }
const newSession = { code: 'nw1234', name: 'purdex-3', cwd: '/w/purdex/.claude/worktrees/x', mode: 'terminal', tmux_instance: 'inst-9' }
const toTerminalOk = { session: newSession, session_id: 'sid-1', archived: true }

let ensure: ReturnType<typeof vi.fn>
let ensureLoaded: ReturnType<typeof vi.fn>

/**
 * A host config `ensureLoaded` that lands the host's resume overrides only
 * when awaited — the store is empty until then, so a lookup that runs before
 * the await sees the defaults.
 */
function configLoadsLater(hostId: string, cc: { exact: string; fallback: string }) {
  ensureLoaded.mockImplementation(async (id: string) => {
    if (id !== hostId) return
    useHostConfigStore.setState((s) => ({
      byHost: { ...s.byHost, [hostId]: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc } } },
    }))
  })
}

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  seedNexHost()
  ensure = vi.fn().mockResolvedValue(undefined)
  useNexHostStore.setState({ ensure } as never)
  ensureLoaded = vi.fn().mockResolvedValue(undefined)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded } as never)
  mockedHandoff.mockReset()
  mockedTakeback.mockReset()
  mockedToTerminal.mockReset()
  fetchHost = vi.fn().mockResolvedValue(undefined)
  useSessionStore.setState({ sessions: {}, fetchHost } as never)
})
afterEach(() => vi.restoreAllMocks())

let fetchHost: ReturnType<typeof vi.fn>

const project = (id: string, slug: string, path: string): HostProject => ({ id, name: id, slug, path })

/** Seed the host's projects (config already loaded) and its live session names. */
function seedHostFor(projects: HostProject[], liveNames: string[]) {
  useHostConfigStore.setState((s) => ({
    byHost: { ...s.byHost, [H]: { ...emptyHostConfigEntry('ready'), projects } },
  }))
  useSessionStore.setState({
    sessions: { [H]: liveNames.map((name, i) => ({ code: `c${i}`, name, cwd: '/', mode: 'terminal' })) },
  })
}

describe('handToNex', () => {
  const args = () => ({ hostId: H, sessionCode: from.sessionCode, tmuxInstance: from.tmuxInstance, cachedName: from.cachedName, ...sessionTab() })

  it('ensures the host, POSTs the pane instance + cc rollback template, and swaps the pane to the execution with `from`', async () => {
    mockedHandoff.mockResolvedValueOnce(handoffOk)
    const a = args()
    const out = await handToNex(a)
    expect(ensure).toHaveBeenCalledWith(H)
    expect(mockedHandoff).toHaveBeenCalledTimes(1)
    expect(mockedHandoff).toHaveBeenCalledWith(H, from.sessionCode, { expected_tmux_instance: from.tmuxInstance, rollback_command: 'claude --resume {id}', keep_session: true })
    expect(out).toEqual({ result: handoffOk, swapped: true })
    expect(paneContent(a.tabId)).toEqual({ kind: 'execution', executionId: 'exc_1', host: H, from })
  })

  describe('keep_session (exec-to-terminal spec §4.3 / G4)', () => {
    it('keepSession:false is sent as keep_session:false; session_kept:false → the pane has no `from` (the button later takes it to a NEW terminal)', async () => {
      mockedHandoff.mockResolvedValueOnce({ ...handoffOk, session_kept: false })
      const a = { ...args(), keepSession: false }
      const out = await handToNex(a)
      expect(mockedHandoff.mock.calls[0][2]).toMatchObject({ keep_session: false })
      expect(out.swapped).toBe(true)
      expect(paneContent(a.tabId)).toEqual({ kind: 'execution', executionId: 'exc_1', host: H })
      expect(paneContent(a.tabId)).not.toHaveProperty('from')
    })

    it('keepSession:false but the daemon could not kill (session_kept:true) → `from` is kept', async () => {
      mockedHandoff.mockResolvedValueOnce({ ...handoffOk, session_kept: true })
      const a = { ...args(), keepSession: false }
      await handToNex(a)
      expect(paneContent(a.tabId)).toEqual({ kind: 'execution', executionId: 'exc_1', host: H, from })
    })

    it('an old daemon that omits session_kept counts as kept (keepSession absent → keep_session:true)', async () => {
      const { session_kept: _omit, ...old } = handoffOk
      void _omit
      mockedHandoff.mockResolvedValueOnce(old as typeof handoffOk)
      const a = args()
      await handToNex(a)
      expect(mockedHandoff.mock.calls[0][2]).toMatchObject({ keep_session: true })
      expect(paneContent(a.tabId)).toEqual({ kind: 'execution', executionId: 'exc_1', host: H, from })
    })
  })

  it('loads the host config before reading the resume template, so a not-yet-loaded override is the rollback command (R1-2)', async () => {
    configLoadsLater(H, { exact: 'cld-yolo --resume {id}', fallback: 'cld-yolo -c' })
    mockedHandoff.mockResolvedValueOnce(handoffOk)
    await handToNex(args())
    expect(ensureLoaded).toHaveBeenCalledWith(H)
    expect(mockedHandoff).toHaveBeenCalledWith(H, from.sessionCode, { expected_tmux_instance: from.tmuxInstance, rollback_command: 'cld-yolo --resume {id}', keep_session: true })
  })

  it('rejects with handoff_unsupported and sends nothing when the host is not handoff-ready', async () => {
    seedNexHost({ capabilities: caps({ sandbox_profiles: ['default'] }) })
    const a = args()
    const err = await rejection(handToNex(a))
    expect(err.code).toBe('handoff_unsupported')
    expect(err.status).toBe(0)
    expect(mockedHandoff).not.toHaveBeenCalled()
    expect(paneContent(a.tabId).kind).toBe('tmux-session')
  })

  it('returns swapped:false when the pane is gone by the time the daemon answers (the handoff still happened)', async () => {
    const d = deferred<typeof handoffOk>()
    mockedHandoff.mockReturnValueOnce(d.promise)
    const a = args()
    const p = handToNex(a)
    await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalled())
    useTabStore.getState().closeTab(a.tabId)
    d.resolve(handoffOk)
    expect(await p).toEqual({ result: handoffOk, swapped: false })
  })

  it('returns swapped:false when the pane now shows another session (compare-and-swap; the other session is left alone)', async () => {
    const d = deferred<typeof handoffOk>()
    mockedHandoff.mockReturnValueOnce(d.promise)
    const a = args()
    const p = handToNex(a)
    await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalled())
    const other: PaneContent = { kind: 'tmux-session', hostId: H, sessionCode: 'other1', mode: 'terminal', cachedName: 'other', tmuxInstance: 'inst-2' }
    useTabStore.getState().setPaneContent(a.tabId, a.paneId, other)
    d.resolve(handoffOk)
    expect(await p).toEqual({ result: handoffOk, swapped: false })
    expect(paneContent(a.tabId)).toEqual(other)
  })

  it('returns swapped:false when the same session was reopened under a new tmux instance', async () => {
    const d = deferred<typeof handoffOk>()
    mockedHandoff.mockReturnValueOnce(d.promise)
    const a = args()
    const p = handToNex(a)
    await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalled())
    useTabStore.getState().setPaneContent(a.tabId, a.paneId, { kind: 'tmux-session', hostId: H, sessionCode: from.sessionCode, mode: 'terminal', cachedName: from.cachedName, tmuxInstance: 'inst-replaced' })
    d.resolve(handoffOk)
    expect(await p).toMatchObject({ swapped: false })
    expect(paneContent(a.tabId)).toMatchObject({ kind: 'tmux-session', tmuxInstance: 'inst-replaced' })
  })

  it('propagates the daemon error untouched and leaves the pane alone', async () => {
    mockedHandoff.mockRejectedValueOnce(new HandoffApiError(409, 'no_cc', { error: 'no cc', code: 'no_cc' }))
    const a = args()
    const err = await rejection(handToNex(a))
    expect(err.code).toBe('no_cc')
    expect(paneContent(a.tabId).kind).toBe('tmux-session')
  })

  describe('single-flight', () => {
    it('a second call for the same host+session while one is in flight rejects with handoff_in_progress without a request', async () => {
      const d = deferred<typeof handoffOk>()
      mockedHandoff.mockReturnValueOnce(d.promise)
      const a = args()
      const first = handToNex(a)
      await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalledTimes(1))
      const err = await rejection(handToNex(a))
      expect(err.code).toBe('handoff_in_progress')
      expect(err.status).toBe(0)
      expect(mockedHandoff).toHaveBeenCalledTimes(1)
      d.resolve(handoffOk)
      await first
    })

    it('two concurrent calls → one request; the key clears after settle (success and failure)', async () => {
      const d = deferred<typeof handoffOk>()
      mockedHandoff.mockReturnValueOnce(d.promise)
      const a = args()
      const results = Promise.allSettled([handToNex(a), handToNex(a)])
      await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalledTimes(1))
      d.resolve(handoffOk)
      const [r1, r2] = await results
      expect(r1.status).toBe('fulfilled')
      expect(r2.status).toBe('rejected')

      mockedHandoff.mockRejectedValueOnce(new HandoffApiError(409, 'no_cc', {}))
      const b = args()
      await rejection(handToNex(b))
      expect(mockedHandoff).toHaveBeenCalledTimes(2)
      mockedHandoff.mockResolvedValueOnce(handoffOk)
      await handToNex(b)
      expect(mockedHandoff).toHaveBeenCalledTimes(3)
    })

    it('the readiness gate rejecting also clears the key', async () => {
      seedNexHost({ phase: 'unavailable', capabilities: null })
      const a = args()
      await rejection(handToNex(a))
      seedNexHost()
      mockedHandoff.mockResolvedValueOnce(handoffOk)
      await expect(handToNex(a)).resolves.toMatchObject({ swapped: true })
    })

    it('different sessions do not block each other', async () => {
      const d = deferred<typeof handoffOk>()
      mockedHandoff.mockReturnValueOnce(d.promise).mockResolvedValueOnce({ ...handoffOk, execution_id: 'exc_2' })
      const a = args()
      const b = { ...args(), sessionCode: 'other1' }
      const first = handToNex(a)
      await vi.waitFor(() => expect(mockedHandoff).toHaveBeenCalledTimes(1))
      await expect(handToNex(b)).resolves.toMatchObject({ result: { execution_id: 'exc_2' } })
      d.resolve(handoffOk)
      await first
    })
  })
})

describe('takeBack', () => {
  const args = (over: { leaseId?: string } = {}) => ({
    hostId: H, executionId: 'exc_1', from, forgetLease: vi.fn<() => void>(), ...executionTab(), ...over,
  })

  it('POSTs the from-instance, execution id, cc resume template and lease id; forgets the lease before swapping back', async () => {
    const calls: string[] = []
    mockedTakeback.mockImplementationOnce(async () => takebackOk)
    const a = args({ leaseId: 'lease-9' })
    a.forgetLease.mockImplementation(() => {
      calls.push('forget')
      expect(paneContent(a.tabId).kind).toBe('execution') // not swapped yet
    })
    const out = await takeBack(a)
    expect(mockedTakeback).toHaveBeenCalledWith(H, from.sessionCode, {
      expected_tmux_instance: from.tmuxInstance, execution_id: 'exc_1', resume_command: 'claude --resume {id}', lease_id: 'lease-9',
    })
    expect(calls).toEqual(['forget'])
    expect(out).toEqual({ result: takebackOk, swapped: true })
    expect(paneContent(a.tabId)).toEqual({
      kind: 'tmux-session', hostId: H, sessionCode: from.sessionCode, mode: 'terminal', cachedName: from.cachedName, tmuxInstance: from.tmuxInstance,
    })
  })

  it('loads the host config before reading the resume template, so a not-yet-loaded override is the resume command (R1-2)', async () => {
    configLoadsLater(H, { exact: 'cld-yolo --resume {id}', fallback: 'cld-yolo -c' })
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    await takeBack(args())
    expect(ensureLoaded).toHaveBeenCalledWith(H)
    expect(mockedTakeback.mock.calls[0][2].resume_command).toBe('cld-yolo --resume {id}')
  })

  it('omits lease_id when the tab holds no lease', async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    await takeBack(args())
    const body = mockedTakeback.mock.calls[0][2]
    expect(body).not.toHaveProperty('lease_id')
  })

  it('on failure the lease is left to the hook (forgetLease not called) and the pane is untouched', async () => {
    mockedTakeback.mockRejectedValueOnce(new HandoffApiError(404, 'session_missing', { code: 'session_missing' }))
    const a = args({ leaseId: 'lease-9' })
    const err = await rejection(takeBack(a))
    expect(err.code).toBe('session_missing')
    expect(a.forgetLease).not.toHaveBeenCalled()
    expect(paneContent(a.tabId).kind).toBe('execution')
  })

  it('returns swapped:false when the pane is gone; the lease is still forgotten (the daemon consumed it)', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const a = args()
    const p = takeBack(a)
    await vi.waitFor(() => expect(mockedTakeback).toHaveBeenCalled())
    useTabStore.getState().closeTab(a.tabId)
    d.resolve(takebackOk)
    expect(await p).toEqual({ result: takebackOk, swapped: false })
    expect(a.forgetLease).toHaveBeenCalledTimes(1)
  })

  it('returns swapped:false when the pane now shows a different execution (compare-and-swap); the lease is still forgotten', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const a = args()
    const p = takeBack(a)
    await vi.waitFor(() => expect(mockedTakeback).toHaveBeenCalled())
    const other: PaneContent = { kind: 'execution', executionId: 'exc_other', host: H }
    useTabStore.getState().setPaneContent(a.tabId, a.paneId, other)
    d.resolve(takebackOk)
    expect(await p).toEqual({ result: takebackOk, swapped: false })
    expect(paneContent(a.tabId)).toEqual(other)
    expect(a.forgetLease).toHaveBeenCalledTimes(1)
  })

  it('single-flight per host+execution: second concurrent call rejects with handoff_in_progress, no request', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const a = args()
    const first = takeBack(a)
    await vi.waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    const err = await rejection(takeBack(a))
    expect(err.code).toBe('handoff_in_progress')
    expect(mockedTakeback).toHaveBeenCalledTimes(1)
    d.resolve(takebackOk)
    await first
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    await takeBack(args())
    expect(mockedTakeback).toHaveBeenCalledTimes(2)
  })
})

describe('takeToTerminal (exec-to-terminal spec §4.2)', () => {
  const CWD = '/w/purdex/.claude/worktrees/x'
  const args = (over: { leaseId?: string; cwd?: string } = {}) => ({
    hostId: H, executionId: 'exc_1', cwd: CWD, forgetLease: vi.fn<() => void>(), ...executionTab(false), ...over,
  })

  it('names the session {project slug}-{N} from the nearest ancestor project and the live list; POSTs name + cc resume template + lease; forgets the lease before swapping to the new session; refreshes the session list', async () => {
    seedHostFor([project('p1', 'purdex', '/w/purdex'), project('p2', 'w', '/w')], ['purdex-1', 'purdex-2', 'other'])
    const calls: string[] = []
    mockedToTerminal.mockImplementationOnce(async () => toTerminalOk)
    const a = args({ leaseId: 'lease-9' })
    a.forgetLease.mockImplementation(() => {
      calls.push('forget')
      expect(paneContent(a.tabId).kind).toBe('execution') // not swapped yet
    })
    const out = await takeToTerminal(a)
    expect(mockedToTerminal).toHaveBeenCalledTimes(1)
    expect(mockedToTerminal).toHaveBeenCalledWith(H, 'exc_1', { session_name: 'purdex-3', resume_command: 'claude --resume {id}', lease_id: 'lease-9' })
    expect(calls).toEqual(['forget'])
    expect(out).toEqual({ result: toTerminalOk, swapped: true })
    expect(paneContent(a.tabId)).toEqual({
      kind: 'tmux-session', hostId: H, sessionCode: 'nw1234', mode: 'terminal', cachedName: 'purdex-3', tmuxInstance: 'inst-9',
    })
    expect(fetchHost).toHaveBeenCalledWith(H)
  })

  it("loads the host config first (projects + resume template), so a not-yet-loaded override is the resume command and the slug is the project's", async () => {
    ensureLoaded.mockImplementation(async (id: string) => {
      if (id !== H) return
      useHostConfigStore.setState((s) => ({
        byHost: { ...s.byHost, [H]: { ...emptyHostConfigEntry('ready'), projects: [project('p1', 'pdx', '/w/purdex')], resumeTemplates: { cc: { exact: 'cld-yolo --resume {id}', fallback: 'cld-yolo -c' } } } },
      }))
    })
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await takeToTerminal(args())
    expect(ensureLoaded).toHaveBeenCalledWith(H)
    expect(mockedToTerminal.mock.calls[0][2]).toEqual({ session_name: 'pdx-1', resume_command: 'cld-yolo --resume {id}' })
  })

  it('resolves the host home once through check-path when a project is stored as ~/… (codex attacker F5)', async () => {
    seedHostFor([project('p1', 'pdx', '~/w/purdex')], [])
    vi.mocked(checkHostPath).mockResolvedValueOnce({ status: 'dir', resolved: '/Users/x' })
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await takeToTerminal(args({ cwd: '/Users/x/w/purdex/.claude/worktrees/t' }))
    expect(checkHostPath).toHaveBeenCalledWith(H, '~')
    expect(mockedToTerminal.mock.calls[0][2]).toEqual({ session_name: 'pdx-1', resume_command: 'claude --resume {id}' })
  })

  it('does not ask for the home when no project needs it, and falls back (not guesses) when the lookup fails', async () => {
    vi.mocked(checkHostPath).mockClear()
    seedHostFor([project('p1', 'pdx', '/w/purdex')], [])
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await takeToTerminal(args({ cwd: '/w/purdex' }))
    expect(checkHostPath).not.toHaveBeenCalled()

    seedHostFor([project('p1', 'pdx', '~/w/purdex')], [])
    vi.mocked(checkHostPath).mockRejectedValueOnce(new Error('offline'))
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await takeToTerminal(args({ cwd: '/Users/x/w/purdex' }))
    expect(mockedToTerminal.mock.calls[1][2]).toEqual({ session_name: 'purdex-1', resume_command: 'claude --resume {id}' })
  })

  it('falls back to the cleaned cwd basename when no project matches; omits lease_id without a lease', async () => {
    seedHostFor([project('p1', 'purdex', '/w/purdex')], [])
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await takeToTerminal(args({ cwd: '/srv/my app.v2' }))
    expect(mockedToTerminal.mock.calls[0][2]).toEqual({ session_name: 'my-app-v2-1', resume_command: 'claude --resume {id}' })
  })

  it('retries once on session_exists with the refused name counted as taken, then rethrows a second refusal', async () => {
    seedHostFor([project('p1', 'purdex', '/w/purdex')], ['purdex-1'])
    mockedToTerminal
      .mockRejectedValueOnce(new HandoffApiError(409, 'session_exists', { code: 'session_exists', session_name: 'purdex-2' }))
      .mockResolvedValueOnce(toTerminalOk)
    const a = args()
    await expect(takeToTerminal(a)).resolves.toMatchObject({ swapped: true })
    expect(mockedToTerminal.mock.calls.map((c) => c[2].session_name)).toEqual(['purdex-2', 'purdex-3'])

    mockedToTerminal.mockReset()
    mockedToTerminal.mockRejectedValue(new HandoffApiError(409, 'session_exists', { code: 'session_exists' }))
    const b = args()
    const err = await rejection(takeToTerminal(b))
    expect(err.code).toBe('session_exists')
    expect(mockedToTerminal).toHaveBeenCalledTimes(2)
    expect(b.forgetLease).not.toHaveBeenCalled()
    expect(paneContent(b.tabId).kind).toBe('execution')
  })

  it('session_create_failed with session_alive → refreshes the session list (the orphan is visible) and rethrows; lease left to the hook, pane untouched', async () => {
    seedHostFor([], [])
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(500, 'session_create_failed', { code: 'session_create_failed', session_name: 'x-1', session_alive: true }))
    const a = args({ leaseId: 'lease-9' })
    const err = await rejection(takeToTerminal(a))
    expect(err.code).toBe('session_create_failed')
    expect(fetchHost).toHaveBeenCalledWith(H)
    expect(a.forgetLease).not.toHaveBeenCalled()
    expect(paneContent(a.tabId).kind).toBe('execution')
  })

  it('any other failure (incl. session_create_failed without session_alive) → no refresh, no forget, pane untouched', async () => {
    seedHostFor([], [])
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(500, 'session_create_failed', { code: 'session_create_failed', session_name: 'x-1', session_alive: false }))
    const a = args()
    await rejection(takeToTerminal(a))
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(409, 'cwd_missing', { code: 'cwd_missing' }))
    const err = await rejection(takeToTerminal(a))
    expect(err.code).toBe('cwd_missing')
    expect(fetchHost).not.toHaveBeenCalled()
    expect(a.forgetLease).not.toHaveBeenCalled()
    expect(paneContent(a.tabId).kind).toBe('execution')
  })

  it('a session list refresh failure is swallowed (fire-and-forget)', async () => {
    seedHostFor([], [])
    fetchHost.mockRejectedValue(new Error('offline'))
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    await expect(takeToTerminal(args())).resolves.toMatchObject({ swapped: true })
    await Promise.resolve()
  })

  it('returns swapped:false when the pane now shows another execution; the lease is still forgotten (the daemon consumed it)', async () => {
    seedHostFor([], [])
    const d = deferred<typeof toTerminalOk>()
    mockedToTerminal.mockReturnValueOnce(d.promise)
    const a = args()
    const p = takeToTerminal(a)
    await vi.waitFor(() => expect(mockedToTerminal).toHaveBeenCalled())
    const other: PaneContent = { kind: 'execution', executionId: 'exc_other', host: H }
    useTabStore.getState().setPaneContent(a.tabId, a.paneId, other)
    d.resolve(toTerminalOk)
    expect(await p).toEqual({ result: toTerminalOk, swapped: false })
    expect(paneContent(a.tabId)).toEqual(other)
    expect(a.forgetLease).toHaveBeenCalledTimes(1)
  })

  it('an older daemon without tmux_instance on the session → tmuxInstance "" (never a match)', async () => {
    seedHostFor([], [])
    const { tmux_instance: _omit, ...bare } = newSession
    void _omit
    mockedToTerminal.mockResolvedValueOnce({ ...toTerminalOk, session: bare })
    const a = args()
    await takeToTerminal(a)
    expect(paneContent(a.tabId)).toMatchObject({ kind: 'tmux-session', sessionCode: 'nw1234', tmuxInstance: '' })
  })

  it('shares the single-flight key with takeBack: neither can start while the other is in flight for the same execution', async () => {
    seedHostFor([], [])
    const d = deferred<typeof toTerminalOk>()
    mockedToTerminal.mockReturnValueOnce(d.promise)
    const a = args()
    const first = takeToTerminal(a)
    await vi.waitFor(() => expect(mockedToTerminal).toHaveBeenCalledTimes(1))
    let err = await rejection(takeToTerminal(a))
    expect(err.code).toBe('handoff_in_progress')
    err = await rejection(takeBack({ hostId: H, executionId: 'exc_1', from, forgetLease: vi.fn(), ...executionTab() }))
    expect(err.code).toBe('handoff_in_progress')
    expect(mockedTakeback).not.toHaveBeenCalled()
    d.resolve(toTerminalOk)
    await first

    const d2 = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d2.promise)
    const second = takeBack({ hostId: H, executionId: 'exc_1', from, forgetLease: vi.fn(), ...executionTab() })
    await vi.waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    err = await rejection(takeToTerminal(args()))
    expect(err.code).toBe('handoff_in_progress')
    expect(mockedToTerminal).toHaveBeenCalledTimes(1)
    d2.resolve(takebackOk)
    await second
  })
})

describe('handoffErrorMessage', () => {
  const t = vi.fn((key: string, params?: Record<string, string | number>) => JSON.stringify([key, params ?? null]))
  beforeEach(() => t.mockClear())

  const decode = (s: string) => JSON.parse(s) as [string, Record<string, string | number> | null]

  const table: Array<[string, Record<string, unknown>, Record<string, string | number> | null]> = [
    // handoff endpoint
    ['nex_unavailable', {}, null],
    ['malformed_body', {}, null],
    ['invalid_instance', {}, null],
    ['session_missing', {}, null],
    ['handoff_unsupported', {}, null],
    ['handoff_in_progress', {}, null],
    ['tmux_instance_mismatch', { after_exit: true, rolled_back: true, session_id: 'sid' }, null],
    ['no_identity', {}, null],
    ['no_cc', {}, null],
    ['delegate_rejected', { reject_reason: 'quota', rolled_back: true, session_id: 'sid' }, { reject_reason: 'quota', rolled_back: '["handoff.rolled_back",null]' }],
    ['delegate_rejected', { reject_reason: 'quota', rolled_back: false, session_id: 'sid' }, { reject_reason: 'quota', rolled_back: '["handoff.not_rolled_back",null]' }],
    ['principal_unresolved', {}, null],
    ['session_lookup_failed', {}, null],
    ['cc_exit_timeout', { step: 'wait-exit' }, { step: 'wait-exit' }],
    // takeback endpoint
    ['missing_execution_id', {}, null],
    ['missing_resume_command', {}, null],
    ['execution_not_found', {}, null],
    ['cc_already_running', { session_id: 'sid' }, null],
    ['execution_not_bound', { execution_id: 'exc_9', session_code: 'abc' }, { execution_id: 'exc_9', session_code: 'abc' }],
    ['held_by', { principal: 'tab:xyz' }, { principal: 'tab:xyz' }],
    ['execution_not_settled', { state: 'running' }, { state: 'running' }],
    ['no_session_id', {}, null],
    ['store_error', {}, null],
    ['lease_error', {}, null],
    ['interrupt_failed', {}, null],
    ['send_failed', { session_id: 'sid' }, null],
    ['interrupt_unconfirmed', {}, null],
    ['cc_start_timeout', { session_id: 'sid' }, null],
    // take-to-terminal endpoint (codes not already above)
    ['session_exists', { session_name: 'purdex-3' }, { session_name: 'purdex-3' }],
    ['missing_session_name', {}, null],
    ['invalid_session_name', {}, null],
    ['session_create_failed', { session_name: 'purdex-3', session_alive: true }, { session_name: 'purdex-3' }],
    ['cwd_missing', {}, null],
    ['provider_unsupported', {}, null],
    ['takeback_in_progress', {}, null],
    ['execution_archived', {}, null],
    ['archive_failed', {}, null],
    // client-side
    ['network', {}, null],
    ['host_removed', {}, null],
  ]

  it.each(table)('%s → handoff.error.%s with params', (code, body, params) => {
    const msg = handoffErrorMessage(t, new HandoffApiError(409, code, { code, ...body }))
    const [key, got] = decode(msg)
    expect(key).toBe(`handoff.error.${code}`)
    expect(got).toEqual(params)
  })

  it('covers every code in the baseline table exactly once', () => {
    const codes = new Set(table.map(([c]) => c))
    expect([...codes].sort()).toEqual([...HANDOFF_ERROR_CODES].sort())
  })

  it('unknown code → handoff.error.generic with the code', () => {
    const [key, params] = decode(handoffErrorMessage(t, new HandoffApiError(500, 'http_500', {})))
    expect(key).toBe('handoff.error.generic')
    expect(params).toEqual({ code: 'http_500' })
  })

  it('missing params fall back to placeholders, never `undefined`', () => {
    const [, params] = decode(handoffErrorMessage(t, new HandoffApiError(504, 'cc_exit_timeout', {})))
    expect(params).toEqual({ step: '?' })
    const [, p2] = decode(handoffErrorMessage(t, new HandoffApiError(409, 'delegate_rejected', {})))
    expect(p2).toEqual({ reject_reason: '?', rolled_back: '["handoff.not_rolled_back",null]' })
  })

  it('every mapped code has a locale string in en and zh-TW (with the same placeholders)', () => {
    const enMap = en as Record<string, string>
    const zhMap = zhTW as Record<string, string>
    const placeholders = (s: string) => (s.match(/\{\{\w+\}\}/g) ?? []).sort()
    for (const code of [...HANDOFF_ERROR_CODES, 'generic']) {
      const key = `handoff.error.${code}`
      expect(enMap[key], key).toBeTruthy()
      expect(zhMap[key], key).toBeTruthy()
      expect(placeholders(zhMap[key]), key).toEqual(placeholders(enMap[key]))
    }
    for (const key of ['handoff.rolled_back', 'handoff.not_rolled_back', 'handoff.menu', 'handoff.confirm_title', 'handoff.confirm_body', 'handoff.success', 'handoff.open_execution', 'handoff.keep_session', 'handoff.other_panes', 'takeback.button', 'takeback.confirm_running', 'takeback.success', 'takeback.manual_resume']) {
      expect(enMap[key], key).toBeTruthy()
      expect(zhMap[key], key).toBeTruthy()
      expect(placeholders(zhMap[key]), key).toEqual(placeholders(enMap[key]))
    }
    expect(enMap['takeback.manual_resume']).toContain('{{id}}')
    expect(enMap['handoff.other_panes']).toContain('{{count}}')
  })
})

describe('manualResumeHint', () => {
  it('returns the session id only for the codes the daemon puts it on', () => {
    for (const code of HANDOFF_ERROR_CODES) {
      const hint = manualResumeHint(new HandoffApiError(409, code, { code, session_id: 'sid-7' }))
      if (SESSION_ID_CODES.has(code)) expect(hint, code).toBe('sid-7')
      else expect(hint, code).toBeNull()
    }
    expect([...SESSION_ID_CODES].sort()).toEqual(['cc_already_running', 'cc_start_timeout', 'delegate_rejected', 'send_failed', 'tmux_instance_mismatch'])
  })

  it('returns null when the body carries no usable session id', () => {
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { code: 'delegate_rejected' }))).toBeNull()
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { code: 'delegate_rejected', session_id: '' }))).toBeNull()
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { code: 'delegate_rejected', session_id: 42 }))).toBeNull()
  })

  it('returns null when the daemon already rolled back (CC is back in the terminal, nothing to resume by hand)', () => {
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: true, session_id: 'sid-9' }))).toBeNull()
    expect(manualResumeHint(new HandoffApiError(409, 'tmux_instance_mismatch', { after_exit: true, rolled_back: true, session_id: 'sid-9' }))).toBeNull()
    // Not rolled back, or the field is absent / not a boolean: the id stands.
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: false, session_id: 'sid-9' }))).toBe('sid-9')
    expect(manualResumeHint(new HandoffApiError(409, 'tmux_instance_mismatch', { after_exit: true, session_id: 'sid-9' }))).toBe('sid-9')
    expect(manualResumeHint(new HandoffApiError(409, 'delegate_rejected', { rolled_back: 'true', session_id: 'sid-9' }))).toBe('sid-9')
  })
})
