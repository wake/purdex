// spa/src/hooks/useWorkerUploads.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useWorkerUploads } from './useWorkerUploads'
import * as api from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'

vi.mock('../lib/nex/nex-api', () => ({ uploadWorkerFile: vi.fn() }))

type Saved = { path: string; name: string; size: number }
function deferred() {
  let resolve!: (v: Saved) => void
  let reject!: (e: unknown) => void
  const p = new Promise<Saved>((res, rej) => { resolve = res; reject = rej })
  return { p, resolve, reject }
}
const txt = (name: string) => new File(['x'], name, { type: 'text/plain' })
const png = (name: string) => new File(['x'], name, { type: 'image/png' })

let createUrl: ReturnType<typeof vi.fn>, revokeUrl: ReturnType<typeof vi.fn>
beforeEach(() => {
  vi.mocked(api.uploadWorkerFile).mockReset()
  let n = 0
  createUrl = vi.fn(() => `blob:${++n}`)
  revokeUrl = vi.fn()
  Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revokeUrl })
})
afterEach(() => vi.restoreAllMocks())

describe('useWorkerUploads', () => {
  it('adds an uploading chip per file and uploads them one at a time, in order', async () => {
    const a = deferred(), b = deferred()
    vi.mocked(api.uploadWorkerFile).mockReturnValueOnce(a.p).mockReturnValueOnce(b.p)
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([txt('a.txt'), txt('b.txt')]))
    expect(result.current.chips.map((c) => [c.name, c.status])).toEqual([['a.txt', 'uploading'], ['b.txt', 'uploading']])
    await vi.waitFor(() => expect(api.uploadWorkerFile).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.uploadWorkerFile).mock.calls[0][0]).toBe('h')
    expect(vi.mocked(api.uploadWorkerFile).mock.calls[0][1]).toBe('exc_1')
    expect(vi.mocked(api.uploadWorkerFile).mock.calls[0][2].name).toBe('a.txt')
    await act(async () => { a.resolve({ path: '/w/a.txt', name: 'a.txt', size: 1 }) })
    await vi.waitFor(() => expect(api.uploadWorkerFile).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.uploadWorkerFile).mock.calls[1][2].name).toBe('b.txt')
    expect(result.current.chips[0]).toMatchObject({ status: 'done', path: '/w/a.txt' })
    await act(async () => { b.reject(new NexApiError(413, 'file_too_large', 'big')) })
    expect(result.current.chips[1]).toMatchObject({ status: 'failed', error: 'file_too_large' })
  })

  it('an unstructured rejection fails the chip with no code', async () => {
    vi.mocked(api.uploadWorkerFile).mockRejectedValueOnce(new Error('boom'))
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([txt('a.txt')]))
    await vi.waitFor(() => expect(result.current.chips[0].status).toBe('failed'))
    expect(result.current.chips[0].error).toBeUndefined()
  })

  it('an image chip gets a thumbnail URL, revoked on remove', async () => {
    vi.mocked(api.uploadWorkerFile).mockResolvedValue({ path: '/w/p.png', name: 'p.png', size: 1 })
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([png('p.png'), txt('a.txt')]))
    expect(result.current.chips[0].previewUrl).toBe('blob:1')
    expect(result.current.chips[1].previewUrl).toBeUndefined()
    act(() => result.current.remove(result.current.chips[0].key))
    expect(revokeUrl).toHaveBeenCalledWith('blob:1')
    expect(result.current.chips.map((c) => c.name)).toEqual(['a.txt'])
  })

  it('a chip removed mid-upload stays removed when its upload settles', async () => {
    const a = deferred()
    vi.mocked(api.uploadWorkerFile).mockReturnValueOnce(a.p)
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([txt('a.txt')]))
    act(() => result.current.remove(result.current.chips[0].key))
    await act(async () => { a.resolve({ path: '/w/a.txt', name: 'a.txt', size: 1 }) })
    expect(result.current.chips).toEqual([])
  })

  it('clear(keys) removes only those chips; clear() removes all and revokes thumbnails', async () => {
    vi.mocked(api.uploadWorkerFile).mockResolvedValue({ path: '/w/x', name: 'x', size: 1 })
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([png('a.png'), txt('b.txt')]))
    const [first] = result.current.chips
    act(() => result.current.clear([first.key]))
    expect(result.current.chips.map((c) => c.name)).toEqual(['b.txt'])
    expect(revokeUrl).toHaveBeenCalledWith('blob:1')
    act(() => result.current.clear())
    expect(result.current.chips).toEqual([])
  })

  it('revokes remaining thumbnails on unmount', () => {
    vi.mocked(api.uploadWorkerFile).mockReturnValue(new Promise(() => {}))
    const { result, unmount } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([png('a.png')]))
    unmount()
    expect(revokeUrl).toHaveBeenCalledWith('blob:1')
  })
})
