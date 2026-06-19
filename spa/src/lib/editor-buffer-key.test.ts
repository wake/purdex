// spa/src/lib/editor-buffer-key.test.ts
import { describe, it, expect } from 'vitest'
import { bufferKey } from './editor-buffer-key'

describe('bufferKey', () => {
  it('encodes daemon source with hostId', () => {
    expect(bufferKey({ type: 'daemon', hostId: 'h1' }, '/a.md')).toBe('daemon:h1:/a.md')
  })

  it('encodes inapp source', () => {
    expect(bufferKey({ type: 'inapp' }, '/a.md')).toBe('inapp:/a.md')
  })

  it('encodes local source', () => {
    expect(bufferKey({ type: 'local' }, '/a.md')).toBe('local:/a.md')
  })

  it('produces distinct keys for different hostId with same path', () => {
    const a = bufferKey({ type: 'daemon', hostId: 'h1' }, '/a.md')
    const b = bufferKey({ type: 'daemon', hostId: 'h2' }, '/a.md')
    expect(a).not.toBe(b)
  })
})
