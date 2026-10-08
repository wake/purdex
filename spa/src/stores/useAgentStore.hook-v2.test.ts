// spa/src/stores/useAgentStore.hook-v2.test.ts — U1-3a: the transition rule for daemon `hook` frames
// (applyHookEvent), the complete-host replace (applyAgentSnapshot) and the worker projection's rule staying put.
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore, backgroundOf, type NormalizedEvent } from './useAgentStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneLayout, Tab } from '../types/tab'

const H = 'h1'
const key = (code: string) => `${H}:${code}`
const st = () => useAgentStore.getState()

const ev = (status: string, extra: Partial<NormalizedEvent> = {}): NormalizedEvent =>
  ({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, ...extra })
const hook = (code: string, e: NormalizedEvent) => st().applyHookEvent(H, code, e)
const seed = (code: string, status: 'idle' | 'running' | 'waiting' | 'error') =>
  useAgentStore.setState({ statuses: { ...st().statuses, [key(code)]: status } })

const leaf = (id: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId: H, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })
/** Make `code` visible (a pane of the active tab) or not (an active tab that does not show it). */
const visible = (code: string | null) =>
  useTabStore.setState({ tabs: { t1: tab('t1', code ? leaf('p1', code) : blank('p1')) }, activeTabId: 't1' })

beforeEach(() => {
  useAgentStore.setState({
    statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {},
  })
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
  visible(null)
})

describe('applyHookEvent — same status never marks unread', () => {
  it('idle → idle when only the representative pane or model changed', () => {
    seed('dev', 'idle')
    hook('dev', ev('idle', { model: 'sonnet' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('idle → idle when only background changed', () => {
    seed('dev', 'idle')
    hook('dev', ev('idle', { background: 'monitor' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('running (hook) → running (mod)', () => {
    seed('dev', 'running')
    hook('dev', ev('running', { source: 'mod' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('idle (hook) → idle (mod): the end of a turn shows two idles', () => {
    seed('dev', 'running')
    hook('dev', ev('idle', { source: 'hook' }))
    useAgentStore.setState({ unread: {} }) // the first idle is the real transition
    hook('dev', ev('idle', { source: 'mod' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('25 repeated subagent idles', () => {
    seed('dev', 'idle')
    for (let i = 0; i < 25; i++) hook('dev', ev('idle', { raw_event_name: 'PdxPostToolUse' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('a PdxSubagentStop idle on an idle code', () => {
    seed('dev', 'idle')
    hook('dev', ev('idle', { raw_event_name: 'PdxSubagentStop', detail: { agent_id: 'a1' } }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it.each(['waiting', 'error'] as const)('%s → %s (a repeated frame)', (s) => {
    seed('dev', s)
    hook('dev', ev(s))
    expect(st().unread[key('dev')]).toBeUndefined()
  })
})

describe('applyHookEvent — a real transition marks unread', () => {
  it.each([
    ['running', 'idle'],
    ['running', 'waiting'],
    ['waiting', 'error'],
    ['idle', 'waiting'],
  ] as const)('%s → %s: unread when not visible, not when visible', (from, to) => {
    seed('dev', from)
    hook('dev', ev(to))
    expect(st().unread[key('dev')]).toBe(true)

    useAgentStore.setState({ unread: {} })
    seed('dev', from)
    visible('dev')
    hook('dev', ev(to))
    expect(st().unread[key('dev')]).toBeUndefined()
  })
})

describe('applyHookEvent — unknown previous status never marks unread', () => {
  it.each(['idle', 'waiting', 'error'])('first sight of a code with %s', (s) => {
    hook('dev', ev(s))
    expect(st().statuses[key('dev')]).toBe(s)
    expect(st().unread[key('dev')]).toBeUndefined()
  })
})

describe('applyHookEvent — kept exclusions and side effects', () => {
  it('a Notification idle and a silent Stop keep their exclusions', () => {
    seed('dev', 'running')
    hook('dev', ev('idle', { raw_event_name: 'PdxNotification' }))
    expect(st().unread[key('dev')]).toBeUndefined()
    seed('dev', 'running')
    hook('dev', ev('idle', { detail: { notification_silent: true } }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('running clears a leftover unread', () => {
    seed('dev', 'idle')
    useAgentStore.setState({ unread: { [key('dev')]: true } })
    hook('dev', ev('running', { raw_event_name: 'PdxUserPromptSubmit' }))
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('clear wipes the code', () => {
    seed('dev', 'idle')
    useAgentStore.setState({ agentTypes: { [key('dev')]: 'cc' }, unread: { [key('dev')]: true } })
    hook('dev', ev('clear'))
    expect(st().statuses[key('dev')]).toBeUndefined()
    expect(st().agentTypes[key('dev')]).toBeUndefined()
    expect(st().unread[key('dev')]).toBeUndefined()
  })

  it('model is kept when an event carries none', () => {
    hook('dev', ev('idle', { model: 'opus' }))
    hook('dev', ev('idle'))
    expect(st().models[key('dev')]).toBe('opus')
  })

  it('background is stored in lastEvents', () => {
    hook('dev', ev('idle', { background: 'schedule' }))
    expect(backgroundOf(st().lastEvents[key('dev')])).toBe('schedule')
  })

  it('the subagents presence rule is unchanged (absent keeps, [] removes)', () => {
    const ref = { id: 's1', type: 'cc', started_at: 0, source_pid: 0, source_start_time: '' }
    hook('dev', ev('running', { subagents: [ref] }))
    expect(st().subagents[key('dev')]).toHaveLength(1)
    hook('dev', ev('running'))
    expect(st().subagents[key('dev')]).toHaveLength(1)
    hook('dev', ev('running', { subagents: [] }))
    expect(st().subagents[key('dev')]).toBeUndefined()
  })
})

describe('handleNormalizedEvent — the worker projection keeps its own rule', () => {
  it('waiting → waiting with a new request still marks unread', () => {
    seed('exec-e1', 'waiting')
    st().handleNormalizedEvent(H, 'exec-e1', ev('waiting', { raw_event_name: 'request' }))
    expect(st().unread[key('exec-e1')]).toBe(true)
  })
})

describe('applyAgentSnapshot — replaces the host', () => {
  const entry = (session: string, e: NormalizedEvent) => ({ session, event: { ...e, raw_event_name: 'replay', snapshot: true } })

  it('applies the listed codes and clears an absent code that holds state', () => {
    seed('gone', 'idle')
    st().applyAgentSnapshot(H, [entry('a', ev('idle')), entry('b', ev('running'))])
    expect(st().statuses[key('a')]).toBe('idle')
    expect(st().statuses[key('b')]).toBe('running')
    expect(st().statuses[key('gone')]).toBeUndefined()
  })

  it('leaves an absent code that has only an OSC title, an exec- code and another host alone', () => {
    useAgentStore.setState({
      oscTitles: { [key('shell')]: 'vim' },
      statuses: { [key('exec-e1')]: 'running', 'h2:x': 'idle' },
    })
    st().applyAgentSnapshot(H, [entry('a', ev('idle'))])
    expect(st().oscTitles[key('shell')]).toBe('vim')
    expect(st().statuses[key('exec-e1')]).toBe('running')
    expect(st().statuses['h2:x']).toBe('idle')
  })

  it('an empty snapshot clears the host\'s agent codes', () => {
    seed('a', 'idle')
    seed('b', 'running')
    st().applyAgentSnapshot(H, [])
    expect(st().statuses[key('a')]).toBeUndefined()
    expect(st().statuses[key('b')]).toBeUndefined()
  })

  it('is one set (one render)', () => {
    seed('gone', 'idle')
    let calls = 0
    const unsub = useAgentStore.subscribe(() => { calls++ })
    st().applyAgentSnapshot(H, [entry('a', ev('idle')), entry('b', ev('waiting')), entry('c', ev('running'))])
    unsub()
    expect(calls).toBe(1)
  })

  it('unread follows the transition rule per entry', () => {
    seed('known', 'running')
    seed('same', 'idle')
    useAgentStore.setState({ unread: { [key('same')]: true } })
    st().applyAgentSnapshot(H, [
      entry('known', ev('idle')),   // running → idle while disconnected: news
      entry('fresh', ev('idle')),   // first sight: not news
      entry('same', ev('idle')),    // unchanged: not news, existing unread kept
    ])
    expect(st().unread[key('known')]).toBe(true)
    expect(st().unread[key('fresh')]).toBeUndefined()
    expect(st().unread[key('same')]).toBe(true)
  })

  it('an entry without a provenance record flags an unverified agent like a replay hook frame', () => {
    const t = createTab({ kind: 'tmux-session', hostId: H, sessionCode: 'abc123', mode: 'terminal', cachedName: 'dev', tmuxInstance: '222:2000' })
    useTabStore.setState({ tabs: { [t.id]: t }, tabOrder: [t.id], activeTabId: t.id })
    useTabStore.getState().setPaneRebuild(H, 'abc123', '222:2000', {
      kind: 'agent-group', ordered: false,
      record: { tmuxInstance: '222:2000', capturedAt: 1, agent: { type: 'codex', sessionId: 'S1', updatedAt: 1 } },
    })
    st().applyAgentSnapshot(H, [entry('abc123', ev('idle', { agent_type: 'cc' }))])
    const l = useTabStore.getState().tabs[t.id].layout
    const rebuild = l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
    expect(rebuild?.unverified).toBe(true)
  })
})

describe('backgroundOf', () => {
  it('knows the three kinds and ignores everything else', () => {
    for (const k of ['workflow', 'monitor', 'schedule'] as const) expect(backgroundOf(ev('idle', { background: k }))).toBe(k)
    expect(backgroundOf(ev('idle', { background: '' }))).toBeUndefined()
    expect(backgroundOf(ev('idle'))).toBeUndefined()
    expect(backgroundOf(undefined)).toBeUndefined()
    expect(backgroundOf({ ...ev('idle'), background: 'teleport' as unknown as 'monitor' })).toBeUndefined()
  })
})
