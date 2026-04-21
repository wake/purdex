import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { editor } from 'monaco-editor'
import { MonacoWrapper } from './MonacoWrapper'

const editorPropsSpy = vi.hoisted(() => vi.fn())
const editorMock = vi.hoisted(() => ({
  addAction: vi.fn(),
  onDidChangeCursorPosition: vi.fn(),
  restoreViewState: vi.fn(),
  saveViewState: vi.fn(() => ({ scrollTop: 42 })),
}))

vi.mock('@monaco-editor/react', () => ({
  default: (props: Record<string, unknown>) => {
    editorPropsSpy(props)
    const onMount = props.onMount as ((editor: typeof editorMock, monaco: { KeyMod: { CtrlCmd: number }; KeyCode: { KeyS: number } }) => void) | undefined
    onMount?.(editorMock, { KeyMod: { CtrlCmd: 1 }, KeyCode: { KeyS: 2 } })
    return <div data-testid="monaco-editor" />
  },
}))

describe('MonacoWrapper', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('uses the provided modelId as Monaco path', () => {
    render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={() => {}}
      />,
    )

    expect(editorPropsSpy).toHaveBeenCalledWith(expect.objectContaining({ path: 'model-1' }))
  })

  it('restores and saves pane view state', () => {
    const onViewStateChange = vi.fn()
    const initialViewState = { scrollTop: 12 } as unknown as editor.ICodeEditorViewState
    const { unmount } = render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        initialViewState={initialViewState}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={onViewStateChange}
        onSave={() => {}}
      />,
    )

    expect(editorMock.restoreViewState).toHaveBeenCalledWith(initialViewState)

    unmount()

    expect(onViewStateChange).toHaveBeenCalledWith({ scrollTop: 42 })
  })
})
