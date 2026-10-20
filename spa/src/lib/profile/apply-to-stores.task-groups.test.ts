// spa/src/lib/profile/apply-to-stores.task-groups.test.ts — `purdex-task-groups` through the settings apply (task groups
// spec §4.1, TG-1a-1 task 3): wire ids back to local, an unknown wire id kept, an older (ordinal-11) payload clears the store.
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { hashSection } from './hash'
import { masterWorkspaceIds } from './master-world'
import { buildSettingsSection } from './sections'
import type { SettingsPayload } from './types'
import { applySectionToStores, readSettingsSources } from './apply-to-stores'
import { identityOfSync, syncIdOfSync } from './host-identity'

const M = 'host-master'
const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)
const UNKNOWN = syncIdOfSync('other-lab:abc123')
const ctx = { masterHostId: M }

const hostCfg = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over })

const identity = () => identityOfSync(useHostStore.getState().hosts)
const built = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds() ?? new Set(), identity()))) as SettingsPayload

beforeEach(() => {
  useHostStore.setState({ hosts: { [M]: hostCfg(M, { daemonId: DAEMON }) }, hostOrder: [M], activeHostId: M, devHostId: null, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useManualGroupsStore.setState({ order: [], groups: {} })
})

describe('applySectionToStores — settings: purdex-task-groups', () => {
  it('collect → apply: wire member hosts become local, an unknown wire host is kept, and the rebuilt hash is the payload\'s', async () => {
    const payload: SettingsPayload = {
      ...built(),
      'purdex-task-groups': {
        order: ['aaaaaaaa'],
        groups: { aaaaaaaa: { name: 'A', color: '#112233', members: [{ host: UNKNOWN, root: 'r0' }, { host: WIRE, root: 'r1' }].sort((x, y) => (x.host < y.host ? -1 : 1)), createdAt: 5 } },
      },
    }
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(outcome).toMatchObject({ ok: true })
    const members = useManualGroupsStore.getState().groups.aaaaaaaa.members
    expect(members).toHaveLength(2)
    expect(members).toContainEqual({ host: M, root: 'r1' })
    expect(members).toContainEqual({ host: UNKNOWN, root: 'r0' })
    expect(buildSettingsSection(readSettingsSources(), masterWorkspaceIds() ?? new Set(), identity())).toEqual(payload)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('an ordinal-11 payload (no purdex-task-groups) clears the store', async () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', { name: 'A', members: [{ host: M, root: 'r1' }], createdAt: 1 })
    const legacy = built()
    delete legacy['purdex-task-groups']
    await applySectionToStores('settings', legacy, ctx)
    expect(useManualGroupsStore.getState().order).toEqual([])
    expect(useManualGroupsStore.getState().groups).toEqual({})
  })

  it('an empty store builds { order: [], groups: {} } — present, so only an older client\'s payload lacks it', () => {
    expect(built()['purdex-task-groups']).toEqual({ order: [], groups: {} })
  })

  it('an applied payload that lists a group twice in a member list leaves one member (the store merge)', async () => {
    const payload: SettingsPayload = {
      ...built(),
      'purdex-task-groups': { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'A', members: [{ host: WIRE, root: 'r1' }, { host: WIRE, root: 'r1' }], createdAt: 1 } } },
    }
    await applySectionToStores('settings', payload, ctx)
    expect(useManualGroupsStore.getState().groups.aaaaaaaa.members).toEqual([{ host: M, root: 'r1' }])
  })
})
