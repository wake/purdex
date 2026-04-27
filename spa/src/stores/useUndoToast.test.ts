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
    const action = vi.fn()
    useUndoToast.getState().show('Deleted host', action)
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toBe('Deleted host')
    expect(toast!.action).toBe(action)
  })

  it('dismiss clears toast', () => {
    useUndoToast.getState().show('msg', vi.fn())
    useUndoToast.getState().dismiss()
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('calling show again replaces previous toast', () => {
    const action1 = vi.fn()
    const action2 = vi.fn()
    useUndoToast.getState().show('first', action1)
    useUndoToast.getState().show('second', action2)
    const toast = useUndoToast.getState().toast
    expect(toast!.message).toBe('second')
    expect(toast!.action).toBe(action2)
  })
})

describe('useUndoToast — optional action / actionLabel (codex round-1 B4)', () => {
  beforeEach(() => {
    useUndoToast.setState({ toast: null })
  })

  it('back-compat: show(msg, fn) keeps action defined and actionLabel undefined', () => {
    useUndoToast.getState().show('msg', () => {})
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeTypeOf('function')
    expect(toast?.actionLabel).toBeUndefined()
  })

  it('show(msg) with no action stores undefined for both', () => {
    useUndoToast.getState().show('Failed')
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeUndefined()
    expect(toast?.actionLabel).toBeUndefined()
  })

  it('show(msg, fn, label) stores both', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeTypeOf('function')
    expect(toast?.actionLabel).toBe('Retry')
  })
})
