// spa/src/lib/profile/profile-state.test.ts — the profile level of Profile Sync
// (spec §4.4, §4.5, §4.6.3): shape verdicts, the whole-profile schema lock, the
// profile status roll-up, and which `tabs.*` sections should exist.
import { describe, expect, it } from 'vitest'
import { shapeTable } from './projections'
import { compareShape, profileLock, profileStatus, reconcileSectionSet } from './profile-state'
import { initialSectionState } from './sync-state'
import type { SectionSyncState } from './sync-state'
import type { SectionKind, Shape, SotIndexEntry } from './types'

const FP_A = 'a'.repeat(64)
const FP_B = 'b'.repeat(64)

function shape(fingerprint: string, ordinal: number): Shape {
  return { fingerprint, ordinal }
}

const MINE: Record<SectionKind, Shape> = {
  hosts: shape(FP_A, 2),
  settings: shape(FP_A, 2),
  workspaces: shape(FP_A, 2),
  tabs: shape(FP_A, 2),
}

function entry(section: string, fingerprint: string, ordinal: number): SotIndexEntry {
  return { section, rev: 1, hash: 'h'.repeat(64), fingerprint, ordinal }
}

function section(status: SectionSyncState['status']): SectionSyncState {
  return { ...initialSectionState(null), status }
}

function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null) {
    for (const v of Object.values(value)) deepFreeze(v)
    Object.freeze(value)
  }
  return value
}

/** The real shape table of this build, as `profileLock` takes it. */
async function realShapes(): Promise<Record<SectionKind, Shape>> {
  const table = await shapeTable()
  const of = (kind: SectionKind): Shape => ({ fingerprint: table[kind][0], ordinal: table[kind][1] })
  return { hosts: of('hosts'), settings: of('settings'), workspaces: of('workspaces'), tabs: of('tabs') }
}

describe('compareShape (spec §4.5)', () => {
  it('same fingerprint → ok, even when the ordinals differ', () => {
    expect(compareShape(shape(FP_A, 1), shape(FP_A, 1))).toBe('ok')
    expect(compareShape(shape(FP_A, 1), shape(FP_A, 9))).toBe('ok')
    expect(compareShape(shape(FP_A, 9), shape(FP_A, 1))).toBe('ok')
  })

  it('different fingerprint, my ordinal higher → i-am-newer', () => {
    expect(compareShape(shape(FP_A, 3), shape(FP_B, 2))).toBe('i-am-newer')
  })

  it('different fingerprint, my ordinal lower → sot-is-newer', () => {
    expect(compareShape(shape(FP_A, 2), shape(FP_B, 3))).toBe('sot-is-newer')
  })

  it('different fingerprint, equal ordinal → shape-changed-without-ordinal (fail closed)', () => {
    expect(compareShape(shape(FP_A, 2), shape(FP_B, 2))).toBe('shape-changed-without-ordinal')
  })
})

describe('profileLock (spec §4.4: any moved shape locks the whole profile)', () => {
  it('empty index → null', () => {
    expect(profileLock([], MINE)).toBeNull()
  })

  it('every section ok → null', () => {
    const index = [entry('hosts', FP_A, 2), entry('settings', FP_A, 2), entry('workspaces', FP_A, 2), entry('tabs.ws1', FP_A, 2)]
    expect(profileLock(index, MINE)).toBeNull()
  })

  it('i-am-newer does not lock', () => {
    expect(profileLock([entry('hosts', FP_B, 1)], MINE)).toBeNull()
  })

  it('sot-is-newer locks and names the section', () => {
    expect(profileLock([entry('hosts', FP_A, 2), entry('settings', FP_B, 3)], MINE)).toEqual({
      section: 'settings',
      kind: 'settings',
      verdict: 'sot-is-newer',
      mine: MINE.settings,
      sot: shape(FP_B, 3),
    })
  })

  it('equal ordinal with a different fingerprint locks', () => {
    const lock = profileLock([entry('workspaces', FP_B, 2)], MINE)
    expect(lock?.section).toBe('workspaces')
    expect(lock?.verdict).toBe('shape-changed-without-ordinal')
  })

  it('a section of unknown kind is skipped, whatever its shape', () => {
    expect(profileLock([entry('plugins', FP_B, 99), entry('tabs.', FP_B, 99), entry('tabs.bad id', FP_B, 99)], MINE)).toBeNull()
  })

  it('several offenders → the first by section key, regardless of input order', () => {
    const offenders = [entry('workspaces', FP_B, 3), entry('tabs.zz', FP_B, 2), entry('hosts', FP_B, 3), entry('tabs.aa', FP_B, 3)]
    const forward = profileLock(offenders, MINE)
    const backward = profileLock([...offenders].reverse(), MINE)
    // `hosts` sorts first but is retired (host ownership H3a-2): the first offender is the next one
    expect(forward?.section).toBe('tabs.aa')
    expect(backward).toEqual(forward)
  })

  it('every tabs.* section is compared against mine.tabs', () => {
    const mine = { ...MINE, tabs: shape(FP_B, 5) }
    expect(profileLock([entry('tabs.a', FP_B, 5), entry('tabs.b', FP_B, 5)], mine)).toBeNull()
    const lock = profileLock([entry('tabs.a', FP_B, 5), entry('tabs.b', FP_A, 6)], mine)
    expect(lock).toEqual({ section: 'tabs.b', kind: 'tabs', verdict: 'sot-is-newer', mine: mine.tabs, sot: shape(FP_A, 6) })
  })

  it('does not mutate its inputs', () => {
    const index = deepFreeze([entry('workspaces', FP_B, 3), entry('settings', FP_B, 3)])
    expect(profileLock(index, deepFreeze({ ...MINE }))?.section).toBe('settings')
  })

  // host ownership H3a-2 (spec §5.1): `hosts` is a retired kind — this client never reads, writes or deletes it, so
  // whatever shape the SOT's row has cannot concern it. Still a KNOWN kind (sectionKind), only skipped here.
  it('a retired section (hosts) never locks: newer ordinal, or unorderable, alike', () => {
    expect(profileLock([entry('hosts', FP_B, 3)], MINE)).toBeNull()
    expect(profileLock([entry('hosts', FP_B, 2)], MINE)).toBeNull()
    expect(profileLock([entry('hosts', FP_B, 99), entry('workspaces', FP_A, 2)], MINE)).toBeNull()
  })

  it('a retired section does not shield a real offender next to it', () => {
    expect(profileLock([entry('hosts', FP_B, 3), entry('settings', FP_B, 3)], MINE)).toMatchObject({ section: 'settings', verdict: 'sot-is-newer' })
  })

  it('acceptance 7: an older client against the real shape table is locked, naming the section', async () => {
    const current = await realShapes()
    const index = (['hosts', 'settings', 'workspaces'] as const).map((k) => entry(k, current[k].fingerprint, current[k].ordinal))
    index.push(entry('tabs.ws1', current.tabs.fingerprint, current.tabs.ordinal))
    expect(profileLock(index, current)).toBeNull()

    const older = { ...current, tabs: { fingerprint: FP_A, ordinal: current.tabs.ordinal - 1 } }
    const lock = profileLock(index, older)
    expect(lock?.section).toBe('tabs.ws1')
    expect(lock?.kind).toBe('tabs')
    expect(lock?.verdict).toBe('sot-is-newer')
    expect(lock?.sot).toEqual(current.tabs)
  })
})

describe('profileStatus (spec §4.4)', () => {
  const lock = { section: 'hosts', kind: 'hosts', verdict: 'sot-is-newer', mine: shape(FP_A, 1), sot: shape(FP_B, 2) } as const

  it('no master → idle, over everything else', () => {
    expect(profileStatus({ hasMaster: false, sections: {}, lock: null })).toBe('idle')
    expect(profileStatus({ hasMaster: false, sections: { hosts: section('locked:reset') }, lock })).toBe('idle')
  })

  it('a schema lock → locked:schema, over any section status', () => {
    expect(profileStatus({ hasMaster: true, sections: { hosts: section('locked:reset') }, lock })).toBe('locked:schema')
    expect(profileStatus({ hasMaster: true, sections: {}, lock })).toBe('locked:schema')
  })

  it('no sections → synced', () => {
    expect(profileStatus({ hasMaster: true, sections: {}, lock: null })).toBe('synced')
  })

  it('all synced → synced', () => {
    expect(profileStatus({ hasMaster: true, sections: { hosts: section('synced'), settings: section('synced') }, lock: null })).toBe('synced')
  })

  it('worst of sections: locked:reset > locked:conflict > locked:invalid > pending > synced', () => {
    const status = (...statuses: SectionSyncState['status'][]) =>
      profileStatus({
        hasMaster: true,
        sections: Object.fromEntries(statuses.map((s, i) => [`tabs.w${i}`, section(s)])),
        lock: null,
      })
    expect(status('synced', 'pending')).toBe('pending')
    expect(status('pending', 'synced')).toBe('pending')
    expect(status('pending', 'locked:conflict', 'synced')).toBe('locked:conflict')
    expect(status('locked:conflict', 'pending')).toBe('locked:conflict')
    expect(status('locked:conflict', 'locked:reset', 'pending')).toBe('locked:reset')
    expect(status('locked:reset', 'locked:conflict')).toBe('locked:reset')
    expect(status('synced', 'locked:invalid')).toBe('locked:invalid')
    expect(status('locked:invalid', 'pending')).toBe('locked:invalid')
    expect(status('pending', 'locked:invalid', 'synced')).toBe('locked:invalid')
    expect(status('locked:invalid', 'locked:conflict')).toBe('locked:conflict')
    expect(status('locked:conflict', 'locked:invalid', 'pending')).toBe('locked:conflict')
    expect(status('locked:invalid', 'locked:reset')).toBe('locked:reset')
  })
})

describe('reconcileSectionSet (spec §4.6.3)', () => {
  const FIXED = ['hosts', 'settings', 'workspaces']
  const none = { create: [], remove: [], keepUnrendered: [], unknown: [] }

  it('a workspace with no tabs.* on either side → create', () => {
    expect(
      reconcileSectionSet({ workspaceIds: ['w1', 'w2'], previousWorkspaceIds: ['w1'], localKeys: [...FIXED, 'tabs.w1'], sotKeys: [...FIXED, 'tabs.w1'] }),
    ).toEqual({ ...none, create: ['tabs.w2'] })
  })

  it('tabs.* missing on one side only is not a create (that is a plain push or pull)', () => {
    expect(reconcileSectionSet({ workspaceIds: ['w1'], previousWorkspaceIds: [], localKeys: FIXED, sotKeys: [...FIXED, 'tabs.w1'] })).toEqual(none)
    expect(reconcileSectionSet({ workspaceIds: ['w1'], previousWorkspaceIds: [], localKeys: [...FIXED, 'tabs.w1'], sotKeys: FIXED })).toEqual(none)
  })

  it('a workspace this client knew, gone from the applied workspaces → remove (from either side)', () => {
    expect(
      reconcileSectionSet({ workspaceIds: ['w1'], previousWorkspaceIds: ['w1', 'w2', 'w3'], localKeys: ['tabs.w1', 'tabs.w2'], sotKeys: ['tabs.w1', 'tabs.w3'] }),
    ).toEqual({ ...none, remove: ['tabs.w2', 'tabs.w3'] })
  })

  it('a tabs.* whose workspace was never seen → keepUnrendered, never removed', () => {
    expect(
      reconcileSectionSet({ workspaceIds: ['w1'], previousWorkspaceIds: ['w1'], localKeys: ['tabs.w1'], sotKeys: ['tabs.w1', 'tabs.early'] }),
    ).toEqual({ ...none, keepUnrendered: ['tabs.early'] })
  })

  it('a key of unknown kind → unknown, never in remove', () => {
    const out = reconcileSectionSet({
      workspaceIds: [],
      previousWorkspaceIds: ['plugins', 'bad id'],
      localKeys: ['plugins'],
      sotKeys: ['tabs.bad id', 'future.kind', 'tabs.'],
    })
    expect(out).toEqual({ ...none, unknown: ['future.kind', 'plugins', 'tabs.', 'tabs.bad id'] })
  })

  it('the three fixed sections never appear in any list', () => {
    const out = reconcileSectionSet({ workspaceIds: [], previousWorkspaceIds: ['hosts', 'settings', 'workspaces'], localKeys: FIXED, sotKeys: FIXED })
    expect(out).toEqual(none)
  })

  it('a workspace id that cannot form a section key throws', () => {
    expect(() => reconcileSectionSet({ workspaceIds: ['bad id'], previousWorkspaceIds: [], localKeys: [], sotKeys: [] })).toThrow(/cannot form a section key/)
  })

  it('outputs are de-duplicated and sorted', () => {
    const out = reconcileSectionSet({
      workspaceIds: ['n2', 'n1', 'n2'],
      previousWorkspaceIds: ['g2', 'g1', 'g1'],
      localKeys: ['tabs.g2', 'tabs.g1', 'tabs.u2', 'zeta', 'tabs.g1'],
      sotKeys: ['tabs.g1', 'tabs.u1', 'tabs.u2', 'alpha', 'zeta'],
    })
    expect(out).toEqual({
      create: ['tabs.n1', 'tabs.n2'],
      remove: ['tabs.g1', 'tabs.g2'],
      keepUnrendered: ['tabs.u1', 'tabs.u2'],
      unknown: ['alpha', 'zeta'],
    })
  })

  it('does not mutate its inputs', () => {
    const args = deepFreeze({ workspaceIds: ['w2', 'w1'], previousWorkspaceIds: ['w3'], localKeys: ['tabs.w3', 'x'], sotKeys: ['tabs.w9'] })
    expect(reconcileSectionSet(args)).toEqual({ create: ['tabs.w1', 'tabs.w2'], remove: ['tabs.w3'], keepUnrendered: ['tabs.w9'], unknown: ['x'] })
  })
})
