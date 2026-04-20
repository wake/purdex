import { describe, it, expect } from 'vitest'
import { migrateTabStore } from './useTabStore'

describe('useTabStore — persist migration', () => {
  it('migrates nested inapp editor panes to stable docId while preserving split layout', () => {
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1', pinned: false, locked: false, createdAt: 1000,
          layout: {
            type: 'split' as const, id: 'split1', direction: 'h' as const, sizes: [40, 60],
            children: [
              {
                type: 'leaf' as const,
                pane: {
                  id: 'pane1',
                  content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/todo.md' },
                },
              },
              {
                type: 'split' as const, id: 'split2', direction: 'v' as const, sizes: [50, 50],
                children: [
                  {
                    type: 'leaf' as const,
                    pane: {
                      id: 'pane2',
                      content: { kind: 'session', hostId: 'h1', sessionCode: 'abc123', mode: 'terminal', cachedName: 'test', tmuxInstance: '123:456' },
                    },
                  },
                  {
                    type: 'leaf' as const,
                    pane: {
                      id: 'pane3',
                      content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/todo.md' },
                    },
                  },
                ],
              },
            ],
          },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }
    const migrated = migrateTabStore(v1State, 1)
    const layout = migrated.tabs.tab1.layout
    expect(layout.type).toBe('split')
    if (layout.type !== 'split') return
    expect(layout.children[0].type).toBe('leaf')
    expect(layout.children[1].type).toBe('split')
    if (layout.children[0].type !== 'leaf' || layout.children[1].type !== 'split') return
    expect(layout.children[0].pane.content.kind).toBe('editor')
    expect(layout.children[0].pane.content.docId).toBeDefined()
    expect(layout.children[0].pane.content.docId).toBe(layout.children[1].children[1].pane.content.docId)
    expect(layout.children[1].children[0].pane.content.kind).toBe('tmux-session')
    expect(layout.children[1].children[1].pane.content.kind).toBe('editor')
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
