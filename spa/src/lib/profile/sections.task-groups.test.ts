// spa/src/lib/profile/sections.task-groups.test.ts — `purdex-task-groups` in the settings builder (task groups spec §4.1,
// TG-1a-1 task 3): members travel as WIRE ids, translated first, then de-duplicated, then sorted by (wire host, root).
import { describe, expect, it } from 'vitest'
import type { HostConfig } from '../../stores/useHostStore'
import { buildSettingsSection, type SettingsBuildInput } from './sections'
import { identityOfSync, syncIdOfSync } from './host-identity'

const NO_WS: ReadonlySet<string> = new Set()
const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)
const hostCfg = (id: string, extra: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
const identityA = () => identityOfSync({ bbbbbb: hostCfg('bbbbbb', { daemonId: DAEMON }) })
// device B knows the same daemon under another local id
const identityB = () => identityOfSync({ zzzzzz: hostCfg('zzzzzz', { daemonId: DAEMON }) })

const input = (members: Array<{ host: string; root: string }>): SettingsBuildInput => ({
  'purdex-task-groups': { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'A', members, createdAt: 1 } } },
})
const membersOut = (inp: SettingsBuildInput, identity?: ReturnType<typeof identityA>) =>
  (buildSettingsSection(inp, NO_WS, identity)['purdex-task-groups'] as { groups: Record<string, { members: unknown[] }> }).groups.aaaaaaaa.members

describe('buildSettingsSection — purdex-task-groups members', () => {
  it('writes a local host id as its wire id', () => {
    expect(membersOut(input([{ host: 'bbbbbb', root: 'r1' }]), identityA())).toEqual([{ host: WIRE, root: 'r1' }])
  })

  it('keeps a host id it does not know as received', () => {
    expect(membersOut(input([{ host: 'd1_unknownhost', root: 'r1' }]), identityA())).toEqual([{ host: 'd1_unknownhost', root: 'r1' }])
  })

  it('sorts by (wire host, root); a member held under its local id and under its kept wire id builds once', () => {
    const out = membersOut(input([{ host: WIRE, root: 'r2' }, { host: 'bbbbbb', root: 'r2' }, { host: 'bbbbbb', root: 'r1' }, { host: 'alpha', root: 'z' }]), identityA())
    expect(out).toEqual([{ host: 'alpha', root: 'z' }, { host: WIRE, root: 'r1' }, { host: WIRE, root: 'r2' }])
  })

  it('two devices with different local host ids, adding the same members in another order, build the same payload', () => {
    const a = buildSettingsSection(input([{ host: 'bbbbbb', root: 'r1' }, { host: 'bbbbbb', root: 'r2' }]), NO_WS, identityA())
    const b = buildSettingsSection(input([{ host: 'zzzzzz', root: 'r2' }, { host: 'zzzzzz', root: 'r1' }]), NO_WS, identityB())
    expect(JSON.stringify(b)).toBe(JSON.stringify(a))
  })

  it("does not touch the store's own state", () => {
    const inp = input([{ host: 'bbbbbb', root: 'r2' }, { host: 'bbbbbb', root: 'r1' }])
    const before = JSON.stringify(inp)
    buildSettingsSection(inp, NO_WS, identityA())
    expect(JSON.stringify(inp)).toBe(before)
  })

  it('without an identity passes members through verbatim', () => {
    expect(membersOut(input([{ host: 'bbbbbb', root: 'r2' }, { host: 'bbbbbb', root: 'r1' }]))).toEqual([{ host: 'bbbbbb', root: 'r2' }, { host: 'bbbbbb', root: 'r1' }])
  })
})
