import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore } from './useQuickCommandStore'

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
