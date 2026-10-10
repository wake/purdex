// The host's pictures behind thumbnails: one read per (host, path), pinned to the host, 10 MB at most, revoked when let go.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, render, renderHook } from '@testing-library/react'
import { PNG_BYTES, JPEG_BYTES } from './image-fixtures'
import { clearFsImages, FS_IMAGE_MAX_BYTES, useFsImage } from './fs-image'

const mocks = vi.hoisted(() => ({ read: vi.fn(), backend: vi.fn() }))
vi.mock('../fs-backend-daemon', () => ({ createDaemonBackendForHost: (h: string) => { mocks.backend(h); return { read: mocks.read } } }))

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
let revoked: string[] = []
let n = 0
beforeEach(() => {
  revoked = []; n = 0
  URL.createObjectURL = vi.fn(() => `blob:fs/${++n}`)
  URL.revokeObjectURL = vi.fn((u: string) => { revoked.push(u) })
  mocks.read.mockReset().mockResolvedValue(PNG_BYTES)
  mocks.backend.mockReset()
})
afterEach(() => { cleanup(); clearFsImages() })

describe('useFsImage', () => {
  it('reads through the given host only, then is ready with a blob: URL typed by the extension', async () => {
    const blobs: Blob[] = []
    URL.createObjectURL = vi.fn((b: Blob) => { blobs.push(b); return 'blob:fs/x' })
    mocks.read.mockResolvedValue(JPEG_BYTES)
    const { result } = renderHook(() => useFsImage('host-a', '/up/a.JPG'))
    expect(result.current.status).toBe('loading')
    await flush()
    expect(mocks.backend).toHaveBeenCalledTimes(1)
    expect(mocks.backend).toHaveBeenCalledWith('host-a')
    expect(result.current).toEqual({ status: 'ready', url: 'blob:fs/x' })
    expect(blobs[0].type).toBe('image/jpeg')
  })

  it('is idle and reads nothing when disabled', async () => {
    const { result } = renderHook(() => useFsImage('h', '/up/a.png', false))
    await flush()
    expect(result.current.status).toBe('idle')
    expect(mocks.read).not.toHaveBeenCalled()
  })

  it('fails (no throw) when the read fails or the body is over 10 MB', async () => {
    mocks.read.mockRejectedValueOnce(new Error('413'))
    const a = renderHook(() => useFsImage('h', '/up/a.png'))
    mocks.read.mockResolvedValueOnce(Object.assign(new Uint8Array(FS_IMAGE_MAX_BYTES + 1), { 0: 0x89, 1: 0x50, 2: 0x4e, 3: 0x47, 4: 0x0d, 5: 0x0a, 6: 0x1a, 7: 0x0a }))
    const b = renderHook(() => useFsImage('h', '/up/b.png'))
    await flush()
    expect(a.result.current.status).toBe('failed')
    expect(b.result.current.status).toBe('failed')
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })

  it('the same (host, path) is read once; another host is another picture', async () => {
    renderHook(() => useFsImage('h', '/up/a.png'))
    renderHook(() => useFsImage('h', '/up/a.png'))
    renderHook(() => useFsImage('other', '/up/a.png'))
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(2)
  })

  it('keeps unused pictures in a small cache and revokes the oldest beyond it', async () => {
    const views = Array.from({ length: 30 }, (_, i) => render(<Probe path={`/up/${i}.png`} />))
    await flush()
    expect(revoked).toEqual([])
    for (const v of views) v.unmount()
    // 24 stay, the 6 that were let go first are revoked, oldest first
    expect(revoked).toEqual(['blob:fs/1', 'blob:fs/2', 'blob:fs/3', 'blob:fs/4', 'blob:fs/5', 'blob:fs/6'])
    clearFsImages()
    expect(revoked).toHaveLength(30)
  })

  it('a picture in use is never revoked', async () => {
    const keep = render(<Probe path="/up/keep.png" />)
    await flush()
    for (let i = 0; i < 30; i++) render(<Probe path={`/up/${i}.png`} />).unmount()
    expect(revoked).not.toContain('blob:fs/1')
    keep.unmount()
  })
})

function Probe({ path }: { path: string }) {
  useFsImage('h', path)
  return null
}
