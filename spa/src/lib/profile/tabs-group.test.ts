// spa/src/lib/profile/tabs-group.test.ts — `tabs.*.group` / `tabs.*.groupRank` in the tabs section (task groups spec
// §4.2, TG-1b-1 task 3): built, translated (the team host inside `group`), applied field by field (absent = cleared),
// and refused when malformed — all before any translation runs.
import { describe, expect, it } from 'vitest'
import type { HostConfig } from '../../stores/useHostStore'
import type { Tab, Workspace } from '../../types/tab'
import { isWellFormedSection, applyTabs, tabsFromWire } from './applier'
import { hashSection } from './hash'
import { identityOfSync, syncIdOfSync } from './host-identity'
import { PROJECTIONS, SECTION_SCHEMA_ORDINAL } from './projections'
import { buildTabsSection, wireResolverOf } from './sections'
import type { TabsPayload } from './types'

const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)
const LOCAL = 'loc001'
const hostCfg = (id: string, extra: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...extra })
const hostsSrc = () => ({ hosts: { [LOCAL]: hostCfg(LOCAL, { daemonId: DAEMON }) }, hostOrder: [LOCAL] })

const tab = (id: string, extra: Partial<Tab> = {}): Tab => ({
  id, pinned: false, locked: false, createdAt: 1,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: LOCAL, sessionCode: id, mode: 'terminal', cachedName: id, tmuxInstance: 'i' } } },
  ...extra,
})
const ws = (tabs: string[]): Workspace => ({ id: 'wa', name: 'A', tabs, activeTabId: tabs[0] ?? null })
const TEAM = `team\u0000${LOCAL}\u0000t1`
const TEAM_WIRE = `team\u0000${WIRE}\u0000t1`

describe('projection', () => {
  it('lists both fields and moves the tabs ordinal to 5', () => {
    expect(PROJECTIONS.tabs).toContain('tabs.*.group')
    expect(PROJECTIONS.tabs).toContain('tabs.*.groupRank')
    expect(SECTION_SCHEMA_ORDINAL.tabs).toBe(5)
  })
})

describe('buildTabsSection', () => {
  it('carries both fields, with the team host of `group` written as its wire id', () => {
    const tabs = { a: tab('a', { group: TEAM, groupRank: 'V' }), b: tab('b', { group: 'group\u0000aaaaaaaa' }), c: tab('c', { group: 'ungrouped' }) }
    const p = buildTabsSection(ws(['a', 'b', 'c']), tabs, identityOfSync(hostsSrc().hosts))
    expect(p.tabs.a).toMatchObject({ group: TEAM_WIRE, groupRank: 'V' })
    expect(p.tabs.b).toMatchObject({ group: 'group\u0000aaaaaaaa' })
    expect(Object.hasOwn(p.tabs.b, 'groupRank')).toBe(false)
    expect(p.tabs.c).toMatchObject({ group: 'ungrouped' })
  })

  it('a tab with neither field builds neither key; a host it does not know passes through', () => {
    const p = buildTabsSection(ws(['a', 'b']), { a: tab('a'), b: tab('b', { group: 'team\u0000d1_unknown\u0000t9' }) }, identityOfSync(hostsSrc().hosts))
    expect(Object.hasOwn(p.tabs.a, 'group')).toBe(false)
    expect(Object.hasOwn(p.tabs.a, 'groupRank')).toBe(false)
    expect(p.tabs.b.group).toBe('team\u0000d1_unknown\u0000t9')
  })
})

describe('tabsFromWire + applyTabs', () => {
  const incoming = (): TabsPayload => buildTabsSection(ws(['a', 'b']), { a: tab('a', { group: TEAM, groupRank: 'V' }), b: tab('b', { group: 'team\u0000d1_unknown\u0000t9', groupRank: 'a' }) }, identityOfSync(hostsSrc().hosts))
  const resolve = () => wireResolverOf(hostsSrc())!

  it('translates the team host back to the local id and an unknown wire host is kept', () => {
    const local = tabsFromWire(incoming(), resolve())
    expect(local.tabs.a.group).toBe(TEAM)
    expect(local.tabs.b.group).toBe('team\u0000d1_unknown\u0000t9')
  })

  it('applyTabs copies both fields', () => {
    const { next } = applyTabs({ tabs: {}, workspaces: [ws([])] }, 'wa', tabsFromWire(incoming(), resolve()))
    expect(next.tabs.a).toMatchObject({ group: TEAM, groupRank: 'V' })
    expect(next.tabs.b).toMatchObject({ groupRank: 'a' })
  })

  it('absent = cleared: a payload without the fields takes them off a tab that had them', () => {
    const had = { a: tab('a', { group: TEAM, groupRank: 'V' }) }
    const p = buildTabsSection(ws(['a']), { a: tab('a') }, identityOfSync(hostsSrc().hosts))
    const { next } = applyTabs({ tabs: had, workspaces: [ws(['a'])] }, 'wa', p)
    expect(Object.hasOwn(next.tabs.a, 'group')).toBe(false)
    expect(Object.hasOwn(next.tabs.a, 'groupRank')).toBe(false)
  })

  it('hash(build(apply(p))) === hash(p), the fields and an unknown host included', async () => {
    const p = incoming()
    const { next } = applyTabs({ tabs: {}, workspaces: [ws([])] }, 'wa', tabsFromWire(p, resolve()))
    const rebuilt = buildTabsSection(next.workspaces[0], next.tabs, identityOfSync(hostsSrc().hosts))
    expect(await hashSection(rebuilt)).toBe(await hashSection(p))
  })
})

describe('isWellFormedSection(tabs)', () => {
  const base = (): TabsPayload => buildTabsSection(ws(['a']), { a: tab('a', { group: 'ungrouped', groupRank: 'V' }) }, identityOfSync(hostsSrc().hosts))
  const withEntry = (patch: Record<string, unknown>): unknown => {
    const p = JSON.parse(JSON.stringify(base())) as TabsPayload
    return { ...p, tabs: { a: { ...p.tabs.a, ...patch } } }
  }

  it('accepts the fields', () => {
    expect(isWellFormedSection('tabs', base())).toBe(true)
    expect(isWellFormedSection('tabs', withEntry({ group: 'group\u0000aaaaaaaa' }))).toBe(true)
    expect(isWellFormedSection('tabs', withEntry({ groupRank: 'a'.repeat(128) }))).toBe(true)
  })

  it('refuses a group that is not a string or not a group key', () => {
    for (const group of [5, null, {}, '', 'nope', 'group\u0000', 'team\u0000onlyhost']) expect(isWellFormedSection('tabs', withEntry({ group })), String(group)).toBe(false)
  })

  it('refuses a rank that is not a string, too long, or not in the alphabet', () => {
    for (const groupRank of [5, null, '', 'a'.repeat(129), 'a-b', 'a b']) expect(isWellFormedSection('tabs', withEntry({ groupRank })), String(groupRank)).toBe(false)
  })

  it('refuses an unlisted extra field next to them', () => {
    expect(isWellFormedSection('tabs', withEntry({ groupRankX: 'V' }))).toBe(false)
  })
})
