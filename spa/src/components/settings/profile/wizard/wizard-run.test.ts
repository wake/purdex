// spa/src/components/settings/profile/wizard/wizard-run.test.ts — the wizard's last step against the REAL
// switch-active.ts and the real stores; only `attachMaster` (the network) is replaced. What is pinned here is THE
// TABLE of wizard-run.ts: which world a pull is about to replace, and that the copy kept of it is a copy of THAT
// world. Every world carries a sentinel, so "whose content is that" is a string search.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { useHostStore } from '../../../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../../stores/useProfileStore'
import { useRebuildStore } from '../../../../stores/useRebuildStore'
import { useTabStore } from '../../../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../../../stores/useWorkspaceSettingsStore'
import type { Tab, Workspace } from '../../../../types/tab'
import { __resetMasterWorldForTest, readMasterWorld } from '../../../../lib/profile/master-world'
import { attachMaster } from '../../../../lib/profile/start'
import { ATTACH_REASONS, countWorld, runPlan, subStepsOf, worldToBeMaster, type SubStepState, type WizardPlan } from './wizard-run'

vi.mock('../../../../lib/profile/start', () => ({ attachMaster: vi.fn() }))

const MASTER = 'SENTINEL-MASTER'
const S1 = 'SENTINEL-ONE'
const S2 = 'SENTINEL-TWO'
const P = 'p_000000000001'

function tab(id: string, sentinel: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'inst' } } } }
}
function world(prefix: string, sentinel: string, tabs = 2): ParkedWorld {
  const ids = Array.from({ length: tabs }, (_, i) => `${prefix}t${i}`)
  const ws: Workspace = { id: `${prefix}ws`, name: `${sentinel}-ws`, tabs: ids, activeTabId: ids[0] ?? null }
  return { workspaces: [ws], tabs: Object.fromEntries(ids.map((id) => [id, tab(id, sentinel)])), activeWorkspaceId: ws.id, activeTabId: ids[0] ?? null }
}
const slave = (id: string, w: ParkedWorld | null) => ({ id, name: `Slave ${id}`, createdAt: 1, world: w })

function putOnScreen(w: ParkedWorld, worldId: string, epoch: number): void {
  useTabStore.setState({ tabs: w.tabs, tabOrder: w.workspaces.flatMap((x) => x.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId, worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId, worldEpoch: epoch })
}
/** The master on screen, `s1` and `s2` parked. */
function masterOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: { s1: slave('s1', world('a', S1)), s2: slave('s2', world('b', S2)) }, slaveOrder: ['s1', 's2'], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, master: { name: null } })
  putOnScreen(world('m', MASTER), MASTER_PROFILE_ID, 0)
}
/** `s1` on screen, the master and `s2` parked. */
function slaveOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: { s1: slave('s1', null), s2: slave('s2', world('b', S2)) }, slaveOrder: ['s1', 's2'], activeProfileId: 's1', parkedMaster: world('m', MASTER), worldEpoch: 1, master: { name: null } })
  putOnScreen(world('a', S1), 's1', 1)
}

const plan = (over: Partial<WizardPlan>): WizardPlan => ({ hostId: 'h1', profileId: P, localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', ...over })
const textOf = (w: unknown): string => JSON.stringify(w)
const sentinelsIn = (text: string): string[] => [MASTER, S1, S2].filter((s) => text.includes(s))
const slaveNamed = (name: string) => Object.values(useLocalProfilesStore.getState().slaves).find((s) => s.name === name)
const run = (p: WizardPlan, from = 0) => {
  const seen: Array<[number, SubStepState]> = []
  return runPlan(p, from, (i, s) => seen.push([i, s])).then((result) => ({ result, seen }))
}
/** What `attachMaster` saw of the device WHEN IT WAS CALLED: the master's world, and the copy that was kept. */
let atAttach: { master: string[]; kept: string[] | null } | null = null

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  atAttach = null
  vi.mocked(attachMaster).mockReset().mockImplementation(async () => {
    const read = readMasterWorld()
    const kept = slaveNamed('Kept')
    atAttach = { master: read.settled ? sentinelsIn(textOf(read.world)) : ['UNSETTLED'], kept: kept === undefined ? null : sentinelsIn(textOf(kept.world)) }
    return { ok: true }
  })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: {} })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
  useDeviceNameStore.setState({ deviceName: 'Laptop' })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  masterOnScreen()
})

afterEach(() => {
  __resetMasterWorldForTest()
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, master: { name: null } })
  putOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }, MASTER_PROFILE_ID, 0)
})

describe('the sub-steps of a plan, in their order', () => {
  it('the master as it is, push: attach only', () => expect(subStepsOf(plan({ direction: 'push' }))).toEqual(['attach']))
  it('a local profile, push: promote, then attach', () => expect(subStepsOf(plan({ localId: 's1', direction: 'push' }))).toEqual(['promote', 'attach']))
  it('pull with a copy kept: the copy is made AFTER the promote and BEFORE the attach', () => expect(subStepsOf(plan({ localId: 's1' }))).toEqual(['promote', 'save', 'attach']))
  it('pull without a copy', () => expect(subStepsOf(plan({ saveAs: null }))).toEqual(['attach']))
  it('a push keeps no copy, whatever the plan carries: nothing on this device is replaced', () => expect(subStepsOf(plan({ direction: 'push', saveAs: 'Kept' }))).toEqual(['attach']))
})

describe('THE TABLE — a pull replaces the world that is the master WHEN THE ATTACH IS MADE; the copy kept is of that world', () => {
  it('row 1 · master on screen, the master chosen: the copy is of the master (= the screen); nothing is promoted', async () => {
    const { result } = await run(plan({}))
    expect(result).toEqual({ done: true })
    expect(atAttach).toEqual({ master: [MASTER], kept: [MASTER] })
    expect(useLocalProfilesStore.getState().relabelCount ?? 0).toBe(0)
  })

  it('row 2 · master on screen, a PARKED local profile chosen: the copy is of THAT profile — not of the screen, which the promote has already kept as a profile of its own', async () => {
    const { result } = await run(plan({ localId: 's1' }))
    expect(result).toEqual({ done: true })
    expect(atAttach).toEqual({ master: [S1], kept: [S1] })
    // the old master: demoted, once — not copied a second time
    const holdingOldMaster = Object.values(useLocalProfilesStore.getState().slaves).filter((s) => textOf(s.world).includes(MASTER) || (s.world === null && textOf(useTabStore.getState().tabs).includes(MASTER)))
    expect(holdingOldMaster).toHaveLength(1)
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toHaveLength(3) // s2, the demoted master, the copy
  })

  it('row 3 · a local profile on screen, and it is the one chosen: the copy is of it (the screen, labelled master by then)', async () => {
    slaveOnScreen()
    const { result } = await run(plan({ localId: 's1' }))
    expect(result).toEqual({ done: true })
    expect(atAttach).toEqual({ master: [S1], kept: [S1] })
    expect(useLocalProfilesStore.getState().activeProfileId).toBe(MASTER_PROFILE_ID)
  })

  it('row 4 · a local profile on screen, the (parked) master kept as the master: the copy is of the PARKED master — not of the screen, which the pull does not touch', async () => {
    slaveOnScreen()
    const { result } = await run(plan({}))
    expect(result).toEqual({ done: true })
    expect(atAttach).toEqual({ master: [MASTER], kept: [MASTER] })
  })

  it('row 5 · a local profile on screen, ANOTHER parked one chosen: the copy is of the chosen one', async () => {
    slaveOnScreen()
    const { result } = await run(plan({ localId: 's2' }))
    expect(result).toEqual({ done: true })
    expect(atAttach).toEqual({ master: [S2], kept: [S2] })
    expect(sentinelsIn(textOf(useTabStore.getState().tabs))).toEqual([S1]) // the screen did not move
  })

  it('the copy shares no object and no minted id with the world it was made of', async () => {
    await run(plan({ localId: 's1' }))
    const kept = slaveNamed('Kept')!.world!
    const master = useLocalProfilesStore.getState().parkedMaster!
    expect(Object.keys(kept.tabs).some((id) => id in master.tabs)).toBe(false)
    expect(kept.workspaces[0].id).not.toBe(master.workspaces[0].id)
  })

  it('what the direction step counts is that same world', () => {
    expect(countWorld(worldToBeMaster(MASTER_PROFILE_ID))).toEqual({ workspaces: 1, tabs: 2 })
    useLocalProfilesStore.setState({ slaves: { ...useLocalProfilesStore.getState().slaves, s2: slave('s2', world('b', S2, 5)) } })
    expect(countWorld(worldToBeMaster('s2'))).toEqual({ workspaces: 1, tabs: 5 })
    slaveOnScreen()
    expect(sentinelsIn(textOf(worldToBeMaster('s1')))).toEqual([S1]) // on screen: read off the live stores
    expect(sentinelsIn(textOf(worldToBeMaster(MASTER_PROFILE_ID)))).toEqual([MASTER]) // parked
    expect(worldToBeMaster('nope')).toBeNull()
    expect(countWorld(null)).toBeNull()
  })
})

describe('the order, and what a failure stops', () => {
  it('reports every sub-step as it goes: running, then done', async () => {
    const { seen } = await run(plan({ localId: 's1' }))
    expect(seen).toEqual([[0, 'running'], [0, 'done'], [1, 'running'], [1, 'done'], [2, 'running'], [2, 'done']])
  })

  it('the demoted master is named after this device, and never like the copy', async () => {
    await run(plan({ localId: 's1', saveAs: 'Laptop' }))
    expect(Object.values(useLocalProfilesStore.getState().slaves).map((s) => s.name).sort()).toEqual(['Laptop', 'Laptop 2', 'Slave s2'])
  })

  it('the promote is refused (a master is attached): NOTHING after it runs — no copy, no attach', async () => {
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P, masterEndpoint: '10.0.0.1:7860' })
    const { result, seen } = await run(plan({ localId: 's1' }))
    expect(result).toEqual({ done: false, failedAt: 0, reason: 'master-attached' })
    expect(seen).toEqual([[0, 'running'], [0, 'failed']])
    expect(attachMaster).not.toHaveBeenCalled()
    expect(slaveNamed('Kept')).toBeUndefined()
  })

  it('the copy is refused (a name that is none): the attach is NOT made — a pull without the copy that was asked for would be a loss', async () => {
    const { result } = await run(plan({ saveAs: '   ' }))
    expect(result).toEqual({ done: false, failedAt: 0, reason: 'bad-name' })
    expect(attachMaster).not.toHaveBeenCalled()
  })

  it('the attach fails: what was done stays done, and a retry runs the attach ONLY', async () => {
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason: 'timeout' })
    const first = await run(plan({ localId: 's1' }))
    expect(first.result).toEqual({ done: false, failedAt: 2, reason: 'timeout' })
    const slavesThen = Object.keys(useLocalProfilesStore.getState().slaves).length
    const again = await run(plan({ localId: 's1' }), 2)
    expect(again.result).toEqual({ done: true })
    expect(again.seen).toEqual([[2, 'running'], [2, 'done']])
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toHaveLength(slavesThen) // no second promote, no second copy
    expect(attachMaster).toHaveBeenLastCalledWith('h1', P, 'pull')
  })

  it.each(ATTACH_REASONS)('attach reason %s is passed on as it is', async (reason) => {
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason })
    expect((await run(plan({ saveAs: null }))).result).toEqual({ done: false, failedAt: 0, reason })
  })

  it('a reason that is a transport\'s message — `attachMaster` hands over `Error.message` when the PUT throws — never leaves this file', async () => {
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason: 'Failed to fetch http://10.0.0.1:7860/api/profiles?token=SECRET' })
    expect((await run(plan({ saveAs: null }))).result).toEqual({ done: false, failedAt: 0, reason: 'other' })
    vi.mocked(attachMaster).mockRejectedValueOnce(new Error('boom SECRET'))
    expect((await run(plan({ saveAs: null }))).result).toEqual({ done: false, failedAt: 0, reason: 'other' })
  })
})
