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
import { ATTACH_REASONS, announceRun, countWorld, createSotProfile, prepareRun, previewPull, runPlan, sotFingerprint, subStepsOf, worldToBeMaster, type SubStepState, type WizardDraft, type WizardPlan } from './wizard-run'

vi.mock('../../../../lib/profile/start', () => ({ attachMaster: vi.fn() }))
vi.mock('../../../../lib/profile/api', () => ({ listProfiles: vi.fn(), createProfile: vi.fn(), getSection: vi.fn() }))

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

const plan = (over: Partial<WizardPlan>): WizardPlan => ({ hostId: 'h1', profileId: P, localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', removesHosts: [], ...over })
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
  vi.mocked(listProfiles).mockReset()
  vi.mocked(createProfile).mockReset()
  vi.mocked(getSection).mockReset()
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
    expect(r).toEqual({ ok: true, plan: { hostId: 'h1', profileId: P, localId: 's1', direction: 'push', saveAs: null, removesHosts: [] } })
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

describe('prepareRun — a PULL: the host verified, and the hosts it removes are the ones the user was shown (host-sync-identity §8)', () => {
  const DAEMON = 'mini-lab:278cbm'
  const AT = '10.0.0.1:7860'
  const HOSTS_META = meta('hosts', 3)
  const SEEN = [HOSTS_META, meta('workspaces', 7)]
  const draft = (over: Partial<WizardDraft> = {}): WizardDraft => ({ hostId: 'h1', profileId: P, seen: sotFingerprint(indexEntry(P, 'default', SEEN)), localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', removesSeen: [], ...over })
  const h = (id: string, name: string, extra: Record<string, unknown> = {}) => ({ id, name, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
  /** The SOT's `hosts` section: one row per daemon, keyed by whatever the writer used. */
  const sotHosts = (rows: Record<string, unknown>, m = HOSTS_META) =>
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...m, payload: { hosts: rows, hostOrder: Object.keys(rows) } } })
  const verified = { status: 'connected' as const, daemonIdVerified: { endpoint: AT, daemonId: DAEMON } }

  beforeEach(() => {
    // h1 is the attach host (verified); h2 is a host only this device has, with no claim — no row can match it
    useHostStore.setState({ hosts: { h1: h('h1', 'mlab', { daemonId: DAEMON }), h2: { ...h('h2', 'old box'), ip: '10.0.0.2' } }, hostOrder: ['h1', 'h2'], runtime: { h1: verified } })
    listed(indexEntry(P, 'default', SEEN))
    sotHosts({ d1_whatever: { id: 'd1_whatever', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0, daemonId: DAEMON } })
  })

  it('the host is verified, the list shown is the list now: a plan that carries it; both reads pinned to the host\'s address', async () => {
    const r = await prepareRun(draft({ removesSeen: ['h2'] }))
    expect(r).toEqual({ ok: true, plan: { hostId: 'h1', profileId: P, localId: MASTER_PROFILE_ID, direction: 'pull', saveAs: 'Kept', removesHosts: ['h2'] } })
    expect(r.ok && Object.isFrozen(r.plan.removesHosts)).toBe(true)
    expect(getSection).toHaveBeenCalledWith('h1', P, 'hosts', { expectEndpoint: AT })
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: AT })
  })

  it('NOT VERIFIED — no claim, or confirmed at another address, or of another claim: refused before the host is asked', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    useHostStore.setState({ runtime: { h1: { ...verified, daemonIdVerified: { endpoint: '10.0.0.9:7860', daemonId: DAEMON } } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: h('h1', 'mlab') }, runtime: { h1: verified } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-unverified' })
    expect(getSection).not.toHaveBeenCalled()
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('A MISMATCH — the daemon at that address is another one: refused as such, before the host is asked', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected', daemonIdMismatch: { stored: DAEMON, observed: 'other:zzzzzz', endpoint: AT } } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'master-mismatch' })
    expect(getSection).not.toHaveBeenCalled()
  })

  it('… and AGAIN after the host has answered: a mismatch found meanwhile is caught', async () => {
    vi.mocked(listProfiles).mockImplementation(async () => {
      useHostStore.setState({ runtime: { h1: { status: 'connected', daemonIdMismatch: { stored: DAEMON, observed: 'other:zzzzzz', endpoint: AT } } } })
      return { kind: 'ok', value: [indexEntry(P, 'default', SEEN)] }
    })
    expect(await prepareRun(draft({ removesSeen: ['h2'] }))).toEqual({ ok: false, reason: 'master-mismatch' })
  })

  it('a push is unchanged: an unverified host pushes', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(await prepareRun(draft({ direction: 'push' }))).toMatchObject({ ok: true, plan: { direction: 'push', removesHosts: [] } })
  })

  it('never shown, or shown another list: refused with the list as it is now — the user is to see it before anything runs', async () => {
    expect(await prepareRun(draft({ removesSeen: null }))).toEqual({ ok: false, reason: 'removes-changed', removes: ['h2'] })
    expect(await prepareRun(draft({ removesSeen: undefined }))).toEqual({ ok: false, reason: 'removes-changed', removes: ['h2'] })
    expect(await prepareRun(draft({ removesSeen: [] }))).toEqual({ ok: false, reason: 'removes-changed', removes: ['h2'] })
    // a host added here meanwhile, which no row names: it would go too
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h3: { ...h('h3', 'new'), ip: '10.0.0.3' } } })
    expect(await prepareRun(draft({ removesSeen: ['h2'] }))).toEqual({ ok: false, reason: 'removes-changed', removes: ['h2', 'h3'] })
  })

  it('a host this device has is matched by its daemon — whatever key the row travels under: not removed', async () => {
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...h('h2', 'old box', { daemonId: 'box:111111' }), ip: '10.0.0.2' } } })
    sotHosts({ zz9999: { name: 'mlab', daemonId: DAEMON }, 'foreign-local-id': { name: 'box', daemonId: 'box:111111' } })
    expect(await prepareRun(draft({ removesSeen: [] }))).toMatchObject({ ok: true, plan: { removesHosts: [] } })
  })

  it('no hosts section on the SOT: a pull touches no host — nothing to remove', async () => {
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: null })
    listed(indexEntry(P, 'default', [meta('workspaces', 7)]))
    const r = await prepareRun(draft({ seen: sotFingerprint(indexEntry(P, 'default', [meta('workspaces', 7)])), removesSeen: [] }))
    expect(r).toMatchObject({ ok: true, plan: { removesHosts: [] } })
  })

  it.each([
    ['two rows name one daemon', { a: { daemonId: DAEMON }, b: { daemonId: DAEMON } }, 'duplicate-host-identity'],
  ])('the rows cannot be matched one-to-one (%s): refused, nothing runs', async (_label, rows, reason) => {
    sotHosts(rows)
    expect(await prepareRun(draft({ removesSeen: ['h2'] }))).toEqual({ ok: false, reason })
  })

  it('two hosts HERE claim the daemon of one row: host-identity-conflict', async () => {
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...h('h2', 'twin', { daemonId: DAEMON }), ip: '10.0.0.2' } } })
    expect(await prepareRun(draft({ removesSeen: [] }))).toEqual({ ok: false, reason: 'host-identity-conflict' })
  })

  it('the hosts section moved between the read and the list: the profile changed — nothing runs', async () => {
    sotHosts({ d1_whatever: { daemonId: DAEMON } }, meta('hosts', 2)) // read at rev 2, the index says 3
    expect(await prepareRun(draft({ removesSeen: ['h2'] }))).toMatchObject({ ok: false, reason: 'profile-changed' })
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: null }) // read: none; the index: rev 3
    expect(await prepareRun(draft({ removesSeen: [] }))).toMatchObject({ ok: false, reason: 'profile-changed' })
  })

  it('the hosts section cannot be read, or is not what a hosts payload is: refused with the failure\'s class', async () => {
    vi.mocked(getSection).mockResolvedValue(transportFailure('endpoint-changed'))
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'list-failed', request: 'endpoint-changed' })
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: ['x'] } } })
    expect(await prepareRun(draft())).toEqual({ ok: false, reason: 'list-failed', request: 'malformed' })
    vi.mocked(getSection).mockRejectedValue(new Error('boom SECRET'))
    const r = await prepareRun(draft())
    expect(r).toEqual({ ok: false, reason: 'list-failed', request: 'thrown' })
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('NO ROW FOR THE ATTACH HOST ITSELF (the pull would remove the very host it pulls through): master-unmatched — from prepareRun and previewPull alike', async () => {
    // one row, for another daemon: h1 (the attach host) matches nothing
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...h('h2', 'old box', { daemonId: 'box:111111' }), ip: '10.0.0.2' } } })
    sotHosts({ zz: { name: 'box', daemonId: 'box:111111' } })
    expect(await prepareRun(draft({ removesSeen: ['h1'] }))).toEqual({ ok: false, reason: 'master-unmatched' })
    expect(await previewPull('h1', P)).toEqual({ ok: false, reason: 'master-unmatched' })
    // no row at all: every host would go, the attach host among them
    sotHosts({})
    expect(await prepareRun(draft({ removesSeen: ['h1', 'h2'] }))).toEqual({ ok: false, reason: 'master-unmatched' })
    expect(await previewPull('h1', P)).toEqual({ ok: false, reason: 'master-unmatched' })
    // OTHER unmatched hosts are no refusal: listed, as before
    sotHosts({ d1: { name: 'mlab', daemonId: DAEMON } })
    expect(await previewPull('h1', P)).toEqual({ ok: true, removes: ['h2'] })
  })

  it('previewPull — what the direction step names: the same list, or why there is none', async () => {
    expect(await previewPull('h1', P)).toEqual({ ok: true, removes: ['h2'] })
    expect(getSection).toHaveBeenCalledWith('h1', P, 'hosts', { expectEndpoint: AT })
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    expect(await previewPull('h1', P)).toEqual({ ok: false, reason: 'master-unverified' })
    useHostStore.setState({ runtime: { h1: verified } })
    vi.mocked(getSection).mockResolvedValue(transportFailure('timeout'))
    expect(await previewPull('h1', P)).toEqual({ ok: false, reason: 'list-failed', request: 'timeout' })
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
    listed(indexEntry('p_00000000000c', 'Work', [meta('hosts', 1)]), indexEntry('p_00000000000d', 'Work', [], { attachments: [{ clientId: 'c', profileId: 'p_00000000000d', deviceName: 'd', attachedAt: 1, lastSeen: 1 }] }), indexEntry('p_00000000000e', 'Other'))
    expect(await createSotProfile('h1', 'Work', BASE, false)).toMatchObject({ ok: false, outcome: 'not-created' })
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
