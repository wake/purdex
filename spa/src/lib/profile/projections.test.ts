import { describe, it, expect } from 'vitest'
import {
  PROJECTIONS,
  SECTION_SCHEMA_ORDINAL,
  WIRE_MARKERS,
  fingerprintOf,
  project,
  sectionFingerprint,
  sectionKind,
  shapeTable,
  tabsSectionKey,
  workspaceIdOf,
} from './projections'
import type { ProfileSectionKey, SectionKind } from './projections'
import { STORAGE_KEYS } from '../storage/keys'
import { generateId } from '../id'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { compareShape, profileLock } from './profile-state'
import type { Shape, SotIndexEntry } from './types'

const KINDS: SectionKind[] = ['hosts', 'settings', 'workspaces', 'tabs']

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const v of Object.values(value as Record<string, unknown>)) deepFreeze(v)
    Object.freeze(value)
  }
  return value
}

const leaf = (id: string) => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })

/** The settings projection grouped by storage key: `{ 'purdex-layout': ['tabPosition'], … }`. */
function settingsFieldsByStore(): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const path of PROJECTIONS.settings) {
    const dot = path.indexOf('.')
    ;(out[path.slice(0, dot)] ??= []).push(path.slice(dot + 1))
  }
  return out
}

describe('project — includes', () => {
  it('copies only the listed top-level and nested paths', () => {
    const src = { a: 1, b: { c: 2, d: 3 }, e: 4 }
    expect(project(src, ['a', 'b.c'])).toEqual({ a: 1, b: { c: 2 } })
  })

  it('expands * over every key of a record', () => {
    const src = {
      hosts: { h1: { id: 'h1', name: 'one', secret: 's' }, h2: { id: 'h2', name: 'two', secret: 's' } },
      hostOrder: ['h2', 'h1'],
      activeHostId: 'h1',
    }
    expect(project(src, ['hostOrder', 'hosts.*.id', 'hosts.*.name'])).toEqual({
      hosts: { h1: { id: 'h1', name: 'one' }, h2: { id: 'h2', name: 'two' } },
      hostOrder: ['h2', 'h1'],
    })
  })

  it('a path that matches nothing contributes nothing (optional fields)', () => {
    const src = { hosts: { h1: { id: 'h1' }, h2: { id: 'h2', icon: 'Laptop' } } }
    const out = project(src, ['hosts.*.id', 'hosts.*.icon', 'nope', 'nope.deeper', 'hosts.*.id.deeper'])
    expect(out).toEqual({ hosts: { h1: { id: 'h1' }, h2: { id: 'h2', icon: 'Laptop' } } })
    expect('icon' in (out as { hosts: Record<string, object> }).hosts.h1).toBe(false)
    expect('nope' in (out as object)).toBe(false)
  })

  it('an explicitly undefined field is treated as absent', () => {
    expect(project({ a: undefined, b: 1 }, ['a', 'b'])).toEqual({ b: 1 })
    expect('a' in (project({ a: undefined, b: 1 }, ['a', 'b']) as object)).toBe(false)
  })

  it('keeps null, false, 0 and empty containers', () => {
    const src = { a: null, b: false, c: 0, d: '', e: [], f: {} }
    expect(project(src, ['a', 'b', 'c', 'd', 'e', 'f'])).toEqual(src)
  })

  it('a record with no entries still yields nothing for its * paths', () => {
    expect(project({ hosts: {}, hostOrder: [] }, ['hostOrder', 'hosts.*.id'])).toEqual({ hostOrder: [] })
  })

  it('does not treat * as matching array elements or non-objects', () => {
    expect(project({ xs: [{ id: 1 }], n: 5 }, ['xs.*.id', 'n.*.id'])).toEqual({})
  })

  it('returns an empty object for a non-object source', () => {
    expect(project(null, ['a'])).toEqual({})
    expect(project('str', ['a'])).toEqual({})
  })

  it('overlapping includes merge rather than clobber', () => {
    const src = { t: { x: { id: 'x', layout: { type: 'leaf' }, junk: 1 } } }
    expect(project(src, ['t.*.layout', 't.*.id'])).toEqual({ t: { x: { id: 'x', layout: { type: 'leaf' } } } })
    expect(project(src, ['t.*.id', 't.*.layout'])).toEqual({ t: { x: { id: 'x', layout: { type: 'leaf' } } } })
  })

  it('ignores inherited and prototype-polluting keys', () => {
    const src = JSON.parse('{"__proto__": {"polluted": true}, "a": 1}') as Record<string, unknown>
    const out = project(src, ['a', '__proto__', '__proto__.polluted', 'constructor', 'toString']) as Record<string, unknown>
    expect(out).toEqual({ a: 1 })
    expect(({} as Record<string, unknown>).polluted).toBeUndefined()
  })
})

describe('project — exclusions', () => {
  const TAB_PATHS = PROJECTIONS.tabs

  it('depth 0: the layout itself is a split and loses its sizes', () => {
    const src = {
      order: ['t1'],
      tabs: {
        t1: {
          id: 't1', pinned: false, locked: false, createdAt: 1,
          layout: { type: 'split', id: 's0', direction: 'h', children: [leaf('p1'), leaf('p2')], sizes: [30, 70] },
        },
      },
    }
    expect(project(src, TAB_PATHS)).toEqual({
      order: ['t1'],
      tabs: {
        t1: {
          id: 't1', pinned: false, locked: false, createdAt: 1,
          layout: { type: 'split', id: 's0', direction: 'h', children: [leaf('p1'), leaf('p2')] },
        },
      },
    })
  })

  it('depth 1: a key one object below the prefix', () => {
    const src = { p: { a: { sizes: [1], keep: 1 }, keep: 2 } }
    expect(project(src, ['p', '!p..sizes'])).toEqual({ p: { a: { keep: 1 }, keep: 2 } })
  })

  it('depth 3 through children[]: every nested split loses its sizes', () => {
    const deep = {
      type: 'split', id: 's0', direction: 'h', sizes: [50, 50],
      children: [
        leaf('p0'),
        {
          type: 'split', id: 's1', direction: 'v', sizes: [20, 80],
          children: [
            leaf('p1'),
            { type: 'split', id: 's2', direction: 'h', sizes: [10, 90], children: [leaf('p2'), leaf('p3')] },
          ],
        },
      ],
    }
    const src = { order: ['t1'], tabs: { t1: { id: 't1', pinned: true, locked: false, createdAt: 2, layout: deep } } }
    const out = project(src, TAB_PATHS)
    expect(JSON.stringify(out)).not.toContain('sizes')
    expect(out).toEqual({
      order: ['t1'],
      tabs: {
        t1: {
          id: 't1', pinned: true, locked: false, createdAt: 2,
          layout: {
            type: 'split', id: 's0', direction: 'h',
            children: [
              leaf('p0'),
              {
                type: 'split', id: 's1', direction: 'v',
                children: [
                  leaf('p1'),
                  { type: 'split', id: 's2', direction: 'h', children: [leaf('p2'), leaf('p3')] },
                ],
              },
            ],
          },
        },
      },
    })
  })

  it('gives the same result with the exclusion listed first or last', () => {
    const src = {
      order: ['t1'],
      tabs: { t1: { id: 't1', layout: { type: 'split', id: 's', direction: 'h', children: [leaf('a')], sizes: [100] } } },
    }
    const includes = TAB_PATHS.filter((p) => !p.startsWith('!'))
    const exclusions = TAB_PATHS.filter((p) => p.startsWith('!'))
    expect(exclusions).toHaveLength(1)
    const first = project(src, [...exclusions, ...includes])
    const last = project(src, [...includes, ...exclusions])
    expect(first).toEqual(last)
    expect(JSON.stringify(first)).not.toContain('sizes')
  })

  it('does not remove a same-named key outside the prefix', () => {
    const src = {
      sizes: [1, 2],
      tabs: { t1: { sizes: 'tab-level', layout: { type: 'split', sizes: [50, 50], children: [] } } },
    }
    expect(project(src, ['sizes', 'tabs.*.sizes', 'tabs.*.layout', '!tabs.*.layout..sizes'])).toEqual({
      sizes: [1, 2],
      tabs: { t1: { sizes: 'tab-level', layout: { type: 'split', children: [] } } },
    })
  })

  it('an exclusion whose prefix matches nothing is a no-op', () => {
    expect(project({ a: { sizes: 1 } }, ['a', '!b..sizes', '!a.x..sizes'])).toEqual({ a: { sizes: 1 } })
  })

  it('throws on a malformed exclusion', () => {
    expect(() => project({ a: 1 }, ['a', '!a.sizes'])).toThrow()
    expect(() => project({ a: 1 }, ['a', '!..sizes'])).toThrow()
    expect(() => project({ a: 1 }, ['a', '!a..'])).toThrow()
  })
})

describe('project — purity', () => {
  it('never mutates its input (deep-frozen) and returns fresh structure', () => {
    const src = deepFreeze({
      order: ['t1'],
      tabs: {
        t1: {
          id: 't1', pinned: false, locked: false, createdAt: 1,
          layout: { type: 'split', id: 's0', direction: 'h', children: [leaf('p1'), leaf('p2')], sizes: [30, 70] },
        },
      },
    })
    const before = JSON.stringify(src)
    const out = project(src, PROJECTIONS.tabs) as typeof src
    expect(JSON.stringify(src)).toBe(before)
    expect(src.tabs.t1.layout.sizes).toEqual([30, 70])
    expect(out.order).not.toBe(src.order)
    expect(out.tabs.t1.layout).not.toBe(src.tabs.t1.layout)
    expect(out.tabs.t1.layout.children).not.toBe(src.tabs.t1.layout.children)
    expect(out.tabs.t1.layout.children[0]).not.toBe(src.tabs.t1.layout.children[0])
    expect(Object.isFrozen(out.tabs.t1.layout)).toBe(false)
  })
})

describe('PROJECTIONS', () => {
  it('hosts, workspaces and tabs are the corrected §4.2 lists', () => {
    expect([...PROJECTIONS.hosts].sort()).toEqual([
      'hostOrder', 'hosts.*.aliases', 'hosts.*.color', 'hosts.*.colors', 'hosts.*.daemonId', 'hosts.*.icon', 'hosts.*.iconWeight',
      'hosts.*.id', 'hosts.*.ip', 'hosts.*.name', 'hosts.*.order', 'hosts.*.port', 'hosts.*.token',
    ])
    expect([...PROJECTIONS.workspaces].sort()).toEqual([
      'order', 'workspaces.*.icon', 'workspaces.*.iconWeight', 'workspaces.*.moduleConfig', 'workspaces.*.name',
    ])
    expect([...PROJECTIONS.tabs].sort()).toEqual([
      '!tabs.*.layout..sizes', 'order', 'tabs.*.createdAt', 'tabs.*.id', 'tabs.*.layout', 'tabs.*.locked',
      'tabs.*.pinned',
    ])
  })

  it('no list has duplicates', () => {
    for (const kind of KINDS) expect(new Set(PROJECTIONS[kind]).size).toBe(PROJECTIONS[kind].length)
  })

  it('settings uses no wildcard and no exclusion, and every path is exactly <storageKey>.<field>', () => {
    for (const path of PROJECTIONS.settings) {
      expect(path).not.toContain('*')
      expect(path.startsWith('!')).toBe(false)
      expect(path.split('.')).toHaveLength(2)
    }
  })

  it('every settings prefix is a real storage key, and no storage key contains a dot', () => {
    const real = new Set<string>(Object.values(STORAGE_KEYS))
    for (const key of real) expect(key).not.toContain('.')
    const prefixes = Object.keys(settingsFieldsByStore())
    expect(prefixes).toHaveLength(8)
    for (const prefix of prefixes) expect(real.has(prefix)).toBe(true)
  })

  // Module on/off is a device-local preference (useModuleEnabledStore's own
  // comment): a host with limited resources turns off what it cannot run.
  it('no settings path starts with purdex-module-enabled — the whole store is device-local', () => {
    expect(STORAGE_KEYS.MODULE_ENABLED).toBe('purdex-module-enabled')
    expect(Object.keys(useModuleEnabledStore.getState())).toContain('enabled') // the field exists; it is unlisted on purpose
    for (const path of PROJECTIONS.settings) {
      expect(path.startsWith('purdex-module-enabled'), path).toBe(false)
    }
    expect(settingsFieldsByStore()[STORAGE_KEYS.MODULE_ENABLED]).toBeUndefined()
  })

  // Editor preferences are device-local too — useEditorSettingsStore's header:
  // "Not registered with `syncManager` — editor preferences are a device-local
  // choice (the small-screen laptop may want fontSize 11 while the big monitor
  // uses 14) rather than shared config."
  it('no settings path starts with purdex-editor-settings — the whole store is device-local', () => {
    expect(STORAGE_KEYS.EDITOR_SETTINGS).toBe('purdex-editor-settings')
    expect(Object.keys(useEditorSettingsStore.getState())).toContain('fontSize') // the fields exist; they are unlisted on purpose
    for (const path of PROJECTIONS.settings) {
      expect(path.startsWith('purdex-editor-settings'), path).toBe(false)
    }
    expect(settingsFieldsByStore()[STORAGE_KEYS.EDITOR_SETTINGS]).toBeUndefined()
  })

  it('never lists the three persisted non-preference fields', () => {
    const fields = settingsFieldsByStore()
    expect(fields[STORAGE_KEYS.UI_SETTINGS]).not.toContain('terminalSettingsVersion')
    expect(fields[STORAGE_KEYS.NEW_TAB_LAYOUT]).toEqual(['presets'])
    expect(fields[STORAGE_KEYS.LAYOUT]).toEqual(['tabPosition'])
  })

  it('every listed settings field exists in its store (a rename must not silently unsync a field)', () => {
    const stores: Record<string, () => object> = {
      [STORAGE_KEYS.UI_SETTINGS]: useUISettingsStore.getState,
      [STORAGE_KEYS.THEMES]: useThemeStore.getState,
      [STORAGE_KEYS.I18N]: useI18nStore.getState,
      [STORAGE_KEYS.NOTIFICATION_SETTINGS]: useNotificationSettingsStore.getState,
      [STORAGE_KEYS.WORKSPACE_SETTINGS]: useWorkspaceSettingsStore.getState,
      [STORAGE_KEYS.HOST_SETTINGS]: useHostSettingsStore.getState,
      [STORAGE_KEYS.NEW_TAB_LAYOUT]: useNewTabLayoutStore.getState,
      [STORAGE_KEYS.LAYOUT]: useLayoutStore.getState,
    }
    const fields = settingsFieldsByStore()
    expect(Object.keys(fields).sort()).toEqual(Object.keys(stores).sort())
    for (const [storageKey, names] of Object.entries(fields)) {
      const state = stores[storageKey]() as Record<string, unknown>
      for (const name of names) {
        expect(name in state, `${storageKey}.${name} is not a field of its store`).toBe(true)
        expect(typeof state[name], `${storageKey}.${name} is a function, not a value`).not.toBe('function')
      }
    }
  })

  it('lists every value field of useUISettingsStore except terminalSettingsVersion', () => {
    const state = useUISettingsStore.getState() as unknown as Record<string, unknown>
    const valueFields = Object.keys(state).filter((k) => typeof state[k] !== 'function').sort()
    const listed = [...settingsFieldsByStore()[STORAGE_KEYS.UI_SETTINGS]].sort()
    expect(listed).toEqual(valueFields.filter((k) => k !== 'terminalSettingsVersion'))
    expect(listed).toHaveLength(25)
  })
})

describe('section keys', () => {
  it('sectionKind maps the four kinds', () => {
    expect(sectionKind('hosts')).toBe('hosts')
    expect(sectionKind('settings')).toBe('settings')
    expect(sectionKind('workspaces')).toBe('workspaces')
    expect(sectionKind('tabs.ws1')).toBe('tabs')
    expect(sectionKind('tabs.A_b-9')).toBe('tabs')
    expect(sectionKind(`tabs.${'x'.repeat(64)}`)).toBe('tabs')
  })

  it('sectionKind rejects garbage', () => {
    for (const key of [
      '', 'tabs', 'tabs.', 'tabs.a.b', 'tabsx', 'tabsx.a', 'Hosts', 'hosts ', ' hosts', 'hosts.x', 'settings.ui',
      'tabs.a b', 'tabs.a/b', 'tabs.é', `tabs.${'x'.repeat(65)}`, 'tabs.a\n', 'tabs.a\nhosts', '__proto__',
      'constructor', 'toString',
    ]) {
      expect(sectionKind(key), JSON.stringify(key)).toBeNull()
    }
  })

  it('tabsSectionKey builds a key the daemon accepts, and throws otherwise', () => {
    expect(tabsSectionKey('ws1')).toBe('tabs.ws1')
    expect(tabsSectionKey('A_b-9')).toBe('tabs.A_b-9')
    expect(tabsSectionKey('x'.repeat(64))).toBe(`tabs.${'x'.repeat(64)}`)
    for (const bad of ['', 'a.b', 'a b', 'a/b', 'é', 'x'.repeat(65), 'a\n']) {
      expect(() => tabsSectionKey(bad), JSON.stringify(bad)).toThrow()
    }
  })

  it('workspaceIdOf inverts tabsSectionKey and returns null for anything else', () => {
    expect(workspaceIdOf('tabs.ws1')).toBe('ws1')
    expect(workspaceIdOf(tabsSectionKey('A_b-9'))).toBe('A_b-9')
    for (const key of ['hosts', 'settings', 'workspaces', 'tabs.', 'tabs.a.b', 'tabs.a b', `tabs.${'x'.repeat(65)}`, 'tabs.a\n']) {
      expect(workspaceIdOf(key as ProfileSectionKey), JSON.stringify(key)).toBeNull()
    }
  })

  it("generateId() stays inside the daemon's section-id alphabet and length", () => {
    for (let i = 0; i < 200; i++) {
      const id = generateId()
      expect(id).toMatch(/^[A-Za-z0-9_-]{1,64}$/)
      expect(workspaceIdOf(tabsSectionKey(id))).toBe(id)
    }
  })
})

describe('shape: fingerprint and ordinal', () => {
  it('SECTION_SCHEMA_ORDINAL covers exactly the four kinds with positive integers', () => {
    expect(Object.keys(SECTION_SCHEMA_ORDINAL).sort()).toEqual([...KINDS].sort())
    for (const kind of KINDS) {
      expect(Number.isInteger(SECTION_SCHEMA_ORDINAL[kind])).toBe(true)
      expect(SECTION_SCHEMA_ORDINAL[kind]).toBeGreaterThanOrEqual(1)
    }
  })

  it('a fingerprint is 64 lowercase hex, distinct per kind', async () => {
    const fps = await Promise.all(KINDS.map((k) => sectionFingerprint(k)))
    for (const fp of fps) expect(fp).toMatch(/^[0-9a-f]{64}$/)
    expect(new Set(fps).size).toBe(KINDS.length)
  })

  it('sectionFingerprint(kind) is fingerprintOf(PROJECTIONS[kind] + WIRE_MARKERS[kind]) — the markers are hashed, never projected', async () => {
    for (const kind of KINDS) expect(await sectionFingerprint(kind)).toBe(await fingerprintOf([...PROJECTIONS[kind], ...WIRE_MARKERS[kind]]))
    expect(WIRE_MARKERS.workspaces).toEqual([]) // its wire meaning did not change: its fingerprint must not either
    for (const kind of KINDS) for (const m of WIRE_MARKERS[kind]) expect(PROJECTIONS[kind]).not.toContain(m)
  })

  it('reordering a projection list leaves the fingerprint unchanged', async () => {
    const paths = PROJECTIONS.tabs
    expect(await fingerprintOf([...paths].reverse())).toBe(await fingerprintOf(paths))
    expect(await fingerprintOf([...paths.slice(3), ...paths.slice(0, 3)])).toBe(await fingerprintOf(paths))
  })

  it('adding or removing a path changes the fingerprint — the exclusion entry included', async () => {
    const paths = PROJECTIONS.tabs
    const base = await fingerprintOf(paths)
    expect(await fingerprintOf([...paths, 'tabs.*.title'])).not.toBe(base)
    expect(await fingerprintOf(paths.filter((p) => p !== 'tabs.*.pinned'))).not.toBe(base)
    expect(await fingerprintOf(paths.filter((p) => !p.startsWith('!')))).not.toBe(base)
  })

  it('fingerprintOf does not mutate (sort in place) the list it is given', async () => {
    const paths = Object.freeze(['b', 'a'])
    await fingerprintOf(paths)
    expect(paths).toEqual(['b', 'a'])
  })

  // P3e: settings ordinal 3 → 4 (`purdex-newtab-layout.profiles` → `.presets`).
  // An ordinal-3 client and this build, judged by shape alone: the old one
  // locks on a row of ours (`sot-is-newer` → locked:schema), we pull its rows.
  it('coexistence by shape: the ordinal-3 settings shape (newtab `profiles`) and this one order, never lock this build', async () => {
    const legacyList = PROJECTIONS.settings.map((p) => (p === 'purdex-newtab-layout.presets' ? 'purdex-newtab-layout.profiles' : p))
    expect(legacyList).not.toEqual(PROJECTIONS.settings) // the swap happened
    const mine = { fingerprint: await sectionFingerprint('settings'), ordinal: SECTION_SCHEMA_ORDINAL.settings }
    const old = { fingerprint: await fingerprintOf(legacyList), ordinal: 3 }
    expect(old.fingerprint).not.toBe(mine.fingerprint)
    expect(compareShape(mine, old)).toBe('i-am-newer')
    expect(compareShape(old, mine)).toBe('sot-is-newer')
  })

  // host-daemon-id D6: hosts ordinal 1 → 2 (`hosts.*.daemonId` added); host-sync-identity: 2 → 3 (wire ids,
  // `hosts.*.aliases` added). Each older client and this build, judged by shape alone: the old one locks on a row of
  // ours (`sot-is-newer` → locked:schema), we pull its rows (and match / upcast them on apply).
  it.each([
    ['ordinal-2 (no `aliases`)', ['hosts.*.aliases'], 2],
    ['ordinal-1 (no `daemonId`, no `aliases`)', ['hosts.*.aliases', 'hosts.*.daemonId'], 1],
  ])('coexistence by shape: the %s hosts shape and this one order, never lock this build', async (_name, dropped, ordinal) => {
    const legacyList = PROJECTIONS.hosts.filter((p) => !dropped.includes(p))
    expect(legacyList).toHaveLength(PROJECTIONS.hosts.length - dropped.length) // the fields were there to drop
    const mine = { fingerprint: await sectionFingerprint('hosts'), ordinal: SECTION_SCHEMA_ORDINAL.hosts }
    const old = { fingerprint: await fingerprintOf(legacyList), ordinal }
    expect(old.fingerprint).not.toBe(mine.fingerprint)
    expect(compareShape(mine, old)).toBe('i-am-newer')
    expect(compareShape(old, mine)).toBe('sot-is-newer')
  })

  // host-sync-identity: hosts / tabs / settings re-interpret host ids (wire ids). `compareShape` calls equal
  // fingerprints 'ok' whatever the ordinals, and old clients cannot be changed — so each of the three carries a wire
  // marker in its FINGERPRINT (WIRE_MARKERS), and an ordinal-2-era client meets EACH section on its own as newer.
  describe('host-sync-identity: an alpha.434 client (the ordinal-2-era shapes) is locked out by EACH host-bearing section alone', () => {
    /** What alpha.434 computed: the projection lists without this PR's additions, no marker. */
    async function oldShapes(): Promise<Record<SectionKind, Shape>> {
      return {
        hosts: { fingerprint: await fingerprintOf(PROJECTIONS.hosts.filter((p) => p !== 'hosts.*.aliases')), ordinal: 2 },
        tabs: { fingerprint: await fingerprintOf(PROJECTIONS.tabs), ordinal: 1 },
        settings: { fingerprint: await fingerprintOf(PROJECTIONS.settings), ordinal: 4 },
        workspaces: { fingerprint: await fingerprintOf(PROJECTIONS.workspaces), ordinal: 1 },
      }
    }
    const entry = async (section: string, kind: SectionKind): Promise<SotIndexEntry> => ({
      section, rev: 1, hash: 'h', fingerprint: await sectionFingerprint(kind), ordinal: SECTION_SCHEMA_ORDINAL[kind],
    })

    it.each([
      ['hosts', 'hosts'],
      ['tabs.w1', 'tabs'],
      ['settings', 'settings'],
    ] as const)('an index holding ONLY a new %s row → sot-is-newer (locked:schema) for the old client', async (section, kind) => {
      const lock = profileLock([await entry(section, kind)], await oldShapes())
      expect(lock).toMatchObject({ section, kind, verdict: 'sot-is-newer' })
    })

    it('this build meets each old row as i-am-newer (pulls it), and the workspaces shape is unchanged (no lock either way)', async () => {
      const old = await oldShapes()
      for (const kind of ['hosts', 'tabs', 'settings'] as const) {
        const mine = { fingerprint: await sectionFingerprint(kind), ordinal: SECTION_SCHEMA_ORDINAL[kind] }
        expect(compareShape(mine, old[kind]), kind).toBe('i-am-newer')
      }
      expect(await sectionFingerprint('workspaces')).toBe(old.workspaces.fingerprint)
      expect(profileLock([await entry('workspaces', 'workspaces')], old)).toBeNull()
    })
  })

  // GUARD (spec §4.5). If this fails: a projection changed — bump
  // `SECTION_SCHEMA_ORDINAL.<kind>` and update this snapshot in the same commit.
  // Never update the snapshot alone: a fingerprint that changes with an unchanged
  // ordinal makes every other client lock the section (`locked:schema`).
  it('guard: every projection change comes with an ordinal bump', async () => {
    expect(await shapeTable()).toMatchInlineSnapshot(`
      {
        "hosts": [
          "17265091ed11818ecd62b5cf092246454fa0d89cf805d090c06ad2b5ace6e412",
          3,
        ],
        "settings": [
          "dc4aa5a072306c61072f27189161c7b5852a84e320f68ac183b02341cd073c18",
          5,
        ],
        "tabs": [
          "c14fe14a3e27bb16cb13f2a8b4e406373a3c5bd2d13142c506edc237c2c57605",
          2,
        ],
        "workspaces": [
          "7986550194df9cf330ec521be44e68989a703ac1e90d433f73e9aab00410c87e",
          1,
        ],
      }
    `)
  })
})
