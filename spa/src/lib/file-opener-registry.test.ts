import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerFileOpener,
  getDefaultOpener,
  getFileOpeners,
  clearFileOpenerRegistry,
  unregisterByOwner,
  clearAllForHmr,
  getRegisteredOpeners,
} from './file-opener-registry'
import type { FileOpener } from './file-opener-registry'
import type { FileInfo } from '../types/fs'

const textFile: FileInfo = { name: 'test.ts', path: '/test.ts', extension: 'ts', size: 100, isDirectory: false }
const imageFile: FileInfo = { name: 'logo.png', path: '/logo.png', extension: 'png', size: 5000, isDirectory: false }

const mkOpener = (id: string, ownerModuleId: string, overrides: Partial<FileOpener> = {}): FileOpener & { ownerModuleId: string } => ({
  id,
  label: id,
  icon: 'File',
  match: () => true,
  priority: 'default',
  createContent: () => ({ kind: 'editor' } as never),
  ownerModuleId,
  ...overrides,
})

describe('file-opener-registry', () => {
  beforeEach(() => clearAllForHmr())

  it('returns null when no opener matches', () => {
    expect(getDefaultOpener(textFile)).toBeNull()
  })

  it('returns registered default opener', () => {
    const opener = mkOpener('text-editor', 'editor', {
      label: 'Text Editor',
      createContent: (_source, file) => ({ kind: 'editor', source: { type: 'inapp' }, filePath: file.path } as never),
    })
    registerFileOpener(opener)
    expect(getDefaultOpener(textFile)?.id).toBe('text-editor')
  })

  it('prefers default over option priority', () => {
    const option = mkOpener('option-opener', 'editor', { priority: 'option' })
    const def = mkOpener('default-opener', 'editor')
    registerFileOpener(option)
    registerFileOpener(def)
    expect(getDefaultOpener(textFile)?.id).toBe('default-opener')
  })

  it('returns only matching openers', () => {
    const textOpener = mkOpener('text', 'editor', {
      match: (f) => !['png', 'jpg'].includes(f.extension),
    })
    const imageOpener = mkOpener('image', 'editor', {
      icon: 'Image',
      match: (f) => ['png', 'jpg'].includes(f.extension),
      createContent: () => ({ kind: 'image-preview' } as never),
    })
    registerFileOpener(textOpener)
    registerFileOpener(imageOpener)

    expect(getFileOpeners(textFile).map((o) => o.id)).toEqual(['text'])
    expect(getFileOpeners(imageFile).map((o) => o.id)).toEqual(['image'])
  })
})

describe('file-opener-registry owner-scoped', () => {
  beforeEach(() => clearAllForHmr())

  it('registers opener with ownerModuleId visible via getRegisteredOpeners', () => {
    registerFileOpener(mkOpener('a', 'editor'))
    const openers = getRegisteredOpeners()
    expect(openers).toHaveLength(1)
    expect(openers[0].ownerModuleId).toBe('editor')
  })

  it('unregisterByOwner only removes openers of that owner', () => {
    registerFileOpener(mkOpener('a', 'editor'))
    registerFileOpener(mkOpener('b', 'plugin-x'))
    unregisterByOwner('editor')
    const remaining = getRegisteredOpeners()
    expect(remaining).toHaveLength(1)
    expect(remaining[0].id).toBe('b')
    expect(remaining[0].ownerModuleId).toBe('plugin-x')
  })

  it('clearAllForHmr removes all owners', () => {
    registerFileOpener(mkOpener('a', 'editor'))
    registerFileOpener(mkOpener('b', 'plugin-x'))
    clearAllForHmr()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })

  it('clearFileOpenerRegistry remains as transitional alias for clearAllForHmr', () => {
    registerFileOpener(mkOpener('a', 'editor'))
    clearFileOpenerRegistry()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })

  it('re-registering the same owner+id replaces the previous entry', () => {
    registerFileOpener(mkOpener('a', 'editor', { label: 'first' }))
    registerFileOpener(mkOpener('a', 'editor', { label: 'second' }))
    const openers = getRegisteredOpeners()
    expect(openers).toHaveLength(1)
    expect(openers[0].label).toBe('second')
  })
})
