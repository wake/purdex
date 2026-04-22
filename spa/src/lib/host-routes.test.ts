/**
 * host-routes.test.ts
 *
 * §3.2 tests for Commit 3: isHostSubPage dynamic via contribution registry.
 *
 * Case 1: Built-in sub-pages (all six) → isHostSubPage returns true.
 * Case 2: After registering a fake host contribution → isHostSubPage returns true.
 * Case 3: Unknown value → isHostSubPage returns false.
 */

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { isHostSubPage, HOST_SUB_PAGES } from './host-routes'
import {
  clearContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import { clearModuleRegistry } from './module-registry'
import { registerBuiltinModules } from './register-modules'

beforeEach(() => {
  clearContributions()
  clearModuleRegistry()
  registerBuiltinModules()
})

afterEach(() => {
  clearContributions()
  clearModuleRegistry()
})

describe('isHostSubPage (§3.2)', () => {
  // Case 1: all six built-in sub-pages return true
  it('C1: all six built-in sub-pages return true', () => {
    for (const page of HOST_SUB_PAGES) {
      expect(isHostSubPage(page), `expected isHostSubPage('${page}') to be true`).toBe(true)
    }
  })

  // Case 2: a dynamically registered host contribution also returns true
  it('C2: dynamically registered localId returns true', () => {
    registerSettingsContribution({
      moduleId: 'testmod',
      id: 'testmod.mysection',
      localId: 'mysection',
      scope: 'host',
      order: 100,
      labelKey: 'testmod.mysection',
      component: () => null,
    })

    expect(isHostSubPage('mysection')).toBe(true)
  })

  // Case 3: unknown value returns false
  it('C3: nonexistent value returns false', () => {
    expect(isHostSubPage('nonexistent')).toBe(false)
    expect(isHostSubPage('')).toBe(false)
    expect(isHostSubPage('OVERVIEW')).toBe(false) // case-sensitive
  })
})
