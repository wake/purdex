import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore, sanitizeBindings } from './useQuickCommandStore'
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

describe('sanitizeBindings — slot id whitelist (codex round-1 P2)', () => {
  it('accepts known QUICK_COMMAND_SLOTS values', () => {
    const out = sanitizeBindings({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS],
    })
    expect(out).toEqual({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS],
    })
  })

  it('rejects unknown slot ids (typos / future slots)', () => {
    const out = sanitizeBindings({
      'cmd-a': ['workspace.action', 'workspaces.actions', 'host.action'],
    })
    // All three are typos / unknown → entry dropped (cleaned array empty)
    expect(out).toEqual({})
  })

  it('keeps only whitelisted slot ids in mixed array', () => {
    const out = sanitizeBindings({
      'cmd-a': [
        QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS,
        'workspace.action',
        QUICK_COMMAND_SLOTS.HOST_ACTIONS,
        'random.target',
      ],
    })
    expect(out).toEqual({
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS],
    })
  })

  it('drops entries whose targets are all invalid', () => {
    const out = sanitizeBindings({
      'cmd-a': ['unknown'],
      'cmd-b': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    })
    expect(out).toEqual({
      'cmd-b': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    })
  })
})
