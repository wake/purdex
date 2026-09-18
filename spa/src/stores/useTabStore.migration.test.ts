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

  // P-D.3: version 3 rewrites every tmux-session leaf's mode to 'terminal'.
  // A plain merge/rehydrate hook would not run for an existing v2 blob, so the
  // persist version is what carries the normalisation.
  describe('version 3 — tmux-session mode narrows to terminal', () => {
    const tmux = (id: string, mode: string) => ({
      type: 'leaf' as const,
      pane: { id, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: id, mode, cachedName: id, tmuxInstance: '1:2', rebuild: { sessionName: id, tmuxInstance: '1:2', capturedAt: 1 } } },
    })

    it('rewrites a stream pane in a v2 blob and leaves the terminal pane, split and other tabs untouched', () => {
      const v2State = {
        tabs: {
          mixed: {
            id: 'mixed', pinned: false, locked: false, createdAt: 1000,
            layout: {
              type: 'split' as const, id: 'split1', direction: 'h' as const,
              children: [
                tmux('streamy', 'stream'),
                { type: 'split' as const, id: 'split2', direction: 'v' as const, children: [tmux('termy', 'terminal'), { type: 'leaf' as const, pane: { id: 'dash', content: { kind: 'dashboard' } } }], sizes: [50, 50] },
              ],
              sizes: [30, 70],
            },
          },
          plain: {
            id: 'plain', pinned: true, locked: false, createdAt: 2000,
            layout: { type: 'leaf' as const, pane: { id: 'ed', content: { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' } } },
          },
        },
        tabOrder: ['mixed', 'plain'],
        activeTabId: 'plain',
      }
      const before = JSON.parse(JSON.stringify(v2State))

      const migrated = migrateTabStore(v2State, 2)

      const [first, inner] = migrated.tabs.mixed.layout.children
      expect(first.pane.content.mode).toBe('terminal')
      // Everything else on the rewritten leaf survives.
      expect(first.pane.content).toEqual({ ...before.tabs.mixed.layout.children[0].pane.content, mode: 'terminal' })
      expect(inner.children[0].pane.content).toEqual(before.tabs.mixed.layout.children[1].children[0].pane.content)
      expect(inner.children[1].pane.content).toEqual({ kind: 'dashboard' })
      expect(migrated.tabs.mixed.layout.sizes).toEqual([30, 70])
      expect(migrated.tabs.plain).toEqual(before.tabs.plain)
      expect(migrated.tabOrder).toEqual(['mixed', 'plain'])
      expect(migrated.activeTabId).toBe('plain')
      // Input untouched.
      expect(v2State).toEqual(before)
    })

    it('runs the < 2 kind rename first for a v1 blob, then the < 3 mode rewrite', () => {
      const v1State = {
        tabs: {
          tab1: {
            id: 'tab1', pinned: false, locked: false, createdAt: 1000,
            layout: {
              type: 'leaf' as const,
              pane: { id: 'pane1', content: { kind: 'session', hostId: 'h1', sessionCode: 'abc123', mode: 'stream', cachedName: 'test', tmuxInstance: '123:456' } },
            },
          },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      const migrated = migrateTabStore(v1State, 1)
      const content = migrated.tabs.tab1.layout.pane.content
      expect(content.kind).toBe('tmux-session')
      expect(content.mode).toBe('terminal')
      expect(content.sessionCode).toBe('abc123')
    })

    it('returns a v3 blob unchanged (same reference)', () => {
      const v3State = {
        tabs: {
          tab1: { id: 'tab1', pinned: false, locked: false, createdAt: 1000, layout: tmux('t', 'terminal') },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      const migrated = migrateTabStore(v3State, 3)
      expect(migrated).toBe(v3State)
    })

    it('tolerates a tmux-session leaf with no mode at all', () => {
      const v2State = {
        tabs: {
          tab1: {
            id: 'tab1', pinned: false, locked: false, createdAt: 1000,
            layout: { type: 'leaf' as const, pane: { id: 'p', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'x', cachedName: 'x', tmuxInstance: '' } } },
          },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      expect(() => migrateTabStore(v2State, 2)).not.toThrow()
      expect(migrateTabStore(v2State, 2).tabs.tab1.layout.pane.content.mode).toBe('terminal')
    })
  })
})
