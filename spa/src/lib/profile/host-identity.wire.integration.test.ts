// spa/src/lib/profile/host-identity.wire.integration.test.ts — host-sync-identity
// PR 2, end to end through the REAL builders (collector's `buildSectionPayload`),
// `applySectionToStores` and the zustand stores: two devices that added the same
// daemon each under its OWN local id — the scenario that shipped broken (B's pull
// ended `locked:invalid` / `removes-master-host`, every other shared daemon was
// cascaded as removed).
//
// One process plays both devices: a device's state is set into the stores, its
// sections are built, then the stores are reset to the other device. Sections are
// applied in the executor's order (hosts → workspaces → settings → tabs.*).
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
const KEYS: ProfileSectionKey[] = ['hosts', 'workspaces', 'settings', 'tabs.w1']

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

describe('two devices, one daemon, INDEPENDENT local ids (the scenario that shipped broken)', () => {
  it('A builds, B applies: B keeps its id everywhere, nothing is host-removed, no lock, B\'s own extra host goes; B builds the SAME hashes — hosts but for B\'s own alias, agreed after ONE round', async () => {
    load(deviceA())
    const a = await buildAll()
    expect(Object.keys((a.hosts.payload as { hosts: object }).hosts)).toEqual([WIRE]) // no local id on the wire

    load(deviceB())
    const outcomes = await applyAll(a, B_ID)

    // no lock; every apply but hosts already holds what the SOT holds (no push back)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
    for (const key of KEYS.filter((k) => k !== 'hosts')) expect(outcomes[key], key).toEqual({ ok: true, hash: a[key].hash })

    // hosts: B's mlab kept its local id, updated in place; the host only B had is gone (cascade)
    const hs = useHostStore.getState()
    expect(Object.keys(hs.hosts)).toEqual([B_ID])
    expect(hs.hostOrder).toEqual([B_ID])
    expect(hs.hosts[B_ID]).toMatchObject({ id: B_ID, daemonId: DAEMON, ip: '100.64.0.2' })

    // tabs: every host-bearing field names B's id; nothing branded host-removed
    const [tmux, editor, exec] = panesOf('t1')
    expect(tmux).toMatchObject({ kind: 'tmux-session', hostId: B_ID })
    expect(tmux).not.toHaveProperty('terminated')
    expect(editor).toMatchObject({ source: { type: 'daemon', hostId: B_ID } })
    expect(exec).toMatchObject({ host: B_ID })

    // settings: host-settings and both preset column kinds name B's id
    expect(useHostSettingsStore.getState().hosts).toEqual({ [B_ID]: { files: { root: '/srv' } } })
    expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn(B_ID))

    // B builds: the very hashes A built — for hosts, A's row plus B's OWN id as an alias (§11.7: B's ordinal-2-era
    // key must resolve on every device). That is the ONE hosts push this pull costs (the apply reported it).
    const b = await buildAll()
    for (const key of KEYS.filter((k) => k !== 'hosts')) expect(b[key].hash, key).toBe(a[key].hash)
    expect(outcomes.hosts).toEqual({ ok: true, hash: b.hosts.hash })
    const aRow = (a.hosts.payload as { hosts: Record<string, Record<string, unknown>> }).hosts[WIRE]
    const bRow = (b.hosts.payload as { hosts: Record<string, Record<string, unknown>> }).hosts[WIRE]
    expect(aRow.aliases).toEqual([A_ID])
    expect(bRow).toEqual({ ...aRow, aliases: [A_ID, B_ID] })

    // A takes B's push: its build is B's — no second round, either way
    const bHosts = b.hosts
    load(deviceA())
    expect(await applySectionToStores('hosts', JSON.parse(JSON.stringify(bHosts.payload)), { masterHostId: A_ID })).toEqual({ ok: true, hash: bHosts.hash })
    expect((await buildAll(['hosts'])).hosts.hash).toBe(bHosts.hash)
  })

  it('and back: B edits, A applies B\'s build — A keeps ITS id; A\'s build equals B\'s (no ping-pong either way)', async () => {
    load(deviceA())
    const a = await buildAll()
    load(deviceB())
    await applyAll(a, B_ID)
    useHostStore.setState({ hosts: { [B_ID]: { ...useHostStore.getState().hosts[B_ID], name: 'mlab (renamed on B)' } } })
    const b = await buildAll()

    load(deviceA())
    const outcomes = await applyAll(b, A_ID)
    for (const key of KEYS) expect(outcomes[key], key).toEqual({ ok: true, hash: b[key].hash }) // B's row lists A's id already
    expect(useHostStore.getState().hosts[A_ID].name).toBe('mlab (renamed on B)')
    expect(panesOf('t1')[0]).toMatchObject({ hostId: A_ID })
    const again = await buildAll()
    for (const key of KEYS) expect(again[key].hash, key).toBe(b[key].hash)
  })
})

describe('transition from ordinal-2 data (local-id keys) — spec §7, §11.2, §11.6, §11.7', () => {
  /** What an alpha.434 client (no translation) wrote for device A: the same builders with an identity that maps nothing. */
  async function legacyOfA(): Promise<Record<string, { payload: unknown; hash: string }>> {
    load(deviceA())
    const s = useHostStore.getState()
    const none = identityOfSync({})
    const tabsState = useTabStore.getState()
    const ws = useWorkspaceStore.getState().workspaces[0]
    const settings = buildSettingsSection(
      { 'purdex-host-settings': useHostSettingsStore.getState(), 'purdex-newtab-layout': useNewTabLayoutStore.getState() },
      new Set(['w1']),
    )
    const built: Record<string, unknown> = {
      hosts: buildHostsSection(s, none),
      workspaces: buildSectionPayload('workspaces')!.payload,
      settings,
      'tabs.w1': buildTabsSection(ws, tabsState.tabs),
    }
    const out: Record<string, { payload: unknown; hash: string }> = {}
    for (const [key, payload] of Object.entries(built)) out[key] = { payload: JSON.parse(JSON.stringify(payload)) as unknown, hash: await hashSection(payload) }
    expect(Object.keys((out.hosts.payload as { hosts: object }).hosts)).toEqual([A_ID]) // legacy: A's local id
    return out
  }

  it('B pulls A\'s ordinal-2 profile: matched by daemonId, A\'s id becomes an alias, tabs / settings resolve through it; B\'s next build is canonical', async () => {
    const legacy = await legacyOfA()
    load(deviceB())
    // only `settings`' presets are compared below: the legacy settings payload carries two stores only
    const outcomes = await applyAll(legacy, B_ID)
    for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })

    expect(Object.keys(useHostStore.getState().hosts)).toEqual([B_ID])
    expect(useHostStore.getState().hosts[B_ID].syncAliases).toEqual([A_ID])
    expect(panesOf('t1').map((c) => c.hostId ?? (c.source as { hostId?: string } | undefined)?.hostId ?? c.host)).toEqual([B_ID, B_ID, B_ID])
    expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    expect(useHostSettingsStore.getState().hosts).toEqual({ [B_ID]: { files: { root: '/srv' } } })
    expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn(B_ID))

    // canonical from now on: the hosts row is keyed by the sync id and remembers A's id
    const b = await buildAll()
    const hosts = b.hosts.payload as { hosts: Record<string, { aliases?: string[] }> }
    expect(Object.keys(hosts.hosts)).toEqual([WIRE])
    expect(hosts.hosts[WIRE].aliases).toEqual([A_ID, B_ID]) // A's id (matched from) and B's own
    expect(JSON.stringify(b['tabs.w1'].payload)).not.toContain(B_ID)
    for (const key of ['hosts', 'tabs.w1'] as const) expect(outcomes[key], key).not.toEqual({ ok: true, hash: legacy[key].hash }) // → one push each
  })

  it('A (upgraded too) then pulls B\'s canonical profile: A keeps its id, takes the alias, and its build equals B\'s — the transition ends in one round', async () => {
    const legacy = await legacyOfA()
    load(deviceB())
    await applyAll(legacy, B_ID)
    const b = await buildAll()

    load(deviceA())
    const outcomes = await applyAll(b, A_ID)
    for (const key of KEYS) expect(outcomes[key], key).toEqual({ ok: true, hash: b[key].hash })
    expect(useHostStore.getState().hosts[A_ID].syncAliases).toEqual([A_ID, B_ID])
    const a = await buildAll()
    for (const key of KEYS) expect(a[key].hash, key).toBe(b[key].hash)
  })

  describe('INTERRUPTED at a write boundary (§11.7): the hosts row is canonical, the rest is still legacy', () => {
    /** The SOT after B's first reconciliation stopped: B's canonical hosts (with A's id as alias), A's legacy rest. */
    async function interrupted(): Promise<Record<string, { payload: unknown; hash: string }>> {
      const legacy = await legacyOfA()
      load(deviceB())
      await applyAll(legacy, B_ID, ['hosts'])
      const b = await buildAll(['hosts'])
      return { ...legacy, hosts: b.hosts }
    }

    it.each([
      ['tabs not pushed, settings not pushed', [] as ProfileSectionKey[]],
      ['settings pushed, tabs not', ['settings'] as ProfileSectionKey[]],
      ['tabs pushed, settings not', ['tabs.w1'] as ProfileSectionKey[]],
    ])('a THIRD device (its own id for the daemon) pulls it — %s: every legacy id resolves through the alias', async (_name, canonicalKeys) => {
      const sot = await interrupted()
      // B's canonical builds of the sections that did make it
      if (canonicalKeys.length > 0) {
        await applyAll(sot, B_ID, ['workspaces', 'settings', 'tabs.w1'])
        Object.assign(sot, await buildAll(canonicalKeys))
      }
      load({ ...deviceB(), hosts: { cccccc: mlab('cccccc') }, hostOrder: ['cccccc'], presets: presetsOn('cccccc') })
      const outcomes = await applyAll(sot, 'cccccc')
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(useHostStore.getState().hosts.cccccc.syncAliases).toEqual([A_ID, B_ID])
      expect(panesOf('t1')[0]).toMatchObject({ hostId: 'cccccc' })
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
      expect(useHostSettingsStore.getState().hosts).toEqual({ cccccc: { files: { root: '/srv' } } })
      expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn('cccccc'))
    })

    it('A itself pulls it (the alias is its own id): everything lands on A\'s host', async () => {
      const sot = await interrupted()
      load(deviceA())
      const outcomes = await applyAll(sot, A_ID)
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(Object.keys(useHostStore.getState().hosts)).toEqual([A_ID])
      expect(panesOf('t1')[0]).toMatchObject({ hostId: A_ID })
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    })

    // The device that goes canonical FIRST by PUSHING (its base agreed with the ordinal-2 SOT, so after the upgrade it
    // is dirty, not behind) never matched a legacy row — but its canonical row carries its OWN local id as an alias,
    // and that id is what its still-legacy tabs name. Interrupted after its hosts PUT, a device with an independent
    // id resolves them through it.
    it('A itself pushes canonical hosts first, tabs still legacy — a third device resolves A\'s old id through A\'s own alias', async () => {
      const legacy = await legacyOfA()
      load(deviceA())
      const canonicalHosts = await buildAll(['hosts'])
      const sot = { ...legacy, hosts: canonicalHosts.hosts }
      load({ ...deviceB(), hosts: { cccccc: mlab('cccccc') }, hostOrder: ['cccccc'] })
      const outcomes = await applyAll(sot, 'cccccc')
      for (const key of KEYS) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(panesOf('t1').map((c) => c.hostId ?? (c.source as { hostId?: string } | undefined)?.hostId ?? c.host)).toEqual(['cccccc', 'cccccc', 'cccccc'])
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
      expect(useHostSettingsStore.getState().hosts).toEqual({ cccccc: { files: { root: '/srv' } } })
      expect(useNewTabLayoutStore.getState().presets).toEqual(presetsOn('cccccc'))
    })

    it('ACROSS A RESTART: the alias is persisted (syncAliases) — a reload between the hosts apply and the tabs apply still resolves', async () => {
      const sot = await interrupted()
      load({ ...deviceB(), hosts: { cccccc: mlab('cccccc') }, hostOrder: ['cccccc'] })
      await applyAll(sot, 'cccccc', ['hosts'])
      // the reload: in-memory state gone, the persisted host list read back through the store's own merge
      const persisted = localStorage.getItem(STORAGE_KEYS.HOSTS)
      expect(persisted).toContain('syncAliases')
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      localStorage.setItem(STORAGE_KEYS.HOSTS, persisted as string)
      await useHostStore.persist.rehydrate()
      expect(useHostStore.getState().hosts.cccccc.syncAliases).toEqual([A_ID, B_ID])
      const outcomes = await applyAll(sot, 'cccccc', ['workspaces', 'settings', 'tabs.w1'])
      for (const key of ['workspaces', 'settings', 'tabs.w1']) expect(outcomes[key], key).toMatchObject({ ok: true })
      expect(panesOf('t1')[0]).toMatchObject({ hostId: 'cccccc' })
      expect(panesOf('t1')[0]).not.toHaveProperty('terminated')
    })
  })
})
