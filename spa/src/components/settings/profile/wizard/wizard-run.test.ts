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
import en from '../../../../locales/en.json'
import { createProfile, getSection, listProfiles } from '../../../../lib/profile/api'
import type { ProfileIndexEntry } from '../../../../lib/profile/api'
import { useUndoToast } from '../../../../stores/useUndoToast'
import { isRetiredSection } from '../../../../lib/profile/projections'
import { ATTACH_REASONS, announceRun, countWorld, createSotProfile, hasLiveSections, prepareRun, retargetPlan, runPlan, sotFingerprint, sotNow, subStepsOf, worldToBeMaster, type SubStepState, type WizardDraft, type WizardPlan } from './wizard-run'

vi.mock('../../../../lib/profile/start', () => ({ attachMaster: vi.fn() }))
vi.mock('../../../../lib/profile/api', () => ({ listProfiles: vi.fn(), createProfile: vi.fn(), getSection: vi.fn() }))
// The real retired set, spied: the wizard must ask projections.ts, not keep a list of its own.
vi.mock('../../../../lib/profile/projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../../lib/profile/projections')>()
  return { ...actual, isRetiredSection: vi.fn(actual.isRetiredSection) }
})

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
const slave = (id: string, w: ParkedWorld | null) => ({ id, name: `Slave ${id}`, createdAt: 1, shownHostIds: [], world: w })

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

/** The daemon h1 is recorded as, and confirmed at its address: a pull through it may be made. */
const H1_DAEMON = 'mini-lab:278cbm'
const H1_AT = '10.0.0.1:7860'
/** An empty profile P, as `plan` says it was seen (`seen`) — so that the re-check before the attach passes. */
const EMPTY_SEEN = JSON.stringify([])
const plan = (over: Partial<WizardPlan>): WizardPlan => ({ hostId: 'h1', profileId: P, localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', at: H1_AT, seen: EMPTY_SEEN, ...over })
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
  // P is on h1, empty, with no `hosts` section: what `plan` was prepared against (the re-check before the attach)
  vi.mocked(listProfiles).mockReset().mockResolvedValue({ kind: 'ok', value: [{ id: P, name: 'default', createdAt: 1, updatedAt: 1, sections: [], attachments: [] }] })
  vi.mocked(createProfile).mockReset()
  vi.mocked(getSection).mockReset().mockResolvedValue({ kind: 'ok', value: null })
  vi.mocked(attachMaster).mockReset().mockImplementation(async () => {
    const read = readMasterWorld()
    const kept = slaveNamed('Kept')
    atAttach = { master: read.settled ? sentinelsIn(textOf(read.world)) : ['UNSETTLED'], kept: kept === undefined ? null : sentinelsIn(textOf(kept.world)) }
    return { ok: true }
  })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0, daemonId: H1_DAEMON } }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: { h1: { status: 'connected', daemonIdVerified: { endpoint: H1_AT, daemonId: H1_DAEMON } } } })
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

describe('retargetPlan — a retry keeps its plan, re-aimed at what the door just read', () => {
  const old = plan({ localId: 's1', at: '10.0.0.1:7860', seen: 'f1' })
  it('the address and the fingerprint are the fresh plan\'s; the rest, and so the sub-steps, the old one\'s', () => {
    const fresh = plan({ localId: 's1', at: '10.0.0.9:7860', seen: 'f2' })
    expect(retargetPlan(old, fresh)).toEqual({ ...old, at: '10.0.0.9:7860', seen: 'f2' })
  })
  it('other sub-steps (no copy now, or a push): not the same plan', () => {
    expect(retargetPlan(old, plan({ localId: 's1', saveAs: null }))).toBeNull()
    expect(retargetPlan(old, plan({ localId: 's1', direction: 'push' }))).toBeNull()
  })
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

  // per-workbench shown hosts A3: a promote whose rollback did not finish is shown as a persistent notice (H1c)
  describe('a promote that could not be fully undone', () => {
    const notice = () => useUndoToast.getState().notice?.message ?? null
    beforeEach(() => useUndoToast.getState().dismissNotice())
    afterEach(() => {
      vi.restoreAllMocks()
      useUndoToast.getState().dismissNotice()
    })

    it('rollback-incomplete: the run stops there, and the persistent notice says to reload and check', async () => {
      vi.spyOn(useWorkspaceStore, 'setState').mockImplementationOnce(() => {
        throw new Error('stamp failed')
      })
      vi.spyOn(useLocalProfilesStore, 'setState').mockImplementation(() => {
        throw new Error('restore failed')
      })
      const { result } = await run(plan({ localId: 's1' }))
      expect(result).toEqual({ done: false, failedAt: 0, reason: 'rollback-incomplete' })
      expect(notice()).toBe(en['settings.profile.wizard.promote.rollback_incomplete'])
      expect(notice()).toBe('Making this the workbench master did not finish and could not be fully undone — reload and check this device\'s workbenches.')
      expect(attachMaster).not.toHaveBeenCalled()
    })

    it('only for rollback-incomplete: a clean write-failed, or a refusal, raises no notice', async () => {
      vi.spyOn(useWorkspaceStore, 'setState').mockImplementationOnce(() => {
        throw new Error('stamp failed')
      })
      expect((await run(plan({ localId: 's1' }))).result).toEqual({ done: false, failedAt: 0, reason: 'write-failed' })
      expect(notice()).toBeNull()
      useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P, masterEndpoint: '10.0.0.1:7860' })
      expect((await run(plan({ localId: 's1' }))).result).toMatchObject({ reason: 'master-attached' })
      expect(notice()).toBeNull()
    })
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
    expect(vi.mocked(attachMaster).mock.lastCall).toEqual(['h1', P, 'pull']) // three arguments: no hosts row (host ownership H3b)
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

// === PR-B: the one door before anything irreversible, the create whose outcome is not known, the result that outlives the page ===

const meta = (section: string, rev: number, hash = `h-${section}-${rev}`) => ({ section, rev, hash, fingerprint: 'f', ordinal: 1, writer: 'c', updatedAt: 1 })
const indexEntry = (id: string, name: string, sections: ReturnType<typeof meta>[] = [], over: Partial<ProfileIndexEntry> = {}): ProfileIndexEntry => ({ id, name, createdAt: 1, updatedAt: 1, sections, attachments: [], ...over })
const listed = (...rows: ProfileIndexEntry[]) => vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: rows })
const transportFailure = (reason: string) => ({ kind: 'failed', reason, status: 0, message: `RAW ${reason} token=SECRET` }) as never
const connectHost = () => useHostStore.setState({ runtime: { h1: { status: 'connected' } } })

describe('prepareRun — the ONE door before the run: this device\'s premises AND the SOT profile as the user saw it', () => {
  const SEEN = [meta('hosts', 3), meta('workspaces', 7)]
  const draft = (over: Partial<WizardDraft> = {}): WizardDraft => ({ hostId: 'h1', profileId: P, seen: sotFingerprint(indexEntry(P, 'default', SEEN)), localId: MASTER_PROFILE_ID, direction: 'push', saveAs: null, ...over })

  beforeEach(() => {
    connectHost()
    listed(indexEntry(P, 'default', SEEN))
  })

  it('nothing moved: a plan — frozen, and exactly what was asked for', async () => {
    const r = await prepareRun(draft({ direction: 'push', saveAs: 'Kept', localId: 's1' }))
    expect(r).toEqual({ ok: true, plan: { hostId: 'h1', profileId: P, localId: 's1', direction: 'push', saveAs: null, at: '10.0.0.1:7860', seen: sotFingerprint(indexEntry(P, 'default', SEEN)) } })
    expect(r.ok && Object.isFrozen(r.plan)).toBe(true)
    // pinned to where the host is (host-sync-identity §8): a host re-pointed during the ask is not listed elsewhere
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: '10.0.0.1:7860' })
    // a push reads nothing but the list: the host's verification is a pull's premise only
    expect(getSection).not.toHaveBeenCalled()
  })

  it('the fingerprint is the live sections\' name, rev and hash — in any order; never a payload', () => {
    expect(sotFingerprint(indexEntry(P, 'x', [SEEN[1], SEEN[0]]))).toBe(sotFingerprint(indexEntry(P, 'renamed', SEEN)))
    expect(sotFingerprint(indexEntry(P, 'x', []))).not.toBe(sotFingerprint(indexEntry(P, 'x', SEEN)))
    expect(sotFingerprint(indexEntry(P, 'x', [meta('hosts', 3), meta('workspaces', 8)]))).not.toBe(sotFingerprint(indexEntry(P, 'x', SEEN)))
    expect(sotFingerprint(indexEntry(P, 'x', [meta('hosts', 3), meta('workspaces', 7, 'other')]))).not.toBe(sotFingerprint(indexEntry(P, 'x', SEEN)))
  })

  it('a RETIRED section (`hosts`, host ownership H3b) is no content: not in the fingerprint, and a profile holding only it is empty', () => {
    const withoutHosts = [meta('workspaces', 7)]
    expect(sotFingerprint(indexEntry(P, 'x', [meta('hosts', 3), ...withoutHosts]))).toBe(sotFingerprint(indexEntry(P, 'x', withoutHosts)))
    expect(sotFingerprint(indexEntry(P, 'x', [meta('hosts', 4), ...withoutHosts]))).toBe(sotFingerprint(indexEntry(P, 'x', [meta('hosts', 3), ...withoutHosts])))
    expect(sotNow(indexEntry(P, 'x', [meta('hosts', 3)]))).toEqual(sotNow(indexEntry(P, 'x', [])))
    expect(sotNow(indexEntry(P, 'x', [meta('hosts', 3)])).empty).toBe(true)
    expect(sotNow(indexEntry(P, 'x', [meta('settings', 1)])).empty).toBe(false)
  })

  it('the retired set is projections.ts\' `isRetiredSection` — one list for the sync loop and the wizard', () => {
    const real = vi.mocked(isRetiredSection).getMockImplementation()!
    vi.mocked(isRetiredSection).mockImplementation((key) => key === 'workspaces' || real(key))
    try {
      expect(sotNow(indexEntry(P, 'x', [meta('workspaces', 7)])).empty).toBe(true)
      expect(sotFingerprint(indexEntry(P, 'x', [meta('workspaces', 7), meta('settings', 1)]))).toBe(sotFingerprint(indexEntry(P, 'x', [meta('settings', 1)])))
    } finally {
      vi.mocked(isRetiredSection).mockImplementation(real)
    }
  })

  it('a `hosts` rev change between choosing and Start: no `profile-changed` — a `settings` rev change is one', async () => {
    listed(indexEntry(P, 'default', [meta('hosts', 9), meta('workspaces', 7)]))
    expect(await prepareRun(draft())).toMatchObject({ ok: true })
    listed(indexEntry(P, 'default', [meta('hosts', 3), meta('workspaces', 7), meta('settings', 1)]))
    expect(await prepareRun(draft())).toMatchObject({ ok: false, reason: 'profile-changed' })
  })

  it('THE ATTACK (review F1): seen EMPTY, another device has pushed a whole world since → refused, with what is there now', async () => {
    const fresh = indexEntry(P, 'default', SEEN)
    const r = await prepareRun(draft({ seen: sotFingerprint(indexEntry(P, 'default', [])) }))
    expect(r).toEqual({ ok: false, reason: 'profile-changed', now: { fingerprint: sotFingerprint(fresh), empty: false } })
  })

  it('same sections, ONE rev moved → refused (existence alone is not the check)', async () => {
    listed(indexEntry(P, 'default', [meta('hosts', 3), meta('workspaces', 8)]))
    expect(await prepareRun(draft())).toMatchObject({ ok: false, reason: 'profile-changed', now: { empty: false } })
  })

  it('a section was deleted → refused; emptied altogether → refused, and said to be empty now', async () => {
    listed(indexEntry(P, 'default', [meta('hosts', 3)]))
    expect(await prepareRun(draft())).toMatchObject({ ok: false, reason: 'profile-changed' })
    listed(indexEntry(P, 'default', []))
    expect(await prepareRun(draft())).toMatchObject({ ok: false, reason: 'profile-changed', now: { empty: true } })
  })

  it('the profile is not on the host any more → its own refusal', async () => {
    listed(indexEntry('p_00000000000f', 'another', SEEN))
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'profile-gone' })
  })

  it('the host cannot be asked → refused, with the failure\'s CLASS — never its message', async () => {
    vi.mocked(listProfiles).mockResolvedValue(transportFailure('timeout'))
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'list-failed', request: 'timeout' })
    vi.mocked(listProfiles).mockRejectedValue(new Error('boom SECRET'))
    const r = await prepareRun(draft())
    expect(r).toEqual({ ok: false, reason: 'list-failed', request: 'thrown' })
    expect(JSON.stringify(r)).not.toContain('SECRET')
  })

  it.each([
    ['a master is attached', () => useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P, masterEndpoint: '10.0.0.1:7860' }), 'attached-elsewhere'],
    ['the host is gone', () => useHostStore.setState({ hosts: {}, hostOrder: [] }), 'host-gone'],
    ['the host is offline', () => useHostStore.setState({ runtime: {} }), 'host-offline'],
    ['the local profile is gone', () => useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] }), 'local-gone'],
  ])('this device\'s premises first — %s: refused WITHOUT asking the host', async (_label, breakIt, reason) => {
    breakIt()
    expect(await prepareRun(draft({ localId: 's1' }))).toEqual({ ok: false, reason })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('… and AGAIN after the host has answered: what moved while it was asked is caught', async () => {
    vi.mocked(listProfiles).mockImplementation(async () => {
      useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P, masterEndpoint: '10.0.0.1:7860' })
      return { kind: 'ok', value: [indexEntry(P, 'default', SEEN)] }
    })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'attached-elsewhere' })
  })

  it.each([
    ['no host', { hostId: null }],
    ['no profile', { profileId: null }],
    ['no direction', { direction: null }],
    ['never seen', { seen: null }],
  ])('a draft with %s is no plan', async (_label, over) => {
    expect(await prepareRun(draft(over as Partial<WizardDraft>))).toMatchObject({ ok: false, reason: 'incomplete' })
  })
})

describe('prepareRun — a PULL: the host verified (host-sync-identity §8, D3); no host list read, no host removed (host ownership H3b)', () => {
  const DAEMON = 'mini-lab:278cbm'
  const AT = '10.0.0.1:7860'
  const HOSTS_META = meta('hosts', 3)
  const SEEN = [HOSTS_META, meta('workspaces', 7)]
  const draft = (over: Partial<WizardDraft> = {}): WizardDraft => ({ hostId: 'h1', profileId: P, seen: sotFingerprint(indexEntry(P, 'default', SEEN)), localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', ...over })
  const h = (id: string, name: string, extra: Record<string, unknown> = {}) => ({ id, name, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
  const verified = { status: 'connected' as const, daemonIdVerified: { endpoint: AT, daemonId: DAEMON } }

  beforeEach(() => {
    // h1 is the attach host (verified); h2 is a host only this device has — the SOT's legacy `hosts` row may not name it
    useHostStore.setState({ hosts: { h1: h('h1', 'mlab', { daemonId: DAEMON }), h2: { ...h('h2', 'old box'), ip: '10.0.0.2' } }, hostOrder: ['h1', 'h2'], runtime: { h1: verified } })
    listed(indexEntry(P, 'default', SEEN))
    // a legacy `hosts` row that lists neither h2 nor the attach host: it must not matter, and must not be read
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { z: { name: 'box', daemonId: 'box:111111' } }, hostOrder: ['z'] } } })
  })

  it('the host is verified: a plan — the list the only read, pinned to the host\'s address; the SOT\'s `hosts` is never asked for', async () => {
    const hostsBefore = useHostStore.getState().hosts
    const r = await prepareRun(draft())
    expect(r).toEqual({ ok: true, plan: { hostId: 'h1', profileId: P, localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', at: AT, seen: sotFingerprint(indexEntry(P, 'default', SEEN)) } })
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: AT })
    expect(getSection).not.toHaveBeenCalled()
    expect(useHostStore.getState().hosts).toBe(hostsBefore)
  })

  it('NOT VERIFIED — no claim, or confirmed at another address, or of another claim: refused before the host is asked', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    useHostStore.setState({ runtime: { h1: { ...verified, daemonIdVerified: { endpoint: '10.0.0.9:7860', daemonId: DAEMON } } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: h('h1', 'mlab') }, runtime: { h1: verified } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('A MISMATCH — the daemon at that address is another one: refused as such, before the host is asked', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected', daemonIdMismatch: { stored: DAEMON, observed: 'other:zzzzzz', endpoint: AT } } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-mismatch' })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('… and AGAIN after the host has answered: a mismatch found meanwhile is caught', async () => {
    vi.mocked(listProfiles).mockImplementation(async () => {
      useHostStore.setState({ runtime: { h1: { status: 'connected', daemonIdMismatch: { stored: DAEMON, observed: 'other:zzzzzz', endpoint: AT } } } })
      return { kind: 'ok', value: [indexEntry(P, 'default', SEEN)] }
    })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-mismatch' })
  })

  it('a push is unchanged: an unverified host pushes', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(await prepareRun(draft({ direction: 'push' }))).toMatchObject({ ok: true, plan: { direction: 'push' } })
  })

  it('THE ATTACH HOST MUST EXIST HERE (spec §5.1): gone → host-gone, before the host is asked (§0.7)', async () => {
    useHostStore.setState({ hosts: { h2: useHostStore.getState().hosts.h2 }, hostOrder: ['h2'] })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'host-gone' })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('a host added here meanwhile changes nothing a pull decides: still a plan', async () => {
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h3: { ...h('h3', 'new'), ip: '10.0.0.3' } } })
    expect(await prepareRun(draft())).toMatchObject({ ok: true, plan: { direction: 'pull' } })
    expect(getSection).not.toHaveBeenCalled()
  })
})

describe('THE ATTACH ASKS AGAIN: promote and copy take time, and what the pull was prepared against may move meanwhile', () => {
  const SEEN = [meta('hosts', 3), meta('workspaces', 7)]
  const h = (id: string, name: string, extra: Record<string, unknown> = {}) => ({ id, name, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
  /** As `prepareRun` froze it: s1 promoted, a copy kept. */
  const prepared = (over: Partial<WizardPlan> = {}) => plan({ localId: 's1', saveAs: 'Kept', seen: sotFingerprint(indexEntry(P, 'default', SEEN)), ...over })
  /** `runPlan`, with `move` made while sub-step `during` runs. */
  const runMoving = (p: WizardPlan, during: 'promote' | 'save', move: () => void) =>
    runPlan(p, 0, (i, state) => {
      if (state === 'running' && subStepsOf(p)[i] === during) move()
    })

  beforeEach(() => {
    useHostStore.setState({ hosts: { h1: h('h1', 'mlab', { daemonId: H1_DAEMON }), h2: { ...h('h2', 'old box'), ip: '10.0.0.2' } }, hostOrder: ['h1', 'h2'], runtime: { h1: { status: 'connected', daemonIdVerified: { endpoint: H1_AT, daemonId: H1_DAEMON } } } })
    listed(indexEntry(P, 'default', SEEN))
  })

  it('nothing moved: the attach is made with THREE arguments — after the host was asked again, at the address the plan was made for; `hosts` never read', async () => {
    expect(await runPlan(prepared(), 0, () => {})).toEqual({ done: true })
    expect(attachMaster).toHaveBeenCalledTimes(1)
    expect(vi.mocked(attachMaster).mock.calls[0]).toEqual(['h1', P, 'pull'])
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: H1_AT })
    expect(getSection).not.toHaveBeenCalled()
  })

  it.each(['promote', 'save'] as const)('during the %s, the SOT moves: no attach — the profile changed, with what is there now', async (during) => {
    const moved = [meta('hosts', 3), meta('workspaces', 8)]
    const r = await runMoving(prepared(), during, () => listed(indexEntry(P, 'default', moved)))
    expect(attachMaster).not.toHaveBeenCalled()
    expect(r).toEqual({ done: false, failedAt: 2, reason: 'profile-changed', recheck: { ok: false, reason: 'profile-changed', now: { fingerprint: sotFingerprint(indexEntry(P, 'x', moved)), empty: false } } })
  })

  it.each(['promote', 'save'] as const)('during the %s, a host is added here: the attach is made all the same — a pull removes no host', async (during) => {
    const r = await runMoving(prepared(), during, () =>
      useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h3: { ...h('h3', 'new'), ip: '10.0.0.3' } }, hostOrder: ['h1', 'h2', 'h3'] }),
    )
    expect(r).toEqual({ done: true })
    expect(vi.mocked(attachMaster).mock.calls).toEqual([['h1', P, 'pull']])
  })

  it.each(['promote', 'save'] as const)('during the %s, the attach host turns out to be at another daemon: no attach — master-mismatch', async (during) => {
    const r = await runMoving(prepared(), during, () =>
      useHostStore.getState().setRuntime('h1', { daemonIdMismatch: { stored: H1_DAEMON, observed: 'other:zzzzzz', endpoint: H1_AT } }),
    )
    expect(attachMaster).not.toHaveBeenCalled()
    expect(r).toMatchObject({ done: false, failedAt: 2, reason: 'master-mismatch' })
  })

  it('during the promote, the attach host is removed here: no attach — host-gone', async () => {
    const r = await runMoving(prepared(), 'promote', () => useHostStore.setState({ hosts: { h2: useHostStore.getState().hosts.h2 }, hostOrder: ['h2'] }))
    expect(attachMaster).not.toHaveBeenCalled()
    expect(r).toMatchObject({ done: false, failedAt: 2, reason: 'host-gone' })
  })

  it('a push attaches with three arguments too', async () => {
    expect(await runPlan(prepared({ direction: 'push', saveAs: null }), 0, () => {})).toEqual({ done: true })
    expect(vi.mocked(attachMaster).mock.calls[0]).toEqual(['h1', P, 'push'])
  })

  it('the host was re-pointed meanwhile (another address): no attach — said as the request class, and retryable', async () => {
    const r = await runMoving(prepared(), 'save', () => {
      const { hosts } = useHostStore.getState()
      useHostStore.setState({ hosts: { ...hosts, h1: { ...hosts.h1, ip: '10.0.0.9' } } })
    })
    expect(attachMaster).not.toHaveBeenCalled()
    expect(r).toEqual({ done: false, failedAt: 2, reason: 'list-failed', recheck: { ok: false, reason: 'list-failed', request: 'endpoint-changed' } })
  })

  it('a push asks again too: the profile filled meanwhile → no attach', async () => {
    const r = await runMoving(prepared({ direction: 'push', saveAs: null }), 'promote', () => listed(indexEntry(P, 'default', [meta('workspaces', 8)])))
    expect(attachMaster).not.toHaveBeenCalled()
    expect(r).toMatchObject({ done: false, failedAt: 1, reason: 'profile-changed' })
  })
})

describe('createSotProfile — a create whose outcome is NOT KNOWN is looked for before it is ever sent again (review F2)', () => {
  const BASE = ['p_00000000000a']
  const before = indexEntry('p_00000000000a', 'Work') // there when the wizard first listed: same name, empty — and NOT ours
  const ours = indexEntry('p_00000000000b', 'Work', [], { createdAt: 50 })

  it('created: its id', async () => {
    vi.mocked(createProfile).mockResolvedValue({ kind: 'ok', value: { id: 'p_00000000000b', name: 'Work', createdAt: 1, updatedAt: 1 } })
    expect(await createSotProfile('h1', 'Work', BASE, false)).toEqual({ ok: true, id: 'p_00000000000b' })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it.each(['rejected', 'unauthorized', 'too-large', 'contended', 'not-found', 'unknown-host'])('a DEFINITE failure (%s): said, nothing looked for — nothing was created', async (reason) => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure(reason))
    expect(await createSotProfile('h1', 'Work', BASE, false)).toEqual({ ok: false, outcome: 'failed', request: reason })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it.each(['timeout', 'network', 'aborted', 'server', 'malformed'])('outcome unknown (%s), and the list now holds a profile of that name, empty, unattached, that was NOT there before → it MAY be ours — or another device\'s, created in the same moment: POINTED AT, never taken (review F2, second round)', async (reason) => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure(reason))
    listed(before, ours)
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: ours.id })
    expect(createProfile).toHaveBeenCalledTimes(1)
  })

  it('thrown: unknown likewise — a candidate is pointed at', async () => {
    vi.mocked(createProfile).mockRejectedValue(new Error('SECRET'))
    listed(before, ours)
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: ours.id })
  })

  it('THE SAME NAME, EMPTY, BUT THERE BEFORE the wizard opened is never taken for ours', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    listed(before)
    expect(await createSotProfile('h1', 'Work', BASE, false)).toEqual({ ok: false, outcome: 'not-created', request: 'timeout' })
  })

  it('new since, same name — but it HOLDS something, or a device is attached to it: somebody else\'s, not ours', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    listed(indexEntry('p_00000000000c', 'Work', [meta('workspaces', 1)]), indexEntry('p_00000000000d', 'Work', [], { attachments: [{ clientId: 'c', profileId: 'p_00000000000d', deviceName: 'd', attachedAt: 1, lastSeen: 1 }] }), indexEntry('p_00000000000e', 'Other'))
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'not-created' })
  })

  it('A RETIRED SECTION IS NO CONTENT HERE EITHER (host ownership H3b, as `sotNow`): the create timed out, and an older client has since written only a `hosts` row into the new profile — still ours-maybe, never sent again', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    const hostsOnly = indexEntry('p_00000000000b', 'Work', [meta('hosts', 1)], { createdAt: 50 })
    listed(before, hostsOnly)
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: hostsOnly.id })
    listed(before, hostsOnly)
    expect(await createSotProfile('h1', 'Work', BASE, true)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: hostsOnly.id })
    expect(createProfile).toHaveBeenCalledTimes(1) // no second POST
  })

  it('two candidates (an earlier lost attempt of this visit, too): the newest', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    listed(indexEntry('p_00000000000c', 'Work', [], { createdAt: 10 }), ours)
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: ours.id })
  })

  it('unknown, and the list cannot be read either: STILL unknown — and nothing may be sent again until it can', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    vi.mocked(listProfiles).mockResolvedValue(transportFailure('network'))
    expect(await createSotProfile('h1', 'Work', BASE, false)).toEqual({ ok: false, outcome: 'unknown', request: 'timeout' })
  })

  it('THE NEXT PRESS LOOKS FIRST (`lookFirst`): a candidate → pointed at again, with NO second POST; list unreadable → no POST; not there → the POST goes out', async () => {
    listed(before, ours)
    expect(await createSotProfile('h1', 'Work', BASE, true)).toMatchObject({ ok: false, outcome: 'maybe', candidateId: ours.id })
    expect(createProfile).not.toHaveBeenCalled()

    vi.mocked(listProfiles).mockResolvedValue(transportFailure('network'))
    expect(await createSotProfile('h1', 'Work', BASE, true)).toEqual({ ok: false, outcome: 'unknown', request: 'network' })
    expect(createProfile).not.toHaveBeenCalled()

    listed(before)
    vi.mocked(createProfile).mockResolvedValue({ kind: 'ok', value: { id: 'p_00000000000f', name: 'Work', createdAt: 1, updatedAt: 1 } })
    expect(await createSotProfile('h1', 'Work', BASE, true)).toEqual({ ok: true, id: 'p_00000000000f' })
    expect(createProfile).toHaveBeenCalledTimes(1)
  })

  it('NO BASELINE (the list had never been read when "new" was chosen): nothing can be told apart — a same-name empty profile is neither pointed at as ours nor doubled', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    listed(ours)
    expect(await createSotProfile('h1', 'Work', null, false)).toEqual({ ok: false, outcome: 'same-name', request: 'timeout' })
    expect(await createSotProfile('h1', 'Work', null, true)).toEqual({ ok: false, outcome: 'same-name', request: 'thrown' })
    expect(createProfile).toHaveBeenCalledTimes(1)
    listed()
    expect(await createSotProfile('h1', 'Work', null, false)).toMatchObject({ ok: false, outcome: 'not-created' })
  })

  it('NO BASELINE, and the same-name profile holds only a `hosts` row: it counts as empty — `same-name`, not doubled', async () => {
    vi.mocked(createProfile).mockResolvedValue(transportFailure('timeout'))
    listed(indexEntry('p_00000000000b', 'Work', [meta('hosts', 1)]))
    expect(await createSotProfile('h1', 'Work', null, false)).toEqual({ ok: false, outcome: 'same-name', request: 'timeout' })
    expect(await createSotProfile('h1', 'Work', null, true)).toEqual({ ok: false, outcome: 'same-name', request: 'thrown' })
    expect(createProfile).toHaveBeenCalledTimes(1)
  })

  it('one rule for "empty": what `sotNow` calls empty is what the create-recovery calls blank', () => {
    for (const sections of [[], [meta('hosts', 1)], [meta('workspaces', 1)], [meta('hosts', 1), meta('settings', 2)]]) {
      expect(hasLiveSections(indexEntry(P, 'x', sections))).toBe(!sotNow(indexEntry(P, 'x', sections)).empty)
    }
    expect(hasLiveSections(indexEntry(P, 'x', [meta('hosts', 1)]))).toBe(false)
    expect(hasLiveSections(indexEntry(P, 'x', [meta('tabs.w1', 1)]))).toBe(true)
  })
})

describe('announceRun — the result is said where a replaced world cannot take it away (acceptance F6)', () => {
  const said = () => useUndoToast.getState().toast?.message ?? null
  const labels = { profile: 'default', host: 'mlab' }
  beforeEach(() => useUndoToast.getState().dismiss())

  it('done, push: one sentence — also while the wizard is still there', () => {
    announceRun({ done: true }, plan({ direction: 'push' }), labels, true)
    expect(said()).toBe(en['settings.profile.wizard.toast.done'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab'))
  })

  it('done, pull with the copy: and where what this device held is now', () => {
    announceRun({ done: true }, plan({ saveAs: 'Laptop' }), labels, false)
    expect(said()).toBe(en['settings.profile.wizard.toast.done_saved'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab').replace('{{name}}', 'Laptop'))
  })

  it('done, pull without a copy: the plain sentence', () => {
    announceRun({ done: true }, plan({ saveAs: null }), labels, false)
    expect(said()).toBe(en['settings.profile.wizard.toast.done'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab'))
  })

  it('failed while the wizard is on screen: the wizard says it, no toast', () => {
    announceRun({ done: false, failedAt: 0, reason: 'timeout' }, plan({ saveAs: null }), labels, true)
    expect(said()).toBeNull()
  })

  it.each([[0, 'promote'], [1, 'save'], [2, 'attach']] as const)('failed and the wizard is GONE: a toast names the step it stopped at (%i → %s) — and nothing of the reason', (failedAt, step) => {
    announceRun({ done: false, failedAt, reason: 'Failed to fetch SECRET' }, plan({ localId: 's1' }), labels, false)
    expect(said()).toBe(en[`settings.profile.wizard.toast.stopped_${step}`])
  })
})
