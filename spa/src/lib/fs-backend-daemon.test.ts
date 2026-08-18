import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { DaemonBackend, createDaemonBackendForHost } from './fs-backend-daemon'
import { useHostStore } from '../stores/useHostStore'

describe('DaemonBackend', () => {
  let backend: DaemonBackend
  const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

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
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    })

    const result = await backend.list('/home/user')

    expect(testGlobal.fetch).toHaveBeenCalledOnce()
    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/list')
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user' })
    expect(options.headers).toMatchObject({ 'Content-Type': 'application/json', Authorization: 'Bearer test-token' })
    expect(result).toEqual(mockEntries)
  })

  it('read returns Uint8Array from response arrayBuffer', async () => {
    const bytes = new Uint8Array([72, 101, 108, 108, 111]) // "Hello"
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      arrayBuffer: () => Promise.resolve(bytes.buffer),
    })

    const result = await backend.read('/home/user/file.txt')

    expect(testGlobal.fetch).toHaveBeenCalledOnce()
    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/read')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt' })
    expect(result).toBeInstanceOf(Uint8Array)
    expect(new TextDecoder().decode(result)).toBe('Hello')
  })

  it('stat returns FileStat from JSON response', async () => {
    const mockStat = { size: 1024, mtime: 1712345678000, isDirectory: false, isFile: true }
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve(mockStat),
    })

    const result = await backend.stat('/home/user/file.txt')

    expect(testGlobal.fetch).toHaveBeenCalledOnce()
    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/stat')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt' })
    expect(result).toEqual(mockStat)
  })

  it('write sends base64 encoded content', async () => {
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    const content = new TextEncoder().encode('Hello, World!')
    await backend.write('/home/user/file.txt', content)

    expect(testGlobal.fetch).toHaveBeenCalledOnce()
    const [url, options] = testGlobal.fetch.mock.calls[0]
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
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.mkdir('/home/user/newdir', true)

    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/mkdir')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/newdir', recursive: true })
  })

  it('delete sends POST to /api/fs/delete', async () => {
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.delete('/home/user/file.txt', false)

    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/delete')
    expect(JSON.parse(options.body)).toEqual({ path: '/home/user/file.txt', recursive: false })
  })

  it('rename sends POST to /api/fs/rename', async () => {
    testGlobal.fetch.mockResolvedValueOnce({
      ok: true,
      status: 204,
    })

    await backend.rename('/home/user/old.txt', '/home/user/new.txt')

    const [url, options] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://localhost:7860/api/fs/rename')
    expect(JSON.parse(options.body)).toEqual({ from: '/home/user/old.txt', to: '/home/user/new.txt' })
  })

  it('throws on non-ok response', async () => {
    testGlobal.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: () => Promise.resolve('file not found'),
    })

    await expect(backend.read('/nonexistent.txt')).rejects.toThrow('file not found')
  })

  it('throws with HTTP status when text() fails', async () => {
    testGlobal.fetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: () => Promise.reject(new Error('network error')),
    })

    await expect(backend.stat('/some/path')).rejects.toThrow('HTTP 500')
  })

  it('attaches response status to thrown error so isNotFoundError can detect 404', async () => {
    // P5 codex R1 P1: missing-file popup pipeline relied on `status` on the
    // thrown error. Without this, daemon 404 stat errors were classified as
    // "auth/network" and re-thrown instead of triggering the popup flow.
    testGlobal.fetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: () => Promise.resolve('not found'),
    })
    await expect(backend.stat('/nope')).rejects.toMatchObject({ status: 404 })
  })
})

describe('createDaemonBackendForHost', () => {
  const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

  beforeEach(() => {
    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, order: 0 },
      },
      hostOrder: ['hostA'],
      activeHostId: 'hostA',
    })
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useHostStore.getState().reset()
  })

  it('addresses its own host', async () => {
    testGlobal.fetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ size: 0 }) })

    await createDaemonBackendForHost('hostA').stat('/p/a.md')

    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://10.0.0.1:7860/api/fs/stat')
  })

  it('rejects instead of falling back to the active host once its host is gone', async () => {
    // `getDaemonBase` answers an unknown host with the ACTIVE host's base, so a
    // backend bound to a removed host used to read — and write — the same path
    // on a different machine, letting hostA vouch for hostB's paths.
    const backend = createDaemonBackendForHost('hostB')

    expect(backend.available()).toBe(false)
    await expect(backend.stat('/p/a.md')).rejects.toThrow(/hostB/)
    await expect(backend.read('/p/a.md')).rejects.toThrow(/hostB/)
    await expect(backend.write('/p/a.md', new Uint8Array())).rejects.toThrow(/hostB/)
    await expect(backend.list('/p')).rejects.toThrow(/hostB/)
    await expect(backend.mkdir('/p/dir')).rejects.toThrow(/hostB/)
    await expect(backend.delete('/p/a.md')).rejects.toThrow(/hostB/)
    await expect(backend.rename('/p/a.md', '/p/b.md')).rejects.toThrow(/hostB/)
    expect(testGlobal.fetch).not.toHaveBeenCalled()
  })

  it('rejects (never throws synchronously) so existing async call sites still catch it', () => {
    const result = createDaemonBackendForHost('hostB').stat('/p/a.md')
    expect(result).toBeInstanceOf(Promise)
    return expect(result).rejects.toThrow()
  })

  it('is not classified as "file not found" — a gone host must surface as itself', async () => {
    const error = await createDaemonBackendForHost('hostB').stat('/p/a.md').catch((e: unknown) => e)
    expect((error as { code?: string; status?: number }).code).toBeUndefined()
    expect((error as { code?: string; status?: number }).status).toBeUndefined()
  })
})
