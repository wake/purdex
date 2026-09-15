import { describe, expect, it } from 'vitest'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import { paneKey, sourceKey, tabKey } from './identity'

const NO_WS: ReadonlyMap<string, string> = new Map()

function leaf(id: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id, content } }
}

function split(...children: PaneLayout[]): PaneLayout {
  return { type: 'split', id: 's', direction: 'h', sizes: children.map(() => 100 / children.length), children }
}

function tabOf(layout: PaneLayout): Tab {
  return { id: 't1', pinned: false, locked: false, createdAt: 0, layout }
}

const tk = (layout: PaneLayout, ws: ReadonlyMap<string, string> = NO_WS) => tabKey(tabOf(layout), ws)

describe('sourceKey', () => {
  it('encodes daemon, local and inapp sources', () => {
    expect(sourceKey({ type: 'daemon', hostId: 'h1' })).toBe('daemon:h1')
    expect(sourceKey({ type: 'local' })).toBe('local')
    expect(sourceKey({ type: 'inapp' })).toBe('inapp')
  })
})

describe('paneKey', () => {
  it('tmux-session → ["tmux", hostId, cachedName]', () => {
    expect(
      paneKey(
        { kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc', mode: 'terminal', cachedName: 'dev', tmuxInstance: 'i1' },
        NO_WS,
      ),
    ).toBe(JSON.stringify(['tmux', 'h1', 'dev']))
  })

  it('editor → ["editor", sourceKey, filePath]', () => {
    expect(paneKey({ kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/a.ts' }, NO_WS)).toBe(
      JSON.stringify(['editor', 'daemon:h1', '/a.ts']),
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

  it('image-preview and pdf-preview key by kind, source and path', () => {
    expect(paneKey({ kind: 'image-preview', source: { type: 'local' }, filePath: '/p.png' }, NO_WS)).toBe(
      JSON.stringify(['image-preview', 'local', '/p.png']),
    )
    expect(paneKey({ kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/d.pdf' }, NO_WS)).toBe(
      JSON.stringify(['pdf-preview', 'inapp', '/d.pdf']),
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

  it('browser keys by url', () => {
    expect(paneKey({ kind: 'browser', url: 'https://example.com/' }, NO_WS)).toBe(
      JSON.stringify(['browser', 'https://example.com/']),
    )
  })

  it('execution keys by host and id, host defaults to empty', () => {
    expect(paneKey({ kind: 'execution', executionId: 'e1', host: 'h1' }, NO_WS)).toBe(
      JSON.stringify(['execution', 'h1', 'e1']),
    )
    expect(paneKey({ kind: 'execution', executionId: 'e1' }, NO_WS)).toBe(JSON.stringify(['execution', '', 'e1']))
  })

  it('settings global and workspace scopes', () => {
    const ws = new Map([['w1', 'Work']])
    expect(paneKey({ kind: 'settings', scope: 'global' }, NO_WS)).toBe(JSON.stringify(['settings', 'global']))
    expect(paneKey({ kind: 'settings', scope: { workspaceId: 'w1' } }, ws)).toBe(
      JSON.stringify(['settings', 'ws', 'Work']),
    )
  })

  it('settings dangling workspace scope keys as global', () => {
    const ws = new Map([['w1', 'Work']])
    expect(paneKey({ kind: 'settings', scope: { workspaceId: 'gone' } }, ws)).toBe(
      paneKey({ kind: 'settings', scope: 'global' }, NO_WS),
    )
  })

  it('settings workspace name is trimmed', () => {
    const a = paneKey({ kind: 'settings', scope: { workspaceId: 'w1' } }, new Map([['w1', ' Work ']]))
    const b = paneKey({ kind: 'settings', scope: { workspaceId: 'w2' } }, new Map([['w2', 'Work']]))
    expect(a).toBe(b)
  })

  it('settings workspace name empty after trim keys as global', () => {
    expect(paneKey({ kind: 'settings', scope: { workspaceId: 'w1' } }, new Map([['w1', '   ']]))).toBe(
      paneKey({ kind: 'settings', scope: 'global' }, NO_WS),
    )
  })

  it.each(['new-tab', 'dashboard', 'hosts', 'history', 'memory-monitor', 'editor-buffers'] as const)(
    '%s → [kind]',
    (kind) => {
      expect(paneKey({ kind } as PaneContent, NO_WS)).toBe(JSON.stringify([kind]))
    },
  )

  it('tmux host/name containing ":" does not collide', () => {
    const tmux = (hostId: string, cachedName: string): PaneContent => ({
      kind: 'tmux-session',
      hostId,
      sessionCode: 'x',
      mode: 'terminal',
      cachedName,
      tmuxInstance: 'i',
    })
    expect(paneKey(tmux('a:b', 'c'), NO_WS)).not.toBe(paneKey(tmux('a', 'b:c'), NO_WS))
  })

  it('editor path containing ":" does not collide across sources', () => {
    const a = paneKey({ kind: 'editor', source: { type: 'daemon', hostId: 'h' }, filePath: 'x:/y' }, NO_WS)
    const b = paneKey({ kind: 'editor', source: { type: 'daemon', hostId: 'h:x' }, filePath: '/y' }, NO_WS)
    expect(a).not.toBe(b)
  })
})

describe('tabKey', () => {
  it('single-leaf tab → array of the one pane key', () => {
    expect(tk(leaf('p1', { kind: 'browser', url: 'u' }))).toBe(
      JSON.stringify([paneKey({ kind: 'browser', url: 'u' }, NO_WS)]),
    )
  })

  it('split tab lists leaf keys in pre-order (left-to-right, nested)', () => {
    const layout = split(
      leaf('p1', { kind: 'dashboard' }),
      split(leaf('p2', { kind: 'hosts' }), leaf('p3', { kind: 'history' })),
      leaf('p4', { kind: 'browser', url: 'u' }),
    )
    const expected = (
      [{ kind: 'dashboard' }, { kind: 'hosts' }, { kind: 'history' }, { kind: 'browser', url: 'u' }] as PaneContent[]
    ).map((c) => paneKey(c, NO_WS))
    expect(tk(layout)).toBe(JSON.stringify(expected))
  })

  it('any null leaf makes the tab key null', () => {
    const layout = split(
      leaf('p1', { kind: 'dashboard' }),
      leaf('p2', {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'u1',
        untitled: { name: 'Untitled', suggestedExtension: '.md', hasBeenRenamed: false },
      }),
    )
    expect(tk(layout)).toBeNull()
  })

  it('browser url containing "|" does not collide with a split', () => {
    const single = tk(leaf('p1', { kind: 'browser', url: 'a|dashboard' }))
    const splitTab = tk(split(leaf('p1', { kind: 'browser', url: 'a' }), leaf('p2', { kind: 'dashboard' })))
    expect(single).not.toBe(splitTab)
  })

  it('editor path containing "|" does not collide with a split', () => {
    const single = tk(leaf('p1', { kind: 'editor', source: { type: 'local' }, filePath: '/a|hosts' }))
    const splitTab = tk(
      split(leaf('p1', { kind: 'editor', source: { type: 'local' }, filePath: '/a' }), leaf('p2', { kind: 'hosts' })),
    )
    expect(single).not.toBe(splitTab)
  })
})
