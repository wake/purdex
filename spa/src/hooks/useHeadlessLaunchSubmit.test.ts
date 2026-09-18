// spa/src/hooks/useHeadlessLaunchSubmit.test.ts — the submit path of the
// Headless launcher on its own: gate, busy flag, outcome mapping. The form
// around it is covered by HeadlessLauncher.test.tsx.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'

vi.mock('../lib/nex/nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/nex-api')>()),
  delegateExecution: vi.fn(),
}))

import { useHeadlessLaunchSubmit } from './useHeadlessLaunchSubmit'
import { delegateExecution } from '../lib/nex/nex-api'
import { NexApiError, type NexCapabilities } from '../lib/nex/types'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHeadlessLauncherMemoryStore } from '../stores/useHeadlessLauncherMemoryStore'
import { useHostStore } from '../stores/useHostStore'

const H = 'h1'
const delegate = vi.mocked(delegateExecution)
const onSelect = vi.fn()
const invalidateSpy = vi.fn(async () => {})
const caps = { sandbox_profiles: ['standard'], sandbox_default_profile: 'standard', roots: [{ path: '/srv', kind: 'dev' }] } as unknown as NexCapabilities
const input = { brief: 'go', cwd: '/srv/api', root: '/srv', profile: 'standard' }

function seedHost(runtime: Partial<{ status: string; tmuxState: string }> = {}) {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Mini', ip: '127.0.0.1', port: 7860, order: 0 } },
    runtime: { [H]: { status: 'connected', tmuxState: 'ok', ...runtime } as never },
  })
}

const setup = () => renderHook(() => useHeadlessLaunchSubmit(H, caps, onSelect))

beforeEach(() => {
  onSelect.mockReset()
  invalidateSpy.mockClear()
  delegate.mockReset().mockResolvedValue({ id: 'ex-1', state: 'queued' })
  useHeadlessLauncherMemoryStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: {}, invalidate: invalidateSpy })
  seedHost()
})

describe('useHeadlessLaunchSubmit', () => {
  it('sends the request as given, remembers root/profile and selects the execution on success', async () => {
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(delegate).toHaveBeenCalledWith(H, {
      brief: 'go', cwd: '/srv/api', profile: 'standard', labels: { source: 'purdex' }, origin: `purdex://host/${H}/newtab`,
    }, caps)
    expect(useHeadlessLauncherMemoryStore.getState().byHost[H]).toEqual({ root: '/srv', profile: 'standard' })
    expect(onSelect).toHaveBeenCalledWith({ kind: 'execution', executionId: 'ex-1', host: H })
    expect(result.current.error).toBe('')
    expect(result.current.busy).toBe(false)
  })

  it('refuses when the daemon is not connected, with the offline message and no request', async () => {
    seedHost({ status: 'disconnected' })
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(delegate).not.toHaveBeenCalled()
    expect(result.current.error).toContain('offline')
  })

  it('proceeds when the daemon is connected but tmux is unavailable', async () => {
    seedHost({ status: 'connected', tmuxState: 'unavailable' })
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(delegate).toHaveBeenCalledTimes(1)
  })

  it('maps state=rejected to the reject_reason and neither selects nor invalidates', async () => {
    delegate.mockResolvedValue({ id: 'ex-2', state: 'rejected', reject_reason: 'cwd_outside_roots' })
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(result.current.error).toContain('cwd_outside_roots')
    expect(onSelect).not.toHaveBeenCalled()
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('maps a 400 to its code without invalidating', async () => {
    delegate.mockRejectedValue(new NexApiError(400, 'invalid_brief', 'bad'))
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(result.current.error).toContain('invalid_brief')
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it.each([
    ['503 nex_unavailable', new NexApiError(503, 'nex_unavailable', 'engine down'), 'engine down'],
    ['network failure', new NexApiError(0, 'network', 'Failed to fetch'), 'Failed to fetch'],
  ])('%s → unavailable message and the host is invalidated', async (_label, err, text) => {
    delegate.mockRejectedValue(err)
    const { result } = setup()
    await act(() => result.current.submit(input))
    expect(result.current.error).toContain(text)
    expect(invalidateSpy).toHaveBeenCalledWith(H)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('is busy while the delegate is in flight and ignores a second submit meanwhile', async () => {
    let resolve!: (v: { id: string; state: string }) => void
    delegate.mockReturnValue(new Promise((r) => { resolve = r }))
    const { result } = setup()
    let first!: Promise<void>
    act(() => { first = result.current.submit(input) })
    await waitFor(() => expect(result.current.busy).toBe(true))
    await act(() => result.current.submit(input))
    expect(delegate).toHaveBeenCalledTimes(1)
    await act(async () => { resolve({ id: 'ex-1', state: 'queued' }); await first })
    expect(result.current.busy).toBe(false)
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('does not select or write state after unmount', async () => {
    let resolve!: (v: { id: string; state: string }) => void
    delegate.mockReturnValue(new Promise((r) => { resolve = r }))
    const { result, unmount } = setup()
    let p!: Promise<void>
    act(() => { p = result.current.submit(input) })
    unmount()
    resolve({ id: 'ex-1', state: 'queued' })
    await p
    expect(onSelect).not.toHaveBeenCalled()
  })
})
