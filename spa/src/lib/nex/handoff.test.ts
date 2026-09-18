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
import { HandoffApiError, nexHandoff, nexTakeback } from './handoff-api'
import {
  handToNex,
  takeBack,
  handoffErrorMessage,
  manualResumeHint,
  HANDOFF_ERROR_CODES,
  SESSION_ID_CODES,
} from './handoff'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

vi.mock('./handoff-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./handoff-api')>()),
  nexHandoff: vi.fn(),
  nexTakeback: vi.fn(),
}))

const mockedHandoff = vi.mocked(nexHandoff)
const mockedTakeback = vi.mocked(nexTakeback)

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

function executionTab(): { tabId: string; paneId: string } {
  const tab = createTab({ kind: 'execution', executionId: 'exc_1', host: H, from })
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

const handoffOk = { execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w' }
const takebackOk = { session_id: 'sid-1', archived: true }

let ensure: ReturnType<typeof vi.fn>

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  seedNexHost()
  ensure = vi.fn().mockResolvedValue(undefined)
  useNexHostStore.setState({ ensure } as never)
  mockedHandoff.mockReset()
  mockedTakeback.mockReset()
})
afterEach(() => vi.restoreAllMocks())

describe('handToNex', () => {
  const args = () => ({ hostId: H, sessionCode: from.sessionCode, tmuxInstance: from.tmuxInstance, cachedName: from.cachedName, ...sessionTab() })

  it('ensures the host, POSTs the pane instance + cc rollback template, and swaps the pane to the execution with `from`', async () => {
    mockedHandoff.mockResolvedValueOnce(handoffOk)
    const a = args()
    const out = await handToNex(a)
    expect(ensure).toHaveBeenCalledWith(H)
    expect(mockedHandoff).toHaveBeenCalledTimes(1)
    expect(mockedHandoff).toHaveBeenCalledWith(H, from.sessionCode, { expected_tmux_instance: from.tmuxInstance, rollback_command: 'claude --resume {id}' })
    expect(out).toEqual({ result: handoffOk, swapped: true })
    expect(paneContent(a.tabId)).toEqual({ kind: 'execution', executionId: 'exc_1', host: H, from })
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
    // client-side
    ['network', {}, null],
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
    for (const key of ['handoff.rolled_back', 'handoff.not_rolled_back', 'handoff.menu', 'handoff.confirm_title', 'handoff.confirm_body', 'handoff.success', 'handoff.open_execution', 'takeback.button', 'takeback.confirm_running', 'takeback.success', 'takeback.manual_resume']) {
      expect(enMap[key], key).toBeTruthy()
      expect(zhMap[key], key).toBeTruthy()
      expect(placeholders(zhMap[key]), key).toEqual(placeholders(enMap[key]))
    }
    expect(enMap['takeback.manual_resume']).toContain('{{id}}')
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
