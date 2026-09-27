import { beforeEach, describe, expect, it } from 'vitest'
import { useConflictPanelStore } from './useConflictPanelStore'

beforeEach(() => useConflictPanelStore.setState({ openWsId: null }))

describe('useConflictPanelStore', () => {
  it('one panel at a time: openFor replaces, close clears', () => {
    const s = useConflictPanelStore.getState()
    s.openFor('w1')
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    s.openFor('w2')
    expect(useConflictPanelStore.getState().openWsId).toBe('w2')
    s.close()
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
  })

  it('toggle opens, then closes the same one; toggling another moves the panel', () => {
    const s = useConflictPanelStore.getState()
    s.toggle('w1')
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    s.toggle('w2')
    expect(useConflictPanelStore.getState().openWsId).toBe('w2')
    s.toggle('w2')
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
  })

  it('closeFor closes only its own entry: a row going away never closes another row\'s panel', () => {
    const s = useConflictPanelStore.getState()
    s.openFor('w1')
    s.closeFor('w2')
    expect(useConflictPanelStore.getState().openWsId).toBe('w1')
    s.closeFor('w1')
    expect(useConflictPanelStore.getState().openWsId).toBeNull()
  })
})
