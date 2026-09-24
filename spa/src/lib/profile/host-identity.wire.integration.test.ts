// spa/src/lib/profile/host-identity.wire.integration.test.ts — host-sync-identity
// PR 2, end to end through the REAL builders (collector's `buildSectionPayload`),
// `applySectionToStores` and the zustand stores: two devices that added the same
// daemon each under its OWN local id — the scenario that shipped broken.
//
// Host ownership H3 (spec §5.1, §5.2; plan §0.4): the host list is per device, so
// no `hosts` section is built or applied and no alias is LEARNED any more. What a
// pre-H3 `hosts` apply left behind — `HostConfig.syncAliases` — is seeded directly,
// and every legacy (local-id) reference must still resolve through it.
//
// One process plays both devices: a device's state is set into the stores, its
// sections are built, then the stores are reset to the other device. Sections are
// applied in the executor's order (workspaces → settings → tabs.*).
import { beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { STORAGE_KEYS } from '../storage'
import { applySectionToStores } from './apply-to-stores'
import { buildSectionPayload } from './collector'
import { hashSection } from './hash'
import { identityOfSync, syncIdOfSync } from './host-identity'
import { buildHostsSection, buildSettingsSection, buildTabsSection } from './sections'
import type { ProfileSectionKey } from './types'

const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)
const A_ID = 'aaaaaa'
const B_ID = 'bbbbbb'
const ONLY_B = 'onlyb1'
const KEYS: ProfileSectionKey[] = ['workspaces', 'settings', 'tabs.w1']

type Presets = Record<'3col' | '2col' | '1col', { enabled: boolean; columns: string[][] }>

function mlab(id: string, extra: Partial<HostConfig> = {}): HostConfig {
  return { id, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0, daemonId: DAEMON, ...extra }
}

/** A tab whose panes name `hostId` in all three host-bearing fields. */
function tabOn(hostId: string): Tab {
  const leaf = (id: string, content: object): PaneLayout => ({ type: 'leaf', pane: { id, content } }) as PaneLayout
  const layout: PaneLayout = {
    type: 'split', id: 's1', direction: 'h', sizes: [40, 30, 30],
    children: [
      leaf('p-tmux', { kind: 'tmux-session', hostId, sessionCode: 'c1', mode: 'terminal', cachedName: 'one', tmuxInstance: 'i1' }),
      leaf('p-edit', { kind: 'editor', source: { type: 'daemon', hostId }, filePath: '/etc/hosts' }),
      leaf('p-exec', { kind: 'execution', executionId: 'x1', host: hostId }),
    ],
  }
  return { id: 't1', pinned: false, locked: false, createdAt: 1, layout }
}

function presetsOn(hostId: string): Presets {
  return {
    '3col': { enabled: true, columns: [[`sessions:${hostId}`, 'files'], [`headless:${hostId}`], []] },
    '2col': { enabled: true, columns: [[`sessions:${hostId}`], []] },
    '1col': { enabled: true, columns: [[`headless:${hostId}`]] },
  }
}

interface Device {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
  workspaces: Workspace[]
  tabs: Record<string, Tab>
  hostSettings: Record<string, unknown>
  presets: Presets
}

function load(d: Device): void {
  useHostStore.setState({ hosts: d.hosts, hostOrder: d.hostOrder, activeHostId: d.hostOrder[0] ?? null, devHostId: null, runtime: {} })
  useTabStore.setState({ tabs: d.tabs, tabOrder: Object.keys(d.tabs), activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: d.workspaces, activeWorkspaceId: d.workspaces[0]?.id ?? null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useHostSettingsStore.setState({ hosts: d.hostSettings as never })
  useNewTabLayoutStore.setState({ presets: d.presets })
}

const deviceA = (): Device => ({
  hosts: { [A_ID]: mlab(A_ID) },
  hostOrder: [A_ID],
  workspaces: [{ id: 'w1', name: 'W1', tabs: ['t1'], activeTabId: 't1' }],
  tabs: { t1: tabOn(A_ID) },
  hostSettings: { [A_ID]: { files: { root: '/srv' } } },
  presets: presetsOn(A_ID),
})

/** B added the same daemon itself (another local id) and has a host A never had, whose daemon it never reached. */
const deviceB = (): Device => ({
  hosts: { [B_ID]: mlab(B_ID), [ONLY_B]: { id: ONLY_B, name: 'lab2', ip: '10.9.9.9', port: 7860, token: 't2', order: 1 } },
  hostOrder: [B_ID, ONLY_B],
  workspaces: [],
  tabs: {},
  hostSettings: {},
  presets: presetsOn(B_ID),
})

/** What the collector would report now, per section: payload and hash. */
async function buildAll(keys: ProfileSectionKey[] = KEYS): Promise<Record<string, { payload: unknown; hash: string }>> {
  const out: Record<string, { payload: unknown; hash: string }> = {}
  for (const key of keys) {
    const built = buildSectionPayload(key)
    if (built === null || built.payload === null) throw new Error(`${key}: nothing built`)
    out[key] = { payload: JSON.parse(JSON.stringify(built.payload)) as unknown, hash: await hashSection(built.payload) }
  }
  return out
}

/** Applies each section as a pull would (the SOT's payload), in the executor's order; returns each outcome. */
async function applyAll(sot: Record<string, { payload: unknown }>, master: string, keys: ProfileSectionKey[] = KEYS) {
  const outcomes: Record<string, unknown> = {}
  for (const key of keys) outcomes[key] = await applySectionToStores(key, JSON.parse(JSON.stringify(sot[key].payload)), { masterHostId: master })
  return outcomes
}

function panesOf(tabId: string): Array<Record<string, unknown>> {
  const layout = useTabStore.getState().tabs[tabId].layout as Extract<PaneLayout, { type: 'split' }>
  return layout.children.map((c) => (c as Extract<PaneLayout, { type: 'leaf' }>).pane.content as unknown as Record<string, unknown>)
}

beforeEach(() => {
  localStorage.clear()
})

/** What a pre-H3 `hosts` apply left on a device: its host of the daemon remembers these legacy ids. */
function remembering(d: Device, id: string, aliases: string[]): Device {
  return { ...d, hosts: { ...d.hosts, [id]: { ...d.hosts[id], syncAliases: aliases } } }
}

const hostIdsOfPanes = (tabId: string): unknown[] => panesOf(tabId).map((c) => c.hostId ?? (c.source as { hostId?: string } | undefined)?.hostId ?? c.host)

describe('two devices, one daemon, INDEPENDENT local ids (the scenario that shipped broken)', () => {
  it('A builds, B applies: B keeps its id everywhere and its own hosts untouched, nothing is host-removed, no lock; B builds the SAME hashes', async () => {
    load(deviceA())
    const a = await buildAll()
    for (const key of KEYS) expect(JSON.stringify(a[key].payload), key).not.toContain(A_ID) // no local id on the wire

    load(deviceB())
    const hostsBefore = useHostStore.getState()
    const outcomes = await applyAll(a, B_ID)

    // no lock; every apply already holds what the SOT holds (no push back)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true, hash: a[key].hash })

    // hosts: B's own list, as it was — the host only B has stays (the host list is per device)
    expect(useHostStore.getState()).toBe(hostsBefore)

    // tabs: every host-bearing field names B's id; nothing branded host-removed
    const [tmux, editor, exec] = panesOf('t1')
    expect(tmux).toMatchObject({ kind: 'tmux-session', hostId: B_ID })
    expect(tmux).not.toHaveProperty('terminated')
    expect(editor).toMatchObject({ source: { type: 'daemon', hostId: B_ID } })
    expect(exec).toMatchObject({ host: B_ID })

    // settings: host-settings and both preset column kinds name B's id
    expect(useHostSettingsStore.getState().hosts).toEqual({ [B_ID]: { files: { root: '/srv' } } })
    expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn(B_ID))

    // B builds: the very hashes A built — nothing to push
    const b = await buildAll()
    for (const key of KEYS) expect(b[key].hash, key).toBe(a[key].hash)
  })

  it('and back: B edits, A applies B\'s build — A keeps ITS id; A\'s build equals B\'s (no ping-pong either way)', async () => {
    load(deviceA())
    const a = await buildAll()
    load(deviceB())
    await applyAll(a, B_ID)
    useHostSettingsStore.setState({ hosts: { [B_ID]: { files: { root: '/srv/edited-on-b' } } } as never })
    const b = await buildAll()

    load(deviceA())
    const outcomes = await applyAll(b, A_ID)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true, hash: b[key].hash })
    expect(useHostSettingsStore.getState().hosts).toEqual({ [A_ID]: { files: { root: '/srv/edited-on-b' } } })
    expect(panesOf('t1')[0]).toMatchObject({ hostId: A_ID })
    const again = await buildAll()
    for (const key of KEYS) expect(again[key].hash, key).toBe(b[key].hash)
  })
})

describe('transition from ordinal-2 data (local-id keys), with no hosts apply — spec §7, §11.2, §11.7; host ownership plan §0.4', () => {
  /** What an alpha.434 client (no translation) wrote for device A: the same builders with an identity that maps nothing. */
  async function legacyOfA(): Promise<Record<string, { payload: unknown; hash: string }>> {
    load(deviceA())
    const tabsState = useTabStore.getState()
    const ws = useWorkspaceStore.getState().workspaces[0]
    const settings = buildSettingsSection(
      { 'purdex-host-settings': useHostSettingsStore.getState(), 'purdex-newtab-layout': useNewTabLayoutStore.getState() },
      new Set(['w1']),
    )
    const built: Record<string, unknown> = {
      workspaces: buildSectionPayload('workspaces')!.payload,
      settings,
      'tabs.w1': buildTabsSection(ws, tabsState.tabs),
    }
    const out: Record<string, { payload: unknown; hash: string }> = {}
    for (const [key, payload] of Object.entries(built)) out[key] = { payload: JSON.parse(JSON.stringify(payload)) as unknown, hash: await hashSection(payload) }
    expect(JSON.stringify(out['tabs.w1'].payload)).toContain(A_ID) // legacy: A's local id
    return out
  }

  it('B, whose host remembers A\'s id (a pre-H3 apply left it), pulls A\'s ordinal-2 tabs / settings: every reference lands on B\'s host; B\'s next build is canonical — one push each', async () => {
    const legacy = await legacyOfA()
    load(remembering(deviceB(), B_ID, [A_ID]))
    const outcomes = await applyAll(legacy, B_ID)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })

    expect(hostIdsOfPanes('t1')).toEqual([B_ID, B_ID, B_ID])
    expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    expect(useHostSettingsStore.getState().hosts).toEqual({ [B_ID]: { files: { root: '/srv' } } })
    expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn(B_ID))
    expect(Object.keys(useHostStore.getState().hosts)).toEqual([B_ID, ONLY_B]) // nothing learned, nothing removed
    expect(useHostStore.getState().hosts[B_ID].syncAliases).toEqual([A_ID])

    // canonical from now on: the sync id, neither device's local id
    const b = await buildAll()
    expect(JSON.stringify(b['tabs.w1'].payload)).toContain(WIRE)
    expect(JSON.stringify(b['tabs.w1'].payload)).not.toContain(A_ID)
    expect(JSON.stringify(b['tabs.w1'].payload)).not.toContain(B_ID)
    for (const key of ['settings', 'tabs.w1'] as const) {
      expect(outcomes[key], key).not.toMatchObject({ ok: true, hash: legacy[key].hash }) // → one push each
      expect(outcomes[key], key).toMatchObject({ ok: true, hash: b[key].hash }) // … of what B now builds
    }
  })

  it('A (upgraded too) then pulls B\'s canonical profile: A keeps its id, and its build equals B\'s — the transition ends in one round', async () => {
    const legacy = await legacyOfA()
    load(remembering(deviceB(), B_ID, [A_ID]))
    await applyAll(legacy, B_ID)
    const b = await buildAll()

    load(deviceA())
    const outcomes = await applyAll(b, A_ID)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true, hash: b[key].hash })
    expect(hostIdsOfPanes('t1')).toEqual([A_ID, A_ID, A_ID])
    const a = await buildAll()
    for (const key of KEYS) expect(a[key].hash, key).toBe(b[key].hash)
  })

  describe('INTERRUPTED at a write boundary (§11.7): some sections canonical, the rest still legacy', () => {
    const third = (): Device => remembering({ ...deviceB(), hosts: { cccccc: mlab('cccccc') }, hostOrder: ['cccccc'], presets: presetsOn('cccccc') }, 'cccccc', [A_ID, B_ID])

    it.each([
      ['tabs not pushed, settings not pushed', [] as ProfileSectionKey[]],
      ['settings pushed, tabs not', ['settings'] as ProfileSectionKey[]],
      ['tabs pushed, settings not', ['tabs.w1'] as ProfileSectionKey[]],
    ])('a THIRD device (its own id for the daemon, A\'s and B\'s remembered) pulls it — %s: every legacy id resolves through the alias', async (_name, canonicalKeys) => {
      const sot = await legacyOfA()
      // B's canonical builds of the sections that did make it
      if (canonicalKeys.length > 0) {
        load(remembering(deviceB(), B_ID, [A_ID]))
        await applyAll(sot, B_ID)
        Object.assign(sot, await buildAll(canonicalKeys))
      }
      load(third())
      const outcomes = await applyAll(sot, 'cccccc')
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(hostIdsOfPanes('t1')).toEqual(['cccccc', 'cccccc', 'cccccc'])
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
      expect(useHostSettingsStore.getState().hosts).toEqual({ cccccc: { files: { root: '/srv' } } })
      expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn('cccccc'))
    })

    it('A itself pulls it (the legacy id is its own): everything lands on A\'s host', async () => {
      const sot = await legacyOfA()
      load(deviceA())
      const outcomes = await applyAll(sot, A_ID)
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(hostIdsOfPanes('t1')).toEqual([A_ID, A_ID, A_ID])
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    })

    it('ACROSS A RESTART: the alias is persisted (syncAliases) — read back by the store\'s own merge, it still resolves', async () => {
      const sot = await legacyOfA()
      load(third())
      // the reload: in-memory state gone, the persisted host list read back through the store's own merge
      const persisted = localStorage.getItem(STORAGE_KEYS.HOSTS)
      expect(persisted).toContain('syncAliases')
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      localStorage.setItem(STORAGE_KEYS.HOSTS, persisted as string)
      await useHostStore.persist.rehydrate()
      expect(useHostStore.getState().hosts.cccccc.syncAliases).toEqual([A_ID, B_ID])
      const outcomes = await applyAll(sot, 'cccccc')
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(panesOf('t1')[0]).toMatchObject({ hostId: 'cccccc' })
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    })
  })

  it('a `hosts` payload arriving now teaches nothing: its canonical row lists a NEW alias, and syncAliases stay as they were', async () => {
    // A's canonical hosts row as an older client still writes it: it lists `zzzzzz`, an id B has never seen
    load(remembering(deviceA(), A_ID, ['zzzzzz']))
    const hosts = JSON.parse(JSON.stringify(buildHostsSection(useHostStore.getState(), identityOfSync(useHostStore.getState().hosts)))) as { hosts: Record<string, { aliases?: string[] }> }
    expect(hosts.hosts[WIRE].aliases).toEqual([A_ID, 'zzzzzz'])

    load(remembering(deviceB(), B_ID, [A_ID]))
    const before = useHostStore.getState()
    expect(await applySectionToStores('hosts', hosts, { masterHostId: B_ID })).toMatchObject({ ok: false, reason: 'invalid', code: 'unknown-section' })
    expect(useHostStore.getState()).toBe(before)
    expect(useHostStore.getState().hosts[B_ID].syncAliases).toEqual([A_ID])
  })
})
