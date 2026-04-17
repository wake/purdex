import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { InlineTab } from './InlineTab'
import type { Tab, PaneContent } from '../../../types/tab'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'

function renderWith(
  tab: Tab,
  title: string,
  overrides: Partial<React.ComponentProps<typeof InlineTab>> = {},
) {
  return render(
    <DndContext>
      <SortableContext items={[tab.id]}>
        <InlineTab
          tab={tab}
          title={title}
          isActive={false}
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
          {...overrides}
        />
      </SortableContext>
    </DndContext>,
  )
}

interface MkTabOpts {
  id?: string
  pinned?: boolean
  locked?: boolean
  hostId?: string
  sessionCode?: string
  terminated?: boolean
}

const mkTab = (opts: MkTabOpts = {}): Tab => {
  const id = opts.id ?? 't1'
  const content: PaneContent =
    opts.hostId && opts.sessionCode
      ? ({
          kind: 'tmux-session',
          hostId: opts.hostId,
          sessionCode: opts.sessionCode,
          ...(opts.terminated ? { terminated: true } : {}),
        } as PaneContent)
      : ({ kind: 'new-tab' } as PaneContent)
  return {
    id,
    pinned: opts.pinned ?? false,
    locked: opts.locked ?? false,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content } },
  } as Tab
}

describe('InlineTab', () => {
  it('renders given title', () => {
    renderWith(mkTab(), 'My Tab')
    expect(screen.getByText('My Tab')).toBeInTheDocument()
  })

  it('click triggers onSelect', () => {
    const onSelect = vi.fn()
    renderWith(mkTab(), 'Untitled', { onSelect })
    fireEvent.click(screen.getByText('Untitled'))
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('close button triggers onClose and stops propagation', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    renderWith(mkTab(), 'Untitled', { onSelect, onClose })
    fireEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(onClose).toHaveBeenCalledWith('t1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('active state adds a purple ring class', () => {
    const { container } = renderWith(mkTab(), 'Untitled', { isActive: true })
    const row = container.querySelector('[data-testid="inline-tab-row"]')!
    expect(row.className).toMatch(/ring/)
  })

  it('middle click triggers onMiddleClick', () => {
    const onMiddleClick = vi.fn()
    renderWith(mkTab(), 'Untitled', { onMiddleClick })
    const row = screen.getByText('Untitled').closest('[data-testid="inline-tab-row"]')!
    fireEvent.mouseDown(row, { button: 1 })
    expect(onMiddleClick).toHaveBeenCalledWith('t1')
  })
})

describe('InlineTab — visual parity', () => {
  beforeEach(() => {
    cleanup()
    useAgentStore.setState({
      statuses: {},
      subagents: {},
      unread: {},
      tabIndicatorStyle: 'overlay',
    } as Partial<ReturnType<typeof useAgentStore.getState>> as never)
    useHostStore.setState({ runtime: {} } as Partial<ReturnType<typeof useHostStore.getState>> as never)
  })

  it('renders Lock icon for locked tabs', () => {
    renderWith(mkTab({ locked: true }), 'Locked')
    expect(screen.getByTestId('inline-tab-lock')).toBeInTheDocument()
  })

  it('hides Close button for locked tabs (parity with SortableTab)', () => {
    renderWith(mkTab({ locked: true }), 'Locked')
    expect(screen.queryByLabelText(/^Close /)).not.toBeInTheDocument()
  })

  it('shows unread dot when tab has unread flag and is not active', () => {
    useAgentStore.setState({
      unread: { 'host1:sc1': true },
    } as Partial<ReturnType<typeof useAgentStore.getState>> as never)
    renderWith(
      mkTab({ hostId: 'host1', sessionCode: 'sc1' }),
      'Unread',
      { sourceWsId: null, isActive: false },
    )
    expect(screen.getByTestId('inline-tab-unread')).toBeInTheDocument()
  })

  it('hides unread dot when tab is active', () => {
    useAgentStore.setState({
      unread: { 'host1:sc1': true },
    } as Partial<ReturnType<typeof useAgentStore.getState>> as never)
    renderWith(
      mkTab({ hostId: 'host1', sessionCode: 'sc1' }),
      'Active',
      { sourceWsId: null, isActive: true },
    )
    expect(screen.queryByTestId('inline-tab-unread')).not.toBeInTheDocument()
  })

  it('shows WifiSlash when host offline and tab not terminated', () => {
    useHostStore.setState({
      runtime: { host1: { status: 'disconnected' } },
    } as Partial<ReturnType<typeof useHostStore.getState>> as never)
    renderWith(
      mkTab({ hostId: 'host1', sessionCode: 'sc1' }),
      'Offline',
      { sourceWsId: null },
    )
    expect(screen.getByTestId('inline-tab-host-offline')).toBeInTheDocument()
  })

  it('hides WifiSlash when session is terminated', () => {
    useHostStore.setState({
      runtime: { host1: { status: 'disconnected' } },
    } as Partial<ReturnType<typeof useHostStore.getState>> as never)
    renderWith(
      mkTab({ hostId: 'host1', sessionCode: 'sc1', terminated: true }),
      'Terminated',
      { sourceWsId: null },
    )
    expect(screen.queryByTestId('inline-tab-host-offline')).not.toBeInTheDocument()
  })
})

describe('InlineTab — drag-safe pointerdown + isPinned data', () => {
  it('click on row still fires onSelect after pointerdown', () => {
    const onSelect = vi.fn()
    renderWith(mkTab({ pinned: false }), 'T1', { sourceWsId: null, onSelect })
    const row = screen.getByTestId('inline-tab-row')
    fireEvent.pointerDown(row, { button: 0, clientX: 10, clientY: 10 })
    fireEvent.click(row)
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('pointerdown on active tab prevents default to stop focus theft', () => {
    renderWith(mkTab({ pinned: false }), 'T1', { sourceWsId: null, isActive: true })
    const row = screen.getByTestId('inline-tab-row')
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
    row.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(true)
  })

  it('pointerdown on inactive tab does NOT preventDefault', () => {
    renderWith(mkTab({ pinned: false }), 'T1', { sourceWsId: null, isActive: false })
    const row = screen.getByTestId('inline-tab-row')
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
    row.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(false)
  })
})
