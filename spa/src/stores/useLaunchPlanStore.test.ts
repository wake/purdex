// The shared launch-plan state: latest request wins, a re-read keeps the old plan, a failure drops it.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { LaunchPlan } from '../lib/launch-plan'
import { useLaunchPlanStore, resetLaunchPlanFlights, launchPlanKey, selectLaunchPlanEntry } from './useLaunchPlanStore'

const fetchPlan = vi.hoisted(() => vi.fn())
vi.mock('../lib/launch-plan', async (orig) => ({ ...(await orig<typeof import('../lib/launch-plan')>()), fetchLaunchPlan: fetchPlan }))

const mk = (tmuxInstance: string): LaunchPlan => ({
  agentRunning: false, tmuxInstance, dir: null, resume: { line: null, reason: 'no_record' }, fresh: { line: 'claude' }, busy: null,
})
const deferred = () => {
  let resolve!: (p: LaunchPlan) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<LaunchPlan>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const entry = (h = 'h', c = 'abc') => selectLaunchPlanEntry(h, c)(useLaunchPlanStore.getState())
const flush = async () => { for (let i = 0; i < 5; i++) await Promise.resolve() }

beforeEach(() => {
  fetchPlan.mockReset()
  useLaunchPlanStore.setState({ entries: {} })
  resetLaunchPlanFlights()
})

describe('useLaunchPlanStore', () => {
  it('a key with no answer yet is loading, then ready', async () => {
    const d = deferred()
    fetchPlan.mockReturnValueOnce(d.promise)
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(entry()).toEqual({ status: 'loading', plan: null })
    d.resolve(mk('A'))
    await flush()
    expect(entry()?.status).toBe('ready')
    expect(entry()?.plan?.tmuxInstance).toBe('A')
  })

  it('an older response resolving after a newer one is dropped', async () => {
    const older = deferred(); const newer = deferred()
    fetchPlan.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    newer.resolve(mk('NEW'))
    await flush()
    older.resolve(mk('OLD'))
    await flush()
    expect(entry()?.plan?.tmuxInstance).toBe('NEW')
  })

  it('an older failure after a newer success does not drop the plan', async () => {
    const older = deferred(); const newer = deferred()
    fetchPlan.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    newer.resolve(mk('NEW'))
    await flush()
    older.reject(new Error('boom'))
    await flush()
    expect(entry()?.status).toBe('ready')
  })

  it('a re-load keeps the old ready plan until the answer arrives', async () => {
    fetchPlan.mockResolvedValueOnce(mk('A'))
    await useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    const d = deferred()
    fetchPlan.mockReturnValueOnce(d.promise)
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(entry()?.status).toBe('ready')
    expect(entry()?.plan?.tmuxInstance).toBe('A')
    d.resolve(mk('B'))
    await flush()
    expect(entry()?.plan?.tmuxInstance).toBe('B')
  })

  it('a failed read sets error and drops the plan', async () => {
    fetchPlan.mockResolvedValueOnce(mk('A'))
    await useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    fetchPlan.mockRejectedValueOnce(new Error('boom'))
    await useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(entry()).toEqual({ status: 'error', plan: null })
  })

  it('an error key re-loading is loading again (no stale panel)', () => {
    useLaunchPlanStore.setState({ entries: { [launchPlanKey('h', 'abc')]: { status: 'error', plan: null } } })
    fetchPlan.mockReturnValueOnce(new Promise(() => {}))
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(entry()).toEqual({ status: 'loading', plan: null })
  })

  it('keys are per host and code; prototype-member ids have no entry', async () => {
    fetchPlan.mockResolvedValueOnce(mk('A'))
    await useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(entry('h', 'other')).toBeUndefined()
    expect(entry('h', 'toString')).toBeUndefined()
    expect(selectLaunchPlanEntry('', '')(useLaunchPlanStore.getState())).toBeUndefined()
    expect(selectLaunchPlanEntry('constructor', 'x')(useLaunchPlanStore.getState())).toBeUndefined()
  })

  it('two unforced loads of the same key share one request', async () => {
    const d = deferred()
    fetchPlan.mockReturnValue(d.promise)
    const a = useLaunchPlanStore.getState().load('h', 'abc', '')
    const b = useLaunchPlanStore.getState().load('h', 'abc', '')
    expect(fetchPlan).toHaveBeenCalledTimes(1)
    d.resolve(mk('A'))
    await Promise.all([a, b])
    expect(entry()?.status).toBe('ready')
  })

  it('a forced load aborts the in-flight request, and the aborted one failing later never writes', async () => {
    const first = deferred(); const second = deferred()
    fetchPlan.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    void useLaunchPlanStore.getState().load('h', 'abc', '')
    const signal = fetchPlan.mock.calls[0][2] as AbortSignal
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true })
    expect(signal.aborted).toBe(true)
    expect(fetchPlan).toHaveBeenCalledTimes(2)
    second.resolve(mk('A'))
    await flush()
    first.reject(new Error('aborted'))
    await flush()
    expect(entry()?.status).toBe('ready')
  })

  it('a new tmux instance is a new key: loading although the old key is ready', async () => {
    fetchPlan.mockResolvedValueOnce(mk('')) // no generation named: only the key tells the two apart
    await useLaunchPlanStore.getState().load('h', 'abc', 'A')
    expect(selectLaunchPlanEntry('h', 'abc', 'A')(useLaunchPlanStore.getState())?.status).toBe('ready')
    fetchPlan.mockReturnValueOnce(new Promise(() => {}))
    void useLaunchPlanStore.getState().load('h', 'abc', 'B')
    expect(selectLaunchPlanEntry('h', 'abc', 'B')(useLaunchPlanStore.getState())).toEqual({ status: 'loading', plan: null })
  })

  it('an entry whose plan names another generation than the key is not ready', () => {
    useLaunchPlanStore.setState({ entries: { [launchPlanKey('h', 'abc', 'B')]: { status: 'ready', plan: mk('A') } } })
    expect(selectLaunchPlanEntry('h', 'abc', 'B')(useLaunchPlanStore.getState())?.status).toBe('loading')
    expect(selectLaunchPlanEntry('h', 'abc', 'A')(useLaunchPlanStore.getState())).toBeUndefined()
  })

  it('keep:false drops the ready plan before the answer', async () => {
    fetchPlan.mockResolvedValueOnce(mk(''))
    await useLaunchPlanStore.getState().load('h', 'abc', '')
    fetchPlan.mockReturnValueOnce(new Promise(() => {}))
    void useLaunchPlanStore.getState().load('h', 'abc', '', { force: true, keep: false })
    expect(entry()).toEqual({ status: 'loading', plan: null })
  })
})
