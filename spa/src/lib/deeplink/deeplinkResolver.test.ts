import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  openExecutionDetailTab,
  resolveDeeplink,
  registerDeeplinkResolver,
  type ResolveDeeplinkDeps,
} from './deeplinkResolver'
import { resolveExecutionHostId } from '../nex/resolve-host'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { getPrimaryPane } from '../pane-tree'
import { syncIdOfSync } from '../profile/host-identity'

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

  it('with the real resolveExecutionHostId, an unknown host hint passes through verbatim (spec §4.3.2 step 5)', async () => {
    const deps = makeDeps({ resolveHostId: resolveExecutionHostId })
    await resolveDeeplink({ executionId: 'exc_1', host: 'unknown-host' }, deps)
    expect(deps.openDetail).toHaveBeenCalledWith('exc_1', 'unknown-host')
  })
})

// Host ownership H2d-3 T4 — a deep link naming a host that is not shown in this workbench (hidden, or a ref that is
// neither a local host nor listed) lands on the Hosts page: no execution tab. Real stores, default deps.
describe('openExecutionDetailTab / resolveDeeplink on a host not shown (H2d-3)', () => {
  const DAEMON = 'air-lab:26aaaa'
  const X = syncIdOfSync('nowhere:000000')
  const kinds = () => Object.values(useTabStore.getState().tabs).map((t) => getPrimaryPane(t.layout).content.kind)
  const content = () => getPrimaryPane(useTabStore.getState().tabs[useTabStore.getState().activeTabId!].layout).content

  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'H1', ip: '1', port: 1, order: 0, daemonId: DAEMON },
        h2: { id: 'h2', name: 'H2', ip: '2', port: 1, order: 1 },
      },
      hostOrder: ['h1', 'h2'],
      activeHostId: 'h2',
    })
    useShownHostsStore.setState({ ids: ['h2'] }) // h1 hidden
  })

  it('openExecutionDetailTab on a hidden host → the Hosts page on that host, no execution tab', () => {
    openExecutionDetailTab('exc_1', 'h1')
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h1')
  })

  it('resolveDeeplink { host: hidden } and a hostless link whose hostOrder[0] is hidden → the Hosts page, no tab', async () => {
    await resolveDeeplink({ executionId: 'exc_1', host: 'h1' })
    await resolveDeeplink({ executionId: 'exc_2' })
    expect(kinds()).toEqual(['hosts'])
  })

  it('resolveDeeplink { host: d1_X } with d1_X neither local nor listed → the Hosts page, activeHostId unchanged, no tab', async () => {
    await resolveDeeplink({ executionId: 'exc_1', host: X })
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h2')
  })

  it('d1_X listed → the execution tab as today', async () => {
    useShownHostsStore.setState({ ids: ['h2', X] })
    await resolveDeeplink({ executionId: 'exc_1', host: X })
    expect(content()).toEqual({ kind: 'execution', executionId: 'exc_1', host: X })
  })

  it('a shown host (by its d1_ id) → the execution tab as today', async () => {
    useShownHostsStore.setState({ ids: [syncIdOfSync(DAEMON)] })
    await resolveDeeplink({ executionId: 'exc_1', host: 'h1' })
    expect(content()).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
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
