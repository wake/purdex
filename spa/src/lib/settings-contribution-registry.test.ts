import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerSettingsContribution,
  listContributions,
  getContribution,
  clearContributions,
} from './settings-contribution-registry'
import registrySrc from './settings-contribution-registry.ts?raw'
import type { AnySettingsContribution } from './settings-contribution-types'

const Fake = () => null

function make(overrides: Partial<AnySettingsContribution> = {}): AnySettingsContribution {
  return {
    id: 'mod.general',
    moduleId: 'mod',
    localId: 'general',
    scope: 'purdex',
    order: 0,
    labelKey: 'settings.general.label',
    component: Fake,
    ...overrides,
  } as AnySettingsContribution
}

describe('settings-contribution-registry', () => {
  beforeEach(() => clearContributions())

  // --- Basic ---

  it('register + listContributions(scope) returns that scope items', () => {
    const def = make()
    registerSettingsContribution(def)
    const items = listContributions('purdex')
    expect(items).toHaveLength(1)
    expect(items[0]).toBe(def)
  })

  it('getContribution(id) finds it', () => {
    const def = make({ id: 'mod.foo', localId: 'foo' })
    registerSettingsContribution(def)
    expect(getContribution('mod.foo')).toBe(def)
    expect(getContribution('mod.missing')).toBeUndefined()
  })

  it('listContributions sorts ascending by order', () => {
    registerSettingsContribution(make({ id: 'mod.a', localId: 'a', order: 20 }))
    registerSettingsContribution(make({ id: 'mod.b', localId: 'b', order: 5 }))
    registerSettingsContribution(make({ id: 'mod.c', localId: 'c', order: 10 }))
    expect(listContributions('purdex').map((c) => c.id)).toEqual(['mod.b', 'mod.c', 'mod.a'])
  })

  it('listContributions scope filter — no leaks, no misses', () => {
    registerSettingsContribution(make({ id: 'mod.p1', localId: 'p1', scope: 'purdex' }))
    registerSettingsContribution(make({ id: 'mod.h1', localId: 'h1', scope: 'host' }))
    registerSettingsContribution(make({ id: 'mod.w1', localId: 'w1', scope: 'workspace' }))
    registerSettingsContribution(make({ id: 'mod.p2', localId: 'p2', scope: 'purdex' }))

    expect(listContributions('purdex').map((c) => c.id).sort()).toEqual(['mod.p1', 'mod.p2'])
    expect(listContributions('host').map((c) => c.id)).toEqual(['mod.h1'])
    expect(listContributions('workspace').map((c) => c.id)).toEqual(['mod.w1'])
  })

  it('listContributions returns a fresh array (not the internal one)', () => {
    registerSettingsContribution(make())
    expect(listContributions('purdex')).not.toBe(listContributions('purdex'))
  })

  // --- HMR / Re-entry ---

  it('same id + same object reference re-registered: silent skip (no throw)', () => {
    const def = make()
    registerSettingsContribution(def)
    expect(() => registerSettingsContribution(def)).not.toThrow()
    expect(listContributions('purdex')).toHaveLength(1)
    expect(getContribution(def.id)).toBe(def)
  })

  it('same id + different object: throws with id in message', () => {
    const a = make({ labelKey: 'settings.a.label' })
    const b = make({ labelKey: 'settings.b.label' })
    registerSettingsContribution(a)
    expect(() => registerSettingsContribution(b)).toThrow(/mod\.general/)
  })

  it('after clearContributions, re-register same id with different object succeeds', () => {
    const a = make({ labelKey: 'settings.a.label' })
    registerSettingsContribution(a)
    clearContributions()
    const b = make({ labelKey: 'settings.b.label' })
    expect(() => registerSettingsContribution(b)).not.toThrow()
    expect(getContribution('mod.general')).toBe(b)
  })

  // --- Collision ---

  it('cross-registration with same id but different objects throws', () => {
    const a = make({ id: 'mod.dup', localId: 'dup', labelKey: 'a' })
    const b = make({ id: 'mod.dup', localId: 'dup', labelKey: 'b' })
    registerSettingsContribution(a)
    expect(() => registerSettingsContribution(b)).toThrow(/mod\.dup/)
  })

  // --- Validation ---

  it('empty moduleId throws', () => {
    expect(() =>
      registerSettingsContribution(make({ moduleId: '', id: '.general' })),
    ).toThrow()
  })

  it('empty localId throws', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: '', id: 'mod.' })),
    ).toThrow()
  })

  it('empty id throws', () => {
    expect(() => registerSettingsContribution(make({ id: '' }))).toThrow()
  })

  it('id not equal to `${moduleId}.${localId}` throws', () => {
    expect(() =>
      registerSettingsContribution(make({ id: 'other.general' })),
    ).toThrow()
  })

  it('localId contains whitespace throws', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: 'foo bar', id: 'mod.foo bar' })),
    ).toThrow()
  })

  it('localId contains `.` throws', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: 'foo.bar', id: 'mod.foo.bar' })),
    ).toThrow()
  })

  it('localId contains CJK / non-ASCII throws', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: '設定', id: 'mod.設定' })),
    ).toThrow()
  })

  it('localId starting with digit throws', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: '1foo', id: 'mod.1foo' })),
    ).toThrow()
  })

  it('localId with uppercase passes', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: 'FooBar', id: 'mod.FooBar' })),
    ).not.toThrow()
  })

  it('localId with underscore passes', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: 'foo_bar', id: 'mod.foo_bar' })),
    ).not.toThrow()
  })

  it('localId with dash passes', () => {
    expect(() =>
      registerSettingsContribution(make({ localId: 'foo-bar', id: 'mod.foo-bar' })),
    ).not.toThrow()
  })

  // --- Isolation ---

  it('clearContributions then listContributions returns empty array', () => {
    registerSettingsContribution(make({ id: 'mod.a', localId: 'a' }))
    registerSettingsContribution(make({ id: 'mod.b', localId: 'b', scope: 'host' }))
    clearContributions()
    expect(listContributions('purdex')).toEqual([])
    expect(listContributions('host')).toEqual([])
    expect(listContributions('workspace')).toEqual([])
  })

  // --- #539: internal API boundary -----------------------------------------
  //
  // These are source-text assertions. TypeScript's `@internal` is JSDoc-only;
  // we can't enforce it at the type layer without stripInternal + a separate
  // declaration build. Instead, lock the JSDoc presence so removing the tag
  // (e.g. during a well-meaning cleanup) raises a PR-time signal. Consumers
  // are audited by `rg "registerSettingsContribution"` per the plan.

  it('#539: registerSettingsContribution is marked @internal', () => {
    expect(registrySrc).toMatch(
      /\/\*\*[\s\S]*?@internal[\s\S]*?\*\/\s*export function registerSettingsContribution/,
    )
  })

  it('#539: clearContributions is marked @internal', () => {
    expect(registrySrc).toMatch(
      /\/\*\*[\s\S]*?@internal[\s\S]*?\*\/\s*export function clearContributions/,
    )
  })

  it('#539: assertValidSettingsContribution is marked @internal', () => {
    expect(registrySrc).toMatch(
      /\/\*\*[\s\S]*?@internal[\s\S]*?\*\/\s*export function assertValidSettingsContribution/,
    )
  })

  it('#539: listContributions / getContribution stay public (no @internal tag)', () => {
    // Ensure we did not accidentally mark the consumer-facing read APIs.
    const listBlockMatch = registrySrc.match(
      /(?:\/\*\*[\s\S]*?\*\/\s*)?export function listContributions/,
    )
    expect(listBlockMatch).toBeTruthy()
    const listBlock = listBlockMatch![0]
    expect(listBlock).not.toMatch(/@internal/)

    const getBlockMatch = registrySrc.match(
      /(?:\/\*\*[\s\S]*?\*\/\s*)?export function getContribution/,
    )
    expect(getBlockMatch).toBeTruthy()
    const getBlock = getBlockMatch![0]
    expect(getBlock).not.toMatch(/@internal/)
  })
})
