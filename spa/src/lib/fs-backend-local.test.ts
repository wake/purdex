import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { LocalBackend } from './fs-backend-local'

const mockFs = {
  read: vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3])),
  write: vi.fn().mockResolvedValue(undefined),
  stat: vi.fn().mockResolvedValue({ size: 100, mtime: 1000, isDirectory: false, isFile: true }),
  list: vi.fn().mockResolvedValue([{ name: 'test.txt', isDir: false, size: 100 }]),
  mkdir: vi.fn().mockResolvedValue(undefined),
  delete: vi.fn().mockResolvedValue(undefined),
  rename: vi.fn().mockResolvedValue(undefined),
}

describe('LocalBackend', () => {
  let backend: LocalBackend

  beforeEach(() => {
    vi.restoreAllMocks()
    ;(window as any).electronAPI = { fs: mockFs }
    backend = new LocalBackend()
  })

  afterEach(() => {
    delete (window as any).electronAPI
  })

  it('available() returns false when no electronAPI', () => {
    delete (window as any).electronAPI
    expect(backend.available()).toBe(false)
  })

  it('available() returns true when electronAPI.fs exists', () => {
    expect(backend.available()).toBe(true)
  })

  it('read() delegates to api.read()', async () => {
    const result = await backend.read('/test.txt')
    expect(mockFs.read).toHaveBeenCalledWith('/test.txt')
    expect(result).toEqual(new Uint8Array([1, 2, 3]))
  })

  it('write() delegates to api.write()', async () => {
    const content = new Uint8Array([4, 5, 6])
    await backend.write('/out.txt', content)
    expect(mockFs.write).toHaveBeenCalledWith('/out.txt', content)
  })

  it('stat() delegates to api.stat()', async () => {
    const result = await backend.stat('/test.txt')
    expect(mockFs.stat).toHaveBeenCalledWith('/test.txt')
    expect(result).toEqual({ size: 100, mtime: 1000, isDirectory: false, isFile: true })
  })

  it('list() delegates to api.list()', async () => {
    const result = await backend.list('/dir')
    expect(mockFs.list).toHaveBeenCalledWith('/dir')
    expect(result).toEqual([{ name: 'test.txt', isDir: false, size: 100 }])
  })

  it('mkdir() defaults recursive to false', async () => {
    await backend.mkdir('/newdir')
    expect(mockFs.mkdir).toHaveBeenCalledWith('/newdir', false)
  })

  it('mkdir() passes recursive when provided', async () => {
    await backend.mkdir('/newdir', true)
    expect(mockFs.mkdir).toHaveBeenCalledWith('/newdir', true)
  })

  it('delete() defaults recursive to false', async () => {
    await backend.delete('/file.txt')
    expect(mockFs.delete).toHaveBeenCalledWith('/file.txt', false)
  })

  it('delete() passes recursive when provided', async () => {
    await backend.delete('/dir', true)
    expect(mockFs.delete).toHaveBeenCalledWith('/dir', true)
  })

  it('rename() delegates to api.rename()', async () => {
    await backend.rename('/old.txt', '/new.txt')
    expect(mockFs.rename).toHaveBeenCalledWith('/old.txt', '/new.txt')
  })

  it('throws when api not available', async () => {
    delete (window as any).electronAPI
    await expect(backend.read('/test.txt')).rejects.toThrow('Local filesystem not available (requires Electron)')
  })
})
