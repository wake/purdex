import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { applySectionToStores, readSettingsSources } from './profile/apply-to-stores'
import { masterWorkspaceIds } from './profile/master-world'
import { buildSettingsSection } from './profile/sections'
import { identityOfSync, syncIdOfSync } from './profile/host-identity'
import type { SettingsPayload } from './profile/types'
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

// host ownership H2c-3 T3: the label reads the look store, so a synced rename must re-notify New Tab.
describe('createHostSessionProviderSource — labels follow the look store (H2c-3 T3)', () => {
  const AIR = syncIdOfSync('d1_air')

  beforeEach(() => {
    useHostStore.setState({
      hosts: { h1: host('h1', 'mlab', 0), h2: { ...host('h2', 'air', 1), daemonId: 'd1_air' } },
      hostOrder: ['h1', 'h2'],
    })
    useHostLookStore.setState({ looks: {} })
  })

  it('a look-store write to the host\'s entry notifies; getProviders() then carries the new name', () => {
    const src = createHostSessionProviderSource()
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
    const src = createHostSessionProviderSource()
    const listener = vi.fn()
    const unsub = src.subscribe(listener)
    expect(await applySectionToStores('settings', payload, { masterHostId: 'h1' })).toMatchObject({ ok: true })
    unsub()
    expect(listener).toHaveBeenCalled()
    expect(src.getProviders()[1].labelParams).toEqual({ host: 'air-synced' })
  })
})
