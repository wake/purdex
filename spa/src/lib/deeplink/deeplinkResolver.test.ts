import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  resolveDeeplink,
  registerDeeplinkResolver,
  type ResolveDeeplinkDeps,
} from './deeplinkResolver'

function makeDeps(over: Partial<ResolveDeeplinkDeps> = {}) {
  const deps: ResolveDeeplinkDeps = {
    resolveHostId: vi.fn(() => 'host-a'),
    openDetail: vi.fn(),
    ...over,
  }
  return deps
}

describe('resolveDeeplink', () => {
  it('resolves the host hint and opens the execution detail pane', async () => {
    const deps = makeDeps()
    await resolveDeeplink({ executionId: 'exc_1', host: 'h9' }, deps)
    expect(deps.resolveHostId).toHaveBeenCalledWith('h9')
    expect(deps.openDetail).toHaveBeenCalledWith('exc_1', 'host-a')
  })

  it('ignores an empty executionId', async () => {
    const deps = makeDeps()
    await resolveDeeplink({ executionId: '' }, deps)
    expect(deps.resolveHostId).not.toHaveBeenCalled()
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
    const deps = makeDeps()
    registerDeeplinkResolver(deps)
    captured?.({ executionId: 'exc_9' })
    // allow the async resolve microtasks to flush
    await Promise.resolve()
    await Promise.resolve()
    expect(deps.openDetail).toHaveBeenCalledWith('exc_9', 'host-a')
  })

  it('is a no-op (safe cleanup) when electron is absent', () => {
    globalThis.window = {} as Window & typeof globalThis
    const cleanup = registerDeeplinkResolver(makeDeps())
    expect(() => cleanup()).not.toThrow()
  })
})
