import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  registerFsBackend,
  registerFsBackendResolver,
  getFsBackend,
  clearFsBackendRegistry,
} from './fs-backend'
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

describe('FsBackend resolver layer', () => {
  beforeEach(() => clearFsBackendRegistry())

  it('prefers a registered resolver over the flat registry and passes it the full source', () => {
    const flat = createMockBackend('daemon-flat')
    const hostBound = createMockBackend('daemon-hostB')
    registerFsBackend('daemon', flat)

    const resolver = vi.fn((_source: FileSource): FsBackend | undefined => hostBound)
    registerFsBackendResolver('daemon', resolver)

    const source: FileSource = { type: 'daemon', hostId: 'hostB' }
    expect(getFsBackend(source)).toBe(hostBound)
    expect(resolver).toHaveBeenCalledWith(source)
  })

  it('falls back to the flat registry when the resolver returns undefined', () => {
    const flat = createMockBackend('daemon-flat')
    registerFsBackend('daemon', flat)
    registerFsBackendResolver('daemon', () => undefined)

    expect(getFsBackend({ type: 'daemon', hostId: '' })).toBe(flat)
  })

  it('leaves types without a resolver on the flat registry', () => {
    const inapp = createMockBackend('inapp')
    const local = createMockBackend('local')
    registerFsBackend('inapp', inapp)
    registerFsBackend('local', local)
    registerFsBackendResolver('daemon', () => createMockBackend('daemon-hostB'))

    expect(getFsBackend({ type: 'inapp' })).toBe(inapp)
    expect(getFsBackend({ type: 'local' })).toBe(local)
  })

  it('returns undefined when neither a resolver nor a flat backend produces a backend', () => {
    registerFsBackendResolver('daemon', () => undefined)
    expect(getFsBackend({ type: 'daemon', hostId: 'hostB' })).toBeUndefined()
  })

  it('clearFsBackendRegistry() also drops resolvers so they do not leak between suites', () => {
    const hostBound = createMockBackend('daemon-hostB')
    registerFsBackendResolver('daemon', () => hostBound)
    expect(getFsBackend({ type: 'daemon', hostId: 'hostB' })).toBe(hostBound)

    clearFsBackendRegistry()

    const flat = createMockBackend('daemon-flat')
    registerFsBackend('daemon', flat)
    expect(getFsBackend({ type: 'daemon', hostId: 'hostB' })).toBe(flat)
  })
})
