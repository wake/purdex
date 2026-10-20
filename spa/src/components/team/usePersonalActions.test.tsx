// spa/src/components/team/usePersonalActions.test.tsx — the personal panel's writes go one at a time (TG-5c-2, codex attack): a second one while the first waits for the session's root is dropped.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'

const fileSession = vi.fn()
const takeSessionOut = vi.fn()
vi.mock('../../lib/task-groups/session-actions', () => ({
  fileSession: (...a: unknown[]) => fileSession(...a),
  takeSessionOut: (...a: unknown[]) => takeSessionOut(...a),
  startGroupWithSession: vi.fn(),
}))
const placeLast = vi.fn()
vi.mock('../../lib/task-groups/place-last', () => ({ placeLastInGroup: (...a: unknown[]) => placeLast(...a) }))

import { usePersonalActions } from './usePersonalActions'
import type { PersonalTarget } from '../../lib/task-groups/personal'

const target: PersonalTarget = { tabId: 't1', hostId: 'h1', sessionCode: 'c1', agentSessionId: 'S1', agentType: 'cc', ended: false }

beforeEach(() => {
  fileSession.mockReset(); takeSessionOut.mockReset(); placeLast.mockReset()
})

describe('usePersonalActions', () => {
  it('a second write while one waits is dropped — the first one\'s answer decides, and the tab is placed once', async () => {
    let resolve!: (r: { ok: true }) => void
    fileSession.mockReturnValue(new Promise((r) => { resolve = r }))
    const { result } = renderHook(() => usePersonalActions(target))
    let first!: Promise<boolean>
    let second!: Promise<boolean>
    act(() => { first = result.current.move('g1'); second = result.current.move('g2') })
    expect(result.current.busy).toBe(true)
    await expect(second).resolves.toBe(false)
    await act(async () => { resolve({ ok: true }); await first })
    expect(fileSession).toHaveBeenCalledTimes(1)
    expect(fileSession).toHaveBeenCalledWith({ hostId: 'h1', agentSessionId: 'S1', tabId: 't1' }, 'g1')
    expect(placeLast).toHaveBeenCalledTimes(1)
    expect(placeLast).toHaveBeenCalledWith('t1')
    expect(result.current.busy).toBe(false)
  })

  it('a refusal is a notice and the tab is not moved; the gate opens again after it', async () => {
    takeSessionOut.mockResolvedValue({ ok: false, reason: 'no-root' })
    const { result } = renderHook(() => usePersonalActions(target))
    await act(async () => { await result.current.takeOut() })
    expect(result.current.notice).toBeTruthy()
    expect(placeLast).not.toHaveBeenCalled()
    takeSessionOut.mockResolvedValue({ ok: true })
    await act(async () => { await result.current.takeOut() })
    expect(result.current.notice).toBeNull()
    expect(placeLast).toHaveBeenCalledTimes(1)
  })
})
