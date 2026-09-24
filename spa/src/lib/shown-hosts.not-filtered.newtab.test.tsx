// spa/src/lib/shown-hosts.not-filtered.newtab.test.tsx — hidden ≠ absent, New Tab registration and layout (host
// ownership plan H2d-5 T4, §0.21 table: "New Tab `sessions:` / `headless:` blocks of X — not rendered; provider stays
// REGISTERED, the column stays in every preset and in `knownIds`"). Hiding a host only stops `NewTabPage` from
// RENDERING its blocks (H2d-3); below the page nothing changes: the provider sources still produce air26's
// `sessions:` / `headless:` providers, `getStaleNewTabProviderIds` never reports them, `useNewTabBootstrap` places them
// on a fresh layout and keeps them in every preset and in `knownIds`, and the next `settings` build carries the layout
// unchanged. Each case observes air26 HIDDEN (`ids: [<mlab wire>]`) and SHOWN (`ids: [<mlab wire>, <air26 wire>]`)
// and requires the two observations to be identical.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useNewTabBootstrap } from '../hooks/useNewTabBootstrap'
import {
  clearNewTabRegistry,
  getNewTabProviders,
  getReadyNewTabProviders,
  getStaleNewTabProviderIds,
  registerNewTabProviderSource,
} from './new-tab-registry'
import { createHostSessionProviderSource } from './session-new-tab-providers'
import { createHeadlessProviderSource } from './headless-new-tab-providers'
import { identityOfSync, syncIdOfSync } from './profile/host-identity'
import { readSettingsSources } from './profile/apply-to-stores'
import { masterWorkspaceIds } from './profile/master-world'
import { buildSettingsSection } from './profile/sections'
import { isRefShownNow } from './shown-hosts'

const MLAB = 'h-mlab'
const AIR = 'h-air26'
const MLAB_WIRE = syncIdOfSync('mini-lab:27bbbb')
const AIR_WIRE = syncIdOfSync('air-lab:26aaaa')
const AIR_IDS = [`sessions:${AIR}`, `headless:${AIR}`]
const ALL_IDS = [`sessions:${MLAB}`, `headless:${MLAB}`, ...AIR_IDS]

type Setting = 'hidden' | 'shown'
const IDS: Record<Setting, string[]> = { hidden: [MLAB_WIRE], shown: [MLAB_WIRE, AIR_WIRE] }

const host = (id: string, ip: string, daemonId: string, order: number): HostConfig =>
  ({ id, name: id, ip, port: 7860, token: 'tok', order, daemonId })

function arrange(setting: Setting): void {
  localStorage.clear()
  clearNewTabRegistry()
  useHostStore.setState({
    hosts: { [MLAB]: host(MLAB, '100.64.0.2', 'mini-lab:27bbbb', 0), [AIR]: host(AIR, '100.64.0.4', 'air-lab:26aaaa', 1) },
    hostOrder: [MLAB, AIR],
    activeHostId: MLAB,
    runtime: {},
  })
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useShownHostsStore.setState({ ids: IDS[setting] })
  registerNewTabProviderSource(createHostSessionProviderSource())
  registerNewTabProviderSource(createHeadlessProviderSource())
  // The premises: the setting really hides / shows air26; the sources are ready (the host list is hydrated).
  expect(isRefShownNow(AIR)).toBe(setting === 'shown')
  expect(isRefShownNow(MLAB)).toBe(true)
  expect(useHostStore.persist.hasHydrated()).toBe(true)
}

function both<T>(observe: (setting: Setting) => T): { hidden: T; shown: T } {
  return { hidden: observe('hidden'), shown: observe('shown') }
}

const layoutNow = () => {
  const { presets, knownIds } = useNewTabLayoutStore.getState()
  return { presets: JSON.parse(JSON.stringify(presets)) as typeof presets, knownIds: [...knownIds] }
}

beforeEach(() => clearNewTabRegistry())

afterEach(() => {
  clearNewTabRegistry()
  useShownHostsStore.setState({ ids: [] })
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useHostStore.getState().reset()
})

describe('hidden ≠ absent — New Tab registration and layout (H2d-5 T4)', () => {
  it('the provider sources still return air26\'s sessions: / headless: providers — identical hidden and shown', () => {
    const { hidden, shown } = both((setting) => {
      arrange(setting)
      return {
        all: getNewTabProviders().map((p) => p.id),
        ready: getReadyNewTabProviders().map((p) => p.id),
      }
    })
    for (const id of ALL_IDS) {
      expect(hidden.all).toContain(id)
      expect(hidden.ready).toContain(id)
    }
    expect(hidden).toEqual(shown)
  })

  it('`getStaleNewTabProviderIds` does not report air26\'s ids — identical hidden and shown', () => {
    const { hidden, shown } = both((setting) => {
      arrange(setting)
      return getStaleNewTabProviderIds([...ALL_IDS])
    })
    expect(hidden).toEqual([])
    expect(hidden).toEqual(shown)
  })

  it('`useNewTabBootstrap` on a fresh layout places air26\'s columns and knows them — identical hidden and shown', () => {
    const { hidden, shown } = both((setting) => {
      arrange(setting)
      renderHook(() => useNewTabBootstrap()).unmount()
      return layoutNow()
    })
    for (const id of AIR_IDS) {
      expect(hidden.knownIds).toContain(id)
      expect(hidden.presets['1col'].columns.flat()).toContain(id)
    }
    expect(hidden).toEqual(shown)
  })

  it('`useNewTabBootstrap` keeps air26\'s columns in every preset and in knownIds — identical hidden and shown', () => {
    const seeded = {
      presets: {
        '3col': { enabled: true, columns: [[`sessions:${MLAB}`], [`sessions:${AIR}`], [`headless:${AIR}`, `headless:${MLAB}`]] },
        '2col': { enabled: true, columns: [[`sessions:${MLAB}`, `headless:${AIR}`], [`sessions:${AIR}`, `headless:${MLAB}`]] },
        '1col': { enabled: true, columns: [[`headless:${AIR}`, `sessions:${MLAB}`, `sessions:${AIR}`, `headless:${MLAB}`]] },
      },
      knownIds: [...ALL_IDS],
    }
    const { hidden, shown } = both((setting) => {
      arrange(setting)
      useNewTabLayoutStore.setState(JSON.parse(JSON.stringify(seeded)))
      renderHook(() => useNewTabBootstrap()).unmount()
      return layoutNow()
    })
    expect(hidden.presets).toEqual(seeded.presets)
    expect(hidden.knownIds).toEqual(seeded.knownIds)
    expect(hidden).toEqual(shown)
  })

  it('the next `settings` build carries the New Tab layout with air26\'s columns unchanged — identical hidden and shown', () => {
    const { hidden, shown } = both((setting) => {
      arrange(setting)
      renderHook(() => useNewTabBootstrap()).unmount()
      const ids = masterWorkspaceIds()
      expect(ids).not.toBeNull()
      const built = buildSettingsSection(readSettingsSources(), ids!, identityOfSync(useHostStore.getState().hosts)) as Record<string, unknown>
      return JSON.parse(JSON.stringify(built['purdex-newtab-layout'])) as { presets: Record<string, { columns: string[][] }> }
    })
    // Carried in WIRE form (`sessions:d1_…`, plan §0.12): air26's blocks travel whatever this workbench shows.
    const flat = Object.values(hidden.presets).flatMap((p) => p.columns.flat())
    expect(flat).toContain(`sessions:${AIR_WIRE}`)
    expect(flat).toContain(`headless:${AIR_WIRE}`)
    expect(hidden).toEqual(shown)
  })
})
