import { describe, it, expect, beforeEach } from 'vitest'
import { useEditorStore } from './useEditorStore'
import type { EditorBufferMetadata } from './useEditorStore'

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
      // null = no explicit choice yet; the component resolves the language
      // default (markdown → wysiwyg, else raw) at render time.
      editorMode: null,
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
      // switching buffers resets pane-local state to fresh defaults; editorMode
      // null means "resolve language default at render" (key-b is markdown → wysiwyg).
      editorMode: null,
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

  it('tiptapViewState defaults to null and saveTiptapViewState writes it (AC4)', () => {
    const store = useEditorStore.getState()
    store.openBuffer('k1', 'hello', { language: 'markdown' })
    store.attachPane('p1', 'k1')
    expect(useEditorStore.getState().paneStates['p1'].tiptapViewState).toBeNull()

    store.saveTiptapViewState('p1', { scrollTop: 120, selection: { type: 'text', from: 3, to: 7 } })
    expect(useEditorStore.getState().paneStates['p1'].tiptapViewState).toEqual({
      scrollTop: 120,
      selection: { type: 'text', from: 3, to: 7 },
    })
  })

  it('saveTiptapViewState on a missing pane is a no-op (AC4)', () => {
    const before = useEditorStore.getState().paneStates
    useEditorStore.getState().saveTiptapViewState('nope', { scrollTop: 1, selection: null })
    expect(useEditorStore.getState().paneStates).toBe(before)
  })

  // --- T2.4: the shape of the file as loaded ---------------------------------
  //
  // `eol` is a live property of the current content — `normalizeMetadata`
  // recomputes it on every updateContent/reloadBuffer — so it cannot say what
  // the file looked like on disk. `sourceEol` / `sourceTrailingNewline` can:
  // they are written once per load and never move while the user edits.

  it('records the loaded line ending and trailing newline (T2.4)', () => {
    useEditorStore.getState().openBuffer('crlf', 'a\r\nb\r\n', { language: 'markdown' })
    useEditorStore.getState().openBuffer('lf', 'a\nb', { language: 'markdown' })

    expect(useEditorStore.getState().buffers['crlf']).toMatchObject({
      sourceEol: 'crlf',
      sourceTrailingNewline: true,
    })
    expect(useEditorStore.getState().buffers['lf']).toMatchObject({
      sourceEol: 'lf',
      sourceTrailingNewline: false,
    })
  })

  it('leaves the source shape alone while the content changes, unlike eol (T2.4)', () => {
    useEditorStore.getState().openBuffer('key1', 'a\r\nb\r\n', { language: 'markdown' })

    // Editing away every CRLF and the trailing newline: `eol` follows the draft,
    // the source shape does not — it still describes the file on disk.
    useEditorStore.getState().updateContent('key1', 'a\nb')

    expect(useEditorStore.getState().buffers['key1']).toMatchObject({
      eol: 'lf',
      sourceEol: 'crlf',
      sourceTrailingNewline: true,
    })
  })

  it('re-derives the source shape when the file is reloaded from disk (T2.4)', () => {
    useEditorStore.getState().openBuffer('key1', 'a\r\nb\r\n', { language: 'markdown' })
    useEditorStore.getState().reloadBuffer('key1', 'a\nb')

    expect(useEditorStore.getState().buffers['key1']).toMatchObject({
      sourceEol: 'lf',
      sourceTrailingNewline: false,
    })
  })

  it('carries the source shape across markSaved and renameBuffer (T2.4)', () => {
    useEditorStore.getState().openBuffer('old', 'a\r\nb\r\n', { language: 'markdown' })
    useEditorStore.getState().updateContent('old', 'a\r\nb\r\nc\r\n')
    useEditorStore.getState().markSaved('old', { mtime: 5, size: 9 })
    useEditorStore.getState().renameBuffer('old', 'new', { language: 'markdown' })

    expect(useEditorStore.getState().buffers['new']).toMatchObject({
      sourceEol: 'crlf',
      sourceTrailingNewline: true,
    })
  })

  // Preservation relies on the partial-merge shape of `renameBuffer`
  // (`{ ...buffer, ...metadata }`) rather than on any special case, so it holds
  // only as long as the source shape stays OUT of `EditorBufferMetadata`. This
  // pins that: a caller passing every metadata field must not be able to reach
  // these two.
  it('cannot be clobbered by a metadata caller that passes everything (T2.4)', () => {
    useEditorStore.getState().openBuffer('old', 'a\r\nb\r\n', { language: 'markdown' })
    useEditorStore.getState().renameBuffer('old', 'new', {
      language: 'plaintext',
      languageSource: 'manual',
      eol: 'lf',
      encoding: 'utf8',
      untitled: undefined,
    })

    expect(useEditorStore.getState().buffers['new']).toMatchObject({
      language: 'plaintext',
      eol: 'lf',
      sourceEol: 'crlf',
      sourceTrailingNewline: true,
    })
  })

  // Leading blank lines belong to the same family: Tiptap drops them at parse
  // time, so the only place they can survive is the buffer.

  it('records how many blank lines the file opened with', () => {
    useEditorStore.getState().openBuffer('none', '# Title\n', { language: 'markdown' })
    useEditorStore.getState().openBuffer('one', '\n# Title\n', { language: 'markdown' })
    useEditorStore.getState().openBuffer('three', '\n\n\n# Title\n', { language: 'markdown' })
    useEditorStore.getState().openBuffer('crlf', '\r\n\r\n# Title\r\n', { language: 'markdown' })

    expect(useEditorStore.getState().buffers['none'].sourceLeadingBlankLines).toBe(0)
    expect(useEditorStore.getState().buffers['one'].sourceLeadingBlankLines).toBe(1)
    expect(useEditorStore.getState().buffers['three'].sourceLeadingBlankLines).toBe(3)
    expect(useEditorStore.getState().buffers['crlf'].sourceLeadingBlankLines).toBe(2)
  })

  // A file of nothing but newlines has no "leading" blank lines to speak of —
  // counting them would double up against `sourceTrailingNewline`.
  it('reports no leading blank lines for a file that is only newlines', () => {
    useEditorStore.getState().openBuffer('blank', '\n\n\n', { language: 'markdown' })
    expect(useEditorStore.getState().buffers['blank'].sourceLeadingBlankLines).toBe(0)
  })

  it('leaves the leading blank line count alone while the content changes', () => {
    useEditorStore.getState().openBuffer('key1', '\n\n# Title\n', { language: 'markdown' })
    useEditorStore.getState().updateContent('key1', '# Title changed')

    expect(useEditorStore.getState().buffers['key1'].sourceLeadingBlankLines).toBe(2)
  })

  it('re-derives the leading blank line count when the file is reloaded', () => {
    useEditorStore.getState().openBuffer('key1', '\n\n# Title\n', { language: 'markdown' })
    useEditorStore.getState().reloadBuffer('key1', '# Title\n')

    expect(useEditorStore.getState().buffers['key1'].sourceLeadingBlankLines).toBe(0)
  })

  // Keeping the source shape out of `EditorBufferMetadata` makes it unreachable
  // through the TYPE, which is not the same as unreachable at runtime: a caller
  // casting its way past the signature would silently rewrite the file's shape
  // and, with it, what the next save writes to disk. `renameBuffer` therefore
  // re-asserts the buffer's own values after the merge instead of trusting the
  // spread order.
  it('survives a metadata caller that casts the source shape keys in', () => {
    useEditorStore.getState().openBuffer('old', '\n\na\r\nb\r\n', { language: 'markdown' })

    useEditorStore.getState().renameBuffer('old', 'new', {
      language: 'plaintext',
      sourceEol: 'lf',
      sourceTrailingNewline: false,
      sourceLeadingBlankLines: 0,
    } as unknown as Partial<EditorBufferMetadata>)

    expect(useEditorStore.getState().buffers['new']).toMatchObject({
      language: 'plaintext',
      sourceEol: 'crlf',
      sourceTrailingNewline: true,
      sourceLeadingBlankLines: 2,
    })
  })
})
