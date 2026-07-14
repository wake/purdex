import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listContributions } from '../settings-contribution-registry'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from './test-bootstrap-harness'
import { isModuleOwnedContribution } from '../settings-contribution-types'
import { SETTINGS_ORDER } from '../settings-order'

// Spec §4.1.3 (PR-2) refined by 2026-05-03 settings-modules-order spec
// §4.1: the module-owned band is now alphabetical by English sidebar
// short label (Browser / Commands / Editor / Files / Monitor / Sync),
// and Browser + Files declare a purdex-scope placeholder so every
// disableable module carries an entry (spec §I1). The always-on
// purdex-scope sidebar order is:
//
//   appearance(0)
//   terminal(1)
//   interface(2)
//   electron(5)             — gated by canSystemTray; filtered out
//   module-config(10)
//   browser(11)
//   quick-commands(12)
//   editor(13)
//   files(14)
//   performance-monitor(15)
//   sync(16)
//   dev-environment(20)     — only when caps.devUpdateEnabled
//   tmux-agent-monitor(21)  — DEV or devUpdateEnabled
//   snapshot(22)            — always-on (Workspace Snapshot, Phase 3)
//
// Optional caps-gated entries are excluded from the strict equality so
// the assertion is stable across DEV / prod-like jsdom runs.

beforeEach(() => {
  resetAndRegisterBuiltinModules()
})

afterEach(() => {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
})

describe('PR-2 final sidebar order (spec §4.1.3)', () => {
  // Optional caps-gated entries — must be enumerated explicitly so the
  // strict ASC test below can allow them through but reject any other
  // localId. R2 structural finding: a previous KNOWN_ALWAYS_ON filter
  // would silently let an unexpected new row sneak in by simply not
  // checking against it; explicit allow-listing closes that hole.
  const OPTIONAL_GATED = new Set([
    'electron',           // canSystemTray
    'dev-environment',    // devUpdateEnabled
    'tmux-agent-monitor', // DEV || devUpdateEnabled
  ])

  it('purdex-scope contributions match the exact PR-2 final ASC list (no unexpected rows)', () => {
    const items = listContributions('purdex')
      .map((c) => ({ id: c.localId, order: c.order }))
      .sort((a, b) => a.order - b.order)

    // Step 1: every always-on entry is present in the exact required position.
    const alwaysOn = items.filter((x) => !OPTIONAL_GATED.has(x.id))
    expect(alwaysOn).toEqual([
      { id: 'appearance',          order: SETTINGS_ORDER.APPEARANCE },                  // 0
      { id: 'terminal',            order: SETTINGS_ORDER.TERMINAL },                    // 1
      { id: 'interface',           order: SETTINGS_ORDER.INTERFACE },                   // 2
      { id: 'module-config',       order: SETTINGS_ORDER.MODULE_CONFIG },               // 10
      { id: 'browser',             order: SETTINGS_ORDER.MODULE_BROWSER },              // 11
      { id: 'quick-commands',      order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS },       // 12
      { id: 'editor',              order: SETTINGS_ORDER.MODULE_EDITOR },               // 13
      { id: 'files',               order: SETTINGS_ORDER.MODULE_FILES },                // 14
      { id: 'performance-monitor', order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR },  // 15
      { id: 'sync',                order: SETTINGS_ORDER.MODULE_SYNC },                 // 16
      { id: 'snapshot',            order: SETTINGS_ORDER.SNAPSHOT },                   // 22
    ])

    // Step 2: any entry not in the always-on list must be one of the
    // explicitly allowed gated ids — nothing else.
    const expectedAlwaysOnIds = new Set([
      'appearance', 'terminal', 'interface', 'module-config',
      'browser', 'quick-commands', 'editor', 'files',
      'performance-monitor', 'sync', 'snapshot',
    ])
    const unexpected = items
      .map((x) => x.id)
      .filter((id) => !expectedAlwaysOnIds.has(id) && !OPTIONAL_GATED.has(id))
    expect(unexpected).toEqual([])
  })

  it('module-owned band (10 < order < 20) contains exactly the expected ids in order', () => {
    // Distinct from the "module-config above every module-owned" test below:
    // this guard pins the *contents* of the band, not just relative order,
    // so a stray legacy section squeezed into 10.5 / 12.5 / 13.5 fails here.
    // Order is alphabetical by English sidebar short label
    // (Browser / Commands / Editor / Files / Monitor / Sync — spec §3.1).
    const moduleBand = listContributions('purdex')
      .slice()
      .sort((a, b) => a.order - b.order)
      .filter((c) => c.order > SETTINGS_ORDER.MODULE_CONFIG && c.order < 20)
      .map((c) => c.localId)
    expect(moduleBand).toEqual([
      'browser',
      'quick-commands',
      'editor',
      'files',
      'performance-monitor',
      'sync',
    ])
  })

  it('sidebar no longer carries the collapsed editor sub-entries', () => {
    const ids = listContributions('purdex').map((c) => c.localId)
    expect(ids).not.toContain('open-behavior')
    expect(ids).not.toContain('link-detect')
  })

  it('module-config sits above every module-owned contribution', () => {
    const items = listContributions('purdex')
      .slice()
      .sort((a, b) => a.order - b.order)
    const moduleConfigIdx = items.findIndex((c) => c.localId === 'module-config')
    expect(moduleConfigIdx).toBeGreaterThanOrEqual(0)

    const firstModuleOwnedIdx = items.findIndex((c) => isModuleOwnedContribution(c))
    expect(firstModuleOwnedIdx).toBeGreaterThanOrEqual(0)

    expect(moduleConfigIdx).toBeLessThan(firstModuleOwnedIdx)
  })

  it('every active purdex-scope contribution has a unique order (no collisions)', () => {
    const orders = listContributions('purdex').map((c) => c.order)
    const seen = new Set<number>()
    for (const o of orders) {
      expect(seen.has(o)).toBe(false)
      seen.add(o)
    }
  })

  it('every contribution order is a value drawn from SETTINGS_ORDER (no hand-rolled numbers leak through)', () => {
    // ⚠️ Limitation: runtime cannot tell whether a literal number was
    // sourced from the constant or hard-coded. This guard only catches
    // illegal values — not legal-by-coincidence values. Code review +
    // commit-message discipline still needed for the latter.
    const allowed = new Set<number>(Object.values(SETTINGS_ORDER) as number[])
    for (const c of listContributions('purdex')) {
      expect(allowed.has(c.order)).toBe(true)
    }
  })
})
