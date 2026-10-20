// spa/src/lib/task-groups/session-actions.test.ts — the sidebar's callers of the manual group actions get the root with `resolveRoot` (TG-2b): a cache miss reads the session's host, and only when the host cannot
// answer either is it `no-root` (the group store is not touched then).
import { describe, it, expect, vi, beforeEach } from 'vitest'

const readFirstPage = vi.fn()
vi.mock('../team/unattended-reader', () => ({ readFirstPage: (...a: unknown[]) => readFirstPage(...a), refreshHost: (...a: unknown[]) => readFirstPage(...a) }))

import { fileSession, startGroupWithSession, takeSessionOut, type SessionRef } from './session-actions'
import { useRootCacheStore } from './root-cache'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useHostStore } from '../../stores/useHostStore'
import type { SessionQuota } from '../team/types'

const q = (session_id: string, root_session_id: string): SessionQuota => ({ session_id, root_session_id, title: '', address: `mlab/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })
const ref = (over: Partial<SessionRef> = {}): SessionRef => ({ hostId: 'h1', agentSessionId: 'AG1', tabId: 't1', ...over })
const GROUP = { name: 'G', members: [], createdAt: 1 }
/** t1: a tab showing the session `agent` (null = none recorded) on h1, in the group g1 by its own `group`. */
const tabOf = (agent: string | null, hostId = 'h1') => ({
  id: 't1', group: 'group\u0000g1',
  layout: { type: 'leaf', pane: { id: 'p1', content: { kind: 'tmux-session', hostId, sessionCode: 'S1', mode: 'terminal', cachedName: 'n', tmuxInstance: 'I', ...(agent ? { rebuild: { agent: { sessionId: agent } } } : {}) } } },
}) as never

beforeEach(() => {
  readFirstPage.mockReset()
  useRootCacheStore.setState({ roots: {} })
  useUnattendedStore.setState({ byHost: { h1: { quotaSupport: 'yes' } } as never })
  useHostStore.setState({ hosts: {}, runtime: {} })
  useManualGroupsStore.setState({ order: ['g1', 'g2'], groups: { g1: { ...GROUP }, g2: { ...GROUP, name: 'G2' } } })
  useTabStore.setState({ tabs: { t1: tabOf('AG1') } })
})

describe('fileSession', () => {
  it('uses the cached root without asking the host', async () => {
    useRootCacheStore.getState().ingest('h1', [q('AG1', 'ROOT')])
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: true })
    expect(readFirstPage).not.toHaveBeenCalled()
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h1', root: 'ROOT' }])
  })

  it('a cache miss reads the session\'s host, and files the root the host lists', async () => {
    readFirstPage.mockResolvedValue({ quotas: [q('AG1', 'ROOT')] })
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: true })
    expect(readFirstPage).toHaveBeenCalledWith('h1')
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h1', root: 'ROOT' }])
    expect(useTabStore.getState().tabs.t1.group).toBeUndefined() // a member now: the tab's own `group` goes
  })

  it('is no-root only when the host cannot answer either — and nothing is written', async () => {
    readFirstPage.mockRejectedValue(new Error('down'))
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: false, reason: 'no-root' })
    readFirstPage.mockResolvedValue({ quotas: [q('OTHER', 'X')] })
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: false, reason: 'no-root' })
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([])
    expect(useTabStore.getState().tabs.t1.group).toBe('group\u0000g1')
  })

  it('a pane that recorded no agent session id has no root to look for: no-root, and the host is not asked', async () => {
    useTabStore.setState({ tabs: { t1: tabOf(null) } })
    expect(await fileSession(ref({ agentSessionId: null }), 'g1')).toEqual({ ok: false, reason: 'no-root' })
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('a session of ANY host goes into any group', async () => {
    useUnattendedStore.setState({ byHost: { h1: { quotaSupport: 'yes' }, h2: { quotaSupport: 'yes' } } as never })
    useTabStore.setState({ tabs: { t1: tabOf('AG2', 'h2') } })
    readFirstPage.mockResolvedValue({ quotas: [q('AG2', 'R2')] })
    expect(await fileSession(ref({ hostId: 'h2', agentSessionId: 'AG2' }), 'g1')).toEqual({ ok: true })
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h2', root: 'R2' }])
  })
})

describe('startGroupWithSession', () => {
  it('starts a group with the session, reading the host on a cache miss', async () => {
    readFirstPage.mockResolvedValue({ quotas: [q('AG1', 'ROOT')] })
    const r = await startGroupWithSession(ref(), 'New')
    expect(r.ok).toBe(true)
    const { order, groups } = useManualGroupsStore.getState()
    expect(order).toHaveLength(3)
    expect(groups[order[2]]).toMatchObject({ name: 'New', members: [{ host: 'h1', root: 'ROOT' }] })
  })

  it('is no-root when the host cannot answer; no group is made', async () => {
    readFirstPage.mockRejectedValue(new Error('down'))
    expect(await startGroupWithSession(ref(), 'New')).toEqual({ ok: false, reason: 'no-root' })
    expect(useManualGroupsStore.getState().order).toEqual(['g1', 'g2'])
  })
})

describe('takeSessionOut', () => {
  it('removes the member and clears the tab\'s group', async () => {
    useManualGroupsStore.setState({ groups: { g1: { ...GROUP, members: [{ host: 'h1', root: 'ROOT' }] }, g2: { ...GROUP } } })
    readFirstPage.mockResolvedValue({ quotas: [q('AG1', 'ROOT')] })
    expect(await takeSessionOut(ref())).toEqual({ ok: true })
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([])
    expect(useTabStore.getState().tabs.t1.group).toBeUndefined()
  })

  it('without a root it touches nothing (the member could not be named)', async () => {
    readFirstPage.mockRejectedValue(new Error('down'))
    expect(await takeSessionOut(ref())).toEqual({ ok: false, reason: 'no-root' })
    expect(useTabStore.getState().tabs.t1.group).toBe('group\u0000g1')
  })
})

describe('the tab changes while the root is being got (commit-time check)', () => {
  /** Starts `action` with the host slow to answer; `change` runs while it waits; then the host answers. */
  const duringRead = async <T,>(action: () => Promise<T>, change: () => void): Promise<T> => {
    let answer: (v: unknown) => void = () => {}
    readFirstPage.mockReturnValue(new Promise((r) => { answer = r }))
    const pending = action()
    change()
    answer({ quotas: [q('AG1', 'ROOT')] })
    return pending
  }
  const untouched = () => {
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([])
    expect(useManualGroupsStore.getState().order).toEqual(['g1', 'g2'])
  }
  const close = () => useTabStore.setState({ tabs: {} })
  const rebind = () => useTabStore.setState({ tabs: { t1: tabOf('AG-OTHER') } })

  it.each([['closed', close], ['rebound to another session', rebind]])('fileSession: the tab %s meanwhile — refused, nothing written, the tab keeps its own group', async (_n, change) => {
    expect(await duringRead(() => fileSession(ref(), 'g1'), change)).toEqual({ ok: false, reason: 'tab-changed' })
    untouched()
    if (change === rebind) expect(useTabStore.getState().tabs.t1.group).toBe('group\u0000g1')
  })

  it.each([['closed', close], ['rebound to another session', rebind]])('startGroupWithSession: the tab %s meanwhile — no group is made', async (_n, change) => {
    expect(await duringRead(() => startGroupWithSession(ref(), 'New'), change)).toEqual({ ok: false, reason: 'tab-changed' })
    untouched()
  })

  it.each([['closed', close], ['rebound to another session', rebind]])('takeSessionOut: the tab %s meanwhile — the member stays and the tab keeps its group', async (_n, change) => {
    useManualGroupsStore.setState({ groups: { g1: { ...GROUP, members: [{ host: 'h1', root: 'ROOT' }] }, g2: { ...GROUP } } })
    expect(await duringRead(() => takeSessionOut(ref()), change)).toEqual({ ok: false, reason: 'tab-changed' })
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h1', root: 'ROOT' }])
    if (change === rebind) expect(useTabStore.getState().tabs.t1.group).toBe('group\u0000g1')
  })
})

describe('the host is re-pointed while the root is being got (cache hit or read)', () => {
  const host = (ip: string) => ({ id: 'h1', name: 'h1', ip, port: 7860, token: 't' })
  beforeEach(() => { useHostStore.setState({ hosts: { h1: host('10.0.0.1') } as never, runtime: {} }) })
  const repoint = () => useHostStore.setState({ hosts: { h1: host('10.0.0.9') } as never })

  it.each([
    ['fileSession', (s: SessionRef) => fileSession(s, 'g1')],
    ['startGroupWithSession', (s: SessionRef) => startGroupWithSession(s, 'New')],
    ['takeSessionOut', (s: SessionRef) => takeSessionOut(s)],
  ])('%s: a cached root of the old daemon is not written once the host moved', async (_n, action) => {
    useManualGroupsStore.setState({ groups: { g1: { ...GROUP, members: [{ host: 'h1', root: 'OLD' }] }, g2: { ...GROUP } } })
    useRootCacheStore.getState().ingest('h1', [q('AG1', 'OLD')])
    const pending = action(ref()) // the cache answers, but only after a turn
    repoint()
    expect(await pending).toEqual({ ok: false, reason: 'tab-changed' })
    expect(useManualGroupsStore.getState().order).toEqual(['g1', 'g2'])
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h1', root: 'OLD' }])
    expect(useTabStore.getState().tabs.t1.group).toBe('group\u0000g1')
  })
})

describe('a session that ended (TG-5c, spec 3: a tab\'s agent session is live or ended)', () => {
  const ended = () => {
    const t = tabOf('AG1') as { layout: { pane: { content: Record<string, unknown> } } }
    t.layout.pane.content.terminated = 'tmux-restarted'
    return t as never
  }
  it('can still be filed, started from and taken out: the tab still stands for the session', async () => {
    useRootCacheStore.getState().ingest('h1', [q('AG1', 'ROOT')])
    useTabStore.setState({ tabs: { t1: ended() } })
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: true })
    expect(useManualGroupsStore.getState().groups.g1.members).toEqual([{ host: 'h1', root: 'ROOT' }])
    expect(await takeSessionOut(ref())).toEqual({ ok: true })
    expect((await startGroupWithSession(ref(), 'New')).ok).toBe(true)
  })
  it('is the first pane with an agent session, not the first live pane: a live pane with another session does not stand in', async () => {
    useRootCacheStore.getState().ingest('h1', [q('AG1', 'ROOT')])
    const t = ended() as { layout: unknown }
    const live = { id: 'p2', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S2', mode: 'terminal', cachedName: 'n', tmuxInstance: 'I', rebuild: { agent: { sessionId: 'AG2' } } } }
    useTabStore.setState({ tabs: { t1: { ...t, layout: { type: 'split', direction: 'h', sizes: [50, 50], children: [t.layout, { type: 'leaf', pane: live }] } } as never } })
    expect(await fileSession(ref(), 'g1')).toEqual({ ok: true }) // AG1 (ended) is still the tab's session
    expect(await fileSession(ref({ agentSessionId: 'AG2' }), 'g1')).toEqual({ ok: false, reason: 'tab-changed' })
  })
})
