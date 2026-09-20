// useProfileSync over the REAL snapshot of lib/profile/sync-status (through
// start.ts's re-exports): a render per change, none without one — which is the
// whole reason the snapshot is cached (a fresh object per `getSnapshot` call makes
// `useSyncExternalStore` render forever).
import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { __resetSyncStatusForTest, setLocalSnapshot } from '../lib/profile/sync-status'
import { useProfileSync } from './useProfileSync'

const NONE = { master: null, leader: false, blocked: null, status: null, problems: [] }

afterEach(() => __resetSyncStatusForTest())

describe('useProfileSync', () => {
  it('returns the snapshot, renders again when it is replaced, and not when it is not', () => {
    let renders = 0
    const { result, unmount } = renderHook(() => {
      renders += 1
      return useProfileSync()
    })
    expect(result.current).toEqual({ ...NONE, remote: false, stale: false })
    const first = result.current
    const rendersAtStart = renders

    act(() => setLocalSnapshot({ ...NONE, problems: [] })) // equal content
    expect(result.current).toBe(first)
    expect(renders).toBe(rendersAtStart)

    act(() => setLocalSnapshot({ ...NONE, problems: [{ kind: 'detach-failed', detail: 'x', at: 1 }] }))
    expect(result.current.problems).toEqual([{ kind: 'detach-failed', detail: 'x', at: 1 }])
    expect(renders).toBe(rendersAtStart + 1)

    unmount()
    const last = renders
    act(() => setLocalSnapshot(NONE))
    expect(renders).toBe(last) // unsubscribed
  })
})
