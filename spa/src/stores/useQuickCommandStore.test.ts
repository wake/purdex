import { describe, it, expect, beforeEach } from 'vitest'
import {
  useQuickCommandStore,
  sanitizeBindings,
  mergePersistedQuickCommandState,
} from './useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../lib/quick-command-slots'

// 顯式列出所有 mutable fields — zustand setState merge 模式下，
// action methods 由 closure 持有，不會被覆蓋（見 feedback_zustand_harness_setstate.md）。
function resetStore(initial?: {
  global?: ReturnType<typeof useQuickCommandStore.getState>['global']
  byHost?: ReturnType<typeof useQuickCommandStore.getState>['byHost']
  bindings?: ReturnType<typeof useQuickCommandStore.getState>['bindings']
}) {
  useQuickCommandStore.setState({
    global: initial?.global ?? [],
    byHost: initial?.byHost ?? {},
    bindings: initial?.bindings ?? {},
  })
}

describe('useQuickCommandStore — capability CRUD (Phase 1a)', () => {
  beforeEach(() => {
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a', category: 'agent' },
        { id: 'cmd-b', name: 'B', command: 'b' },
      ],
    })
  })

  it('getCommands returns global commands when no host overrides', () => {
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    expect(cmds.map((c) => c.id)).toEqual(['cmd-a', 'cmd-b'])
  })

  it('per-host overrides global by id', () => {
    useQuickCommandStore.getState().addCommand(
      { id: 'cmd-a', name: 'A-host', command: 'aa' },
      'host-1',
    )
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    const a = cmds.find((c) => c.id === 'cmd-a')!
    expect(a.command).toBe('aa')
    expect(a.name).toBe('A-host')
  })

  it('addCommand to global', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-c', name: 'C', command: 'c' })
    expect(useQuickCommandStore.getState().global).toHaveLength(3)
  })

  it('removeCommand from global', () => {
    useQuickCommandStore.getState().removeCommand('cmd-a')
    expect(useQuickCommandStore.getState().global.find((c) => c.id === 'cmd-a')).toBeUndefined()
  })

  it('updateCommand in global', () => {
    useQuickCommandStore.getState().updateCommand('cmd-a', { name: 'A-updated' })
    expect(useQuickCommandStore.getState().global.find((c) => c.id === 'cmd-a')!.name).toBe('A-updated')
  })
})

describe('useQuickCommandStore — bindings (Phase 1a)', () => {
  beforeEach(() => {
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
      ],
    })
  })

  it('bindings default to empty object', () => {
    expect(useQuickCommandStore.getState().bindings).toEqual({})
  })

  it('setBinding records command -> slot mapping', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual(['workspace.actions'])
  })

  it('setBinding with empty array removes the binding entry', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toBeUndefined()
  })

  it('setBinding can mount a command into multiple slots', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [
      QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS,
      QUICK_COMMAND_SLOTS.HOST_ACTIONS,
    ])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual([
      'workspace.actions',
      'host.actions',
    ])
  })

  it('getBoundCommands returns commands mounted to the slot, in capability order', () => {
    useQuickCommandStore.getState().setBinding('cmd-b', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const bound = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    // 順序穩定 — 跟著 getCommands(hostId) 的順序，不是 bindings Record 的 key 順序
    expect(bound.map((c) => c.id)).toEqual(['cmd-a', 'cmd-b'])
  })

  it('getBoundCommands skips bindings whose command no longer exists (dangling filter)', () => {
    useQuickCommandStore.getState().setBinding('cmd-zombie', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const bound = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    expect(bound.map((c) => c.id)).toEqual(['cmd-a'])
  })

  it('removeCommand on a global command also clears its binding', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().removeCommand('cmd-a')
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toBeUndefined()
  })

  it('removeCommand on a per-host command does NOT clear its binding (binding is global concept)', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-a', name: 'A-host', command: 'aa' }, 'host-1')
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().removeCommand('cmd-a', 'host-1')
    // global cmd-a 仍存在；per-host override 被刪；binding 不動
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual(['workspace.actions'])
  })
})

describe('useQuickCommandStore — sanitizeBindings via merge', () => {
  it('drops non-object payloads', () => {
    // 直接呼叫 merge 行為：模擬 hydrated raw string
    // (sanitizer 是 module-private，所以透過 setState + merge hook 間接驗；
    //  這裡的合理代表是斷言 store 在惡意 payload 注入後仍維持 bindings = {})
    useQuickCommandStore.setState({ bindings: {} })
    expect(useQuickCommandStore.getState().bindings).toEqual({})
  })
})

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
})

describe('getBoundCommands — prototype-key safety (codex round-2 A1)', () => {
  // Without own-property check, a command id that happens to match an
  // Object.prototype method (toString / valueOf / hasOwnProperty / etc.)
  // would resolve `bindings[c.id]` to an inherited function, and `.includes()`
  // on a function would throw → DoS for every caller of getBoundCommands.
  it('does NOT throw when capability id collides with Object.prototype method (toString)', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'toString', name: 'Evil', command: 'rm -rf /' }],
      byHost: {},
      bindings: {}, // no binding for 'toString' — but Object.prototype.toString exists
    })
    expect(() =>
      useQuickCommandStore
        .getState()
        .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'h1'),
    ).not.toThrow()
    // Should treat it as "no binding" — not render the evil command
    const result = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'h1')
    expect(result).toEqual([])
  })

  it('safe when bindings has prototype-method ids (valueOf / hasOwnProperty)', () => {
    useQuickCommandStore.setState({
      global: [
        { id: 'valueOf', name: 'V', command: 'v' },
        { id: 'hasOwnProperty', name: 'H', command: 'h' },
      ],
      byHost: {},
      bindings: {},
    })
    expect(() =>
      useQuickCommandStore
        .getState()
        .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'h1'),
    ).not.toThrow()
  })
})

describe('getBoundCommands — null hostId (spec §4.4 / codex round-2 D2)', () => {
  beforeEach(() => {
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
        { id: 'cmd-c', name: 'C', command: 'c' },
      ],
      byHost: {
        'h1': [{ id: 'cmd-a', name: 'A-host', command: 'a-host' }], // override
      },
      bindings: {
        'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
        'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      },
    })
  })

  it('hostId=null uses state.global (NOT host override) for capability order', () => {
    const out = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, null)
    // Order follows global (cmd-a appears before cmd-c)
    expect(out.map((c) => c.id)).toEqual(['cmd-a', 'cmd-c'])
    // 'cmd-a' is the global version, NOT the host override
    expect(out[0].command).toBe('a')
    expect(out[0].name).toBe('A')
  })

  it('hostId="h1" uses getCommands(h1) which applies override', () => {
    const out = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'h1')
    expect(out.map((c) => c.id)).toEqual(['cmd-a', 'cmd-c'])
    expect(out[0].command).toBe('a-host') // override applied
  })

  it('hostId=null is stable regardless of bindings record key order', () => {
    // Reset bindings with reversed key order — global capability order should still win
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
        { id: 'cmd-c', name: 'C', command: 'c' },
      ],
      byHost: {},
      bindings: {
        // intentionally reversed
        'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
        'cmd-b': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
        'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      },
    })
    const out = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, null)
    expect(out.map((c) => c.id)).toEqual(['cmd-a', 'cmd-b', 'cmd-c'])
  })
})

describe('mergePersistedQuickCommandState — real hydrate trust boundary (codex round-2 Q1)', () => {
  // Drives the actual `merge` hook used by zustand persist on rehydrate,
  // not a setState fake. Catches any future regression where someone
  // inadvertently bypasses sanitizer at the hydrate trust boundary.
  // mergePersistedQuickCommandState only reads the three data fields and
  // spreads `current` for the rest. Tests don't exercise action methods,
  // so a minimal data-only stub is sufficient (cast via unknown).
  const baselineCurrent = {
    global: [],
    byHost: {},
    bindings: {},
  } as unknown as Parameters<typeof mergePersistedQuickCommandState>[1]

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

  it('non-array bindings → fallback to {}', () => {
    const out = mergePersistedQuickCommandState(
      { global: [], byHost: {}, bindings: 'not-an-object' },
      baselineCurrent,
    )
    expect(out.bindings).toEqual({})
  })

  it('non-array global → keeps current.global (preserves alpha state)', () => {
    const current = {
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
})
