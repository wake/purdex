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
    // Path-aware list so the chip click can drive `listTreeUnder`'s recursive
    // descent: `/buffer` has a `dir/` subfolder plus two root files; `dir/`
    // holds a nested .md and .png.
    backendListMock.mockImplementation(async (path: string) => {
      if (path === '/buffer') {
        return [
          { name: 'dir', isDir: true, size: 0 },
          { name: 'a.md', isDir: false, size: 0 },
          { name: 'b.md', isDir: false, size: 0 },
        ]
      }
      if (path === '/buffer/dir') {
        return [
          { name: 'x.md', isDir: false, size: 0 },
          { name: 'pic.png', isDir: false, size: 0 },
        ]
      }
      return []
    })
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

  it('T1.3: renders the dirty dot from isDirty, never from canSave', () => {
    // A never-saved untitled buffer is savable while perfectly clean; the dot
    // must not follow that, it only means "there are unsaved changes".
    const { rerender } = render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={false}
        canSave={true}
        onSave={() => {}}
      />,
    )

    expect(screen.queryByTitle('Unsaved changes')).not.toBeInTheDocument()
    expect(screen.getByTitle('Save (⌘S)')).not.toBeDisabled()

    rerender(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={true}
        canSave={true}
        onSave={() => {}}
      />,
    )

    expect(screen.getByTitle('Unsaved changes')).toBeInTheDocument()
  })

  it('T1.3: enabled Save carries the accent style, disabled keeps the muted style', () => {
    const { rerender } = render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={true}
        onSave={() => {}}
      />,
    )

    const enabled = screen.getByTitle('Save (⌘S)')
    expect(enabled).not.toBeDisabled()
    expect(enabled.className).toMatch(/text-accent/)
    expect(enabled.className).not.toMatch(/text-text-secondary/)

    rerender(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={false}
        onSave={() => {}}
      />,
    )

    const disabled = screen.getByTitle('Save (⌘S)')
    expect(disabled).toBeDisabled()
    expect(disabled.className).not.toMatch(/text-accent/)
    expect(disabled.className).toMatch(/text-text-secondary/)
    expect(disabled.className).toMatch(/disabled:opacity-30/)
  })

  it('C3-7: onNewBuffer dirty-guard gates setPaneContent (v1.4 F7)', async () => {
    // Mirrors C3-6 for the popover's New-buffer button. The popover
    // merely invokes the caller-supplied onNewBuffer; the dirty guard
    // lives in EditorPane — we simulate that contract here so the fix
    // can regress if EditorPane drops the check.
    const originalConfirm = window.confirm
    try {
      async function openAndNew(onNewBuffer: () => void) {
        const result = render(
          <EditorToolbar
            source={{ type: 'inapp' }}
            filePath="/buffer/a.md"
            isDirty={true}
            onSave={() => {}}
            onBufferSwitch={() => {}}
            onManage={() => {}}
            onNewBuffer={onNewBuffer}
          />,
        )
        // Empty list so the "New buffer" button renders in the popover.
        backendListMock.mockResolvedValueOnce([])
        fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
        const newBtn = await waitFor(() =>
          screen.getByTestId('breadcrumb-popover-new-buffer'),
        )
        fireEvent.click(newBtn)
        result.unmount()
      }

      // Case 1: cancel — setPaneContent must NOT fire.
      const confirmFalse = vi.fn(() => false)
      window.confirm = confirmFalse
      const setPaneContentCancel = vi.fn()
      const onNewBufferCancel = vi.fn(() => {
        if (!window.confirm('editor.buffers.confirm_switch_dirty')) return
        setPaneContentCancel()
      })
      await openAndNew(onNewBufferCancel)
      expect(confirmFalse).toHaveBeenCalledTimes(1)
      expect(onNewBufferCancel).toHaveBeenCalledTimes(1)
      expect(setPaneContentCancel).not.toHaveBeenCalled()

      // Case 2: confirm — setPaneContent fires.
      const confirmTrue = vi.fn(() => true)
      window.confirm = confirmTrue
      const setPaneContentOk = vi.fn()
      const onNewBufferOk = vi.fn(() => {
        if (!window.confirm('editor.buffers.confirm_switch_dirty')) return
        setPaneContentOk()
      })
      await openAndNew(onNewBufferOk)
      expect(confirmTrue).toHaveBeenCalledTimes(1)
      expect(onNewBufferOk).toHaveBeenCalledTimes(1)
      expect(setPaneContentOk).toHaveBeenCalledTimes(1)
    } finally {
      window.confirm = originalConfirm
    }
  })

  it('T6-1: popover lists nested files with their full relative paths', async () => {
    render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={false}
        onSave={() => {}}
        onBufferSwitch={() => {}}
        onManage={() => {}}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))

    // Nested files appear labeled by their path relative to STORAGE_ROOT,
    // not just their basenames — this is the regression vs the old flat list.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'dir/x.md' })).toBeInTheDocument(),
    )
    expect(screen.getByRole('button', { name: 'dir/pic.png' })).toBeInTheDocument()
    // Root-level files remain present.
    expect(screen.getByRole('button', { name: /^a\.md$/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^b\.md$/ })).toBeInTheDocument()
  })

  it('T6-2: selecting a nested .png fires onBufferSwitch with the full path', async () => {
    const onBufferSwitch = vi.fn()
    render(
      <EditorToolbar
        source={{ type: 'inapp' }}
        filePath="/buffer/a.md"
        isDirty={false}
        onSave={() => {}}
        onBufferSwitch={onBufferSwitch}
        onManage={() => {}}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const pngButton = await waitFor(() => screen.getByRole('button', { name: 'dir/pic.png' }))
    fireEvent.click(pngButton)

    expect(onBufferSwitch).toHaveBeenCalledTimes(1)
    expect(onBufferSwitch).toHaveBeenCalledWith('/buffer/dir/pic.png')
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
