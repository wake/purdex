// spa/src/components/SortableTab.fresh.test.tsx — 「已寫好接力檔」 on the top tab bar (relay v2 contract 5): the icon beside the label,
// matched by (hostId, code); it must not take the label's room (the tab is ~140px) and a pinned (icon-only) tab shows none.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, within } from '@testing-library/react'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useRelayFreshStore } from '../stores/useRelayFreshStore'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
const { SortableTab } = await import('./SortableTab')

const tabOf = (id: string, code: string, pinned = false): Tab => ({
  ...createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' }, { pinned }),
  id,
})
const props = (tab: Tab) => ({ tab, isActive: false, onSelect: vi.fn(), onClose: vi.fn(), onMiddleClick: vi.fn(), onContextMenu: vi.fn() })
const fresh = (code?: string) => [{ session_id: 'S', ref: '_s', op_id: 'o', path: '/w/h.md', written_at: 1, ...(code ? { code } : {}) }]

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {}, hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } } })
  useAgentStore.setState({ unread: {}, statuses: {}, subagents: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', tabNameTooltipMode: 'both', hostBadgeTabBarEnabled: false })
  useRelayFreshStore.getState().reset()
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('SortableTab — handoff mark', () => {
  it('shows the icon when the list holds the tab\'s (host, code), and drops it with the entry', () => {
    render(<SortableTab {...props(tabOf('t1', 'sc1'))} />)
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
    act(() => useRelayFreshStore.getState().apply('h1', fresh('sc1')))
    const tab = screen.getByRole('tab')
    const mark = within(tab).getByTestId('half-handoff-mark')
    expect(mark).toHaveAttribute('data-variant', 'icon')
    expect(mark).toHaveAttribute('aria-label', '已寫好接力檔')
    act(() => useRelayFreshStore.getState().apply('h1', []))
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('keeps the label shrinkable and the mark fixed: the label is what gives way', () => {
    render(<SortableTab {...props(tabOf('t1', 'sc1'))} />)
    act(() => useRelayFreshStore.getState().apply('h1', fresh('sc1')))
    const mark = screen.getByTestId('half-handoff-mark')
    const label = mark.parentElement!.querySelector('span.overflow-hidden')!
    expect(label.className).toContain('min-w-0')
    expect(label.className).toContain('flex-1')
    expect(mark.className).toContain('flex-shrink-0')
    expect(label.compareDocumentPosition(mark) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('an entry without a code, or for another code, marks nothing', () => {
    render(<SortableTab {...props(tabOf('t1', 'sc1'))} />)
    act(() => useRelayFreshStore.getState().apply('h1', [...fresh(), ...fresh('other')]))
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('a pinned tab (icon only, no room) shows none', () => {
    render(<SortableTab {...props(tabOf('t1', 'sc1', true))} pinned />)
    act(() => useRelayFreshStore.getState().apply('h1', fresh('sc1')))
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })
})
