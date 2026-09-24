import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { applySectionToStores, readSettingsSources } from './profile/apply-to-stores'
import { masterWorkspaceIds } from './profile/master-world'
import { buildSettingsSection } from './profile/sections'
import { identityOfSync, syncIdOfSync } from './profile/host-identity'
import type { SettingsPayload } from './profile/types'
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

// host ownership H2c-3 T3: the label reads the look store, so a synced rename must re-notify New Tab.
describe('createHeadlessProviderSource — labels follow the look store (H2c-3 T3)', () => {
  const AIR = syncIdOfSync('d1_air')

  beforeEach(() => {
    useHostStore.setState({
      hosts: { h1: host('h1', 'mlab', 0), h2: { ...host('h2', 'air', 1), daemonId: 'd1_air' } },
      hostOrder: ['h1', 'h2'],
    })
    useHostLookStore.setState({ looks: {} })
  })

  it('a look-store write to the host\'s entry notifies; getProviders() then carries the new name', () => {
    const src = createHeadlessProviderSource()
    expect(src.getProviders()[1].labelParams).toEqual({ host: 'air' })
    const listener = vi.fn()
    const unsub = src.subscribe(listener)
    useHostLookStore.getState().putLook(AIR, { name: 'air-renamed' })
    expect(listener).toHaveBeenCalledTimes(1)
    expect(src.getProviders()[1].labelParams).toEqual({ host: 'air-renamed' })
    unsub()
    useHostLookStore.getState().putLook(AIR, { name: 'again' })
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('an applied settings payload whose looks rename the host\'s d1_ entry notifies and changes the label', async () => {
    const ids = masterWorkspaceIds()
    if (ids === null) throw new Error('the master world is unsettled')
    const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), ids, identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
    const payload = { ...now, 'purdex-host-looks': { looks: { [AIR]: { name: 'air-synced' } } } } as SettingsPayload
    const src = createHeadlessProviderSource()
    const listener = vi.fn()
    const unsub = src.subscribe(listener)
    expect(await applySectionToStores('settings', payload, { masterHostId: 'h1' })).toMatchObject({ ok: true })
    unsub()
    expect(listener).toHaveBeenCalled()
    expect(src.getProviders()[1].labelParams).toEqual({ host: 'air-synced' })
  })
})
