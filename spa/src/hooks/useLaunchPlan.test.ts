// The hook is a thin trigger over the shared store: it loads when enabled, on changeKey, and on refresh().
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { LaunchPlan } from '../lib/launch-plan'
import { useLaunchPlan } from './useLaunchPlan'
import { useLaunchPlanStore } from '../stores/useLaunchPlanStore'

const fetchPlan = vi.hoisted(() => vi.fn())
vi.mock('../lib/launch-plan', async (orig) => ({ ...(await orig<typeof import('../lib/launch-plan')>()), fetchLaunchPlan: fetchPlan }))

const mk = (tmuxInstance: string): LaunchPlan => ({
  agentRunning: false, tmuxInstance, dir: null, resume: { line: null, reason: 'no_record' }, fresh: { line: 'claude' }, busy: null,
})
const deferred = () => {
  let resolve!: (p: LaunchPlan) => void
  const promise = new Promise<LaunchPlan>((r) => { resolve = r })
  return { promise, resolve }
}

beforeEach(() => {
  fetchPlan.mockReset()
  useLaunchPlanStore.setState({ entries: {} })
})

describe('useLaunchPlan', () => {
  it('an older in-flight read resolving after a newer one is ignored', async () => {
    const older = deferred()
    const newer = deferred()
    fetchPlan.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    const { result } = renderHook(() => useLaunchPlan(true, 'h', 'abc', 'k'))
    act(() => result.current.refresh())
    expect(fetchPlan).toHaveBeenCalledTimes(2)
    await act(async () => { newer.resolve(mk('NEW')) })
    expect(result.current.entry?.plan?.tmuxInstance).toBe('NEW')
    await act(async () => { older.resolve(mk('OLD')) })
    expect(result.current.entry?.plan?.tmuxInstance).toBe('NEW')
  })

  it('does not read while disabled, reads when enabled turns on', async () => {
    fetchPlan.mockResolvedValue(mk('A'))
    const { result, rerender } = renderHook(({ on }) => useLaunchPlan(on, 'h', 'abc', 'k'), { initialProps: { on: false } })
    expect(fetchPlan).not.toHaveBeenCalled()
    expect(result.current.entry).toBeUndefined()
    rerender({ on: true })
    await act(async () => {})
    expect(fetchPlan).toHaveBeenCalledTimes(1)
    expect(result.current.entry?.status).toBe('ready')
  })

  it('re-reads when changeKey changes', async () => {
    fetchPlan.mockResolvedValue(mk('A'))
    const { rerender } = renderHook(({ k }) => useLaunchPlan(true, 'h', 'abc', k), { initialProps: { k: 'a' } })
    await act(async () => {})
    rerender({ k: 'b' })
    await act(async () => {})
    expect(fetchPlan).toHaveBeenCalledTimes(2)
  })
})
