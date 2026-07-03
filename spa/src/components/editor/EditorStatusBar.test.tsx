import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorStatusBar } from './EditorStatusBar'
import { useHostStore } from '../../stores/useHostStore'

describe('EditorStatusBar', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
    const hostId = useHostStore.getState().activeHostId
    if (hostId) {
      useHostStore.getState().updateHost(hostId, { name: 'mlab' })
    }
  })

  it('shows plain Purdex text for in-app editor', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={17}
        column={25}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    const source = screen.getByText('Purdex')
    const position = screen.getByText('Ln 17, Col 25')

    expect(source).toHaveClass('text-text-secondary', 'select-none')
    expect(position).toHaveClass('text-text-secondary', 'select-none')
  })

  it('shows daemon host name on the left', () => {
    const hostId = useHostStore.getState().activeHostId!
    render(
      <EditorStatusBar
        source={{ type: 'daemon', hostId }}
        line={3}
        column={9}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByText('mlab')).toBeInTheDocument()
  })

  it('shows document metadata values in the status bar', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="crlf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByText('UTF-8')).toBeInTheDocument()
    expect(screen.getByText('CRLF')).toBeInTheDocument()
    expect(screen.getByTitle('Select language')).toHaveTextContent('Markdown')
  })

  it('uses Source / Live Mode labels and switches modes from the menu', () => {
    const onModeChange = vi.fn()
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        onModeChange={onModeChange}
        onLanguageChange={() => {}}
      />,
    )

    fireEvent.click(screen.getByTitle('Toggle editor mode'))
    fireEvent.click(screen.getByRole('button', { name: 'Live Mode' }))

    expect(onModeChange).toHaveBeenCalledWith('wysiwyg')
  })

  it('keeps the mode selector for non-markdown files and disables Live Mode', () => {
    render(
      <EditorStatusBar
        source={{ type: 'local' }}
        line={8}
        column={4}
        language="plaintext"
        eol="lf"
        encoding="utf8"
        isMarkdown={false}
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByTitle('Toggle editor mode')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Toggle editor mode'))

    expect(screen.getByRole('button', { name: 'Source ✓' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Live Mode' })).toBeDisabled()
    expect(screen.getByText('Ln 8, Col 4')).toBeInTheDocument()
  })

  it('switches the language from the selector', () => {
    const onLanguageChange = vi.fn()

    render(
      <EditorStatusBar
        source={{ type: 'local' }}
        line={8}
        column={4}
        language="plaintext"
        eol="lf"
        encoding="utf8"
        isMarkdown={false}
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={onLanguageChange}
      />,
    )

    fireEvent.click(screen.getByTitle('Select language'))
    fireEvent.click(screen.getByRole('button', { name: 'Markdown' }))

    expect(onLanguageChange).toHaveBeenCalledWith('markdown')
  })

  it('keeps cursor info and selectors inside the same block flex container', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    const controls = screen.getByText('Ln 1, Col 1').parentElement

    expect(controls?.tagName).toBe('DIV')
    expect(controls).toHaveClass('ml-auto')
    expect(controls).toContainElement(screen.getByTitle('Toggle editor mode'))
    expect(controls).toContainElement(screen.getByTitle('Select language'))
  })

  it('renders the content-width toggle in Live Mode when onContentWidthChange is provided', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="narrow"
        onContentWidthChange={() => {}}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByTitle('Narrow width')).toBeInTheDocument()
  })

  it('does not render the content-width toggle in raw mode', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="raw"
        contentWidth="narrow"
        onContentWidthChange={() => {}}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.queryByTitle('Narrow width')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Full width')).not.toBeInTheDocument()
  })

  it('does not render the content-width toggle without an onContentWidthChange handler', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="narrow"
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.queryByTitle('Narrow width')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Full width')).not.toBeInTheDocument()
  })

  it('toggles between narrow and full on click', () => {
    const onContentWidthChange = vi.fn()
    const { rerender } = render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="narrow"
        onContentWidthChange={onContentWidthChange}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    fireEvent.click(screen.getByTitle('Narrow width'))
    expect(onContentWidthChange).toHaveBeenCalledWith('full')

    rerender(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="full"
        onContentWidthChange={onContentWidthChange}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    fireEvent.click(screen.getByTitle('Full width'))
    expect(onContentWidthChange).toHaveBeenLastCalledWith('narrow')
  })

  it('reflects the current contentWidth in the toggle title', () => {
    const { rerender } = render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="narrow"
        onContentWidthChange={() => {}}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByTitle('Narrow width')).toBeInTheDocument()
    expect(screen.queryByTitle('Full width')).not.toBeInTheDocument()

    rerender(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        language="markdown"
        eol="lf"
        encoding="utf8"
        isMarkdown
        editorMode="wysiwyg"
        contentWidth="full"
        onContentWidthChange={() => {}}
        onModeChange={() => {}}
        onLanguageChange={() => {}}
      />,
    )

    expect(screen.getByTitle('Full width')).toBeInTheDocument()
    expect(screen.queryByTitle('Narrow width')).not.toBeInTheDocument()
  })

  it('keeps cursor info and hides selectors only when changes are unavailable', () => {
    render(
      <EditorStatusBar
        source={{ type: 'local' }}
        line={8}
        column={4}
        language="plaintext"
        eol="lf"
        encoding="utf8"
        isMarkdown={false}
        editorMode="raw"
      />,
    )

    expect(screen.queryByTitle('Toggle editor mode')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Select language')).not.toBeInTheDocument()
    expect(screen.getByText('Ln 8, Col 4')).toBeInTheDocument()
  })
})
