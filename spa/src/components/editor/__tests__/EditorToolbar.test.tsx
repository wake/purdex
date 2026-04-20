import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { EditorToolbar } from '../EditorToolbar'

describe('EditorToolbar', () => {
  it('renders breadcrumb segments for absolute path', () => {
    render(
      <EditorToolbar
        filePath="/notes/daily/2026-04-20.md"
        isDirty={false}
        isMarkdown
        editorMode="raw"
        onSave={vi.fn()}
      />,
    )

    expect(screen.getByText('notes')).toBeTruthy()
    expect(screen.getByText('daily')).toBeTruthy()
    expect(screen.getByText('2026-04-20.md')).toBeTruthy()
  })
})
