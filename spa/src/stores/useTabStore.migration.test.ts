import { describe, it, expect } from 'vitest'
import { migrateTabStore } from './useTabStore'

describe('useTabStore — persist migration', () => {
  it('migrates kind "session" to "tmux-session" in version 2', () => {
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1', pinned: false, locked: false, createdAt: 1000,
          layout: {
            type: 'leaf' as const,
            pane: {
              id: 'pane1',
              content: { kind: 'session', hostId: 'h1', sessionCode: 'abc123', mode: 'terminal', cachedName: 'test', tmuxInstance: '123:456' },
            },
          },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }
    const migrated = migrateTabStore(v1State, 1)
    const pane = migrated.tabs.tab1.layout.pane
    expect(pane.content.kind).toBe('tmux-session')
  })

  it('migrates kind "session" inside split layouts', () => {
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1', pinned: false, locked: false, createdAt: 1000,
          layout: {
            type: 'split' as const, id: 'split1', direction: 'h' as const,
            children: [
              { type: 'leaf' as const, pane: { id: 'p1', content: { kind: 'session', hostId: 'h1', sessionCode: 'a', mode: 'terminal', cachedName: 'A', tmuxInstance: '1:2' } } },
              { type: 'leaf' as const, pane: { id: 'p2', content: { kind: 'dashboard' } } },
            ],
            sizes: [50, 50],
          },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }
    const migrated = migrateTabStore(v1State, 1)
    const children = migrated.tabs.tab1.layout.children
    expect(children[0].pane.content.kind).toBe('tmux-session')
    expect(children[1].pane.content.kind).toBe('dashboard')
  })

  it('preserves non-session tabs during migration', () => {
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1', pinned: false, locked: false, createdAt: 1000,
          layout: { type: 'leaf' as const, pane: { id: 'pane1', content: { kind: 'dashboard' } } },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }
    const migrated = migrateTabStore(v1State, 1)
    const pane = migrated.tabs.tab1.layout.pane
    expect(pane.content.kind).toBe('dashboard')
  })
})
