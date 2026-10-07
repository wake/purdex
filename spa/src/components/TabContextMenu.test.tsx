import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabContextMenu } from './TabContextMenu'
import { TITLE_BAR_HEIGHT } from './FloatingPanel'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'

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

  // --- Tear-off section ---
  // The App is the only shell, so the item is always offered; the 'tearOff'
  // handler (features/workspace/hooks.ts) is what checks for electronAPI.
  it('offers "Move to New Window" with no electronAPI present', () => {
    expect(window.electronAPI).toBeUndefined()
    const props = renderMenu()
    fireEvent.click(screen.getByText('Move to New Window'))
    expect(props.onAction).toHaveBeenCalledWith('tearOff', undefined)
  })

  it('"Move to New Window" is disabled when tab is locked', () => {
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

// #1801. The top TITLE_BAR_HEIGHT px of the window is the title bar's OS drag
// region: a click there drags the window instead of choosing an item, so no
// part of the menu may be placed in it — a tall one scrolls instead.
describe('TabContextMenu stays below the title bar (#1801)', () => {
  const saved: Array<[string, PropertyDescriptor | undefined]> = []

  /** jsdom lays nothing out: the menu's own box is the only one its placement reads. */
  function stubLayout(menu: { width: number; height: number }, viewport: { width: number; height: number }) {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { left: 0, top: 0, width: menu.width, height: menu.height, right: menu.width, bottom: menu.height, x: 0, y: 0, toJSON: () => ({}) } as DOMRect,
    )
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: viewport.width })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: viewport.height })
  }

  function menuAt(position: { x: number; y: number }, targetTabs?: Tab[]) {
    return (
      <TabContextMenu
        tab={makeSessionTab()}
        position={position}
        onClose={vi.fn()}
        onAction={vi.fn()}
        hasOtherUnlocked
        hasRightUnlocked
        targetTabs={targetTabs}
      />
    )
  }

  /** Twelve "merge to tab" rows: a menu taller than a short window. */
  const manyTargets = () => Array.from({ length: 12 }, () => makeNonSessionTab())

  beforeEach(() => {
    cleanup()
    useI18nStore.getState().setLocale('en')
    for (const key of ['innerWidth', 'innerHeight']) saved.push([key, Object.getOwnPropertyDescriptor(window, key)])
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    for (const [key, descriptor] of saved.splice(0)) {
      if (descriptor) Object.defineProperty(window, key, descriptor)
      else delete (window as unknown as Record<string, unknown>)[key]
    }
  })

  it('a tall menu opened near the top of a short window starts below the title bar and scrolls', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const { container } = render(menuAt({ x: 100, y: 40 }, manyTargets()))
    expect(screen.getAllByText('Add new-tab tab as pane')).toHaveLength(12)
    const el = container.firstElementChild as HTMLElement
    // 40 + 400 > 300 → moved up to 300 - 400 - 4 = -104 → the title bar's bottom edge
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
    expect(el.style.left).toBe('100px')
  })

  it('a tall menu opened near the bottom moves up only as far as the title bar, not to 4px', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const { container } = render(menuAt({ x: 100, y: 280 }, manyTargets()))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
  })

  it('a menu opened inside the title bar band starts below it, uncapped when it fits', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 10 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe('')
  })

  it('room to open where clicked: the same place as before, and no cap', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 100 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.left).toBe('100px')
    expect(el.style.top).toBe('100px')
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('near the right and bottom edges: corrected as before, and no cap', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 1000, y: 700 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.left).toBe('820px') // 1024 - 200 - 4
    expect(el.style.top).toBe('646px') // 800 - 150 - 4
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('a menu that ends inside the window\'s last 4px is left where it was, not clipped', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 648 })) // 648 + 150 = 798: fits, so never moved
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe('648px')
    expect(el.style.maxHeight).toBe('')
  })

  it('drops an earlier cap when it opens again with room to spare', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const targets = manyTargets()
    const { container, rerender } = render(menuAt({ x: 100, y: 40 }, targets))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    rerender(menuAt({ x: 100, y: 100 }, targets))
    expect(el.style.top).toBe('100px')
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('is marked no-drag, so the title bar region does not take its pointer events', () => {
    const { container } = render(menuAt({ x: 100, y: 100 }))
    const el = container.firstElementChild as HTMLElement
    // React assigns the camelCase property, which jsdom keeps as is (see FloatingPanel.test.tsx).
    expect((el.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
  })
})
