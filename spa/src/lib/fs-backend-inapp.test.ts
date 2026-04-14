import { describe, it, expect, beforeEach } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'

describe('InAppBackend', () => {
  let backend: InAppBackend

  beforeEach(() => {
    backend = new InAppBackend()
  })

  it('reports as available', () => {
    expect(backend.available()).toBe(true)
  })

  it('writes and reads a file', async () => {
    const content = new TextEncoder().encode('hello world')
    await backend.write('/test.txt', content)
    const result = await backend.read('/test.txt')
    expect(new TextDecoder().decode(result)).toBe('hello world')
  })

  it('stat returns file info after write', async () => {
    const content = new TextEncoder().encode('abc')
    await backend.write('/stat-test.txt', content)
    const stat = await backend.stat('/stat-test.txt')
    expect(stat.isFile).toBe(true)
    expect(stat.isDirectory).toBe(false)
    expect(stat.size).toBe(3)
    expect(stat.mtime).toBeGreaterThan(0)
  })

  it('stat throws for nonexistent path', async () => {
    await expect(backend.stat('/no-such-file')).rejects.toThrow()
  })

  it('list returns entries in a directory', async () => {
    await backend.write('/dir/a.txt', new TextEncoder().encode('a'))
    await backend.write('/dir/b.txt', new TextEncoder().encode('b'))
    const entries = await backend.list('/dir')
    const names = entries.map((e) => e.name).sort()
    expect(names).toEqual(['a.txt', 'b.txt'])
  })

  it('mkdir creates a directory entry', async () => {
    await backend.mkdir('/newdir')
    const stat = await backend.stat('/newdir')
    expect(stat.isDirectory).toBe(true)
  })

  it('delete removes a file', async () => {
    await backend.write('/del.txt', new TextEncoder().encode('x'))
    await backend.delete('/del.txt')
    await expect(backend.stat('/del.txt')).rejects.toThrow()
  })

  it('rename moves a file', async () => {
    const content = new TextEncoder().encode('move me')
    await backend.write('/old.txt', content)
    await backend.rename('/old.txt', '/new.txt')
    const result = await backend.read('/new.txt')
    expect(new TextDecoder().decode(result)).toBe('move me')
    await expect(backend.stat('/old.txt')).rejects.toThrow()
  })
})
