import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabContextMenu } from './TabContextMenu'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'

function makeSessionTab(mode: 'terminal' | 'stream' = 'terminal', opts?: { pinned?: boolean; locked?: boolean }): Tab {
  const tab = createTab({ kind: 'session', sessionCode: 'tst001', mode }, { pinned: opts?.pinned })
  if (opts?.locked) return { ...tab, locked: true }
  return tab
}

function makeNonSessionTab(): Tab {
  return createTab({ kind: 'new-tab' })
}

function renderMenu(overrides?: { tab?: Tab; hasOtherUnlocked?: boolean; hasRightUnlocked?: boolean }) {
  const props = {
    tab: overrides?.tab ?? makeSessionTab(),
    position: { x: 100, y: 100 },
    onClose: vi.fn(),
    onAction: vi.fn(),
    hasOtherUnlocked: overrides?.hasOtherUnlocked ?? true,
    hasRightUnlocked: overrides?.hasRightUnlocked ?? true,
  }
  render(<TabContextMenu {...props} />)
  return props
}

describe('TabContextMenu', () => {
  beforeEach(() => { cleanup(); vi.clearAllMocks() })

  // --- ViewMode section ---
  it('shows "切換至 Stream" for session tab in terminal mode', () => {
    renderMenu()
    expect(screen.getByText('切換至 Stream')).toBeInTheDocument()
    expect(screen.queryByText('切換至 Terminal')).not.toBeInTheDocument()
  })

  it('shows "切換至 Terminal" for session tab in stream mode', () => {
    renderMenu({ tab: makeSessionTab('stream') })
    expect(screen.getByText('切換至 Terminal')).toBeInTheDocument()
    expect(screen.queryByText('切換至 Stream')).not.toBeInTheDocument()
  })

  it('hides viewMode toggle for non-session tab', () => {
    renderMenu({ tab: makeNonSessionTab() })
    expect(screen.queryByText('切換至 Stream')).not.toBeInTheDocument()
    expect(screen.queryByText('切換至 Terminal')).not.toBeInTheDocument()
  })

  // --- Lock/Unlock ---
  it('shows "鎖定分頁" for unlocked tab', () => {
    renderMenu()
    expect(screen.getByText('鎖定分頁')).toBeInTheDocument()
    expect(screen.queryByText('解鎖分頁')).not.toBeInTheDocument()
  })

  it('shows "解鎖分頁" for locked non-pinned tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    expect(screen.getByText('解鎖分頁')).toBeInTheDocument()
    expect(screen.queryByText('鎖定分頁')).not.toBeInTheDocument()
  })

  it('shows "解鎖分頁" for pinned + locked tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { pinned: true, locked: true }) })
    expect(screen.getByText('解鎖分頁')).toBeInTheDocument()
  })

  // --- Pin/Unpin ---
  it('shows "固定分頁" for unpinned tab', () => {
    renderMenu()
    expect(screen.getByText('固定分頁')).toBeInTheDocument()
    expect(screen.queryByText('取消固定')).not.toBeInTheDocument()
  })

  it('shows "取消固定" for pinned tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { pinned: true }) })
    expect(screen.getByText('取消固定')).toBeInTheDocument()
    expect(screen.queryByText('固定分頁')).not.toBeInTheDocument()
  })

  // --- Close section ---
  it('"關閉分頁" is disabled when locked', () => {
    renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    const closeBtn = screen.getByText('關閉分頁').closest('button')!
    expect(closeBtn).toBeDisabled()
  })

  it('"關閉分頁" is enabled when unlocked', () => {
    renderMenu()
    const closeItem = screen.getByText('關閉分頁')
    expect(closeItem.closest('button')).not.toHaveClass('opacity-40')
  })

  it('shows "關閉其他分頁" when hasOtherUnlocked', () => {
    renderMenu({ hasOtherUnlocked: true })
    expect(screen.getByText('關閉其他分頁')).toBeInTheDocument()
  })

  it('hides "關閉其他分頁" when no other unlocked', () => {
    renderMenu({ hasOtherUnlocked: false })
    expect(screen.queryByText('關閉其他分頁')).not.toBeInTheDocument()
  })

  it('shows "關閉右側分頁" when hasRightUnlocked', () => {
    renderMenu({ hasRightUnlocked: true })
    expect(screen.getByText('關閉右側分頁')).toBeInTheDocument()
  })

  it('hides "關閉右側分頁" when no right unlocked', () => {
    renderMenu({ hasRightUnlocked: false })
    expect(screen.queryByText('關閉右側分頁')).not.toBeInTheDocument()
  })

  // --- Action callbacks ---
  it('calls onAction with correct action on click', () => {
    const props = renderMenu()
    fireEvent.click(screen.getByText('鎖定分頁'))
    expect(props.onAction).toHaveBeenCalledWith('lock')
    expect(props.onClose).toHaveBeenCalled()
  })

  it('disabled item does not fire onAction', () => {
    const props = renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    fireEvent.click(screen.getByText('關閉分頁'))
    expect(props.onAction).not.toHaveBeenCalled()
  })
})
