import { describe, it, expect, beforeEach } from 'vitest'
import { useEditorStore } from './useEditorStore'

describe('useEditorStore', () => {
  beforeEach(() => {
    useEditorStore.getState().clearAllBuffers()
  })

  it('keeps shared buffer state separate from pane-local view state', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    useEditorStore.getState().attachPane('pane-a', 'key1')
    useEditorStore.getState().attachPane('pane-b', 'key1')

    useEditorStore.getState().setEditorMode('pane-a', 'wysiwyg')
    useEditorStore.getState().setShowDiff('pane-a', true)
    useEditorStore.getState().updateCursor('pane-a', 10, 5)

    const state = useEditorStore.getState()
    expect(state.buffers.key1?.content).toBe('hello')
    expect(state.paneStates['pane-a']).toMatchObject({
      bufferKey: 'key1',
      editorMode: 'wysiwyg',
      showDiff: true,
      cursorPosition: { line: 10, column: 5 },
    })
    expect(state.paneStates['pane-b']).toMatchObject({
      bufferKey: 'key1',
      editorMode: 'raw',
      showDiff: false,
      cursorPosition: { line: 1, column: 1 },
    })
  })

  it('only closes a shared buffer when the last pane detaches', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    useEditorStore.getState().attachPane('pane-a', 'key1')
    useEditorStore.getState().attachPane('pane-b', 'key1')

    useEditorStore.getState().closePane('pane-a')
    expect(useEditorStore.getState().buffers['key1']).toBeDefined()

    useEditorStore.getState().closePane('pane-b')
    expect(useEditorStore.getState().buffers['key1']).toBeUndefined()
  })

  it('switching a pane to another buffer resets pane-local state and releases the old buffer', () => {
    useEditorStore.getState().openBuffer('key-a', 'A', { language: 'typescript' })
    useEditorStore.getState().openBuffer('key-b', 'B', { language: 'markdown' })
    useEditorStore.getState().attachPane('pane-a', 'key-a')
    useEditorStore.getState().setEditorMode('pane-a', 'wysiwyg')
    useEditorStore.getState().setShowDiff('pane-a', true)
    useEditorStore.getState().updateCursor('pane-a', 8, 3)

    useEditorStore.getState().attachPane('pane-a', 'key-b')

    expect(useEditorStore.getState().buffers['key-a']).toBeUndefined()
    expect(useEditorStore.getState().paneStates['pane-a']).toMatchObject({
      bufferKey: 'key-b',
      editorMode: 'raw',
      showDiff: false,
      cursorPosition: { line: 1, column: 1 },
    })
  })

  it('renames a shared buffer key and preserves model identity', () => {
    useEditorStore.getState().openBuffer('old-key', 'hello', { language: 'typescript' })
    useEditorStore.getState().attachPane('pane-a', 'old-key')

    const modelId = useEditorStore.getState().buffers['old-key']?.modelId
    useEditorStore.getState().renameBuffer('old-key', 'new-key', {
      language: 'markdown',
      languageSource: 'extension',
    })

    expect(useEditorStore.getState().buffers['old-key']).toBeUndefined()
    expect(useEditorStore.getState().buffers['new-key']).toMatchObject({
      content: 'hello',
      modelId,
      language: 'markdown',
      languageSource: 'extension',
    })
    expect(useEditorStore.getState().paneStates['pane-a']?.bufferKey).toBe('new-key')
  })

  it('stores document metadata when opening a buffer', () => {
    useEditorStore.getState().openBuffer('key1', 'hello\r\nworld', {
      language: 'typescript',
      languageSource: 'extension',
    })

    expect(useEditorStore.getState().buffers['key1']).toMatchObject({
      language: 'typescript',
      languageSource: 'extension',
      eol: 'crlf',
      encoding: 'utf8',
    })
  })

  it('stores untitled document state when opening a buffer', () => {
    useEditorStore.getState().openBuffer('key1', '', {
      language: 'markdown',
      languageSource: 'template',
      untitled: {
        name: 'Untitled',
        suggestedExtension: '.md',
        hasBeenRenamed: false,
      },
    })

    expect(useEditorStore.getState().buffers['key1']?.untitled).toEqual({
      name: 'Untitled',
      suggestedExtension: '.md',
      hasBeenRenamed: false,
    })
  })

  it('allows manual language changes without changing content state', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })

    useEditorStore.getState().setBufferLanguage('key1', 'markdown')

    expect(useEditorStore.getState().buffers['key1']).toMatchObject({
      language: 'markdown',
      languageSource: 'manual',
      content: 'hello',
      savedContent: 'hello',
      isDirty: false,
    })
  })

  it('preserves manual language when a buffer is renamed', () => {
    useEditorStore.getState().openBuffer('old-key', 'hello', {
      language: 'markdown',
      languageSource: 'manual',
    })

    useEditorStore.getState().renameBuffer('old-key', 'new-key', {
      language: 'markdown',
      languageSource: 'manual',
    })

    expect(useEditorStore.getState().buffers['new-key']).toMatchObject({
      language: 'markdown',
      languageSource: 'manual',
    })
  })

  it('updates untitled state when a buffer is renamed', () => {
    useEditorStore.getState().openBuffer('untitled:Untitled', '', {
      language: 'plaintext',
      languageSource: 'template',
      untitled: {
        name: 'Untitled',
        suggestedExtension: '.txt',
        hasBeenRenamed: false,
      },
    })

    useEditorStore.getState().renameBuffer('untitled:Untitled', 'untitled:notes.txt', {
      language: 'plaintext',
      languageSource: 'extension',
      untitled: {
        name: 'notes.txt',
        suggestedExtension: '.txt',
        hasBeenRenamed: true,
      },
    })

    expect(useEditorStore.getState().buffers['untitled:notes.txt']).toMatchObject({
      untitled: {
        name: 'notes.txt',
        suggestedExtension: '.txt',
        hasBeenRenamed: true,
      },
    })
  })

  it('opens a buffer with content', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf).toBeDefined()
    expect(buf.content).toBe('hello')
    expect(buf.savedContent).toBe('hello')
    expect(buf.isDirty).toBe(false)
    expect(buf.language).toBe('typescript')
  })

  it('updateContent marks buffer as dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    useEditorStore.getState().updateContent('key1', 'hello world')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('hello world')
    expect(buf.isDirty).toBe(true)
  })

  it('markSaved clears dirty flag', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    useEditorStore.getState().updateContent('key1', 'changed')
    useEditorStore.getState().markSaved('key1')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.isDirty).toBe(false)
    expect(buf.savedContent).toBe('changed')
  })

  it('closeBuffer removes the buffer', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    useEditorStore.getState().closeBuffer('key1')
    expect(useEditorStore.getState().buffers['key1']).toBeUndefined()
  })

  it('reloadBuffer replaces content without marking dirty', () => {
    useEditorStore.getState().openBuffer('key1', 'old', { language: 'typescript' })
    useEditorStore.getState().reloadBuffer('key1', 'new')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.content).toBe('new')
    expect(buf.savedContent).toBe('new')
    expect(buf.isDirty).toBe(false)
    expect(buf.eol).toBe('lf')
  })

  it('updateCursor stores cursor position', () => {
    useEditorStore.getState().openBuffer('key1', '', { language: 'plaintext' })
    useEditorStore.getState().attachPane('pane-a', 'key1')
    useEditorStore.getState().updateCursor('pane-a', 10, 5)
    const pane = useEditorStore.getState().paneStates['pane-a']
    expect(pane.cursorPosition).toEqual({ line: 10, column: 5 })
  })

  it('markSaved updates lastStat when stat is provided', () => {
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' })
    const stat = { mtime: 2000, size: 50 }
    useEditorStore.getState().markSaved('key1', stat)
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.lastStat).toEqual({ mtime: 2000, size: 50 })
  })

  it('markSaved preserves existing lastStat when no stat provided', () => {
    const initialStat = { mtime: 1000, size: 30 }
    useEditorStore.getState().openBuffer('key1', 'hello', { language: 'typescript' }, initialStat)
    useEditorStore.getState().markSaved('key1')
    const buf = useEditorStore.getState().buffers['key1']
    expect(buf.lastStat).toEqual({ mtime: 1000, size: 30 })
  })
})
