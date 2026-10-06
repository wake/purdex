import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as api from './handoff-api'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { exitWorker, exitErrorMessage } from './exit-worker'
import { HandoffApiError } from './handoff-api'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('./handoff-api', async (orig) => ({ ...(await orig<typeof import('./handoff-api')>()), nexExitWorker: vi.fn() }))

describe('exitWorker', () => {
  beforeEach(() => {
    vi.mocked(api.nexExitWorker).mockReset()
    vi.spyOn(useExecutionListStore.getState(), 'refetch').mockImplementation(() => {})
  })

  it('posts the lease, forgets it and refetches the list', async () => {
    vi.mocked(api.nexExitWorker).mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
    const forgetLease = vi.fn()
    const r = await exitWorker({ hostId: 'h1', executionId: 'E1', leaseId: 'L1', forgetLease })
    expect(api.nexExitWorker).toHaveBeenCalledWith('h1', 'E1', { lease_id: 'L1' })
    expect(r.exited).toBe(true)
    expect(forgetLease).toHaveBeenCalled()
    expect(useExecutionListStore.getState().refetch).toHaveBeenCalledWith('h1')
  })

  it('collapses a double click into one request', async () => {
    let resolve!: (v: api.NexExitWorkerResult) => void
    vi.mocked(api.nexExitWorker).mockReturnValue(new Promise((r) => { resolve = r }))
    const a = exitWorker({ hostId: 'h1', executionId: 'E1' })
    const b = exitWorker({ hostId: 'h1', executionId: 'E1' })
    resolve({ exited: true, terminated: false, archived: true, state: 'failed' })
    await Promise.all([a, b])
    expect(api.nexExitWorker).toHaveBeenCalledTimes(1)
  })

  it('keeps the lease when the exit is refused', async () => {
    vi.mocked(api.nexExitWorker).mockRejectedValue(new HandoffApiError(409, 'held_by', { principal: 'x' }))
    const forgetLease = vi.fn()
    await expect(exitWorker({ hostId: 'h1', executionId: 'E2', forgetLease })).rejects.toBeInstanceOf(HandoffApiError)
    expect(forgetLease).not.toHaveBeenCalled()
  })

  it('names the holder on held_by', () => {
    const t = useI18nStore.getState().t
    const msg = exitErrorMessage(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }), t)
    expect(msg).toContain('ploom:agent-7')
  })

  it('falls back to failed with the reason for other errors', () => {
    const t = useI18nStore.getState().t
    expect(exitErrorMessage(new HandoffApiError(500, 'archive_failed', {}, 'boom'), t)).toContain('boom')
    expect(exitErrorMessage(new HandoffApiError(500, 'archive_failed', {}, ''), t)).toContain('archive_failed')
    expect(exitErrorMessage('weird', t)).toContain('weird')
  })
})
