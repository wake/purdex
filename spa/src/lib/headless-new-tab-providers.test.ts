import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  createHeadlessProviderSource,
  headlessProviderId,
} from './headless-new-tab-providers'

const host = (id: string, name: string, order: number) => ({ id, name, ip: '1', port: 7860, order })

beforeEach(() => {
  useHostStore.setState({
    hosts: { h1: host('h1', 'mlab', 0), h2: host('h2', 'air', 1) },
    hostOrder: ['h1', 'h2'],
    activeHostId: 'h1',
  })
})

describe('createHeadlessProviderSource', () => {
  it('yields one headless:<hostId> provider per host in hostOrder, after the sessions block', () => {
    const src = createHeadlessProviderSource()
    expect(src.id).toBe('headless')
    const ps = src.getProviders()
    expect(ps.map((p) => p.id)).toEqual(['headless:h1', 'headless:h2'])
    expect(headlessProviderId('h1')).toBe('headless:h1')
    expect(ps[1].label).toBe('newtab.headless.title')
    expect(ps[1].labelParams).toEqual({ host: 'air' })
    expect(ps.every((p) => p.icon === 'Lightning')).toBe(true)
    expect(ps.every((p) => p.order === 5)).toBe(true)
  })

  it('is owned by the execution module so the module disable path can remove it', () => {
    expect(createHeadlessProviderSource().moduleId).toBe('execution')
  })

  it('skips hostOrder entries with no host record', () => {
    useHostStore.setState({ hostOrder: ['h1', 'ghost', 'h2'] })
    expect(createHeadlessProviderSource().getProviders().map((p) => p.id)).toEqual(['headless:h1', 'headless:h2'])
  })

  it('keeps a stable component identity per host across calls', () => {
    const src = createHeadlessProviderSource()
    const a = src.getProviders()[0].component
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: host('h1', 'renamed', 0) } })
    expect(src.getProviders()[0].component).toBe(a)
    expect(src.getProviders()[1].component).not.toBe(a)
  })

  it('owns every headless:* id and nothing else; declares no migrations', () => {
    const src = createHeadlessProviderSource()
    expect(src.ownsId('headless:whatever')).toBe(true)
    expect(src.ownsId('headless')).toBe(false)
    expect(src.ownsId('sessions:h1')).toBe(false)
    expect(src.migrations).toBeUndefined()
  })

  it('retains every headless:<id> column (a host not on this device is kept), and nothing else', () => {
    const src = createHeadlessProviderSource()
    expect(src.retainsId?.('headless:whatever')).toBe(true)
    expect(src.retainsId?.('headless:d1_unknown')).toBe(true)
    expect(src.retainsId?.('headless')).toBe(false)
    expect(src.retainsId?.('sessions:h1')).toBe(false)
  })

  it('is ready only once the host store has hydrated, and notifies on hydration finish', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostStore.persist, 'hasHydrated').mockReturnValue(false)
    const onFinish = vi.spyOn(useHostStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostStore.getState())
      return () => { finish = undefined }
    })
    try {
      const src = createHeadlessProviderSource()
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
    const src = createHeadlessProviderSource()
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
