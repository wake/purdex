import { describe, expect, it } from 'vitest'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import { paneKey, sourceKey, tabKey } from './identity'

const NO_WS: ReadonlyMap<string, string> = new Map()

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function tabOf(layout: PaneLayout): Tab {
  return { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
}

describe('sourceKey', () => {
  it('encodes daemon, local and inapp sources', () => {
    expect(sourceKey({ type: 'daemon', hostId: 'h1' })).toBe('daemon:h1')
    expect(sourceKey({ type: 'local' })).toBe('local')
    expect(sourceKey({ type: 'inapp' })).toBe('inapp')
  })
})

describe('paneKey', () => {
  it('tmux-session → tmux:<hostId>:<cachedName>', () => {
    expect(
      paneKey(
        { kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc', mode: 'terminal', cachedName: 'dev', tmuxInstance: 'i1' },
        NO_WS,
      ),
    ).toBe('tmux:h1:dev')
  })

  it('editor → editor:<sourceKey>:<filePath>', () => {
    expect(paneKey({ kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/a.ts' }, NO_WS)).toBe(
      'editor:daemon:h1:/a.ts',
    )
  })

  it('untitled editor → null', () => {
    expect(
      paneKey(
        {
          kind: 'editor',
          source: { type: 'inapp' },
          filePath: 'untitled-1',
          untitled: { name: 'Untitled', suggestedExtension: '.txt', hasBeenRenamed: false },
        },
        NO_WS,
      ),
    ).toBeNull()
  })

  it('image-preview → image-preview:<sourceKey>:<filePath>', () => {
    expect(paneKey({ kind: 'image-preview', source: { type: 'local' }, filePath: '/p.png' }, NO_WS)).toBe(
      'image-preview:local:/p.png',
    )
  })

  it('pdf-preview → pdf-preview:<sourceKey>:<filePath>', () => {
    expect(paneKey({ kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/d.pdf' }, NO_WS)).toBe(
      'pdf-preview:inapp:/d.pdf',
    )
  })

  it('same filePath on daemon / local / inapp sources yields distinct keys', () => {
    const keys = new Set([
      paneKey({ kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/x' }, NO_WS),
      paneKey({ kind: 'editor', source: { type: 'local' }, filePath: '/x' }, NO_WS),
      paneKey({ kind: 'editor', source: { type: 'inapp' }, filePath: '/x' }, NO_WS),
    ])
    expect(keys.size).toBe(3)
  })

  it('browser → browser:<url>', () => {
    expect(paneKey({ kind: 'browser', url: 'https://example.com/' }, NO_WS)).toBe('browser:https://example.com/')
  })

  it('execution → execution:<host>:<executionId>, host defaults to empty', () => {
    expect(paneKey({ kind: 'execution', executionId: 'e1', host: 'h1' }, NO_WS)).toBe('execution:h1:e1')
    expect(paneKey({ kind: 'execution', executionId: 'e1' }, NO_WS)).toBe('execution::e1')
  })

  it('settings global → settings:global', () => {
    expect(paneKey({ kind: 'settings', scope: 'global' }, NO_WS)).toBe('settings:global')
  })

  it('settings workspace scope resolves to the workspace name', () => {
    const ws = new Map([['w1', 'Work']])
    expect(paneKey({ kind: 'settings', scope: { workspaceId: 'w1' } }, ws)).toBe('settings:ws:Work')
  })

  it('settings dangling workspace scope → settings:global', () => {
    const ws = new Map([['w1', 'Work']])
    expect(paneKey({ kind: 'settings', scope: { workspaceId: 'gone' } }, ws)).toBe('settings:global')
  })

  it.each(['new-tab', 'dashboard', 'hosts', 'history', 'memory-monitor', 'editor-buffers'] as const)(
    '%s → <kind>',
    (kind) => {
      expect(paneKey({ kind } as PaneContent, NO_WS)).toBe(kind)
    },
  )
})

describe('tabKey', () => {
  it('single-leaf tab → the pane key', () => {
    expect(tabKey(tabOf(leaf('p1', { kind: 'browser', url: 'u' })), NO_WS)).toBe('browser:u')
  })

  it('split tab joins leaf keys in pre-order (left-to-right, nested) with |', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      sizes: [50, 50],
      children: [
        leaf('p1', { kind: 'dashboard' }),
        {
          type: 'split',
          id: 's2',
          direction: 'v',
          sizes: [50, 50],
          children: [leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })],
        },
        leaf('p4', { kind: 'browser', url: 'u' }),
      ],
    }
    expect(tabKey(tabOf(layout), NO_WS)).toBe('dashboard|hosts|history|browser:u')
  })

  it('any null leaf makes the tab key null', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      sizes: [50, 50],
      children: [
        leaf('p1', { kind: 'dashboard' }),
        leaf('p2', {
          kind: 'editor',
          source: { type: 'inapp' },
          filePath: 'u1',
          untitled: { name: 'Untitled', suggestedExtension: '.md', hasBeenRenamed: false },
        }),
      ],
    }
    expect(tabKey(tabOf(layout), NO_WS)).toBeNull()
  })
})
