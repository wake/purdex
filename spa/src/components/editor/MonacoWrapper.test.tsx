import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { editor } from 'monaco-editor'
import { MonacoWrapper } from './MonacoWrapper'
import {
  DEFAULT_EDITOR_SETTINGS,
  useEditorSettingsStore,
} from '../../stores/useEditorSettingsStore'

const editorPropsSpy = vi.hoisted(() => vi.fn())
const editorMock = vi.hoisted(() => ({
  addAction: vi.fn(),
  onDidChangeCursorPosition: vi.fn(),
  restoreViewState: vi.fn(),
  saveViewState: vi.fn(() => ({ scrollTop: 42 })),
  focus: vi.fn(),
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
    // merge-mode reset so zustand actions stay on the store.
    useEditorSettingsStore.setState({ ...DEFAULT_EDITOR_SETTINGS })
  })

  afterEach(() => {
    cleanup()
    useEditorSettingsStore.setState({ ...DEFAULT_EDITOR_SETTINGS })
  })

  it('uses the provided modelId as Monaco path', () => {
    render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
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
        isActive={true}
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

  it('does not save view state during a normal rerender', () => {
    const firstOnViewStateChange = vi.fn()
    const secondOnViewStateChange = vi.fn()
    const { rerender, unmount } = render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={firstOnViewStateChange}
        onSave={() => {}}
      />,
    )

    rerender(
      <MonacoWrapper
        content="hello world"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={secondOnViewStateChange}
        onSave={() => {}}
      />,
    )

    expect(firstOnViewStateChange).not.toHaveBeenCalled()
    expect(secondOnViewStateChange).not.toHaveBeenCalled()

    unmount()

    expect(firstOnViewStateChange).not.toHaveBeenCalled()
    expect(secondOnViewStateChange).toHaveBeenCalledWith({ scrollTop: 42 })
  })

  it('uses the latest onSave callback for Monaco save action', () => {
    const firstOnSave = vi.fn()
    const secondOnSave = vi.fn()
    const { rerender } = render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={firstOnSave}
      />,
    )

    rerender(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={secondOnSave}
      />,
    )

    const action = editorMock.addAction.mock.calls[0]?.[0] as { run: () => void }
    action.run()

    expect(firstOnSave).not.toHaveBeenCalled()
    expect(secondOnSave).toHaveBeenCalledTimes(1)
  })

  it('focuses the editor when the pane becomes active', () => {
    const { rerender } = render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={false}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={() => {}}
      />,
    )

    editorMock.focus.mockClear()

    rerender(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={() => {}}
      />,
    )

    expect(editorMock.focus).toHaveBeenCalledTimes(1)
  })

  it('M1-1: Editor options reflect useEditorSettingsStore values', () => {
    useEditorSettingsStore.setState({
      tabSize: 4,
      insertSpaces: false,
      wordWrap: 'off',
      lineNumbers: 'off',
      minimap: false,
      fontSize: 20,
    })

    render(
      <MonacoWrapper
        content="hello"
        language="markdown"
        modelId="model-1"
        isActive={true}
        initialViewState={null}
        onChange={() => {}}
        onCursorChange={() => {}}
        onViewStateChange={() => {}}
        onSave={() => {}}
      />,
    )

    const lastCall = editorPropsSpy.mock.calls.at(-1)?.[0] as { options: Record<string, unknown> }
    expect(lastCall.options).toEqual(
      expect.objectContaining({
        tabSize: 4,
        insertSpaces: false,
        wordWrap: 'off',
        lineNumbers: 'off',
        minimap: { enabled: false },
        fontSize: 20,
      }),
    )
  })
})
