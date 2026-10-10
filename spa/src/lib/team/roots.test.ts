import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { checkRootInput, configureRoots, editOf, midEllipsis, resetRoots, saveRoots, useRootsStore } from './roots'
import { teamKey } from './max-members'
import { parseRosterEvent, type TeamRoster } from './roster'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'

const H = 'h1'
const T = 'team-1'
const lead = { session_id: 'L', ref: '_lead00', address: 'h/lead', live: true }
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({ id: T, host_id: H, created_at: 1, team_name: '', team_label: '', lead, members: [], roots: ['/a', '/b'], roots_rev: 3, ...over })
const seed = (over: Partial<TeamRoster> = {}) => useTeamRosterStore.getState().apply(H, [team(over)])
const roster = () => useTeamRosterStore.getState().byHost[H][0]
const err = (status: number, code: string, body: Record<string, unknown> = {}) => new ApprovalApiError(status, code, typeof body.detail === 'string' ? body.detail : '', null, { error: code, ...body })
const key = teamKey(H, T)

beforeEach(() => {
  resetRoots()
  useTeamRosterStore.getState().reset()
  configureRoots({ identity: () => 'id-1' })
})

describe('saveRoots', () => {
  it('sends the whole new list with if_rev and takes the stored real paths from the answer', async () => {
    seed()
    const put = vi.fn(async () => ({ team_id: T, roots: ['/real/a', '/b', '/c'], roots_rev: 4 }))
    configureRoots({ put })
    expect(await saveRoots(H, T, ['/a', '/b', '/c'])).toBe(true)
    expect(put).toHaveBeenCalledWith(H, T, ['/a', '/b', '/c'], 3)
    expect(roster().roots).toEqual(['/real/a', '/b', '/c'])
    expect(roster().roots_rev).toBe(4)
    expect(editOf(useRootsStore.getState(), key).error).toBeNull()
  })

  it('does not put its answer over a roster event that already moved the revision', async () => {
    seed()
    configureRoots({
      put: async () => {
        seed({ roots: ['/newer'], roots_rev: 5 }) // the broadcast lands before the answer
        return { team_id: T, roots: ['/a'], roots_rev: 4 }
      },
    })
    await saveRoots(H, T, ['/a'])
    expect(roster().roots).toEqual(['/newer'])
    expect(roster().roots_rev).toBe(5)
  })

  it('is one request per team at a time, and does nothing without a revision (a daemon without the route)', async () => {
    seed()
    let release: (v: { team_id: string; roots: string[]; roots_rev: number }) => void = () => {}
    const put = vi.fn(() => new Promise<{ team_id: string; roots: string[]; roots_rev: number }>((r) => { release = r }))
    configureRoots({ put })
    const first = saveRoots(H, T, ['/a'])
    expect(useRootsStore.getState().inflight[key]).toBeDefined()
    expect(await saveRoots(H, T, ['/b'])).toBe(false)
    expect(put).toHaveBeenCalledTimes(1)
    release({ team_id: T, roots: ['/a'], roots_rev: 4 })
    await first
    expect(useRootsStore.getState().inflight[key]).toBeUndefined()

    useTeamRosterStore.getState().apply(H, [{ ...team(), roots: undefined, roots_rev: undefined }])
    expect(await saveRoots(H, T, ['/x'])).toBe(false)
    expect(put).toHaveBeenCalledTimes(1)
  })

  it('400 bad_request: the daemon\'s detail on the row it named; the list is unchanged', async () => {
    seed()
    configureRoots({ put: async () => { throw err(400, 'bad_request', { detail: 'not a directory', field: 'roots[1]' }) } })
    expect(await saveRoots(H, T, ['/a', '/nope'])).toBe(false)
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'bad_request', detail: 'not a directory', index: 1 })
    expect(roster().roots).toEqual(['/a', '/b'])
  })

  it('400 after a removal: the daemon counts rows of the list it was sent, so the rows behind the removed one are shifted back', async () => {
    seed({ roots: ['/a', '/b', '/c'] })
    configureRoots({ put: async () => { throw err(400, 'bad_request', { detail: 'gone', field: 'roots[1]' }) } })
    await saveRoots(H, T, ['/a', '/c'], 1) // /b removed: sent list is [/a, /c], so roots[1] is /c = row 2 of the shown list
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'bad_request', detail: 'gone', index: 2 })
    configureRoots({ put: async () => { throw err(400, 'bad_request', { detail: 'gone', field: 'roots[0]' }) } })
    await saveRoots(H, T, ['/a', '/c'], 1) // roots[0] is before the removed row: unchanged
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'bad_request', detail: 'gone', index: 0 })
  })

  it('200 whose list is not the sent size (a path folded into an existing root) is said, not silent', async () => {
    seed()
    configureRoots({ put: async () => ({ team_id: T, roots: ['/a', '/b'], roots_rev: 4 }) })
    expect(await saveRoots(H, T, ['/a', '/b', '/a/inner'])).toBe(true)
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'merged' })
  })

  it('409 rev_mismatch: refreshes the list from the answer and says so', async () => {
    seed()
    configureRoots({ put: async () => { throw err(409, 'rev_mismatch', { detail: 'x', roots: ['/other'], roots_rev: 7 }) } })
    await saveRoots(H, T, ['/a'])
    expect(roster().roots).toEqual(['/other'])
    expect(roster().roots_rev).toBe(7)
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'rev_mismatch' })
  })

  it('409 roots_in_use: names who blocks the removal; the list is unchanged', async () => {
    seed()
    const in_use = [{ root: '/b', kind: 'member', ref: '_abc123', cwd: '/b/x' }, { root: '/b', kind: 'spawn', cwd: '/b/y' }, { junk: 1 }]
    configureRoots({ put: async () => { throw err(409, 'roots_in_use', { detail: 'x', in_use }) } })
    await saveRoots(H, T, ['/a'], 1)
    expect(editOf(useRootsStore.getState(), key).error).toEqual({
      kind: 'in_use', removed: '/b',
      items: [{ root: '/b', kind: 'member', ref: '_abc123', cwd: '/b/x' }, { root: '/b', kind: 'spawn', cwd: '/b/y' }],
    })
    expect(roster().roots).toEqual(['/a', '/b'])
  })

  it('404: the team ended; any other code is a plain failure carrying it', async () => {
    seed()
    configureRoots({ put: async () => { throw err(404, 'not_found') } })
    await saveRoots(H, T, ['/a'])
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'ended' })
    configureRoots({ put: async () => { throw err(500, 'storage_error') } })
    await saveRoots(H, T, ['/a'])
    expect(editOf(useRootsStore.getState(), key).error).toEqual({ kind: 'failed', code: 'storage_error' })
  })

  it('the next edit clears the earlier error', async () => {
    seed()
    useRootsStore.getState().setError(key, { kind: 'ended' })
    configureRoots({ put: async () => ({ team_id: T, roots: ['/a'], roots_rev: 4 }) })
    await saveRoots(H, T, ['/a'])
    expect(editOf(useRootsStore.getState(), key).error).toBeNull()
  })
})

describe('checkRootInput', () => {
  it('trims, wants an absolute path, no duplicate, at most 16', () => {
    expect(checkRootInput('  /x  ', ['/a'])).toEqual({ ok: true, path: '/x' })
    expect(checkRootInput('   ', [])).toEqual({ ok: false, reason: 'empty' })
    expect(checkRootInput('rel/x', [])).toEqual({ ok: false, reason: 'relative' })
    expect(checkRootInput('~/x', [])).toEqual({ ok: false, reason: 'relative' })
    expect(checkRootInput('/a', ['/a'])).toEqual({ ok: false, reason: 'duplicate' })
    expect(checkRootInput('/z', Array.from({ length: 16 }, (_, i) => `/r${i}`))).toEqual({ ok: false, reason: 'full' })
  })
})

describe('midEllipsis', () => {
  it('keeps a short path, and a long one keeps its head and tail', () => {
    expect(midEllipsis('/a/b')).toBe('/a/b')
    const long = `/Users/wake/Workspace/${'deep/'.repeat(20)}project`
    const out = midEllipsis(long, 40)
    expect(out.length).toBe(40)
    expect(out).toContain('…')
    expect(out.startsWith('/Users/')).toBe(true)
    expect(out.endsWith('project')).toBe(true)
  })
})

describe('roster wire', () => {
  const wire = (extra: Record<string, unknown>) => ({ op: 'snapshot', teams: [{ id: T, host_id: H, created_at: 1, lead, members: [], ...extra }] })
  it('reads roots with their revision, both or neither', () => {
    const ok = parseRosterEvent(wire({ roots: ['/a'], roots_rev: 0 }))
    expect(typeof ok).not.toBe('string')
    expect((ok as { teams: TeamRoster[] }).teams[0]).toMatchObject({ roots: ['/a'], roots_rev: 0 })
    const noRev = (parseRosterEvent(wire({ roots: ['/a'] })) as { teams: TeamRoster[] }).teams[0]
    expect(noRev.roots).toBeUndefined()
    const bad = (parseRosterEvent(wire({ roots: ['/a', 3], roots_rev: 1 })) as { teams: TeamRoster[] }).teams[0]
    expect(bad.roots).toBeUndefined()
    const old = (parseRosterEvent(wire({})) as { teams: TeamRoster[] }).teams[0]
    expect(old.roots).toBeUndefined()
    expect(old.roots_rev).toBeUndefined()
  })
})
