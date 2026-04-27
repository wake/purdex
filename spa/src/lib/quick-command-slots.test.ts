import { describe, it, expect } from 'vitest'
import { QUICK_COMMAND_SLOTS, type QuickCommandSlotId } from './quick-command-slots'

describe('quick-command-slots', () => {
  it('exposes WORKSPACE_ACTIONS and HOST_ACTIONS literals', () => {
    expect(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS).toBe('workspace.actions')
    expect(QUICK_COMMAND_SLOTS.HOST_ACTIONS).toBe('host.actions')
  })

  it('values are unique', () => {
    const values = Object.values(QUICK_COMMAND_SLOTS)
    expect(new Set(values).size).toBe(values.length)
  })

  it('QuickCommandSlotId narrows to the value union', () => {
    const x: QuickCommandSlotId = QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS
    expect(typeof x).toBe('string')
  })
})
