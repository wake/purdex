// A read that was superseded must never overwrite the newer one, even when it resolves last.
import { describe, it, expect, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { LaunchPlan } from '../lib/launch-plan'
import { useLaunchPlan } from './useLaunchPlan'

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

describe('useLaunchPlan', () => {
  it('an older in-flight read resolving after a newer one is ignored', async () => {
    const older = deferred()
    const newer = deferred()
    fetchPlan.mockReset().mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    const { result } = renderHook(() => useLaunchPlan(true, 'h', 'abc', 'k'))
    act(() => result.current.refresh())
    expect(fetchPlan).toHaveBeenCalledTimes(2)
    await act(async () => { newer.resolve(mk('NEW')) })
    expect(result.current.plan?.tmuxInstance).toBe('NEW')
    await act(async () => { older.resolve(mk('OLD')) })
    expect(result.current.plan?.tmuxInstance).toBe('NEW')
  })
})
