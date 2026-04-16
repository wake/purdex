import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { InlineTabList } from './InlineTabList'
import type { Tab } from '../../../types/tab'

const mkTab = (id: string, paneTitle: string): Tab =>
  ({
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `${id}-pane`,
        content: { kind: 'browser', url: `https://${paneTitle}.example.com` },
      },
    },
  }) as Tab

describe('InlineTabList', () => {
  it('renders empty state when tabIds is empty', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={[]}
          tabsById={{}}
          activeTabId={null}
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    expect(screen.getByText(/no tabs yet/i)).toBeInTheDocument()
  })

  it('renders tabs with computed labels in given order', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={['a', 'b']}
          tabsById={{
            a: mkTab('a', 'alpha'),
            b: mkTab('b', 'beta'),
          }}
          activeTabId="a"
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    // `browser` kind in getPaneLabel extracts hostname from URL
    expect(screen.getByText('alpha.example.com')).toBeInTheDocument()
    expect(screen.getByText('beta.example.com')).toBeInTheDocument()
  })

  it('skips ids with no matching tab entry', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={['a', 'missing']}
          tabsById={{ a: mkTab('a', 'alpha') }}
          activeTabId={null}
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    expect(screen.getByText('alpha.example.com')).toBeInTheDocument()
    expect(screen.queryByText('missing')).not.toBeInTheDocument()
  })
})
