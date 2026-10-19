// spa/src/stores/useLocalProfilesStore.teamIcon.test.ts — `teamIcon` of a profile's appearance (name pool v2 spec §9.6, NA-5): the icon the sidebar's view toggle shows in the task-group view.
// A Phosphor name from the catalog, per profile and per device like the rest of the appearance; absent = `UsersThree`. It is independent of `icon` (the profile's own icon).
import { beforeEach, describe, expect, it } from 'vitest'
import { STORAGE_KEYS } from '../lib/storage/keys'
import { useLocalProfilesStore, type ParkedWorld } from './useLocalProfilesStore'

const get = () => useLocalProfilesStore.getState()
const EMPTY_WORLD: ParkedWorld = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }

beforeEach(() => {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})

describe('teamIcon', () => {
  it('is absent until chosen, and a chosen one is kept on the master', () => {
    expect(get().master.teamIcon).toBeUndefined()
    expect(get().setProfileAppearance('master', { teamIcon: 'Rocket' })).toEqual({ ok: true })
    expect(get().master.teamIcon).toBe('Rocket')
  })

  it('null clears it, and it is independent of the profile icon', () => {
    get().setProfileAppearance('master', { icon: 'Star', teamIcon: 'Rocket' })
    get().setProfileAppearance('master', { icon: null })
    expect(get().master).toMatchObject({ teamIcon: 'Rocket' })
    expect(get().master.icon).toBeUndefined()
    get().setProfileAppearance('master', { teamIcon: null })
    expect(Object.hasOwn(get().master, 'teamIcon')).toBe(false)
  })

  it.each(['NotAnIcon', '<b>x</b>', 7])('a name that is not in the catalog (%j) refuses the whole patch and leaves the state alone', (bad) => {
    get().setProfileAppearance('master', { name: 'Work', teamIcon: 'Rocket' })
    const before = get().master
    expect(get().setProfileAppearance('master', { name: 'Other', teamIcon: bad as string })).toEqual({ ok: false, reason: 'bad-team-icon' })
    expect(get().master).toBe(before)
  })

  it('a slave has its own, and the rest of its record is untouched', () => {
    const added = get().addSlave('Lab', EMPTY_WORLD)
    if (!added.ok) throw new Error('addSlave refused')
    expect(get().setProfileAppearance(added.id, { teamIcon: 'Flag' })).toEqual({ ok: true })
    expect(get().slaves[added.id]).toMatchObject({ id: added.id, name: 'Lab', teamIcon: 'Flag', world: EMPTY_WORLD })
    expect(get().master.teamIcon).toBeUndefined()
  })

  it('goes with the appearance when a slave is promoted, and with the master when it is demoted', () => {
    const added = get().addSlave('Lab', EMPTY_WORLD)
    if (!added.ok) throw new Error('addSlave refused')
    get().setProfileAppearance('master', { teamIcon: 'Rocket' })
    get().setProfileAppearance(added.id, { teamIcon: 'Flag' })
    const promoted = get().promoteSlave(added.id, 'Old home', 1, [])
    if (!promoted.ok) throw new Error('promote refused')
    expect(get().master.teamIcon).toBe('Flag')
    expect(get().slaves[promoted.demotedId].teamIcon).toBe('Rocket')
  })

  it('a saved teamIcon that is not in the catalog is dropped alone on load; a good one is kept', async () => {
    const slave = (teamIcon: unknown) => ({ id: 'a1', name: 'Lab', createdAt: 1, shownHostIds: [], world: EMPTY_WORLD, teamIcon })
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({
      state: { master: { name: 'Home', icon: 'Star', teamIcon: 'Flag' }, slaves: { a1: slave('NotAnIcon') }, slaveOrder: ['a1'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0 },
      version: 1,
    }))
    await useLocalProfilesStore.persist.rehydrate()
    expect(get().master).toMatchObject({ name: 'Home', icon: 'Star', teamIcon: 'Flag' })
    expect(Object.hasOwn(get().slaves.a1, 'teamIcon')).toBe(false)
    expect(get().slaves.a1.name).toBe('Lab')
  })
})

// The team icon's weight and colour (759 acceptance): the weight stands alone (the default UsersThree wears one too), the colour is a strict #rrggbb of its own.
describe('teamIconWeight', () => {
  it('stands alone: with no teamIcon chosen, and no profile icon either', () => {
    expect(get().setProfileAppearance('master', { teamIconWeight: 'fill' })).toEqual({ ok: true })
    expect(get().master).toMatchObject({ teamIconWeight: 'fill' })
    expect(get().master.teamIcon).toBeUndefined()
    expect(get().master.icon).toBeUndefined()
  })

  it('is independent of the profile icon weight, and null clears only itself', () => {
    get().setProfileAppearance('master', { icon: 'Star', iconWeight: 'bold', teamIcon: 'Rocket', teamIconWeight: 'fill' })
    expect(get().master).toMatchObject({ iconWeight: 'bold', teamIconWeight: 'fill' })
    get().setProfileAppearance('master', { icon: null })
    expect(get().master).toMatchObject({ teamIcon: 'Rocket', teamIconWeight: 'fill' }) // the profile icon's weight goes with the icon; the team's does not
    get().setProfileAppearance('master', { teamIconWeight: null })
    expect(Object.hasOwn(get().master, 'teamIconWeight')).toBe(false)
    expect(get().master.teamIcon).toBe('Rocket')
  })

  it.each(['heavy', '', 3])('a weight that is none of the weights (%j) refuses the whole patch', (bad) => {
    get().setProfileAppearance('master', { name: 'Work', teamIconWeight: 'fill' })
    const before = get().master
    expect(get().setProfileAppearance('master', { name: 'Other', teamIconWeight: bad as never })).toEqual({ ok: false, reason: 'bad-team-weight' })
    expect(get().master).toBe(before)
  })

  it('goes with a slave when it is promoted, and with the master when it is demoted', () => {
    const added = get().addSlave('Lab', EMPTY_WORLD)
    if (!added.ok) throw new Error('addSlave refused')
    get().setProfileAppearance('master', { teamIconWeight: 'thin' })
    get().setProfileAppearance(added.id, { teamIconWeight: 'duotone' })
    const promoted = get().promoteSlave(added.id, 'Old home', 1, [])
    if (!promoted.ok) throw new Error('promote refused')
    expect(get().master.teamIconWeight).toBe('duotone')
    expect(get().slaves[promoted.demotedId].teamIconWeight).toBe('thin')
  })
})

describe('teamColor', () => {
  it('is kept in lower case, and null clears it', () => {
    expect(get().setProfileAppearance('master', { teamColor: '#AABBCC' })).toEqual({ ok: true })
    expect(get().master.teamColor).toBe('#aabbcc')
    get().setProfileAppearance('master', { teamColor: null })
    expect(Object.hasOwn(get().master, 'teamColor')).toBe(false)
  })

  it('is independent of the profile colour', () => {
    get().setProfileAppearance('master', { icon: 'Star', color: '#111111', teamColor: '#222222' })
    get().setProfileAppearance('master', { color: null })
    expect(get().master.teamColor).toBe('#222222')
    expect(get().master.color).toBeUndefined()
  })

  it.each(['red', '#abc', '#12345g', '#aabbccdd', 7])('a colour that is not strict #rrggbb (%j) refuses the whole patch', (bad) => {
    get().setProfileAppearance('master', { name: 'Work', teamColor: '#112233' })
    const before = get().master
    expect(get().setProfileAppearance('master', { name: 'Other', teamColor: bad as string })).toEqual({ ok: false, reason: 'bad-team-color' })
    expect(get().master).toBe(before)
  })

  it('a slave has its own, and the rest of its record is untouched', () => {
    const added = get().addSlave('Lab', EMPTY_WORLD)
    if (!added.ok) throw new Error('addSlave refused')
    get().setProfileAppearance(added.id, { teamColor: '#445566', teamIconWeight: 'fill' })
    expect(get().slaves[added.id]).toMatchObject({ id: added.id, name: 'Lab', teamColor: '#445566', teamIconWeight: 'fill', world: EMPTY_WORLD })
    expect(get().master.teamColor).toBeUndefined()
  })
})

describe('on load', () => {
  it('a saved teamIconWeight or teamColor that is not valid is dropped alone; good ones are kept (the colour in lower case)', async () => {
    const slave = (extra: Record<string, unknown>) => ({ id: 'a1', name: 'Lab', createdAt: 1, shownHostIds: [], world: EMPTY_WORLD, ...extra })
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({
      state: {
        master: { name: 'Home', teamIcon: 'Flag', teamIconWeight: 'fill', teamColor: '#ABCDEF' },
        slaves: { a1: slave({ teamIcon: 'Flag', teamIconWeight: 'heavy', teamColor: 'blue' }) },
        slaveOrder: ['a1'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0,
      },
      version: 1,
    }))
    await useLocalProfilesStore.persist.rehydrate()
    expect(get().master).toMatchObject({ name: 'Home', teamIcon: 'Flag', teamIconWeight: 'fill', teamColor: '#abcdef' })
    expect(Object.hasOwn(get().slaves.a1, 'teamIconWeight')).toBe(false)
    expect(Object.hasOwn(get().slaves.a1, 'teamColor')).toBe(false)
    expect(get().slaves.a1).toMatchObject({ name: 'Lab', teamIcon: 'Flag' })
  })
})
