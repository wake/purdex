import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useNexHostData } from './useNexHostData'
import { useHostStore } from '../../../stores/useHostStore'
import type { NexInfo } from '../../../lib/host-api'

vi.mock('../../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, hostFetch: vi.fn(), fetchInfo: vi.fn() }
})

import { hostFetch, fetchInfo } from '../../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const mockFetchInfo = vi.mocked(fetchInfo)
const HOST_ID = 'h1'

const info = (over: Partial<NexInfo> = {}): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })

function infoResponse(nex: NexInfo): Response {
  return { ok: true, json: () => Promise.resolve({ nex }) } as Response
}

function deferred() {
  let resolve!: (r: Response) => void
  const promise = new Promise<Response>((r) => { resolve = r })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(info())))
  mockHostFetch.mockImplementation(() => Promise.resolve({ ok: true, json: () => Promise.resolve({ nex: { enabled: true } }) } as Response))
})

afterEach(() => {
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} })
})

describe('useNexHostData', () => {
  it('loads info and a normalised config, then reports ready', async () => {
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    expect(result.current.phase).toBe('loading')
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.info?.ready).toBe(true)
    expect(result.current.config?.repo_roots).toEqual([])
  })

  it('reports offline (and nothing else) while the host is not connected', () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    expect(result.current.phase).toBe('offline')
  })

  it('an older Refresh resolving after a newer one does not overwrite it', async () => {
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    const older = deferred()
    const newer = deferred()
    mockFetchInfo.mockImplementationOnce(() => older.promise).mockImplementationOnce(() => newer.promise)
    act(() => result.current.refresh())
    act(() => result.current.refresh())

    await act(async () => {
      newer.resolve(infoResponse(info({ init_error: 'newer' })))
      await newer.promise
    })
    await waitFor(() => expect(result.current.info?.init_error).toBe('newer'))

    await act(async () => {
      older.resolve(infoResponse(info({ init_error: 'older' })))
      await older.promise
    })
    expect(result.current.info?.init_error).toBe('newer')
  })

  it('a Refresh that fails after a newer successful one does not raise refreshError', async () => {
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    let rejectOlder!: (e: Error) => void
    mockFetchInfo
      .mockImplementationOnce(() => new Promise<Response>((_r, rej) => { rejectOlder = rej }))
      .mockImplementationOnce(() => Promise.resolve(infoResponse(info())))
    act(() => result.current.refresh())
    act(() => result.current.refresh())
    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(3))

    await act(async () => {
      rejectOlder(new Error('late failure'))
      await Promise.resolve()
    })
    expect(result.current.refreshError).toBe(false)
  })
})
