import { describe, it, expect, beforeEach } from 'vitest'
import { registerFileOpener, getDefaultOpener, getFileOpeners, clearFileOpenerRegistry } from './file-opener-registry'
import type { FileOpener } from './file-opener-registry'
import type { FileInfo } from '../types/fs'

const textFile: FileInfo = { name: 'test.ts', path: '/test.ts', extension: 'ts', size: 100, isDirectory: false }
const imageFile: FileInfo = { name: 'logo.png', path: '/logo.png', extension: 'png', size: 5000, isDirectory: false }

describe('file-opener-registry', () => {
  beforeEach(() => clearFileOpenerRegistry())

  it('returns null when no opener matches', () => {
    expect(getDefaultOpener(textFile)).toBeNull()
  })

  it('returns registered default opener', () => {
    const opener: FileOpener = {
      id: 'text-editor',
      label: 'Text Editor',
      icon: 'File',
      match: () => true,
      priority: 'default',
      createContent: (_source, file) => ({ kind: 'editor', source: { type: 'inapp' }, filePath: file.path } as never),
    }
    registerFileOpener(opener)
    expect(getDefaultOpener(textFile)).toBe(opener)
  })

  it('prefers default over option priority', () => {
    const option: FileOpener = {
      id: 'option-opener',
      label: 'Option',
      icon: 'File',
      match: () => true,
      priority: 'option',
      createContent: () => ({ kind: 'editor' } as never),
    }
    const def: FileOpener = {
      id: 'default-opener',
      label: 'Default',
      icon: 'File',
      match: () => true,
      priority: 'default',
      createContent: () => ({ kind: 'editor' } as never),
    }
    registerFileOpener(option)
    registerFileOpener(def)
    expect(getDefaultOpener(textFile)?.id).toBe('default-opener')
  })

  it('returns only matching openers', () => {
    const textOpener: FileOpener = {
      id: 'text',
      label: 'Text',
      icon: 'File',
      match: (f) => !['png', 'jpg'].includes(f.extension),
      priority: 'default',
      createContent: () => ({ kind: 'editor' } as never),
    }
    const imageOpener: FileOpener = {
      id: 'image',
      label: 'Image',
      icon: 'Image',
      match: (f) => ['png', 'jpg'].includes(f.extension),
      priority: 'default',
      createContent: () => ({ kind: 'image-preview' } as never),
    }
    registerFileOpener(textOpener)
    registerFileOpener(imageOpener)

    expect(getFileOpeners(textFile).map((o) => o.id)).toEqual(['text'])
    expect(getFileOpeners(imageFile).map((o) => o.id)).toEqual(['image'])
  })
})
