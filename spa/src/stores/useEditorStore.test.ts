import { describe, it, expect, beforeEach } from 'vitest'
import { useEditorStore } from './useEditorStore'

describe('useEditorStore', () => {
  beforeEach(() => {
    useEditorStore.getState().clearAllBuffers()
  })

  it('opens a buffer with content', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf).toBeDefined()
    expect(buf.content).toBe('hello')
    expect(buf.savedContent).toBe('hello')
    expect(buf.isDirty).toBe(false)
    expect(buf.language).toBe('typescript')
  })

  it('updateContent marks buffer as dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().updateContent('key1', 'hello world')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('hello world')
    expect(buf.isDirty).toBe(true)
  })

  it('markSaved clears dirty flag', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().updateContent('key1', 'changed')
    useEditorStore.getState().markSaved('key1')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.isDirty).toBe(false)
    expect(buf.savedContent).toBe('changed')
  })

  it('closeBuffer removes the buffer', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    useEditorStore.getState().closeBuffer('key1')
    expect(useEditorStore.getState().buffers['key1']).toBeUndefined()
  })

  it('reloadBuffer replaces content without marking dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'old', 'typescript')
    useEditorStore.getState().reloadBuffer('key1', 'new')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('new')
    expect(buf.savedContent).toBe('new')
    expect(buf.isDirty).toBe(false)
  })

  it('updateCursor stores cursor position', () => {
    useEditorStore.getState().openBuffer('key1', '', 'plaintext')
    useEditorStore.getState().updateCursor('key1', 10, 5)
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.cursorPosition).toEqual({ line: 10, column: 5 })
  })

  it('markSaved updates lastStat when stat is provided', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript')
    const stat = { mtime: 2000, size: 50 }
    useEditorStore.getState().markSaved('key1', stat)
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.lastStat).toEqual({ mtime: 2000, size: 50 })
  })

  it('markSaved preserves existing lastStat when no stat provided', () => {
    const initialStat = { mtime: 1000, size: 30 }
    useEditorStore.getState().openBuffer('key1', 'hello', 'typescript', initialStat)
    useEditorStore.getState().markSaved('key1')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.lastStat).toEqual({ mtime: 1000, size: 30 })
  })
})
