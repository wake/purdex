// spa/src/lib/profile/apply-to-stores.pool-hosts.test.ts — `purdex-pool-hosts` through the settings apply (monitoring tabs
// spec A3, MON-2a task 2): keys are wwd host_ids and pass through unchanged, bad entries are sanitised on apply, an older
// (ordinal-12) payload clears the store.
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { hashSection } from './hash'
import { masterWorkspaceIds } from './master-world'
import { buildSettingsSection } from './sections'
import type { SettingsPayload } from './types'
import { applySectionToStores, readSettingsSources } from './apply-to-stores'
import { identityOfSync } from './host-identity'

const M = 'host-master'
const DAEMON = 'mini-lab:278cbm'
const A = '82b11c521df16c12'
const B = 'a0b1c2d3e4f50617'
const ctx = { masterHostId: M }

const hostCfg = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over })

const identity = () => identityOfSync(useHostStore.getState().hosts)
const built = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds() ?? new Set(), identity()))) as SettingsPayload

beforeEach(() => {
  useHostStore.setState({ hosts: { [M]: hostCfg(M, { daemonId: DAEMON }) }, hostOrder: [M], activeHostId: M, devHostId: null, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  usePoolHostsStore.setState({ order: [], looks: {} })
})

describe('applySectionToStores — settings: purdex-pool-hosts', () => {
  it('collect → apply: the payload lands as sent and the rebuilt hash is the payload\'s', async () => {
    const payload: SettingsPayload = {
      ...built(),
      'purdex-pool-hosts': { order: [B, A], looks: { [A]: { icon: 'Cpu', indicator: { shape: 'bars', data: ['cpu', 'mem'] } } } },
    }
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(outcome).toMatchObject({ ok: true })
    expect(usePoolHostsStore.getState().order).toEqual([B, A])
    expect(buildSettingsSection(readSettingsSources(), masterWorkspaceIds() ?? new Set(), identity())).toEqual(payload)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('an ordinal-12 payload (no purdex-pool-hosts) clears the store', async () => {
    usePoolHostsStore.setState({ order: [A], looks: { [A]: { icon: 'Cpu' } } })
    const legacy = built()
    delete legacy['purdex-pool-hosts']
    await applySectionToStores('settings', legacy, ctx)
    expect(usePoolHostsStore.getState().order).toEqual([])
    expect(usePoolHostsStore.getState().looks).toEqual({})
  })

  it('an empty store builds { order: [], looks: {} } — present, so only an older client\'s payload lacks it', () => {
    expect(built()['purdex-pool-hosts']).toEqual({ order: [], looks: {} })
  })

  it('bad entries are sanitised on apply; a key that looks like a local host id is NOT translated', async () => {
    const payload: SettingsPayload = {
      ...built(),
      'purdex-pool-hosts': { order: ['mlab', DAEMON, A], looks: { mlab: { icon: 'Cpu' }, [A]: { icon: 'Cpu', name: 'x' } } },
    }
    await applySectionToStores('settings', payload, ctx)
    expect(usePoolHostsStore.getState().order).toEqual([A])
    expect(usePoolHostsStore.getState().looks).toEqual({ [A]: { icon: 'Cpu' } })
  })
})
