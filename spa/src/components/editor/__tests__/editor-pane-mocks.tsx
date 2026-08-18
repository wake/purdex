// spa/src/components/editor/__tests__/editor-pane-mocks.tsx
//
// The module every `vi.mock` factory in the EditorPane suites reaches into. It
// deliberately imports NOTHING from the component under test: a factory runs
// while `../EditorPane` is still being evaluated, so a module that pulls in
// EditorPane would deadlock on its own import promise. Fixtures and render
// helpers therefore live next door in `editor-pane-harness.tsx`.
import { vi } from 'vitest'

export const getFsBackendMock = vi.fn()
export const editorStatusBarMock = vi.fn()
export const tiptapPropsSpy = vi.fn()
export const monacoPropsSpy = vi.fn()

// The real fs-backend module is kept reachable so the host-binding suite can
// drive EditorPane through the REAL registry — a mocked getFsBackend can never
// prove which host a read lands on.
export const fsBackendActual = {
  current: null as typeof import('../../../lib/fs-backend') | null,
}

export function MonacoWrapperStub(props: { isActive?: boolean; initialViewState?: unknown }) {
  monacoPropsSpy(props)
  return <div data-testid="monaco-wrapper" data-active={props.isActive ? 'true' : 'false'} />
}

export function DiffViewStub() {
  return <div data-testid="diff-view" />
}

export function EditorStatusBarStub(props: { language: string; eol: 'lf' | 'crlf'; encoding: 'utf8'; isMarkdown: boolean; editorMode: 'raw' | 'wysiwyg'; contentWidth?: 'narrow' | 'full'; onContentWidthChange?: (v: 'narrow' | 'full') => void }) {
  editorStatusBarMock(props)
  return (
    <div
      data-testid="editor-status-bar"
      data-language={props.language}
      data-eol={props.eol}
      data-encoding={props.encoding}
      data-is-markdown={props.isMarkdown ? 'true' : 'false'}
      data-editor-mode={props.editorMode}
    />
  )
}

export function TiptapEditorStub(props: { initialViewState: unknown; onViewStateChange: (vs: unknown) => void }) {
  tiptapPropsSpy(props)
  return (
    <button
      data-testid="tiptap-editor"
      onClick={() => props.onViewStateChange({ scrollTop: 42, selection: { type: 'text', from: 2, to: 3 } })}
    />
  )
}
