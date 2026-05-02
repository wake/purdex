import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listContributions } from '../settings-contribution-registry'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from './test-bootstrap-harness'
import { isModuleOwnedContribution } from '../settings-contribution-types'
import { SETTINGS_ORDER } from '../settings-order'

// Spec §4.1.3 — PR-2 final sidebar order. With Editor consolidated and
// Sync promoted to a structural module, the always-on purdex-scope
// sidebar order is:
//
//   appearance(0)
//   terminal(1)
//   interface(2)
//   electron(5)             — gated by canSystemTray; filtered out
//   module-config(10)
//   editor(11)
//   quick-commands(12)
//   performance-monitor(13)
//   sync(14)
//   dev-environment(20)     — only when caps.devUpdateEnabled
//   tmux-agent-monitor(21)  — DEV or devUpdateEnabled
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
  it('purdex-scope contributions match the exact PR-2 final ASC list', () => {
    const KNOWN_ALWAYS_ON = new Set([
      'appearance',
      'terminal',
      'interface',
      'module-config',
      'editor',
      'quick-commands',
      'performance-monitor',
      'sync',
    ])

    const items = listContributions('purdex')
      .filter((c) => KNOWN_ALWAYS_ON.has(c.localId))
      .map((c) => ({ id: c.localId, order: c.order }))
      .sort((a, b) => a.order - b.order)

    expect(items).toEqual([
      { id: 'appearance',          order: SETTINGS_ORDER.APPEARANCE },                  // 0
      { id: 'terminal',            order: SETTINGS_ORDER.TERMINAL },                    // 1
      { id: 'interface',           order: SETTINGS_ORDER.INTERFACE },                   // 2
      { id: 'module-config',       order: SETTINGS_ORDER.MODULE_CONFIG },               // 10
      { id: 'editor',              order: SETTINGS_ORDER.MODULE_EDITOR },               // 11
      { id: 'quick-commands',      order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS },       // 12
      { id: 'performance-monitor', order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR },  // 13
      { id: 'sync',                order: SETTINGS_ORDER.MODULE_SYNC },                 // 14
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
