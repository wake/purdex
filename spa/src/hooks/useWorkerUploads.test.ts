// spa/src/hooks/useWorkerUploads.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useWorkerUploads } from './useWorkerUploads'
import * as api from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import { requestBytes } from '../lib/nex/worker-upload'

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

  it('isLive tracks add, remove and clear (a send checks it after encoding, PR #1527 A1)', async () => {
    vi.mocked(api.uploadWorkerFile).mockResolvedValue({ path: '/w/x', name: 'x', size: 1 })
    const { result } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([txt('a.txt'), txt('b.txt'), txt('c.txt')]))
    const [a, b, c] = result.current.chips.map((x) => x.key)
    expect([a, b, c].map(result.current.isLive)).toEqual([true, true, true])
    act(() => result.current.remove(a))
    act(() => result.current.clear([b]))
    expect([a, b, c].map(result.current.isLive)).toEqual([false, false, true])
    act(() => result.current.clear())
    expect(result.current.isLive(c)).toBe(false)
    expect(result.current.isLive('nope')).toBe(false)
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

  describe('native images (phase E)', () => {
    const MiB = 1024 * 1024
    const caps = (over: Record<string, unknown> = {}) => ({
      media_types: ['image/png', 'image/jpeg'], max_bytes: 5 * MiB, max_count: 10, max_total_bytes: 20 * MiB,
      providers: ['claude'], fetch: { method: 'GET', path: '/x/{id}/{sha256}' }, maxRequestBytes: 32 * MiB, ...over,
    })
    const sized = (name: string, size: number, type = 'image/png') => {
      const f = new File(['x'], name, { type })
      Object.defineProperty(f, 'size', { value: size })
      return f
    }
    beforeEach(() => {
      vi.mocked(api.uploadWorkerFile).mockImplementation(async (_h, _e, f) => ({ path: `/w/${f.name}`, name: f.name, size: 1 }))
    })

    it('null caps (an older daemon): an image uploads by path exactly as in phase D, with no note (Review Focus 1)', async () => {
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: null, getText: () => '' }))
      act(() => result.current.add([png('p.png')]))
      await vi.waitFor(() => expect(result.current.chips[0].status).toBe('done'))
      expect(result.current.chips[0]).toMatchObject({ kind: 'path', path: '/w/p.png' })
      expect(result.current.chips[0].note).toBeUndefined()
      expect(api.uploadWorkerFile).toHaveBeenCalledTimes(1)
      expect(result.current.nativeFiles()).toEqual([])
    })

    it('a fitting image stays local (done, no path, never uploaded); other files still upload', async () => {
      const p = png('p.png'), t = txt('a.txt')
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps(), getText: () => '' }))
      act(() => result.current.add([p, t]))
      expect(result.current.chips[0]).toMatchObject({ kind: 'image', status: 'done', previewUrl: 'blob:1' })
      expect(result.current.chips[0].path).toBeUndefined()
      await vi.waitFor(() => expect(result.current.chips[1].status).toBe('done'))
      expect(vi.mocked(api.uploadWorkerFile).mock.calls.map((c) => c[2].name)).toEqual(['a.txt'])
      expect(result.current.nativeFiles()).toEqual([{ key: result.current.chips[0].key, file: p }])
    })

    it('six 4 MiB images: five stay native, the sixth uploads by path with a note (Review Focus 3)', async () => {
      const files = [1, 2, 3, 4, 5, 6].map((n) => sized(`${n}.png`, 4 * MiB))
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps({ maxRequestBytes: 64 * MiB }), getText: () => '' }))
      act(() => result.current.add(files.slice(0, 5)))
      act(() => result.current.add(files.slice(5)))
      await vi.waitFor(() => expect(result.current.chips[5].status).toBe('done'))
      expect(result.current.chips.map((c) => c.kind)).toEqual(['image', 'image', 'image', 'image', 'image', 'path'])
      expect(result.current.chips[5]).toMatchObject({ note: 'image_as_path', path: '/w/6.png' })
      expect(vi.mocked(api.uploadWorkerFile).mock.calls.map((c) => c[2].name)).toEqual(['6.png'])
    })

    it('plans with the current draft text', () => {
      const limit = requestBytes('', [{ size: 30, type: 'image/png' }])
      let text = ''
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps({ maxRequestBytes: limit }), getText: () => text }))
      act(() => result.current.add([sized('a.png', 30)]))
      expect(result.current.chips[0]).toMatchObject({ kind: 'image' })
      act(() => result.current.remove(result.current.chips[0].key))
      text = 'a longer draft'
      act(() => result.current.add([sized('b.png', 30)]))
      expect(result.current.chips[0]).toMatchObject({ kind: 'path', note: 'image_as_path' })
    })

    it('an unsupported image type goes by path with the note', async () => {
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps(), getText: () => '' }))
      act(() => result.current.add([new File(['x'], 'b.bmp', { type: 'image/bmp' })]))
      expect(result.current.chips[0]).toMatchObject({ kind: 'path', note: 'image_as_path', status: 'uploading' })
    })

    it('demote turns a native chip into a path upload with the note; markFailed fails a chip', async () => {
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps(), getText: () => '' }))
      act(() => result.current.add([png('a.png'), png('b.png')]))
      const [a, b] = result.current.chips
      act(() => result.current.demote([a.key]))
      await vi.waitFor(() => expect(result.current.chips[0]).toMatchObject({ kind: 'path', status: 'done', path: '/w/a.png', note: 'image_as_path' }))
      expect(result.current.nativeFiles().map((n) => n.key)).toEqual([b.key])
      act(() => result.current.markFailed(b.key, 'attachment_too_large'))
      expect(result.current.chips[1]).toMatchObject({ status: 'failed', error: 'attachment_too_large' })
    })

    it('a native chip failed by markFailed is never re-planned or re-uploaded by a later add', async () => {
      let text = ''
      const limit = requestBytes('', [{ size: 30, type: 'image/png' }])
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps({ maxRequestBytes: limit }), getText: () => text }))
      act(() => result.current.add([sized('a.png', 30)]))
      const a = result.current.chips[0]
      expect(a).toMatchObject({ kind: 'image' })
      act(() => result.current.markFailed(a.key, 'attachment_too_large'))
      expect(result.current.nativeFiles()).toEqual([])
      // A longer draft would push `a` to path on a re-plan, and a demote uploads it.
      text = 'a longer draft'
      act(() => result.current.add([txt('b.txt')]))
      await vi.waitFor(() => expect(result.current.chips[1].status).toBe('done'))
      expect(vi.mocked(api.uploadWorkerFile).mock.calls.map((c) => c[2].name)).toEqual(['b.txt'])
      expect(result.current.chips[0]).toMatchObject({ kind: 'image', status: 'failed', error: 'attachment_too_large' })
    })

    it('a failed native chip does not take a slot from a new image', () => {
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps({ max_count: 1 }), getText: () => '' }))
      act(() => result.current.add([png('a.png')]))
      act(() => result.current.markFailed(result.current.chips[0].key))
      act(() => result.current.add([png('b.png')]))
      expect(result.current.chips[1]).toMatchObject({ kind: 'image', status: 'done' })
      expect(api.uploadWorkerFile).not.toHaveBeenCalled()
    })

    it('removing a native chip forgets its file', () => {
      const { result } = renderHook(() => useWorkerUploads('h', 'exc_1', { caps: caps(), getText: () => '' }))
      act(() => result.current.add([png('a.png')]))
      act(() => result.current.remove(result.current.chips[0].key))
      expect(result.current.nativeFiles()).toEqual([])
    })
  })

  it('revokes remaining thumbnails on unmount', () => {
    vi.mocked(api.uploadWorkerFile).mockReturnValue(new Promise(() => {}))
    const { result, unmount } = renderHook(() => useWorkerUploads('h', 'exc_1'))
    act(() => result.current.add([png('a.png')]))
    unmount()
    expect(revokeUrl).toHaveBeenCalledWith('blob:1')
  })
})
