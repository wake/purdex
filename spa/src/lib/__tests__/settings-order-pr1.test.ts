import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listContributions } from '../settings-contribution-registry'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from './test-bootstrap-harness'
import { isModuleOwnedContribution } from '../settings-contribution-types'
import { SETTINGS_ORDER } from '../settings-order'

// Spec §4.1.4 — PR-1 transitional sidebar order. After PR-1 lands but
// before PR-2 ships, the sidebar must look like:
//
//   appearance(0)
//   terminal(1)
//   interface(2)
//   electron(5)            -- gated by canSystemTray, present in tests' jsdom env? No → may not exist; test guards
//   module-config(10)      -- moved up to be the modules group header
//   performance-monitor(11)
//   open-behavior(12)
//   link-detect(13)
//   editor(14)
//   quick-commands(15)
//   sync(16)
//   dev-environment(20)    -- only when caps.devUpdateEnabled
//   tmux-agent-monitor(21) -- DEV or devUpdateEnabled
//
// PR-2 then collapses open-behavior + link-detect into editor and bumps
// the rest down to the SETTINGS_ORDER MODULE_* values; this PR-1 test is
// removed at the start of PR-2 (commit 1 in PR-2 `git rm`s it).

beforeEach(() => {
  resetAndRegisterBuiltinModules()
})

afterEach(() => {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
})

describe('PR-1 transitional sidebar order (spec §4.1.4)', () => {
  it('purdex-scope contributions match the exact PR-1 transitional ASC list', () => {
    // Take only entries we own + control in the PR-1 fixture. Optional
    // entries gated by env / caps (electron / dev-environment /
    // tmux-agent-monitor) are filtered out so the assertion is stable
    // across DEV vs prod-like jsdom runs. The list below is the ENTIRE
    // expected ordered set under those conditions — toEqual rejects any
    // unexpected entry that sneaks in between known ids.
    const KNOWN_ALWAYS_ON = new Set([
      'appearance',
      'terminal',
      'interface',
      'module-config',
      'performance-monitor',
      'open-behavior',
      'link-detect',
      'editor',
      'quick-commands',
      'sync',
    ])

    const items = listContributions('purdex')
      .filter((c) => KNOWN_ALWAYS_ON.has(c.localId))
      .map((c) => ({ id: c.localId, order: c.order }))
      .sort((a, b) => a.order - b.order)

    expect(items).toEqual([
      { id: 'appearance',          order: SETTINGS_ORDER.APPEARANCE },           // 0
      { id: 'terminal',            order: SETTINGS_ORDER.TERMINAL },             // 1
      { id: 'interface',           order: SETTINGS_ORDER.INTERFACE },            // 2
      { id: 'module-config',       order: SETTINGS_ORDER.MODULE_CONFIG },        // 10
      { id: 'performance-monitor', order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR_PR1 }, // 11
      { id: 'open-behavior',       order: SETTINGS_ORDER.MODULE_EDITOR_OPEN_BEHAVIOR_PR1 }, // 12
      { id: 'link-detect',         order: SETTINGS_ORDER.MODULE_EDITOR_LINK_DETECT_PR1 },   // 13
      { id: 'editor',              order: SETTINGS_ORDER.MODULE_EDITOR_PR1 },               // 14
      { id: 'quick-commands',      order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS_PR1 },       // 15
      { id: 'sync',                order: SETTINGS_ORDER.SYNC_PR1 },                        // 16
    ])
  })

  it('no contribution sneaks in between module-config and the modules-group tail', () => {
    // After module-config (=10) there must be ONLY the PR-1 module-owned
    // entries — performance-monitor through sync — until tail-built-in
    // (>= 20). This guard catches a hypothetical legacy section
    // registered with order 10.5 / 12.5 / etc. which would still pass the
    // unique-order check below but visually break the modules group.
    const PR1_MODULE_GROUP_IDS = [
      'performance-monitor',
      'open-behavior',
      'link-detect',
      'editor',
      'quick-commands',
      'sync',
    ]
    const items = listContributions('purdex')
      .slice()
      .sort((a, b) => a.order - b.order)

    const between = items
      .filter((c) => c.order > SETTINGS_ORDER.MODULE_CONFIG && c.order < 20)
      .map((c) => c.localId)
    expect(between).toEqual(PR1_MODULE_GROUP_IDS)
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
})
