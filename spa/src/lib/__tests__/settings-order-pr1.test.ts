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
  it('purdex-scope contributions are in PR-1 transitional ASC order', () => {
    const items = listContributions('purdex')
      .map((c) => ({ id: c.localId, order: c.order }))
      .sort((a, b) => a.order - b.order)
    const ids = items.map((x) => x.id)

    // The bootstrap harness registers every built-in module + dispatches the
    // contributions; in jsdom (no electronAPI, but DEV / devUpdateEnabled are
    // still effectively true via Vite import.meta.env.DEV), expect the
    // following always-on transitional set.
    //
    // Built-in section IDs visible in `listContributions('purdex')` after
    // dispatch land under `_builtin.legacy-section.<id>` but expose their
    // `localId` (= the human-readable id) at the top level.
    expect(ids).toContain('appearance')
    expect(ids).toContain('terminal')
    expect(ids).toContain('interface')
    expect(ids).toContain('module-config')
    expect(ids).toContain('performance-monitor')
    expect(ids).toContain('open-behavior')
    expect(ids).toContain('link-detect')
    expect(ids).toContain('editor')
    expect(ids).toContain('quick-commands')
    expect(ids).toContain('sync')

    // Order assertions for each entry that is always present.
    const orderOf = (localId: string) => items.find((i) => i.id === localId)!.order
    expect(orderOf('appearance')).toBe(0)
    expect(orderOf('terminal')).toBe(1)
    expect(orderOf('interface')).toBe(2)
    expect(orderOf('module-config')).toBe(SETTINGS_ORDER.MODULE_CONFIG) // = 10
    expect(orderOf('performance-monitor')).toBe(11)
    expect(orderOf('open-behavior')).toBe(12)
    expect(orderOf('link-detect')).toBe(13)
    expect(orderOf('editor')).toBe(14)
    expect(orderOf('quick-commands')).toBe(15)
    expect(orderOf('sync')).toBe(16)
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
