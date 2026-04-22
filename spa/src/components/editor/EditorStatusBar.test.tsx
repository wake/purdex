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

  it('shows Purdex badge for in-app editor', () => {
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={17}
        column={25}
        isMarkdown={true}
        editorMode="raw"
        onModeChange={() => {}}
      />,
    )

    const badge = screen.getByText('Purdex')
    expect(badge.className).toContain('violet')
    expect(screen.getByText('Ln 17, Col 25')).toBeInTheDocument()
  })

  it('shows daemon host name on the left', () => {
    const hostId = useHostStore.getState().activeHostId!
    render(
      <EditorStatusBar
        source={{ type: 'daemon', hostId }}
        line={3}
        column={9}
        isMarkdown={true}
        editorMode="raw"
        onModeChange={() => {}}
      />,
    )

    expect(screen.getByText('mlab')).toBeInTheDocument()
  })

  it('uses Source / Live Preview labels and switches modes from the menu', () => {
    const onModeChange = vi.fn()
    render(
      <EditorStatusBar
        source={{ type: 'inapp' }}
        line={1}
        column={1}
        isMarkdown={true}
        editorMode="raw"
        onModeChange={onModeChange}
      />,
    )

    fireEvent.click(screen.getByTitle('Toggle editor mode'))
    fireEvent.click(screen.getByText('Live Preview'))

    expect(onModeChange).toHaveBeenCalledWith('wysiwyg')
  })

  it('does not show mode switcher for non-markdown files', () => {
    render(
      <EditorStatusBar
        source={{ type: 'local' }}
        line={8}
        column={4}
        isMarkdown={false}
        editorMode="raw"
      />,
    )

    expect(screen.queryByTitle('Toggle editor mode')).not.toBeInTheDocument()
    expect(screen.getByText('Ln 8, Col 4')).toBeInTheDocument()
  })
})
