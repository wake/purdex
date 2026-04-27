import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore } from './useQuickCommandStore'
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

describe('useQuickCommandStore — bindings API (Phase 1a)', () => {
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
