import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { EditorToolbar } from './EditorToolbar'

describe('EditorToolbar', () => {
  it('shows a Purdex prefix and hides the buffer segment for in-app paths', () => {
    const { container } = render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/example.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    expect(screen.getByText('Purdex')).toBeInTheDocument()
    expect(container.querySelector('img')).toHaveAttribute('src', '/icons/logo-transparent.png')
    expect(screen.queryByText('buffer')).not.toBeInTheDocument()
    expect(screen.getByText('example.md')).toBeInTheDocument()
  })

  it('keeps filename double click rename affordance for in-app buffers', () => {
    const onRenameStart = vi.fn()

    render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/example.md"
        isDirty={false}
        onSave={() => {}}
        onRenameStart={onRenameStart}
      />,
    )

    fireEvent.doubleClick(screen.getByRole('button', { name: 'example.md' }))

    expect(onRenameStart).toHaveBeenCalledTimes(1)
  })

  it('keeps a root slash prefix for non in-app paths', () => {
    const { container } = render(
      <EditorToolbar
        source={{ type: 'local' }}
        filePath="/notes/example.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    expect(screen.queryByText('Purdex')).not.toBeInTheDocument()
    expect(container.textContent).toContain('/notes/example.md')
  })

  it('uses tighter spacing for the breadcrumb row', () => {
    render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/project/example.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    expect(screen.getByTitle('/buffer/project/example.md').className).toContain('gap-0.5')
  })
})
