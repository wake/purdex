import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  resolveDeeplink,
  registerDeeplinkResolver,
  type ResolveDeeplinkDeps,
} from './deeplinkResolver'
import type { ExecutionView } from '../execution-api'

function makeView(overrides: Partial<ExecutionView> = {}): ExecutionView {
  return {
    execution_id: 'exc_1',
    dispatch_id: 'dsp_1',
    status: 'running',
    launch_state: 'launched',
    session_code: null,
    session_name: 'pdx-exec-1',
    provider: 'claude',
    attempt_no: 1,
    repo_location: '/repo',
    head_at_start: 'base',
    dirty_at_start: false,
    sandbox_profile: 'workspace-write',
    created_at: 0,
    updated_at: 0,
    ...overrides,
  }
}

function makeDeps(over: Partial<ResolveDeeplinkDeps> = {}) {
  const deps: ResolveDeeplinkDeps = {
    resolveHostId: vi.fn(() => 'host-a'),
    fetchExecution: vi.fn(async () => makeView()),
    focusSession: vi.fn(() => false),
    openDetail: vi.fn(),
    ...over,
  }
  return deps
}

describe('resolveDeeplink', () => {
  it('① focuses an already-open session tab (observe-only) when live', async () => {
    const deps = makeDeps({
      fetchExecution: vi.fn(async () => makeView({ session_code: 'sess1' })),
      focusSession: vi.fn(() => true), // tab exists → focused
    })
    await resolveDeeplink({ executionId: 'exc_1' }, deps)
    expect(deps.focusSession).toHaveBeenCalledWith('sess1')
    expect(deps.openDetail).not.toHaveBeenCalled()
  })

  it('② opens the detail page when the session tab is not open', async () => {
    const deps = makeDeps({
      fetchExecution: vi.fn(async () => makeView({ session_code: 'sess1' })),
      focusSession: vi.fn(() => false), // no open tab
    })
    await resolveDeeplink({ executionId: 'exc_1', host: 'host-a' }, deps)
    expect(deps.focusSession).toHaveBeenCalledWith('sess1')
    expect(deps.openDetail).toHaveBeenCalledWith('exc_1', 'host-a')
  })

  it('② opens the detail page when there is no session_code (not launched / terminal)', async () => {
    const deps = makeDeps({
      fetchExecution: vi.fn(async () => makeView({ session_code: null, status: 'completed' })),
    })
    await resolveDeeplink({ executionId: 'exc_1' }, deps)
    expect(deps.focusSession).not.toHaveBeenCalled()
    expect(deps.openDetail).toHaveBeenCalledWith('exc_1', undefined)
  })

  it('② opens the detail page (never dead-ends) when the fetch fails', async () => {
    const deps = makeDeps({
      fetchExecution: vi.fn(async () => { throw new Error('offline') }),
    })
    await resolveDeeplink({ executionId: 'exc_1' }, deps)
    expect(deps.openDetail).toHaveBeenCalledWith('exc_1', undefined)
  })

  it('② opens the detail page when the execution is unknown (404 → null)', async () => {
    const deps = makeDeps({ fetchExecution: vi.fn(async () => null) })
    await resolveDeeplink({ executionId: 'exc_gone' }, deps)
    expect(deps.openDetail).toHaveBeenCalledWith('exc_gone', undefined)
  })

  it('ignores an empty executionId', async () => {
    const deps = makeDeps()
    await resolveDeeplink({ executionId: '' }, deps)
    expect(deps.fetchExecution).not.toHaveBeenCalled()
    expect(deps.openDetail).not.toHaveBeenCalled()
  })
})

describe('registerDeeplinkResolver', () => {
  const original = globalThis.window
  afterEach(() => {
    globalThis.window = original
  })
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('subscribes to onDeeplinkNavigate and returns its unsubscribe', () => {
    const unsub = vi.fn()
    const onDeeplinkNavigate = vi.fn(() => unsub)
    // @ts-expect-error minimal window stub
    globalThis.window = { electronAPI: { onDeeplinkNavigate } }
    const cleanup = registerDeeplinkResolver(makeDeps())
    expect(onDeeplinkNavigate).toHaveBeenCalledTimes(1)
    cleanup()
    expect(unsub).toHaveBeenCalledTimes(1)
  })

  it('resolves each broadcast through the injected deps', async () => {
    let captured: ((p: { executionId: string; host?: string }) => void) | undefined
    const onDeeplinkNavigate = vi.fn((cb) => { captured = cb; return () => {} })
    // @ts-expect-error minimal window stub
    globalThis.window = { electronAPI: { onDeeplinkNavigate } }
    const deps = makeDeps({
      fetchExecution: vi.fn(async () => makeView({ session_code: null })),
    })
    registerDeeplinkResolver(deps)
    captured?.({ executionId: 'exc_9' })
    // allow the async resolve microtasks to flush
    await Promise.resolve()
    await Promise.resolve()
    expect(deps.openDetail).toHaveBeenCalledWith('exc_9', undefined)
  })

  it('is a no-op (safe cleanup) when electron is absent', () => {
    globalThis.window = {} as Window & typeof globalThis
    const cleanup = registerDeeplinkResolver(makeDeps())
    expect(() => cleanup()).not.toThrow()
  })
})
