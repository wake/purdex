import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useWorkerListResize, WORKSPACE_ZONE_MIN, WORKER_DIVIDER_HEIGHT } from './useWorkerListResize'
import {
  useLayoutStore,
  WORKER_LIST_DEFAULT,
  WORKER_LIST_MIN,
  WORKER_LIST_MAX,
} from '../../../stores/useLayoutStore'

/** Replaces ResizeObserver; `fire(h)` reports a content height, `instances` counts the observers created. */
function stubResizeObserver() {
  let callback: ResizeObserverCallback | null = null
  const observe = vi.fn()
  const disconnect = vi.fn()
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(cb: ResizeObserverCallback) {
        callback = cb
      }
      observe = observe
      unobserve() {}
      disconnect = disconnect
    },
  )
  return {
    fire: (height: number) =>
      act(() => callback!([{ contentRect: { height } } as unknown as ResizeObserverEntry], {} as never)),
    observe,
    disconnect,
  }
}

/** Renders the hook closed, attaches a split box to its ref, then opens it (so the effect sees the box). */
function renderResize({ open = true } = {}) {
  const box = document.createElement('div')
  const hook = renderHook(({ open }) => useWorkerListResize(open), { initialProps: { open: false } })
  hook.result.current.splitBoxRef.current = box
  if (open) hook.rerender({ open: true })
  return { ...hook, box }
}

let setWorkerListHeight: Mock<(height: number) => void>

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
  setWorkerListHeight = vi.fn(useLayoutStore.getState().setWorkerListHeight)
  useLayoutStore.setState({ setWorkerListHeight })
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useWorkerListResize', () => {
  it('renders the stored height when nothing is measured (no cap)', () => {
    vi.stubGlobal('ResizeObserver', undefined)
    const { result } = renderResize()
    expect(result.current.height).toBe(WORKER_LIST_DEFAULT)
  })

  it('keeps a draft while dragging and commits it once on resize end', () => {
    const { result } = renderResize()
    act(() => result.current.onResize(-30))
    act(() => result.current.onResize(-20))
    expect(result.current.height).toBe(WORKER_LIST_DEFAULT + 50)
    expect(setWorkerListHeight).not.toHaveBeenCalled()

    act(() => result.current.onResizeEnd())
    expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
    expect(setWorkerListHeight).toHaveBeenCalledWith(WORKER_LIST_DEFAULT + 50)
    expect(result.current.height).toBe(WORKER_LIST_DEFAULT + 50)

    // No drag in progress: a stray resize end writes nothing.
    act(() => result.current.onResizeEnd())
    expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
  })

  it('holds the draft inside [WORKER_LIST_MIN, WORKER_LIST_MAX]', () => {
    const { result } = renderResize()
    act(() => result.current.onResize(-10_000))
    expect(result.current.height).toBe(WORKER_LIST_MAX)
    act(() => result.current.onResize(10_000))
    expect(result.current.height).toBe(WORKER_LIST_MIN)
  })

  it('observes the split box only while open and caps the rendered height at available − zone min − divider', () => {
    const ro = stubResizeObserver()
    const { result, box, rerender } = renderResize({ open: false })
    expect(ro.observe).not.toHaveBeenCalled()

    rerender({ open: true })
    expect(ro.observe).toHaveBeenCalledWith(box)
    ro.fire(300)
    expect(result.current.height).toBe(300 - WORKSPACE_ZONE_MIN - WORKER_DIVIDER_HEIGHT)
    expect(setWorkerListHeight).not.toHaveBeenCalled()

    // A drag cannot go past the cap either.
    act(() => result.current.onResize(-100))
    expect(result.current.height).toBe(200)
  })

  it('a cap that shrinks mid-drag but stays resizable commits the on-screen height, not the larger draft', () => {
    const ro = stubResizeObserver()
    useLayoutStore.setState({ workerListHeight: 300 })
    const { result } = renderResize()
    ro.fire(400 + WORKSPACE_ZONE_MIN + WORKER_DIVIDER_HEIGHT)
    expect(result.current.height).toBe(300)

    act(() => result.current.onResize(-50))
    expect(result.current.height).toBe(350)

    // The box shrinks with the pointer still: cap 250 ≥ WORKER_LIST_MIN, so the screen shows 250 and spec §4.2
    // ("a drag stores what the user sees") means the end stores 250, not the 350 draft.
    ro.fire(250 + WORKSPACE_ZONE_MIN + WORKER_DIVIDER_HEIGHT)
    expect(result.current.height).toBe(250)
    act(() => result.current.onResizeEnd())
    expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
    expect(setWorkerListHeight).toHaveBeenCalledWith(250)
    expect(useLayoutStore.getState().workerListHeight).toBe(250)
    expect(result.current.height).toBe(250)
  })

  // Spec §4.2 "a drag stores what the user sees": below WORKER_LIST_MIN no storable height matches the screen.
  describe('split box too short to resize (cap < WORKER_LIST_MIN)', () => {
    // cap = available − 96 − 4
    const availableFor = (cap: number) => cap + WORKSPACE_ZONE_MIN + WORKER_DIVIDER_HEIGHT

    it('a drag starts no draft and its end writes nothing; the list stays at the cap and returns to stored when room comes back', () => {
      const ro = stubResizeObserver()
      useLayoutStore.setState({ workerListHeight: 800 })
      const { result } = renderResize()
      ro.fire(availableFor(50))
      expect(result.current.height).toBe(50)

      act(() => result.current.onResize(-30))
      expect(result.current.height).toBe(50)
      act(() => result.current.onResize(40))
      expect(result.current.height).toBe(50)
      act(() => result.current.onResizeEnd())
      expect(setWorkerListHeight).not.toHaveBeenCalled()
      expect(useLayoutStore.getState().workerListHeight).toBe(800)
      expect(result.current.height).toBe(50)

      ro.fire(1000)
      expect(result.current.height).toBe(800)
    })

    it('room coming back mid-drag shows the stored height, not a draft left over from the short box', () => {
      const ro = stubResizeObserver()
      useLayoutStore.setState({ workerListHeight: 800 })
      const { result } = renderResize()
      ro.fire(availableFor(50))
      act(() => result.current.onResize(-30))

      ro.fire(1000)
      expect(result.current.height).toBe(800)
      act(() => result.current.onResizeEnd())
      expect(setWorkerListHeight).not.toHaveBeenCalled()
    })

    it('a box that shrinks below the minimum mid-drag drops the draft without committing it', () => {
      const ro = stubResizeObserver()
      useLayoutStore.setState({ workerListHeight: 800 })
      const { result } = renderResize()
      ro.fire(availableFor(200))
      act(() => result.current.onResize(50))
      expect(result.current.height).toBe(150)

      ro.fire(availableFor(50))
      expect(result.current.height).toBe(50)
      act(() => result.current.onResizeEnd())
      expect(setWorkerListHeight).not.toHaveBeenCalled()
      expect(useLayoutStore.getState().workerListHeight).toBe(800)

      ro.fire(1000)
      expect(result.current.height).toBe(800)
    })

    it('a cap of exactly WORKER_LIST_MIN still resizes and commits what is on screen', () => {
      const ro = stubResizeObserver()
      useLayoutStore.setState({ workerListHeight: 800 })
      const { result } = renderResize()
      ro.fire(availableFor(WORKER_LIST_MIN))
      expect(result.current.height).toBe(WORKER_LIST_MIN)

      act(() => result.current.onResize(-30))
      act(() => result.current.onResize(30))
      expect(result.current.height).toBe(WORKER_LIST_MIN)
      act(() => result.current.onResizeEnd())
      expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
      expect(setWorkerListHeight).toHaveBeenCalledWith(WORKER_LIST_MIN)
      expect(useLayoutStore.getState().workerListHeight).toBe(WORKER_LIST_MIN)
    })
  })

  it('unmount disconnects the observer and a late resize call writes nothing', () => {
    const ro = stubResizeObserver()
    const { result, unmount } = renderResize()
    const { onResize, onResizeEnd } = result.current
    act(() => onResize(-50))

    unmount()
    expect(ro.disconnect).toHaveBeenCalledTimes(1)
    onResizeEnd()
    onResize(-10)
    onResizeEnd()
    expect(setWorkerListHeight).not.toHaveBeenCalled()
    expect(useLayoutStore.getState().workerListHeight).toBe(WORKER_LIST_DEFAULT)
  })

  it('closing the list mid-drag disconnects the observer and drops the draft without committing it', () => {
    const ro = stubResizeObserver()
    const { result, rerender } = renderResize()
    act(() => result.current.onResize(-50))
    expect(result.current.height).toBe(WORKER_LIST_DEFAULT + 50)

    rerender({ open: false })
    expect(ro.disconnect).toHaveBeenCalledTimes(1)
    act(() => result.current.onResizeEnd())
    expect(setWorkerListHeight).not.toHaveBeenCalled()
    // A late move from the divider that is going away starts no new draft either.
    act(() => result.current.onResize(-50))

    rerender({ open: true })
    expect(result.current.height).toBe(WORKER_LIST_DEFAULT)
    act(() => result.current.onResizeEnd())
    expect(setWorkerListHeight).not.toHaveBeenCalled()
  })
})
