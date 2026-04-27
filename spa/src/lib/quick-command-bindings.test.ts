import { describe, it, expect } from 'vitest'
import {
  type Bindings,
  type QuickCommandData,
  sanitizeBindings,
  getBindingTargets,
  mergePersistedQuickCommandState,
} from './quick-command-bindings'
import { QUICK_COMMAND_SLOTS } from './quick-command-slots'

describe('sanitizeBindings — forward-compat with unknown slot ids (spec §2.3)', () => {
  // Spec §2.3 explicitly allows unknown slot ids in Phase 1 so cross-version
  // sync (older client pulls newer client's future slot binding) doesn't
  // lose data. SlotHost ignores unknown ids at render time.
  it('accepts known QUICK_COMMAND_SLOTS values', () => {
    const out = sanitizeBindings({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS],
    })
    expect(out).toEqual({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS],
    })
  })

  it('preserves unknown / future slot ids verbatim (forward-compat)', () => {
    const out = sanitizeBindings({
      'cmd-a': ['future.actions', 'workspace.action'],
    })
    // Both kept — spec requires forward-compat. SlotHost doesn't render them
    // because no slot is registered, but data persists for newer clients.
    expect(out).toEqual({
      'cmd-a': ['future.actions', 'workspace.action'],
    })
  })

  it('drops only empty / non-string targets', () => {
    const out = sanitizeBindings({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, '', 123, null, 'host.actions'],
    })
    expect(out).toEqual({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host.actions'],
    })
  })

  it('drops entries whose value is not an array', () => {
    const out = sanitizeBindings({
      'cmd-a': 'workspace.actions', // not an array
      'cmd-b': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    })
    expect(out).toEqual({
      'cmd-b': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    })
  })

  it('rejects prototype-pollution command id keys', () => {
    const out = sanitizeBindings({
      __proto__: ['workspace.actions'],
      constructor: ['workspace.actions'],
      prototype: ['workspace.actions'],
      'cmd-a': ['workspace.actions'],
    })
    expect(out).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('rejects non-object payloads (null / array / primitive)', () => {
    expect(sanitizeBindings(null)).toEqual({})
    expect(sanitizeBindings(undefined)).toEqual({})
    expect(sanitizeBindings('a string')).toEqual({})
    expect(sanitizeBindings(42)).toEqual({})
    expect(sanitizeBindings([['cmd-a', ['workspace.actions']]])).toEqual({})
  })
})

describe('getBindingTargets — own-property guard (codex round-2 A1)', () => {
  // Mirrors the prototype-method protection used inside store.getBoundCommands.
  // Naive `bindings[cmdId]` would resolve inherited Object.prototype methods
  // (toString / valueOf / hasOwnProperty) for capability ids that happen to
  // collide; subsequent .includes() on a function would throw.
  it('returns the array when bindings owns the property', () => {
    const bindings: Bindings = { 'cmd-a': ['workspace.actions'] }
    expect(getBindingTargets(bindings, 'cmd-a')).toEqual(['workspace.actions'])
  })

  it('returns undefined for inherited Object.prototype keys (toString / valueOf)', () => {
    const bindings: Bindings = {}
    expect(getBindingTargets(bindings, 'toString')).toBeUndefined()
    expect(getBindingTargets(bindings, 'valueOf')).toBeUndefined()
    expect(getBindingTargets(bindings, 'hasOwnProperty')).toBeUndefined()
  })

  it('returns undefined when own value is not an array (defensive)', () => {
    // Forced through the type system to simulate a corrupted runtime payload
    // that bypassed sanitizer (e.g. someone wrote setState directly).
    const bindings = { 'cmd-a': 'workspace.actions' as unknown as string[] } as Bindings
    expect(getBindingTargets(bindings, 'cmd-a')).toBeUndefined()
  })
})

describe('mergePersistedQuickCommandState — real hydrate trust boundary (codex round-2 Q1)', () => {
  // Drives the actual `merge` hook used by zustand persist on rehydrate,
  // not a setState fake. Catches any future regression where someone
  // inadvertently bypasses sanitizer at the hydrate trust boundary.
  const baselineCurrent: QuickCommandData = {
    global: [],
    byHost: {},
    bindings: {},
  }

  it('null persisted payload → returns current with empty bindings', () => {
    const out = mergePersistedQuickCommandState(null, baselineCurrent)
    expect(out.bindings).toEqual({})
  })

  it('hostile bindings payload (prototype keys) → sanitized to safe record', () => {
    const out = mergePersistedQuickCommandState(
      {
        global: [],
        byHost: {},
        bindings: {
          __proto__: ['workspace.actions'],
          constructor: ['workspace.actions'],
          'cmd-a': ['workspace.actions'],
        },
      },
      baselineCurrent,
    )
    expect(out.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('non-object bindings → fallback to {}', () => {
    const out = mergePersistedQuickCommandState(
      { global: [], byHost: {}, bindings: 'not-an-object' },
      baselineCurrent,
    )
    expect(out.bindings).toEqual({})
  })

  it('non-array global → keeps current.global (preserves alpha state)', () => {
    const current: QuickCommandData = {
      ...baselineCurrent,
      global: [{ id: 'existing', name: 'E', command: 'e' }],
    }
    const out = mergePersistedQuickCommandState(
      { global: 'corrupt', byHost: {}, bindings: {} },
      current,
    )
    expect(out.global).toEqual([{ id: 'existing', name: 'E', command: 'e' }])
  })

  it('valid payload passes through (sanity)', () => {
    const out = mergePersistedQuickCommandState(
      {
        global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
        byHost: { h1: [] },
        bindings: { 'cmd-a': ['workspace.actions'] },
      },
      baselineCurrent,
    )
    expect(out.global).toEqual([{ id: 'cmd-a', name: 'A', command: 'a' }])
    expect(out.byHost).toEqual({ h1: [] })
    expect(out.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('preserves caller-supplied extra fields on the current shape (generic over T)', () => {
    // Ensures the store can pass its full state (data + actions) and recover
    // the same shape — actions must survive the merge.
    type StoreLike = QuickCommandData & { actionMarker: () => string }
    const current: StoreLike = {
      ...baselineCurrent,
      actionMarker: () => 'still-here',
    }
    const out = mergePersistedQuickCommandState({ global: [], byHost: {}, bindings: {} }, current)
    expect(out.actionMarker()).toBe('still-here')
  })
})
