import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { DaemonBackend } from './fs-backend-daemon'

describe('DaemonBackend', () => {
  let backend: DaemonBackend

  beforeEach(() => {
    backend = new DaemonBackend('http://localhost:7860', () => ({ Authorization: 'Bearer test-token' }))
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('available() returns true when baseUrl is set', () => {
    expect(backend.available()).toBe(true)
  })

  it('available() returns false when baseUrl is empty', () => {
    const emptyBackend = new DaemonBackend('', () => ({}))
    expect(emptyBackend.available()).toBe(false)
  })

  it('list sends POST to /api/fs/list and returns entries', async () => {
    const mockEntries = [
      { name: 'file.txt', isDir: false, size: 42 },
      { name: 'subdir', isDir: true, size: 0 },
    ]
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    })

    const result = await backend.list('/home/user')

    expect(global.fetch).toHaveBeenCalledOnce()
    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/list')
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user' })
    expect(options.headers).toMatchObject({ 'Content-Type': 'application/json', Authorization: 'Bearer test-token' })
    expect(result).toEqual(mockEntries)
  })

  it('read returns Uint8Array from response arrayBuffer', async () => {
    const bytes = new Uint8Array([72, 101, 108, 108, 111]) // "Hello"
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      arrayBuffer: () => Promise.resolve(bytes.buffer),
    })

    const result = await backend.read('/home/user/file.txt')

    expect(global.fetch).toHaveBeenCalledOnce()
    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/read')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt' })
    expect(result).toBeInstanceOf(Uint8Array)
    expect(new TextDecoder().decode(result)).toBe('Hello')
  })

  it('stat returns FileStat from JSON response', async () => {
    const mockStat = { size: 1024, mtime: 1712345678000, isDirectory: false, isFile: true }
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve(mockStat),
    })

    const result = await backend.stat('/home/user/file.txt')

    expect(global.fetch).toHaveBeenCalledOnce()
    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/stat')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt' })
    expect(result).toEqual(mockStat)
  })

  it('write sends base64 encoded content', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    const content = new TextEncoder().encode('Hello, World!')
    await backend.write('/home/user/file.txt', content)

    expect(global.fetch).toHaveBeenCalledOnce()
    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/write')
    const body = JSON.parse(options.body)
    expect(body.path).toBe('/home/user/file.txt')
    // Verify content is base64 encoded
    expect(typeof body.content).toBe('string')
    const decoded = atob(body.content)
    const decodedBytes = new Uint8Array(decoded.length)
    for (let i = 0; i < decoded.length; i++) {
      decodedBytes[i] = decoded.charCodeAt(i)
    }
    expect(new TextDecoder().decode(decodedBytes)).toBe('Hello, World!')
  })

  it('mkdir sends POST to /api/fs/mkdir', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.mkdir('/home/user/newdir', true)

    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/mkdir')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/newdir', recursive: true })
  })

  it('delete sends POST to /api/fs/delete', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.delete('/home/user/file.txt', false)

    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/delete')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt', recursive: false })
  })

  it('rename sends POST to /api/fs/rename', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.rename('/home/user/old.txt', '/home/user/new.txt')

    const [url, options] = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/rename')
    expect(JSON.parse(options.body)).toEqual({ from: '/home/user/old.txt', to: '/home/user/new.txt' })
  })

  it('throws on non-ok response', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: () => Promise.resolve('file not found'),
    })

    await expect(backend.read('/nonexistent.txt')).rejects.toThrow('file not found')
  })

  it('throws with HTTP status when text() fails', async () => {
    ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: () => Promise.reject(new Error('network error')),
    })

    await expect(backend.stat('/some/path')).rejects.toThrow('HTTP 500')
  })
})
