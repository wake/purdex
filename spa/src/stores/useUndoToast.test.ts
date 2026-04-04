// spa/src/stores/useUndoToast.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useUndoToast } from './useUndoToast'

describe('useUndoToast', () => {
  beforeEach(() => {
    useUndoToast.setState({ toast: null })
  })

  it('starts with null toast', () => {
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('show sets toast', () => {
    const restore = vi.fn()
    useUndoToast.getState().show('Deleted host', restore)
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toBe('Deleted host')
    expect(toast!.restore).toBe(restore)
  })

  it('dismiss clears toast', () => {
    useUndoToast.getState().show('msg', vi.fn())
    useUndoToast.getState().dismiss()
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('calling show again replaces previous toast', () => {
    const restore1 = vi.fn()
    const restore2 = vi.fn()
    useUndoToast.getState().show('first', restore1)
    useUndoToast.getState().show('second', restore2)
    const toast = useUndoToast.getState().toast
    expect(toast!.message).toBe('second')
    expect(toast!.restore).toBe(restore2)
  })
})
