// spa/src/lib/shown-hosts.test.ts — the ONE shown predicate, the pane matcher and the writer (host ownership spec §1.2 /
// §4.5, plan H2d-1 T3, §0.21 "What is a pane on X" / "One rule for every opener too").
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type LocalProfile, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { STORAGE_KEYS } from './storage/keys'
import { getPrimaryPane } from './pane-tree'
import { syncIdOfSync } from './profile/host-identity'
import type { PaneContent } from '../types/tab'
import * as shownHosts from './shown-hosts'
import {
  currentShownIds,
  currentShownIdsNow,
  hostRefOf,
  isPaneHostShown,
  isRefShown,
  isRefShownNow,
  landOnHostsPageIfHidden,
  setHostShown,
  shownFormsOf,
  usePaneHostShown,
  useIsRefShown,
  useShownRefFilter,
  wireOfRef,
} from './shown-hosts'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const FAR = syncIdOfSync('nowhere:000000') // a daemon no host of this device has
const LOCAL = 'loc001' // has DAEMON
const PLAIN = 'plain1' // no daemonId

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const HOSTS: Record<string, HostConfig> = { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), [PLAIN]: host(PLAIN, { order: 1 }) }
const ORDER = [LOCAL, PLAIN]
const tmux = (hostId: string, over: object = {}): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', ...over })
const exec = (hostRef?: string): PaneContent => ({ kind: 'execution', executionId: 'e', ...(hostRef === undefined ? {} : { host: hostRef }) })
const ids = () => useShownHostsStore.getState().ids

beforeEach(() => {
  localStorage.clear()
  useHostStore.setState({ hosts: HOSTS, hostOrder: ORDER, activeHostId: LOCAL })
  useShownHostsStore.setState({ ids: [], relabelStamp: 0 })
  // The master on screen, settled (the defaults, restated: a test below moves them).
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0 })
  useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
})

describe('shownFormsOf', () => {
  it('a host with a daemonId → [its d1_ id, its local id]; without → [its local id]', () => {
    expect(shownFormsOf(HOSTS[LOCAL])).toEqual([WIRE, LOCAL])
    expect(shownFormsOf(HOSTS[PLAIN])).toEqual([PLAIN])
  })
})

describe('isRefShown — the one predicate', () => {
  it('[] → every local host hidden (user rule 2)', () => {
    expect(isRefShown(LOCAL, HOSTS, [])).toBe(false)
    expect(isRefShown(PLAIN, HOSTS, [])).toBe(false)
  })

  it('a listed d1_ id shows the local host of that daemon; a no-daemonId host by its local id', () => {
    expect(isRefShown(LOCAL, HOSTS, [WIRE])).toBe(true)
    expect(isRefShown(PLAIN, HOSTS, [PLAIN])).toBe(true)
    expect(isRefShown(PLAIN, HOSTS, [WIRE])).toBe(false)
  })

  it('the local-id form: listed under its local id, daemonId learned before any re-key → still shown', () => {
    expect(isRefShown(LOCAL, HOSTS, [LOCAL])).toBe(true)
    expect(isPaneHostShown(tmux(LOCAL), HOSTS, ORDER, [LOCAL])).toBe(true)
  })

  it('a ref that is not a local host: shown ONLY when listed — never "not local → shown"', () => {
    expect(isRefShown(FAR, HOSTS, [WIRE])).toBe(false)
    expect(isRefShown(FAR, HOSTS, [FAR])).toBe(true)
    expect(isRefShown('gone', HOSTS, [])).toBe(false) // a deleted host's id
    expect(isRefShown('', HOSTS, [''])).toBe(false) // no host at all is never shown
  })

  it('the d1_ form of a host here is the same host', () => {
    expect(isRefShown(WIRE, HOSTS, [WIRE])).toBe(true)
  })

  it('conflict (two local rows, one daemon) → shown / hidden together', () => {
    const hosts = { ...HOSTS, dup: host('dup', { daemonId: DAEMON }) }
    expect(isRefShown(LOCAL, hosts, [WIRE])).toBe(true)
    expect(isRefShown('dup', hosts, [WIRE])).toBe(true)
    expect(isRefShown(LOCAL, hosts, [])).toBe(false)
    expect(isRefShown('dup', hosts, [])).toBe(false)
  })

  it('a host added after the list was written is hidden by every reader', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const added = host('new1', { daemonId: 'new-lab:111111', order: 2 })
    useHostStore.setState({ hosts: { ...HOSTS, new1: added }, hostOrder: [...ORDER, 'new1'] })
    expect(isRefShown('new1', useHostStore.getState().hosts, ids())).toBe(false)
    expect(isRefShownNow('new1')).toBe(false)
    expect(isPaneHostShown(tmux('new1'), useHostStore.getState().hosts, useHostStore.getState().hostOrder, ids())).toBe(false)
    expect(renderHook(() => useIsRefShown('new1')).result.current).toBe(false)
    expect(renderHook(() => usePaneHostShown(tmux('new1'))).result.current).toBe(false)
  })

  it('the module exports no other predicate: no isHostShown / useIsHostShown / useShownHostFilter / isHostShownNow', () => {
    for (const name of ['isHostShown', 'useIsHostShown', 'useShownHostFilter', 'isHostShownNow', 'isRefEnabled', 'isPaneHostEnabled']) {
      expect(Object.hasOwn(shownHosts, name), name).toBe(false)
    }
  })

  // Every reader agrees with the pure predicate, case for case.
  const CASES: [string, string, string[], boolean][] = [
    ['local by d1_', LOCAL, [WIRE], true],
    ['local by local id', LOCAL, [LOCAL], true],
    ['local unlisted', LOCAL, [PLAIN], false],
    ['plain listed', PLAIN, [PLAIN], true],
    ['plain unlisted', PLAIN, [], false],
    ['unresolved d1_ unlisted', FAR, [WIRE], false],
    ['unresolved d1_ listed', FAR, [FAR], true],
    ['deleted id', 'gone', [], false],
    ['empty ref', '', [''], false],
  ]
  it.each(CASES)('%s: isRefShown, useIsRefShown, useShownRefFilter and isRefShownNow agree', (_label, ref, list, expected) => {
    useShownHostsStore.setState({ ids: list })
    expect(isRefShown(ref, HOSTS, list)).toBe(expected)
    expect(isRefShownNow(ref)).toBe(expected)
    expect(renderHook(() => useIsRefShown(ref)).result.current).toBe(expected)
    expect(renderHook(() => useShownRefFilter()).result.current(ref)).toBe(expected)
  })
})

describe('useIsRefShown / useShownRefFilter (live)', () => {
  it('useIsRefShown follows a show / hide and a daemonId learned', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    useHostStore.setState({ hosts: { ...HOSTS, [LOCAL]: host(LOCAL) } }) // daemonId not known yet
    const { result } = renderHook(() => useIsRefShown(LOCAL))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hosts: HOSTS }))
    expect(result.current).toBe(true)
    act(() => useShownHostsStore.getState().hide(WIRE))
    expect(result.current).toBe(false)
  })

  it('useIsRefShown(null) → false', () => {
    expect(renderHook(() => useIsRefShown(null)).result.current).toBe(false)
  })

  it('useShownRefFilter is stable while the stores are, and follows a write', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const { result, rerender } = renderHook(() => useShownRefFilter())
    const first = result.current
    expect(first(LOCAL)).toBe(false)
    expect(first(PLAIN)).toBe(true)
    rerender()
    expect(result.current).toBe(first)
    act(() => useShownHostsStore.getState().show(WIRE))
    expect(result.current(LOCAL)).toBe(true)
  })
})

describe('hostRefOf (the host-bearing leaves, §0.21 / §0.22)', () => {
  it('tmux → hostId, terminated included', () => {
    expect(hostRefOf(tmux(LOCAL), ORDER)).toBe(LOCAL)
    expect(hostRefOf(tmux(WIRE, { terminated: 'session-closed' }), ORDER)).toBe(WIRE)
  })

  it('execution → its host; hostless → hostOrder[0]; hostless with no host at all → null', () => {
    expect(hostRefOf(exec(PLAIN), ORDER)).toBe(PLAIN)
    expect(hostRefOf(exec(), ORDER)).toBe(LOCAL)
    expect(hostRefOf(exec(''), ORDER)).toBe(LOCAL) // an empty hint is no hint (resolveExecutionHostId)
    expect(hostRefOf(exec(), [])).toBeNull()
  })

  it('a daemon-source editor, a new tab, and every other kind → null (not host-bearing)', () => {
    expect(hostRefOf({ kind: 'editor', source: { type: 'daemon', hostId: LOCAL }, filePath: '/a' }, ORDER)).toBeNull()
    expect(hostRefOf({ kind: 'new-tab' }, ORDER)).toBeNull()
    expect(hostRefOf({ kind: 'browser', url: 'https://x' }, ORDER)).toBeNull()
  })
})

describe('wireOfRef / isPaneHostShown (wire space)', () => {
  it('wireOfRef: a local host → its wire id; any other ref → itself', () => {
    expect(wireOfRef(LOCAL, HOSTS)).toBe(WIRE)
    expect(wireOfRef(PLAIN, HOSTS)).toBe(PLAIN)
    expect(wireOfRef(FAR, HOSTS)).toBe(FAR)
    expect(wireOfRef('toString', HOSTS)).toBe('toString') // own keys only
  })

  it('a pane on an unresolved d1_X: unlisted → false, listed → true', () => {
    expect(isPaneHostShown(tmux(FAR), HOSTS, ORDER, [WIRE])).toBe(false)
    expect(isPaneHostShown(tmux(FAR), HOSTS, ORDER, [FAR])).toBe(true)
  })

  it('a pane on a local host with a daemonId → by its d1_ id or its local id', () => {
    expect(isPaneHostShown(tmux(LOCAL), HOSTS, ORDER, [WIRE])).toBe(true)
    expect(isPaneHostShown(tmux(LOCAL), HOSTS, ORDER, [PLAIN])).toBe(false)
    expect(isPaneHostShown(tmux(WIRE), HOSTS, ORDER, [WIRE])).toBe(true)
  })

  it('a hostless execution follows hostOrder[0]: hidden → false, shown → true', () => {
    expect(isPaneHostShown(exec(), HOSTS, ORDER, [PLAIN])).toBe(false)
    expect(isPaneHostShown(exec(), HOSTS, [PLAIN, LOCAL], [PLAIN])).toBe(true)
  })

  it('a non-host-bearing pane → true, also with []', () => {
    expect(isPaneHostShown({ kind: 'new-tab' }, HOSTS, ORDER, [])).toBe(true)
    expect(isPaneHostShown(exec(), HOSTS, [], [])).toBe(true) // no host at all: nothing to hide
  })
})

describe('usePaneHostShown (live)', () => {
  it('re-renders on a show / hide', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const content = tmux(PLAIN)
    const { result } = renderHook(() => usePaneHostShown(content))
    expect(result.current).toBe(false)
    act(() => useShownHostsStore.getState().show(PLAIN))
    expect(result.current).toBe(true)
    act(() => useShownHostsStore.getState().hide(PLAIN))
    expect(result.current).toBe(false)
  })

  it('re-renders on a daemonId learned (the host\'s wire id moves)', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    useHostStore.setState({ hosts: { ...HOSTS, [LOCAL]: host(LOCAL) } })
    const content = tmux(LOCAL)
    const { result } = renderHook(() => usePaneHostShown(content))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hosts: HOSTS }))
    expect(result.current).toBe(true)
  })

  it('re-renders on hostOrder for a hostless execution', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const content = exec()
    const { result } = renderHook(() => usePaneHostShown(content))
    expect(result.current).toBe(false)
    act(() => useHostStore.setState({ hostOrder: [PLAIN, LOCAL] }))
    expect(result.current).toBe(true)
  })

  it('does NOT re-render on an unrelated id\'s write when the result is unchanged', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const content = tmux(LOCAL)
    let renders = 0
    renderHook(() => {
      renders += 1
      return usePaneHostShown(content)
    })
    const after = renders
    act(() => useShownHostsStore.getState().show('d1_unrelated'))
    act(() => useShownHostsStore.getState().hide('d1_unrelated'))
    expect(renders).toBe(after)
  })
})

describe('setHostShown — the writer (one host, its own forms only)', () => {
  it('shown → appends the host\'s wire id only', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown'] })
    setHostShown(LOCAL, true)
    expect(ids()).toEqual(['d1_unknown', WIRE])
    setHostShown(PLAIN, true)
    expect(ids()).toEqual(['d1_unknown', WIRE, PLAIN])
  })

  it('hidden → removes BOTH forms of that host and nothing else (unknown ids and order intact)', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown', LOCAL, PLAIN, WIRE, 'tail'] })
    setHostShown(LOCAL, false)
    expect(ids()).toEqual(['d1_unknown', PLAIN, 'tail'])
  })

  it('an unknown host → no-op (same state object)', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const before = useShownHostsStore.getState()
    setHostShown('gone', true)
    setHostShown('gone', false)
    expect(useShownHostsStore.getState()).toBe(before)
  })
})

// H2d-3 T2 — the landing of every opener that would create / focus a tab (notification, deep link, route, toast).
describe('landOnHostsPageIfHidden — the landing (one opener rule)', () => {
  const kindsOf = () => Object.values(useTabStore.getState().tabs).map((t) => getPrimaryPane(t.layout).content.kind)
  const hostsTabActive = () => {
    const { tabs, activeTabId } = useTabStore.getState()
    return activeTabId !== null && getPrimaryPane(tabs[activeTabId].layout).content.kind === 'hosts'
  }

  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useHostStore.setState({ activeHostId: PLAIN })
  })

  it('shown → false, nothing done', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    const tabs = useTabStore.getState().tabs
    expect(landOnHostsPageIfHidden(LOCAL)).toBe(false)
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(useHostStore.getState().activeHostId).toBe(PLAIN)
  })

  it('a hidden local host → the Hosts tab opened on that host, true, no tab of the host', () => {
    expect(landOnHostsPageIfHidden(LOCAL)).toBe(true)
    expect(kindsOf()).toEqual(['hosts'])
    expect(hostsTabActive()).toBe(true)
    expect(useHostStore.getState().activeHostId).toBe(LOCAL)
  })

  it('a hidden local host with the Hosts tab already open → focused, not duplicated', () => {
    const first = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
    useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
    expect(landOnHostsPageIfHidden(LOCAL)).toBe(true)
    expect(useTabStore.getState().activeTabId).toBe(first)
    expect(kindsOf().filter((k) => k === 'hosts')).toHaveLength(1)
  })

  it('an unlisted d1_X (not a local host) → the Hosts tab, activeHostId unchanged, true, no tab', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    expect(landOnHostsPageIfHidden(FAR)).toBe(true)
    expect(kindsOf()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe(PLAIN)
  })

  it('a deleted host\'s id and \'\' (a hostless link, no host) → the Hosts tab, activeHostId unchanged, true', () => {
    expect(landOnHostsPageIfHidden('gone')).toBe(true)
    expect(landOnHostsPageIfHidden('')).toBe(true)
    expect(kindsOf()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe(PLAIN)
  })

  it('a listed d1_X → false (openable: its pane shows MissingHostPane as today)', () => {
    useShownHostsStore.setState({ ids: [FAR] })
    expect(landOnHostsPageIfHidden(FAR)).toBe(false)
    expect(kindsOf()).toEqual([])
  })
})

// === Per-workbench shown hosts (2026-09-25 plan, A2): which list applies, and fail closed ===

const EMPTY: ParkedWorld = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }
const SLAVE = 'slave1'
const slaveRecord = (shownHostIds: string[], world: ParkedWorld | null = null): LocalProfile => ({ id: SLAVE, name: 'S', createdAt: 1, shownHostIds, world })

/** `SLAVE` on screen, settled at `epoch`, its list `list`; the master's list (the store) is `masterIds`. */
function slaveOnScreen(list: string[], masterIds: string[] = [], epoch = 1): void {
  useLocalProfilesStore.setState({ slaves: { [SLAVE]: slaveRecord(list) }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: EMPTY, worldEpoch: epoch })
  useTabStore.setState({ worldId: SLAVE, worldEpoch: epoch })
  useWorkspaceStore.setState({ worldId: SLAVE, worldEpoch: epoch })
  useShownHostsStore.setState({ ids: masterIds })
}

describe('currentShownIds — pure', () => {
  const shown = { ids: [WIRE], relabelStamp: 2 }
  const local = { slaves: { [SLAVE]: slaveRecord([PLAIN]) }, relabelCount: 2 }

  it('master, stamp = relabelCount → the store\'s ids, same reference', () => {
    expect(currentShownIds(true, MASTER_PROFILE_ID, local, shown)).toBe(shown.ids)
  })

  it('master, stamp ≠ relabelCount → [] (a promote half-arrived: fail closed)', () => {
    expect(currentShownIds(true, MASTER_PROFILE_ID, { ...local, relabelCount: 3 }, shown)).toEqual([])
  })

  it("a slave → its record's list, same reference", () => {
    expect(currentShownIds(true, SLAVE, local, shown)).toBe(local.slaves[SLAVE].shownHostIds)
  })

  it('an unknown id, a non-string tag, or an unsettled world → [] — one stable reference', () => {
    const none = currentShownIds(true, 'gone', local, shown)
    expect(none).toEqual([])
    expect(currentShownIds(true, 7, local, shown)).toBe(none)
    expect(currentShownIds(false, MASTER_PROFILE_ID, local, shown)).toBe(none)
    expect(currentShownIds(false, SLAVE, local, shown)).toBe(none)
  })
})

describe('the current list — every reader (A2)', () => {
  it("a slave on screen: every reader reads ITS list, not the master's (m1)", () => {
    slaveOnScreen([PLAIN], [WIRE])
    expect(currentShownIdsNow()).toEqual([PLAIN])
    expect(isRefShownNow(PLAIN)).toBe(true)
    expect(isRefShownNow(LOCAL)).toBe(false)
    expect(renderHook(() => useIsRefShown(LOCAL)).result.current).toBe(false)
    expect(renderHook(() => useShownRefFilter()).result.current(PLAIN)).toBe(true)
    expect(renderHook(() => usePaneHostShown(tmux(LOCAL))).result.current).toBe(false)
    expect(renderHook(() => usePaneHostShown(tmux(PLAIN))).result.current).toBe(true)
    expect(landOnHostsPageIfHidden(PLAIN)).toBe(false)
    expect(landOnHostsPageIfHidden(LOCAL)).toBe(true)
  })

  it('the master on screen: the store', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    expect(currentShownIdsNow()).toBe(useShownHostsStore.getState().ids)
    expect(isRefShownNow(LOCAL)).toBe(true)
  })

  it('tab tag = a slave but the pointer still says master (another window\'s switch half-arrived) → [] (m7)', () => {
    useShownHostsStore.setState({ ids: [WIRE, PLAIN] })
    useLocalProfilesStore.setState({ slaves: { [SLAVE]: { ...slaveRecord([WIRE, PLAIN]), world: EMPTY } }, slaveOrder: [SLAVE] })
    useTabStore.setState({ worldId: SLAVE })
    useWorkspaceStore.setState({ worldId: SLAVE })
    expect(currentShownIdsNow()).toEqual([])
    expect(isRefShownNow(LOCAL)).toBe(false)
  })

  it('tab tag = master but the pointer says a slave → [] (m7)', () => {
    slaveOnScreen([WIRE], [WIRE])
    useTabStore.setState({ worldId: MASTER_PROFILE_ID })
    useWorkspaceStore.setState({ worldId: MASTER_PROFILE_ID })
    expect(isRefShownNow(LOCAL)).toBe(false)
  })

  it('epochs that disagree, or a world behind the fence → [] although tags agree', () => {
    useShownHostsStore.setState({ ids: [WIRE] })
    useTabStore.setState({ worldEpoch: 1 })
    expect(isRefShownNow(LOCAL)).toBe(false)
    useTabStore.setState({ worldEpoch: 0 })
    expect(isRefShownNow(LOCAL)).toBe(true)
    localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, '5')
    expect(isRefShownNow(LOCAL)).toBe(false)
  })

  it('a slave on screen whose record is gone → []', () => {
    slaveOnScreen([PLAIN])
    useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] })
    expect(currentShownIdsNow()).toEqual([])
  })
})

// A promote made in ANOTHER window reaches this one store by store, in any order (plan §2, fail closed). Four stores
// matter: local profiles (pointer, epoch, relabelCount, the slaves' lists), tab + workspace (tag, epoch), shown (the
// master's list + its stamp). For every subset that has arrived, the answer is the right list or [] — never the wrong
// workbench's list (m10). No fence written: the stamp alone must close the gap.
describe('a promote in another window, every rehydrate order (A2, m10)', () => {
  const OLD = [WIRE] // the master's list before
  const SL = [PLAIN] // the promoted slave's list
  type Snap = { local: object; tab: object; ws: object; shown: object }

  const subsets = (): boolean[][] => Array.from({ length: 16 }, (_, n) => [0, 1, 2, 3].map((b) => ((n >> b) & 1) === 1))

  function apply(before: Snap, after: Snap, arrived: boolean[]): void {
    useLocalProfilesStore.setState((arrived[0] ? after : before).local)
    useTabStore.setState((arrived[1] ? after : before).tab)
    useWorkspaceStore.setState((arrived[2] ? after : before).ws)
    useShownHostsStore.setState((arrived[3] ? after : before).shown)
  }

  it('master on screen, a parked slave promoted: the screen becomes the demoted workbench — its list (OLD) or [], never SL', () => {
    const D = 'demoted'
    const before: Snap = {
      local: { slaves: { [SLAVE]: { ...slaveRecord(SL), world: EMPTY } }, slaveOrder: [SLAVE], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 5, relabelCount: 0 },
      tab: { worldId: MASTER_PROFILE_ID, worldEpoch: 5 },
      ws: { worldId: MASTER_PROFILE_ID, worldEpoch: 5 },
      shown: { ids: OLD, relabelStamp: 0 },
    }
    const after: Snap = {
      local: { slaves: { [D]: { id: D, name: 'D', createdAt: 1, shownHostIds: OLD, world: null } }, slaveOrder: [D], activeProfileId: D, parkedMaster: EMPTY, worldEpoch: 6, relabelCount: 1 },
      tab: { worldId: D, worldEpoch: 6 },
      ws: { worldId: D, worldEpoch: 6 },
      shown: { ids: SL, relabelStamp: 1 },
    }
    for (const arrived of subsets()) {
      apply(before, after, arrived)
      const got = currentShownIdsNow()
      expect([[], OLD], `arrived ${arrived.join(',')}`).toContainEqual(got)
    }
  })

  it('the promoted slave on screen: the screen becomes the master — SL or [], never the old master list', () => {
    const D = 'demoted'
    const before: Snap = {
      local: { slaves: { [SLAVE]: slaveRecord(SL) }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: EMPTY, worldEpoch: 5, relabelCount: 0 },
      tab: { worldId: SLAVE, worldEpoch: 5 },
      ws: { worldId: SLAVE, worldEpoch: 5 },
      shown: { ids: OLD, relabelStamp: 0 },
    }
    const after: Snap = {
      local: { slaves: { [D]: { id: D, name: 'D', createdAt: 1, shownHostIds: OLD, world: EMPTY } }, slaveOrder: [D], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 6, relabelCount: 1 },
      tab: { worldId: MASTER_PROFILE_ID, worldEpoch: 6 },
      ws: { worldId: MASTER_PROFILE_ID, worldEpoch: 6 },
      shown: { ids: SL, relabelStamp: 1 },
    }
    for (const arrived of subsets()) {
      apply(before, after, arrived)
      expect([[], SL], `arrived ${arrived.join(',')}`).toContainEqual(currentShownIdsNow())
    }
  })
})

describe('the hooks follow every input (A2)', () => {
  it('useIsRefShown / usePaneHostShown re-render on a world switch and on a write to the slave\'s list', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const content = tmux(PLAIN)
    const ref = renderHook(() => useIsRefShown(PLAIN))
    const pane = renderHook(() => usePaneHostShown(content))
    expect([ref.result.current, pane.result.current]).toEqual([true, true])
    act(() => slaveOnScreen([], [PLAIN]))
    expect([ref.result.current, pane.result.current]).toEqual([false, false])
    act(() => { useLocalProfilesStore.getState().setSlaveShownHosts(SLAVE, () => [PLAIN]) })
    expect([ref.result.current, pane.result.current]).toEqual([true, true])
    act(() => useTabStore.setState({ worldEpoch: 9 })) // unsettled
    expect([ref.result.current, pane.result.current]).toEqual([false, false])
  })

  it('re-renders on a stamp that moves (a promote half-arrived) and back', () => {
    useShownHostsStore.setState({ ids: [PLAIN] })
    const { result } = renderHook(() => useIsRefShown(PLAIN))
    expect(result.current).toBe(true)
    act(() => useLocalProfilesStore.setState({ relabelCount: 1 }))
    expect(result.current).toBe(false)
    act(() => useShownHostsStore.setState({ relabelStamp: 1 }))
    expect(result.current).toBe(true)
  })

  it('useShownRefFilter: the same function while the current list is the same array; a new one when it moves', () => {
    slaveOnScreen([PLAIN], [WIRE])
    const { result } = renderHook(() => useShownRefFilter())
    const first = result.current
    act(() => useTabStore.setState({ activeTabId: null })) // an unrelated tab-store write
    act(() => useShownHostsStore.getState().show('d1_other')) // the master's list: not the one on screen
    expect(result.current).toBe(first)
    act(() => { useLocalProfilesStore.getState().setSlaveShownHosts(SLAVE, () => [WIRE]) })
    expect(result.current).not.toBe(first)
    expect(result.current(LOCAL)).toBe(true)
  })

  it('a hook does not re-render for an unrelated store write when its answer is unchanged', () => {
    slaveOnScreen([PLAIN])
    const content = tmux(PLAIN)
    let renders = 0
    renderHook(() => {
      renders += 1
      return usePaneHostShown(content)
    })
    const after = renders
    act(() => useTabStore.setState({ activeTabId: null }))
    act(() => useShownHostsStore.getState().show('d1_unrelated'))
    expect(renders).toBe(after)
  })
})
