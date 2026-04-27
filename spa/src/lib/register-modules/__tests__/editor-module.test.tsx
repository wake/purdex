import { afterEach, beforeEach, describe, it, expect } from 'vitest'
import {
  editorModuleDefinition,
  registerEditorNewTabProviders,
} from '../editor-module'
import { clearNewTabRegistry, getNewTabProviders } from '../../new-tab-registry'

describe('editorModuleDefinition.fileOpeners', () => {
  it('declares its three file openers via the fileOpeners field', () => {
    const ids = editorModuleDefinition.fileOpeners?.map((o) => o.id).sort() ?? []
    expect(ids).toEqual(['image-preview', 'monaco-editor', 'pdf-viewer'])
  })

  it('image-preview opener matches png/jpg/jpeg/gif/webp/svg/ico extensions', () => {
    const opener = editorModuleDefinition.fileOpeners?.find((o) => o.id === 'image-preview')
    expect(opener?.priority).toBe('default')
    expect(opener?.match({ name: 'a.png', path: '/a.png', extension: 'png', size: 1, isDirectory: false })).toBe(true)
    expect(opener?.match({ name: 'a.JPG', path: '/a.JPG', extension: 'JPG', size: 1, isDirectory: false })).toBe(true)
    expect(opener?.match({ name: 'a.txt', path: '/a.txt', extension: 'txt', size: 1, isDirectory: false })).toBe(false)
  })

  it('pdf-viewer opener matches pdf only', () => {
    const opener = editorModuleDefinition.fileOpeners?.find((o) => o.id === 'pdf-viewer')
    expect(opener?.match({ name: 'a.pdf', path: '/a.pdf', extension: 'pdf', size: 1, isDirectory: false })).toBe(true)
    expect(opener?.match({ name: 'a.PDF', path: '/a.PDF', extension: 'PDF', size: 1, isDirectory: false })).toBe(true)
    expect(opener?.match({ name: 'a.png', path: '/a.png', extension: 'png', size: 1, isDirectory: false })).toBe(false)
  })

  it('monaco-editor opener matches non-binary text files only', () => {
    const opener = editorModuleDefinition.fileOpeners?.find((o) => o.id === 'monaco-editor')
    expect(opener?.match({ name: 'a.txt', path: '/a.txt', extension: 'txt', size: 1, isDirectory: false })).toBe(true)
    expect(opener?.match({ name: 'a.png', path: '/a.png', extension: 'png', size: 1, isDirectory: false })).toBe(false)
    expect(opener?.match({ name: 'a.pdf', path: '/a.pdf', extension: 'pdf', size: 1, isDirectory: false })).toBe(false)
    // Directories are not matched even when extension is empty.
    expect(opener?.match({ name: 'src', path: '/src', extension: '', size: 0, isDirectory: true })).toBe(false)
  })

  describe('registerEditorNewTabProviders', () => {
    beforeEach(() => clearNewTabRegistry())
    afterEach(() => clearNewTabRegistry())

    it('registers two editor-owned providers tagged with moduleId="editor"', () => {
      registerEditorNewTabProviders()
      const providers = getNewTabProviders()
      const editorEntries = providers.filter((p) => p.moduleId === 'editor')
      expect(editorEntries).toHaveLength(2)
      expect(editorEntries.map((p) => p.id).sort()).toEqual(['editor', 'editor-buffers'])
    })

    it('uses stable order values (editor=5, editor-buffers=6)', () => {
      registerEditorNewTabProviders()
      const byId = Object.fromEntries(getNewTabProviders().map((p) => [p.id, p]))
      expect(byId['editor']?.order).toBe(5)
      expect(byId['editor-buffers']?.order).toBe(6)
    })

    it('idempotent: calling twice does not duplicate the editor entries', () => {
      registerEditorNewTabProviders()
      registerEditorNewTabProviders()
      const editorEntries = getNewTabProviders().filter((p) => p.moduleId === 'editor')
      expect(editorEntries).toHaveLength(2)
      expect(editorEntries.map((p) => p.id).sort()).toEqual(['editor', 'editor-buffers'])
    })
  })

  it('createContent functions return the expected pane content kinds', () => {
    const src = { type: 'inapp' as const }
    const file = { name: 'a.png', path: '/a.png', extension: 'png', size: 1, isDirectory: false }
    const txtFile = { name: 'a.txt', path: '/a.txt', extension: 'txt', size: 1, isDirectory: false }
    const pdfFile = { name: 'a.pdf', path: '/a.pdf', extension: 'pdf', size: 1, isDirectory: false }
    const find = (id: string) => editorModuleDefinition.fileOpeners?.find((o) => o.id === id)
    expect(find('image-preview')?.createContent(src, file)).toMatchObject({ kind: 'image-preview', filePath: '/a.png' })
    expect(find('pdf-viewer')?.createContent(src, pdfFile)).toMatchObject({ kind: 'pdf-preview', filePath: '/a.pdf' })
    expect(find('monaco-editor')?.createContent(src, txtFile)).toMatchObject({ kind: 'editor', filePath: '/a.txt' })
  })
})
