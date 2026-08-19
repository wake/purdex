// spa/src/stores/__tests__/editor-buffer-fixture.ts
//
// The ONE place a test spells out the full shape of an `EditorBuffer` /
// `EditorPaneState`.
//
// Seeding `useEditorStore` needs a complete record — the store types carry no
// optional fields — so every suite that pre-loads a buffer used to inline all
// twelve properties. That let the buffer's internal shape leak into unrelated
// suites: adding `sourceEol` / `sourceTrailingNewline` /
// `sourceLeadingBlankLines` (spec 2.4) meant editing Storage tests that care
// about nothing but a path rename. Callers now pass only the fields their
// assertion is about; a new field lands here and nowhere else.
import type { EditorBuffer, EditorPaneState } from '../useEditorStore'

/** A clean, non-dirty markdown buffer. Override only what the test asserts on. */
export function makeEditorBuffer(overrides: Partial<EditorBuffer> = {}): EditorBuffer {
  return {
    content: 'X',
    savedContent: 'X',
    isDirty: false,
    lastStat: null,
    modelId: 'm1',
    language: 'markdown',
    languageSource: 'extension',
    eol: 'lf',
    encoding: 'utf8',
    sourceEol: 'lf',
    sourceTrailingNewline: false,
    sourceLeadingBlankLines: 0,
    ...overrides,
  }
}

/** The pane state that binds a pane id to `bufferKey`, at rest. */
export function makeEditorPaneState(
  bufferKey: string,
  overrides: Partial<EditorPaneState> = {},
): EditorPaneState {
  return {
    bufferKey,
    editorMode: 'raw',
    showDiff: false,
    cursorPosition: { line: 1, column: 1 },
    monacoViewState: null,
    tiptapViewState: null,
    ...overrides,
  }
}
