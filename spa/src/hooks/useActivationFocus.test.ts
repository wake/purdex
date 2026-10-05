import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { StrictMode } from 'react'
import { renderHook } from '@testing-library/react'
import { useActivationFocus } from './useActivationFocus'

interface Props {
  isActive: boolean
  isFocusTarget: boolean
  focusFn: () => void
}

function mount(initial: Props, opts?: { raf?: boolean }, strict = false) {
  return renderHook((p: Props) => useActivationFocus(p.isActive, p.isFocusTarget, p.focusFn, opts), {
    initialProps: initial,
    wrapper: strict ? StrictMode : undefined,
  })
}

describe('useActivationFocus', () => {
  it('first mount, active and the target → focuses once', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(1)
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(1)
  })

  it('first mount, active but not the target → no focus', () => {
    const focus = vi.fn()
    mount({ isActive: true, isFocusTarget: false, focusFn: focus })
    expect(focus).not.toHaveBeenCalled()
  })

  it('first mount inactive → no focus, even as the target', () => {
    const focus = vi.fn()
    mount({ isActive: false, isFocusTarget: true, focusFn: focus })
    expect(focus).not.toHaveBeenCalled()
  })

  it('keep-alive reactivation: inactive → active as the target → focuses', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: false, isFocusTarget: true, focusFn: focus })
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(1)
  })

  it('inactive → active while not the target → no focus', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: false, isFocusTarget: false, focusFn: focus })
    rerender({ isActive: true, isFocusTarget: false, focusFn: focus })
    expect(focus).not.toHaveBeenCalled()
  })

  it('click inside a visible tab: the target flipping while active never focuses', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: true, isFocusTarget: false, focusFn: focus })
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    rerender({ isActive: true, isFocusTarget: false, focusFn: focus })
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).not.toHaveBeenCalled()
  })

  it('active → inactive → active focuses again', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: true, isFocusTarget: true, focusFn: focus })
    rerender({ isActive: false, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(1)
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(2)
  })

  it('reads the target at activation time: became the target while hidden → focuses on show', () => {
    const focus = vi.fn()
    const { rerender } = mount({ isActive: false, isFocusTarget: false, focusFn: focus })
    rerender({ isActive: false, isFocusTarget: true, focusFn: focus })
    expect(focus).not.toHaveBeenCalled()
    rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
    expect(focus).toHaveBeenCalledTimes(1)
  })

  it('calls the latest focusFn, and a new focusFn alone does not trigger a focus', () => {
    const first = vi.fn()
    const second = vi.fn()
    const { rerender } = mount({ isActive: false, isFocusTarget: true, focusFn: first })
    rerender({ isActive: false, isFocusTarget: true, focusFn: second })
    rerender({ isActive: true, isFocusTarget: true, focusFn: second })
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
    const third = vi.fn()
    rerender({ isActive: true, isFocusTarget: true, focusFn: third })
    expect(third).not.toHaveBeenCalled()
  })

  it('StrictMode (the app runs in it): the dev mount/unmount/mount still focuses exactly once', () => {
    const focus = vi.fn()
    mount({ isActive: true, isFocusTarget: true, focusFn: focus }, undefined, true)
    expect(focus).toHaveBeenCalledTimes(1)
  })

  describe('raf option', () => {
    let frames: FrameRequestCallback[]
    beforeEach(() => {
      frames = []
      vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
        frames.push(cb)
        return frames.length
      })
      vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
        frames[id - 1] = () => {}
      })
    })
    afterEach(() => vi.restoreAllMocks())
    const flush = () => frames.splice(0).forEach((cb) => cb(0))

    it('defers the focus to the next animation frame', () => {
      const focus = vi.fn()
      mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true })
      expect(focus).not.toHaveBeenCalled()
      expect(frames).toHaveLength(1)
      flush()
      expect(focus).toHaveBeenCalledTimes(1)
    })

    it('a pane hidden again before the frame runs does not focus', () => {
      const focus = vi.fn()
      const { rerender } = mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true })
      rerender({ isActive: false, isFocusTarget: true, focusFn: focus })
      flush()
      expect(focus).not.toHaveBeenCalled()
    })

    // P5 review A1: a click on another pane between the activation and its frame moves the target away; the
    // frame must not then steal focus back into this pane.
    it('no longer the target when the frame runs (a click elsewhere in between) → no focus', () => {
      const focus = vi.fn()
      const { rerender } = mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true })
      rerender({ isActive: true, isFocusTarget: false, focusFn: focus })
      flush()
      expect(focus).not.toHaveBeenCalled()
    })

    it('the target lost and regained before the frame runs → focuses once', () => {
      const focus = vi.fn()
      const { rerender } = mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true })
      rerender({ isActive: true, isFocusTarget: false, focusFn: focus })
      rerender({ isActive: true, isFocusTarget: true, focusFn: focus })
      flush()
      expect(focus).toHaveBeenCalledTimes(1)
    })

    it('unmounting before the frame runs does not focus', () => {
      const focus = vi.fn()
      const { unmount } = mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true })
      unmount()
      flush()
      expect(focus).not.toHaveBeenCalled()
    })

    it('StrictMode: the frame cancelled by the dev re-mount is requested again, and focus fires once', () => {
      const focus = vi.fn()
      mount({ isActive: true, isFocusTarget: true, focusFn: focus }, { raf: true }, true)
      flush()
      expect(focus).toHaveBeenCalledTimes(1)
    })

    it('without the option no frame is requested', () => {
      const focus = vi.fn()
      mount({ isActive: true, isFocusTarget: true, focusFn: focus })
      expect(frames).toHaveLength(0)
      expect(focus).toHaveBeenCalledTimes(1)
    })
  })
})
