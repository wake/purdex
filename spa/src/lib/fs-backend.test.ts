import { describe, it, expect, beforeEach } from 'vitest'
import { registerFsBackend, getFsBackend, clearFsBackendRegistry } from './fs-backend'
import type { FsBackend } from './fs-backend'
import type { FileSource } from '../types/fs'

function createMockBackend(id: string): FsBackend {
  return {
    id,
    label: `Mock ${id}`,
    available: () => true,
    read: async () => new Uint8Array(),
    write: async () => {},
    stat: async () => ({ size: 0, mtime: 0, isDirectory: false, isFile: true }),
    list: async () => [],
    mkdir: async () => {},
    delete: async () => {},
    rename: async () => {},
  }
}

describe('FsBackend registry', () => {
  beforeEach(() => clearFsBackendRegistry())

  it('registers and retrieves a backend by source type', () => {
    const backend = createMockBackend('inapp')
    registerFsBackend('inapp', backend)
    const source: FileSource = { type: 'inapp' }
    expect(getFsBackend(source)).toBe(backend)
  })

  it('retrieves daemon backend with hostId', () => {
    const backend = createMockBackend('daemon')
    registerFsBackend('daemon', backend)
    const source: FileSource = { type: 'daemon', hostId: 'host1' }
    expect(getFsBackend(source)).toBe(backend)
  })

  it('returns undefined for unregistered source type', () => {
    const source: FileSource = { type: 'local' }
    expect(getFsBackend(source)).toBeUndefined()
  })
})
