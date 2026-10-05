import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactElement } from 'react'
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

// Like the real @monaco-editor/react, the mock calls `onMount` ONCE per editor instance, from an effect. With
// `mountControl.defer` set it holds the call in `pending` instead, so a test can model Monaco's asynchronous load:
// the wrapper's own effects (activation focus included) run while the editor does not exist yet.
const mountControl = vi.hoisted(() => ({ defer: false, pending: null as null | (() => void) }))

vi.mock('@monaco-editor/react', async () => {
  const { useEffect } = await import('react')
  return {
    default: function MonacoEditorMock(props: Record<string, unknown>) {
      editorPropsSpy(props)
      const onMount = props.onMount as ((editor: typeof editorMock, monaco: { KeyMod: { CtrlCmd: number }; KeyCode: { KeyS: number } }) => void) | undefined
      useEffect(() => {
        const fire = () => onMount?.(editorMock, { KeyMod: { CtrlCmd: 1 }, KeyCode: { KeyS: 2 } })
        if (mountControl.defer) mountControl.pending = fire
        else fire()
        // eslint-disable-next-line react-hooks/exhaustive-deps -- once per instance, like the real editor
      }, [])
      return <div data-testid="monaco-editor" />
    },
  }
})

function renderMonaco(isActive: boolean, isFocusTarget: boolean) {
  return render(
    <MonacoWrapper content="hello" language="markdown" modelId="model-1" isActive={isActive} isFocusTarget={isFocusTarget}
      initialViewState={null} onChange={() => {}} onCursorChange={() => {}}
      onViewStateChange={() => {}} onSave={() => {}} />,
  )
}

function rerenderMonaco(rerender: (ui: ReactElement) => void, isActive: boolean, isFocusTarget: boolean) {
  rerender(
    <MonacoWrapper content="hello" language="markdown" modelId="model-1" isActive={isActive} isFocusTarget={isFocusTarget}
      initialViewState={null} onChange={() => {}} onCursorChange={() => {}}
      onViewStateChange={() => {}} onSave={() => {}} />,
  )
}

describe('MonacoWrapper', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mountControl.defer = false
    mountControl.pending = null
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

  // Shell cleanup §8.2: programmatic focus only at activation (mount active, or isActive false→true), and only
  // when this pane is its tab's focus target. `isActive` alone keeps driving everything that is not focus.
  describe('activation-only focus', () => {
    it('focuses at mount when active and the focus target', () => {
      renderMonaco(true, true)
      expect(editorMock.focus).toHaveBeenCalled()
    })

    it('does not focus at mount when active but not the focus target', () => {
      renderMonaco(true, false)
      expect(editorMock.focus).not.toHaveBeenCalled()
    })

    it('does not focus at mount when the target but inactive', () => {
      renderMonaco(false, true)
      expect(editorMock.focus).not.toHaveBeenCalled()
    })

    it('focuses on inactive→active when the focus target', () => {
      const { rerender } = renderMonaco(false, true)
      editorMock.focus.mockClear()
      rerenderMonaco(rerender, true, true)
      expect(editorMock.focus).toHaveBeenCalledTimes(1)
    })

    it('does not focus on inactive→active when not the focus target', () => {
      const { rerender } = renderMonaco(false, false)
      rerenderMonaco(rerender, true, false)
      expect(editorMock.focus).not.toHaveBeenCalled()
    })

    it('does not focus when it becomes the focus target while already active (a click in a visible tab)', () => {
      const { rerender } = renderMonaco(true, false)
      rerenderMonaco(rerender, true, true)
      expect(editorMock.focus).not.toHaveBeenCalled()
    })

    describe('editor mounts after activation (async Monaco load)', () => {
      beforeEach(() => {
        mountControl.defer = true
      })

      it('focuses on the late mount when active and the focus target', () => {
        renderMonaco(true, true)
        expect(editorMock.focus).not.toHaveBeenCalled() // no editor yet: the activation path had nothing to focus
        act(() => mountControl.pending?.())
        expect(editorMock.focus).toHaveBeenCalledTimes(1)
      })

      it('does not focus on the late mount when active but not the focus target', () => {
        renderMonaco(true, false)
        act(() => mountControl.pending?.())
        expect(editorMock.focus).not.toHaveBeenCalled()
      })

      it('reads the focus target at mount time: no focus if another pane became the target meanwhile', () => {
        const { rerender } = renderMonaco(true, true)
        rerenderMonaco(rerender, true, false)
        act(() => mountControl.pending?.())
        expect(editorMock.focus).not.toHaveBeenCalled()
      })

      it('does not focus on the late mount when inactive', () => {
        renderMonaco(false, true)
        act(() => mountControl.pending?.())
        expect(editorMock.focus).not.toHaveBeenCalled()
      })
    })
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

  it('enables scrollBeyondLastLine so the bottom has a scroll buffer', () => {
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
      expect.objectContaining({ scrollBeyondLastLine: true }),
    )
  })
})
