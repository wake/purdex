// spa/src/stores/useTabStore.hostRefs.test.ts — `rewritePaneHosts`, the tab-store half of the host re-resolve pass
// (host ownership spec §3.3, plan H1b T1).
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const tmux = (hostId: string): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i' })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })

const WIRE = 'd1_aaaaaaaaaaaaaaaa'
const map = (id: string) => (id === WIRE ? 'local1' : id)

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
})

describe('useTabStore.rewritePaneHosts', () => {
  it('maps tmux-session.hostId, a daemon file source and execution.host in every tab; nested splits included', () => {
    const split: PaneLayout = {
      type: 'split', id: 's', direction: 'h', sizes: [30, 70],
      children: [
        leaf('p1', tmux(WIRE)),
        { type: 'split', id: 's2', direction: 'v', sizes: [50, 50], children: [
          leaf('p2', { kind: 'editor', source: { type: 'daemon', hostId: WIRE }, filePath: '/a' }),
          leaf('p3', { kind: 'execution', executionId: 'e1', host: WIRE }),
        ] },
      ],
    }
    useTabStore.setState({ tabs: { t1: tab('t1', split), t2: tab('t2', leaf('p4', { kind: 'image-preview', source: { type: 'daemon', hostId: WIRE }, filePath: '/i.png' })) }, tabOrder: ['t1', 't2'] })
    useTabStore.getState().rewritePaneHosts(map)
    const json = JSON.stringify(useTabStore.getState().tabs)
    expect(json).not.toContain(WIRE)
    expect(json.match(/local1/g)).toHaveLength(4)
    const t1 = useTabStore.getState().tabs.t1.layout as Extract<PaneLayout, { type: 'split' }>
    expect(t1.sizes).toEqual([30, 70])
  })

  it('never maps an empty execution.host, a local source or another kind', () => {
    const tabs = {
      t1: tab('t1', leaf('p1', { kind: 'execution', executionId: 'e', host: '' })),
      t2: tab('t2', leaf('p2', { kind: 'editor', source: { type: 'local' }, filePath: '/a' })),
      t3: tab('t3', leaf('p3', { kind: 'browser', url: 'x' })),
    }
    useTabStore.setState({ tabs, tabOrder: ['t1', 't2', 't3'] })
    const before = useTabStore.getState()
    useTabStore.getState().rewritePaneHosts((id) => (id === '' ? 'local1' : map(id)))
    expect(useTabStore.getState()).toBe(before)
  })

  it('returns the same state — and the same untouched tabs — when nothing changes', () => {
    const t1 = tab('t1', leaf('p1', tmux('local1')))
    const t2 = tab('t2', leaf('p2', tmux(WIRE)))
    useTabStore.setState({ tabs: { t1 }, tabOrder: ['t1'] })
    const before = useTabStore.getState()
    useTabStore.getState().rewritePaneHosts(map)
    expect(useTabStore.getState()).toBe(before)

    useTabStore.setState({ tabs: { t1, t2 }, tabOrder: ['t1', 't2'] })
    useTabStore.getState().rewritePaneHosts(map)
    expect(useTabStore.getState().tabs.t1).toBe(t1)
    expect(useTabStore.getState().tabs.t2).not.toBe(t2)
  })
})
