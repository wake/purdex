import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { EditorToolbar } from './EditorToolbar'

// The popover uses createPortal; render it inline in jsdom.
vi.mock('react-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom')>()
  return { ...actual, createPortal: (node: ReactNode) => node }
})

// i18n → identity.
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string) => string }) => unknown) =>
    selector({ t: (k: string) => k }),
}))

// Provide an inapp backend mock so the chip click can load the buffer list.
const backendListMock = vi.fn()
vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: () => ({
    list: backendListMock,
  }),
  registerFsBackend: vi.fn(),
}))

describe('EditorToolbar', () => {
  beforeEach(() => {
    backendListMock.mockReset()
    backendListMock.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 0 },
      { name: 'b.md', isDir: false, size: 0 },
    ])
  })

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

  it('T3-1: inapp Purdex chip renders as <button> (not span)', () => {
    render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/example.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    // The Purdex chip is the button that opens the buffer quick-switch popover.
    const chipButton = screen.getByRole('button', { name: /Purdex/ })
    expect(chipButton.tagName).toBe('BUTTON')
  })

  it('T3-2: non-inapp source renders no Purdex chip at all', () => {
    render(
      <EditorToolbar
        source={{ type: 'local' }}
        filePath="/notes/example.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    expect(screen.queryByText('Purdex')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Purdex/ })).not.toBeInTheDocument()
  })

  it('C3-6: popover buffer-switch respects dirty-guard both ways', async () => {
    const originalConfirm = window.confirm
    try {
      async function openAndClick(
        onBufferSwitch: (newKey: string) => void,
        targetName: string,
      ) {
        const result = render(
          <EditorToolbar
            source={{ type: 'inapp' }}
            filePath="/buffer/a.md"
            isDirty={true}
            onSave={() => {}}
            onBufferSwitch={onBufferSwitch}
            onManage={() => {}}
          />,
        )
        fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
        const bufferButton = await waitFor(() =>
          screen.getByRole('button', { name: new RegExp(targetName) }),
        )
        fireEvent.click(bufferButton)
        result.unmount()
      }

      // Case 1: user cancels — setPaneContent must NOT be invoked.
      const confirmFalse = vi.fn(() => false)
      window.confirm = confirmFalse
      const setPaneContentCancel = vi.fn()
      const onBufferSwitchCancel = vi.fn((newKey: string) => {
        if (!window.confirm('editor.buffers.confirm_switch_dirty')) return
        setPaneContentCancel(newKey)
      })
      await openAndClick(onBufferSwitchCancel, 'b\\.md')
      expect(confirmFalse).toHaveBeenCalledTimes(1)
      expect(onBufferSwitchCancel).toHaveBeenCalledWith('/buffer/b.md')
      expect(setPaneContentCancel).not.toHaveBeenCalled()

      // Case 2: user confirms — setPaneContent fires with the full path.
      const confirmTrue = vi.fn(() => true)
      window.confirm = confirmTrue
      const setPaneContentOk = vi.fn()
      const onBufferSwitchOk = vi.fn((newKey: string) => {
        if (!window.confirm('editor.buffers.confirm_switch_dirty')) return
        setPaneContentOk(newKey)
      })
      await openAndClick(onBufferSwitchOk, 'b\\.md')
      expect(confirmTrue).toHaveBeenCalledTimes(1)
      expect(onBufferSwitchOk).toHaveBeenCalledWith('/buffer/b.md')
      expect(setPaneContentOk).toHaveBeenCalledWith('/buffer/b.md')
    } finally {
      window.confirm = originalConfirm
    }
  })
})
