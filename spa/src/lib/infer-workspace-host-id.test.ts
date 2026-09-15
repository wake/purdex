import { describe, it, expect } from 'vitest'
import { collectTmuxSessionHostIds } from './infer-workspace-host-id'
import type { PaneLayout } from '../types/tab'

function tmuxLeaf(hostId: string, sessionCode = 'sess'): PaneLayout {
  return {
    type: 'leaf',
    pane: {
      id: `pane-${hostId}-${sessionCode}`,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: 'x',
        tmuxInstance: 'default',
      },
    },
  }
}

function newTabLeaf(): PaneLayout {
  return { type: 'leaf', pane: { id: 'p-newtab', content: { kind: 'new-tab' } } }
}

function splitH(...children: PaneLayout[]): PaneLayout {
  return { type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 1 / children.length) }
}

describe('collectTmuxSessionHostIds', () => {
  it('returns [] for a non tmux-session leaf', () => {
    expect(collectTmuxSessionHostIds(newTabLeaf())).toEqual([])
  })

  it('collects hostIds from nested split layouts in pre-order', () => {
    const layout = splitH(tmuxLeaf('h1'), splitH(newTabLeaf(), tmuxLeaf('h2')), tmuxLeaf('h1'))
    expect(collectTmuxSessionHostIds(layout)).toEqual(['h1', 'h2', 'h1'])
  })
})
