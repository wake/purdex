import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabContextMenu } from './TabContextMenu'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'

vi.mock('../lib/platform', () => ({
  getPlatformCapabilities: vi.fn(() => ({ canTearOffTab: false, canMergeWindow: false, canBrowserPane: false, canSystemTray: false, canNotification: false, isElectron: false, devUpdateEnabled: false })),
}))

import { getPlatformCapabilities } from '../lib/platform'

function makeSessionTab(mode: 'terminal' = 'terminal', opts?: { pinned?: boolean; locked?: boolean }): Tab {
  const tab = createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'tst001', mode, cachedName: '', tmuxInstance: '' }, { pinned: opts?.pinned })
  if (opts?.locked) return { ...tab, locked: true }
  return tab
}

function makeNonSessionTab(): Tab {
  return createTab({ kind: 'new-tab' })
}

function renderMenu(overrides?: { tab?: Tab; hasOtherUnlocked?: boolean; hasRightUnlocked?: boolean; targetTabs?: Tab[] }) {
  const props = {
    tab: overrides?.tab ?? makeSessionTab(),
    position: { x: 100, y: 100 },
    onClose: vi.fn(),
    onAction: vi.fn(),
    hasOtherUnlocked: overrides?.hasOtherUnlocked ?? true,
    hasRightUnlocked: overrides?.hasRightUnlocked ?? true,
    targetTabs: overrides?.targetTabs,
  }
  render(<TabContextMenu {...props} />)
  return props
}

describe('TabContextMenu', () => {
  beforeEach(() => { cleanup(); vi.clearAllMocks(); useI18nStore.getState().setLocale('en') })
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  // --- ViewMode section (removed in P-D.3: terminal is the only mode) ---
  it('shows no view-mode items for a session tab', () => {
    const { onAction } = renderMenu()
    expect(screen.queryByText('Switch to Stream')).not.toBeInTheDocument()
    expect(screen.queryByText('Switch to Terminal')).not.toBeInTheDocument()
    expect(screen.queryByText(/stream/i)).not.toBeInTheDocument()
    // The session-only actions are still there.
    fireEvent.click(screen.getByText('Rename Session'))
    expect(onAction).toHaveBeenCalledWith('rename', undefined)
  })

  it('shows no view-mode items for non-session tab', () => {
    renderMenu({ tab: makeNonSessionTab() })
    expect(screen.queryByText('Switch to Stream')).not.toBeInTheDocument()
    expect(screen.queryByText('Switch to Terminal')).not.toBeInTheDocument()
  })

  // --- Lock/Unlock ---
  it('shows "Lock tab" for unlocked tab', () => {
    renderMenu()
    expect(screen.getByText('Lock tab')).toBeInTheDocument()
    expect(screen.queryByText('Unlock tab')).not.toBeInTheDocument()
  })

  it('shows "Unlock tab" for locked non-pinned tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    expect(screen.getByText('Unlock tab')).toBeInTheDocument()
    expect(screen.queryByText('Lock tab')).not.toBeInTheDocument()
  })

  it('shows "Unlock tab" for pinned + locked tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { pinned: true, locked: true }) })
    expect(screen.getByText('Unlock tab')).toBeInTheDocument()
  })

  // --- Pin/Unpin ---
  it('shows "Pin tab" for unpinned tab', () => {
    renderMenu()
    expect(screen.getByText('Pin tab')).toBeInTheDocument()
    expect(screen.queryByText('Unpin tab')).not.toBeInTheDocument()
  })

  it('shows "Unpin tab" for pinned tab', () => {
    renderMenu({ tab: makeSessionTab('terminal', { pinned: true }) })
    expect(screen.getByText('Unpin tab')).toBeInTheDocument()
    expect(screen.queryByText('Pin tab')).not.toBeInTheDocument()
  })

  // --- Close section ---
  it('"Close tab" is disabled when locked', () => {
    renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    const closeBtn = screen.getByText('Close tab').closest('button')!
    expect(closeBtn).toBeDisabled()
  })

  it('"Close tab" is enabled when unlocked', () => {
    renderMenu()
    const closeItem = screen.getByText('Close tab')
    expect(closeItem.closest('button')).not.toHaveClass('opacity-40')
  })

  it('shows "Close other tabs" when hasOtherUnlocked', () => {
    renderMenu({ hasOtherUnlocked: true })
    expect(screen.getByText('Close other tabs')).toBeInTheDocument()
  })

  it('hides "Close other tabs" when no other unlocked', () => {
    renderMenu({ hasOtherUnlocked: false })
    expect(screen.queryByText('Close other tabs')).not.toBeInTheDocument()
  })

  it('shows "Close tabs to the right" when hasRightUnlocked', () => {
    renderMenu({ hasRightUnlocked: true })
    expect(screen.getByText('Close tabs to the right')).toBeInTheDocument()
  })

  it('hides "Close tabs to the right" when no right unlocked', () => {
    renderMenu({ hasRightUnlocked: false })
    expect(screen.queryByText('Close tabs to the right')).not.toBeInTheDocument()
  })

  // --- Action callbacks ---
  it('calls onAction with correct action on click', () => {
    const props = renderMenu()
    fireEvent.click(screen.getByText('Lock tab'))
    expect(props.onAction).toHaveBeenCalledWith('lock', undefined)
    expect(props.onClose).toHaveBeenCalled()
  })

  it('disabled item does not fire onAction', () => {
    const props = renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    fireEvent.click(screen.getByText('Close tab'))
    expect(props.onAction).not.toHaveBeenCalled()
  })

  // --- Rename section ---
  it('shows "Rename Session" for non-terminated session tab', () => {
    renderMenu()
    expect(screen.getByText('Rename Session')).toBeInTheDocument()
  })

  it('hides "Rename Session" for non-session tab', () => {
    renderMenu({ tab: makeNonSessionTab() })
    expect(screen.queryByText('Rename Session')).not.toBeInTheDocument()
  })

  it('hides "Rename Session" for terminated session tab', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: 'c', mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'session-closed' })
    renderMenu({ tab })
    expect(screen.queryByText('Rename Session')).not.toBeInTheDocument()
  })

  it('calls onAction with "rename" when clicking Rename Session', () => {
    const props = renderMenu()
    fireEvent.click(screen.getByText('Rename Session'))
    expect(props.onAction).toHaveBeenCalledWith('rename', undefined)
  })

  // --- Tear-off section (Electron only) ---
  it('shows "Move to New Window" when caps.canTearOffTab is true', () => {
    vi.mocked(getPlatformCapabilities).mockReturnValue({ canTearOffTab: true, canMergeWindow: false, canBrowserPane: false, canSystemTray: false, canNotification: true, isElectron: true, devUpdateEnabled: false, hasLocalFilesystem: true })
    renderMenu()
    expect(screen.getByText('Move to New Window')).toBeInTheDocument()
  })

  it('does not show "Move to New Window" when no electronAPI (canTearOffTab false)', () => {
    vi.mocked(getPlatformCapabilities).mockReturnValue({ canTearOffTab: false, canMergeWindow: false, canBrowserPane: false, canSystemTray: false, canNotification: false, isElectron: false, devUpdateEnabled: false, hasLocalFilesystem: false })
    renderMenu()
    expect(screen.queryByText('Move to New Window')).not.toBeInTheDocument()
  })

  it('"Move to New Window" is disabled when tab is locked', () => {
    vi.mocked(getPlatformCapabilities).mockReturnValue({ canTearOffTab: true, canMergeWindow: false, canBrowserPane: false, canSystemTray: false, canNotification: true, isElectron: true, devUpdateEnabled: false, hasLocalFilesystem: true })
    renderMenu({ tab: makeSessionTab('terminal', { locked: true }) })
    const tearOffBtn = screen.getByText('Move to New Window').closest('button')!
    expect(tearOffBtn).toBeDisabled()
  })

  // --- mergeToTab section locale (#1337) ---
  describe('mergeToTab menu item locale (#1337)', () => {
    it('shows the English merge-to-tab label for the en locale', () => {
      const targetTab = makeNonSessionTab()
      renderMenu({ targetTabs: [targetTab] })
      expect(screen.getByText('Add new-tab tab as pane')).toBeInTheDocument()
      expect(screen.queryByText('加入 new-tab tab 成為 pane')).not.toBeInTheDocument()
    })

    it('shows the zh-TW merge-to-tab label for the zh-TW locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      const targetTab = makeNonSessionTab()
      renderMenu({ targetTabs: [targetTab] })
      expect(screen.getByText('加入 new-tab tab 成為 pane')).toBeInTheDocument()
    })

    it('calls onAction with mergeToTab and the target tab id when clicked', () => {
      const targetTab = makeNonSessionTab()
      const props = renderMenu({ targetTabs: [targetTab] })
      fireEvent.click(screen.getByText('Add new-tab tab as pane'))
      expect(props.onAction).toHaveBeenCalledWith('mergeToTab', targetTab.id)
    })
  })
})
