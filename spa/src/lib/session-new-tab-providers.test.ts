import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  createHostSessionProviderSource,
  sessionsProviderId,
} from './session-new-tab-providers'

vi.mock('../hooks/useSessionWatch', () => ({ useSessionWatch: vi.fn() }))

const host = (id: string, name: string, order: number) => ({ id, name, ip: '1', port: 7860, order })

beforeEach(() => {
  useHostStore.setState({
    hosts: { h1: host('h1', 'mlab', 0), h2: host('h2', 'air', 1) },
    hostOrder: ['h1', 'h2'],
    activeHostId: 'h1',
  })
})

describe('createHostSessionProviderSource', () => {
  it('yields one sessions:<hostId> provider per host in hostOrder', () => {
    const src = createHostSessionProviderSource()
    const ps = src.getProviders()
    expect(ps.map((p) => p.id)).toEqual(['sessions:h1', 'sessions:h2'])
    expect(sessionsProviderId('h1')).toBe('sessions:h1')
    expect(ps[1].label).toBe('session.provider_label_host')
    expect(ps[1].labelParams).toEqual({ host: 'air' })
  })

  it('skips hostOrder entries with no host record', () => {
    useHostStore.setState({ hostOrder: ['h1', 'ghost', 'h2'] })
    expect(createHostSessionProviderSource().getProviders().map((p) => p.id)).toEqual(['sessions:h1', 'sessions:h2'])
  })

  it('keeps a stable component identity per host across calls', () => {
    const src = createHostSessionProviderSource()
    const a = src.getProviders()[0].component
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: host('h1', 'renamed', 0) } })
    expect(src.getProviders()[0].component).toBe(a)
    expect(src.getProviders()[1].component).not.toBe(a)
  })

  it('migrates the legacy sessions id to every current host block, in host order', () => {
    expect(createHostSessionProviderSource().migrations?.()).toEqual([
      { from: 'sessions', to: ['sessions:h1', 'sessions:h2'] },
    ])
  })

  it('owns the legacy sessions id and every sessions:* id', () => {
    const src = createHostSessionProviderSource()
    expect(src.ownsId('sessions')).toBe(true)
    expect(src.ownsId('sessions:whatever')).toBe(true)
    expect(src.ownsId('editor')).toBe(false)
  })

  it('retains every sessions:<id> column (a host not on this device is kept), but not the legacy sessions id', () => {
    const src = createHostSessionProviderSource()
    expect(src.retainsId?.('sessions:whatever')).toBe(true)
    expect(src.retainsId?.('sessions:d1_unknown')).toBe(true)
    expect(src.retainsId?.('sessions')).toBe(false)
    expect(src.retainsId?.('headless:h1')).toBe(false)
  })

  it('is ready only once the host store has hydrated, and notifies on hydration finish', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostStore.persist, 'hasHydrated').mockReturnValue(false)
    const onFinish = vi.spyOn(useHostStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostStore.getState())
      return () => { finish = undefined }
    })
    try {
      const src = createHostSessionProviderSource()
      expect(src.isReady?.()).toBe(false)
      const listener = vi.fn()
      const unsub = src.subscribe(listener)
      hydrated.mockReturnValue(true)
      finish?.()
      expect(src.isReady?.()).toBe(true)
      expect(listener).toHaveBeenCalledTimes(1)
      unsub()
      expect(finish).toBeUndefined()
    } finally {
      hydrated.mockRestore()
      onFinish.mockRestore()
    }
  })

  it('notifies subscribers when hosts or hostOrder change, not on runtime churn', () => {
    const src = createHostSessionProviderSource()
    const listener = vi.fn()
    const unsub = src.subscribe(listener)
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(listener).not.toHaveBeenCalled()
    useHostStore.setState({ hostOrder: ['h2', 'h1'] })
    expect(listener).toHaveBeenCalledTimes(1)
    unsub()
    useHostStore.setState({ hostOrder: ['h1'] })
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
