import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import {
  getTakeToTerminal, registerTakeToTerminal, subscribeTakeToTerminal, unregisterTakeToTerminal, useTakeToTerminal,
  type TakeToTerminalEntry,
} from './take-to-terminal-registry'

const entry = (extra: Partial<TakeToTerminalEntry> = {}): TakeToTerminalEntry =>
  ({ canTake: true, busy: false, takeToTerminal: vi.fn(), ...extra })

afterEach(() => {
  for (const id of ['p1', 'p2']) unregisterTakeToTerminal(id)
})

describe('take-to-terminal registry', () => {
  it('register makes the entry readable by pane id; unregister removes it', () => {
    const e = entry()
    expect(getTakeToTerminal('p1')).toBeNull()
    registerTakeToTerminal('p1', e)
    expect(getTakeToTerminal('p1')).toBe(e)
    expect(getTakeToTerminal('p2')).toBeNull()
    unregisterTakeToTerminal('p1')
    expect(getTakeToTerminal('p1')).toBeNull()
  })

  it('the unregister returned by register removes only its own entry, not a newer one for the same pane', () => {
    const first = entry()
    const second = entry({ busy: true })
    const unregisterFirst = registerTakeToTerminal('p1', first)
    const unregisterSecond = registerTakeToTerminal('p1', second)
    expect(getTakeToTerminal('p1')).toBe(second)
    unregisterFirst()
    expect(getTakeToTerminal('p1')).toBe(second)
    unregisterSecond()
    expect(getTakeToTerminal('p1')).toBeNull()
  })

  it('notifies subscribers on register and on a removal, not on a no-op removal; unsubscribe stops it', () => {
    const listener = vi.fn()
    const unsubscribe = subscribeTakeToTerminal(listener)
    const unregister = registerTakeToTerminal('p1', entry())
    expect(listener).toHaveBeenCalledTimes(1)
    unregister()
    expect(listener).toHaveBeenCalledTimes(2)
    unregister() // already gone
    unregisterTakeToTerminal('p2') // never there
    expect(listener).toHaveBeenCalledTimes(2)
    unsubscribe()
    registerTakeToTerminal('p1', entry())
    expect(listener).toHaveBeenCalledTimes(2)
  })

  it('reads are stable: the same object until the pane registers again', () => {
    const e = entry()
    registerTakeToTerminal('p1', e)
    expect(getTakeToTerminal('p1')).toBe(getTakeToTerminal('p1'))
  })
})

describe('useTakeToTerminal', () => {
  it('returns the pane\'s entry, follows register / unregister, and null without a pane id', () => {
    const { result, rerender } = renderHook(({ id }: { id: string | null }) => useTakeToTerminal(id), { initialProps: { id: 'p1' as string | null } })
    expect(result.current).toBeNull()
    const e = entry()
    act(() => { registerTakeToTerminal('p1', e) })
    expect(result.current).toBe(e)
    const busy = entry({ busy: true })
    act(() => { registerTakeToTerminal('p1', busy) })
    expect(result.current).toBe(busy)
    act(() => { unregisterTakeToTerminal('p1') })
    expect(result.current).toBeNull()
    rerender({ id: null })
    expect(result.current).toBeNull()
  })

  it('another pane registering does not re-render a hook watching a different pane', () => {
    const e = entry()
    registerTakeToTerminal('p1', e)
    let renders = 0
    const { result } = renderHook(() => { renders++; return useTakeToTerminal('p1') })
    const before = renders
    act(() => { registerTakeToTerminal('p2', entry()) })
    expect(result.current).toBe(e)
    expect(renders).toBe(before)
  })
})
