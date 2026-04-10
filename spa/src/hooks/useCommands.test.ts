import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useCommands } from './useCommands'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

describe('useCommands', () => {
  beforeEach(() => {
    clearModuleRegistry()
    useQuickCommandStore.setState({
      global: [{ id: 'g1', name: 'Global 1', command: 'echo global' }],
      byHost: {},
    })
  })

  it('returns store commands with source "store"', () => {
    const { result } = renderHook(() => useCommands({ hostId: 'h1' }))
    expect(result.current).toHaveLength(1)
    expect(result.current[0].source).toBe('store')
    expect(result.current[0].command).toBe('echo global')
  })

  it('includes module contributions with source = module id', () => {
    registerModule({
      id: 'test-mod',
      name: 'Test',
      commands: [{ id: 'mc1', name: 'Module Cmd', command: 'echo module' }],
    })
    const { result } = renderHook(() => useCommands({ hostId: 'h1' }))
    expect(result.current).toHaveLength(2)
    expect(result.current[1].source).toBe('test-mod')
  })

  it('resolves function commands with context', () => {
    registerModule({
      id: 'dyn-mod',
      name: 'Dynamic',
      commands: [{ id: 'dyn', name: 'Dynamic', command: (ctx) => `cd ${ctx.hostId}` }],
    })
    const { result } = renderHook(() => useCommands({ hostId: 'my-host' }))
    const dyn = result.current.find((c) => c.id === 'dyn')!
    expect(dyn.command).toBe('cd my-host')
  })
})
