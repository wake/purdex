// spa/src/stores/useAgentStore.readsync.test.ts — read sync on the Mac (app-badge spec §7.2, §7.3): the store keeps, per
// unread key, the hook cursor of the event that last set it; `markRead` hands back what was captured at the
// unread -> read transition; a read received from the daemon clears an unread by the cover rule; and an actionable event
// of an agent that is on screen (so never marked unread) is reported as read through its cursor.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createTab } from '../types/tab'
import { useAgentStore, type NormalizedEvent } from './useAgentStore'
import { useTabStore } from './useTabStore'

vi.mock('../lib/read-sync/reporter', () => ({ reportRead: vi.fn() }))
import { reportRead } from '../lib/read-sync/reporter'

const H = 'h1'
const key = (code: string) => `${H}:${code}`

const ev = (status: string, over: Partial<NormalizedEvent> = {}): NormalizedEvent => ({
  agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, ...over,
})
/** An actionable event of `code` with cursor (epoch, seq): running first so the idle that follows is a transition. */
function news(code: string, epoch: string, seq: number, status = 'idle', over: Partial<NormalizedEvent> = {}) {
  const s = useAgentStore.getState()
  if (s.statuses[key(code)] !== 'running') s.applyHookEvent(H, code, ev('running', { epoch, seq: Math.max(0, seq - 1) }))
  useAgentStore.getState().applyHookEvent(H, code, ev(status, { epoch, seq, ...over }))
}
const cursorOfKey = (code: string) => useAgentStore.getState().unreadCursor[key(code)]
const unread = (code: string) => useAgentStore.getState().unread[key(code)] === true

function showInActiveTab(code: string) {
  const tab = createTab({ kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'i' })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
}

beforeEach(() => {
  vi.mocked(reportRead).mockReset()
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {}, unreadCursor: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('the cursor of the event that last set an unread', () => {
  it('an actionable event sets the unread and records its (epoch, seq)', () => {
    news('aaaaaa', 'E1', 7)
    expect(unread('aaaaaa')).toBe(true)
    expect(cursorOfKey('aaaaaa')).toEqual({ epoch: 'E1', seq: 7 })
  })

  it('every event that sets the unread again replaces the cursor (the latest event wins)', () => {
    news('aaaaaa', 'E1', 7)
    news('aaaaaa', 'E1', 9, 'waiting')
    expect(cursorOfKey('aaaaaa')).toEqual({ epoch: 'E1', seq: 9 })
  })

  it('an event with no cursor (epoch "") sets the unread with no cursor recorded', () => {
    news('bbbbbb', '', 0)
    expect(unread('bbbbbb')).toBe(true)
    expect(cursorOfKey('bbbbbb')).toBeUndefined()
  })

  it('a later event with no cursor leaves NO cursor (the cursor of the event that LAST set the unread), not the old one', () => {
    news('aaaaaa', 'E1', 7) // idle: unread with cursor E1:7
    // straight to waiting (idle -> waiting is a transition; no `running` between, which would clear the unread itself)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('waiting', { epoch: '', seq: 0 }))
    expect(unread('aaaaaa')).toBe(true)
    expect(cursorOfKey('aaaaaa')).toBeUndefined()
  })

  it('an event that is not news (the status did not change) leaves the unread and its cursor alone', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('idle', { epoch: 'E1', seq: 12 }))
    expect(cursorOfKey('aaaaaa')).toEqual({ epoch: 'E1', seq: 7 })
  })

  it('an idle notification (not actionable) sets no unread and no cursor', () => {
    useAgentStore.getState().applyHookEvent(H, 'bbbbbb', ev('running', { epoch: 'E1', seq: 1 }))
    useAgentStore.getState().applyHookEvent(H, 'bbbbbb', ev('idle', { epoch: 'E1', seq: 2, raw_event_name: 'PdxNotification' }))
    expect(unread('bbbbbb')).toBe(false)
    expect(cursorOfKey('bbbbbb')).toBeUndefined()
  })

  it('running clears the unread and its cursor', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('running', { epoch: 'E1', seq: 8 }))
    expect(unread('aaaaaa')).toBe(false)
    expect(cursorOfKey('aaaaaa')).toBeUndefined()
  })

  it('clear, clearSession and removeHost take the cursor with the unread', () => {
    news('aaaaaa', 'E1', 7)
    news('bbbbbb', 'E1', 8)
    news('cccccc', 'E1', 9)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('clear', { epoch: 'E1', seq: 10 }))
    useAgentStore.getState().clearSession(H, 'bbbbbb')
    expect(Object.keys(useAgentStore.getState().unreadCursor)).toEqual([key('cccccc')])
    useAgentStore.getState().removeHost(H)
    expect(useAgentStore.getState().unreadCursor).toEqual({})
  })

  it('an agent.snapshot that no longer lists a code drops its unread and cursor', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyAgentSnapshot(H, [])
    expect(unread('aaaaaa')).toBe(false)
    expect(cursorOfKey('aaaaaa')).toBeUndefined()
  })
})

describe('markRead — what the unread -> read transition captured', () => {
  it('returns wasUnread and the cursor of that moment, and clears both', () => {
    news('aaaaaa', 'E1', 7)
    expect(useAgentStore.getState().markRead(H, 'aaaaaa')).toEqual({ wasUnread: true, cursor: { epoch: 'E1', seq: 7 } })
    expect(unread('aaaaaa')).toBe(false)
    expect(cursorOfKey('aaaaaa')).toBeUndefined()
  })

  it('an unread with no cursor is a transition with none', () => {
    news('aaaaaa', '', 0)
    expect(useAgentStore.getState().markRead(H, 'aaaaaa')).toEqual({ wasUnread: true, cursor: undefined })
  })

  it('a key that was already read is not a transition: nothing to send', () => {
    expect(useAgentStore.getState().markRead(H, 'aaaaaa')).toEqual({ wasUnread: false, cursor: undefined })
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().markRead(H, 'aaaaaa')
    expect(useAgentStore.getState().markRead(H, 'aaaaaa')).toEqual({ wasUnread: false, cursor: undefined })
  })

  it('markRead itself never reports (the caller decides: only a user read does)', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().markRead(H, 'aaaaaa')
    expect(reportRead).not.toHaveBeenCalled()
  })
})

describe('applyReads — a read received from the daemon', () => {
  it('clears an unread at or before T (same epoch)', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyReads(H, [{ code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }])
    expect(unread('aaaaaa')).toBe(false)
    expect(cursorOfKey('aaaaaa')).toBeUndefined()
  })

  it('leaves a later event unread, and an unread of another epoch', () => {
    news('aaaaaa', 'E1', 9)
    news('bbbbbb', 'E2', 1)
    useAgentStore.getState().applyReads(H, [
      { code: 'aaaaaa', through: { epoch: 'E1', seq: 8 } },
      { code: 'bbbbbb', through: { epoch: 'E1', seq: 99 } },
    ])
    expect(unread('aaaaaa')).toBe(true)
    expect(unread('bbbbbb')).toBe(true)
  })

  it('an unread with no cursor is cleared by any read of its code', () => {
    news('aaaaaa', '', 0)
    useAgentStore.getState().applyReads(H, [{ code: 'aaaaaa', through: { epoch: 'Z', seq: 0 } }])
    expect(unread('aaaaaa')).toBe(false)
  })

  it('a code with no unread, another code and another host are untouched', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.setState((s) => ({ unread: { ...s.unread, 'h2:aaaaaa': true } }))
    useAgentStore.getState().applyReads(H, [{ code: 'zzzzzz', through: { epoch: 'E1', seq: 99 } }])
    expect(unread('aaaaaa')).toBe(true)
    useAgentStore.getState().applyReads(H, [{ code: 'aaaaaa', through: { epoch: 'E1', seq: 99 } }])
    expect(useAgentStore.getState().unread['h2:aaaaaa']).toBe(true)
  })

  it('is idempotent, and an older read after a newer one changes nothing', () => {
    news('aaaaaa', 'E1', 7)
    const r = { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }
    useAgentStore.getState().applyReads(H, [r])
    const after = useAgentStore.getState().unread
    useAgentStore.getState().applyReads(H, [r])
    expect(useAgentStore.getState().unread).toBe(after) // no new state object when nothing changed
    news('aaaaaa', 'E1', 12)
    useAgentStore.getState().applyReads(H, [{ code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }])
    expect(unread('aaaaaa')).toBe(true)
  })

  it('a received read is never forwarded', () => {
    news('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyReads(H, [{ code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }])
    expect(reportRead).not.toHaveBeenCalled()
  })
})

describe('seen live — an actionable event of an agent that is on screen', () => {
  it('is not marked unread and is reported as read through the event\'s cursor', () => {
    showInActiveTab('aaaaaa')
    news('aaaaaa', 'E1', 7)
    expect(unread('aaaaaa')).toBe(false)
    expect(reportRead).toHaveBeenCalledExactlyOnceWith(H, 'aaaaaa', { epoch: 'E1', seq: 7 })
  })

  it('an event with no cursor is reported with none', () => {
    showInActiveTab('aaaaaa')
    news('aaaaaa', '', 0)
    expect(reportRead).toHaveBeenCalledExactlyOnceWith(H, 'aaaaaa', undefined)
  })

  // "On screen" for a device elsewhere means a person could actually see it: a minimized or covered window keeps its
  // active tab, so the tab store alone says nothing. Not marked unread here either way (unchanged); only the report
  // that clears OTHER devices' unread needs a visible window.
  it('a hidden window (minimized, covered, another Space) does not report: nobody saw the event', () => {
    const vis = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    try {
      showInActiveTab('aaaaaa')
      news('aaaaaa', 'E1', 7)
      expect(unread('aaaaaa')).toBe(false) // the existing local rule is untouched
      expect(reportRead).not.toHaveBeenCalled()
    } finally {
      vis.mockRestore()
    }
  })

  it('the same event reports once the window is visible', () => {
    const vis = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    try {
      showInActiveTab('aaaaaa')
      news('aaaaaa', 'E1', 7)
      expect(reportRead).toHaveBeenCalledExactlyOnceWith(H, 'aaaaaa', { epoch: 'E1', seq: 7 })
    } finally {
      vis.mockRestore()
    }
  })

  it('an agent that is not on screen is marked unread and nothing is reported', () => {
    showInActiveTab('bbbbbb')
    news('aaaaaa', 'E1', 7)
    expect(unread('aaaaaa')).toBe(true)
    expect(reportRead).not.toHaveBeenCalled()
  })

  it('only an event that would set the unread counts: running, an idle notification and a proxy stop do not', () => {
    showInActiveTab('aaaaaa')
    const apply = (status: string, seq: number, over: Partial<NormalizedEvent> = {}) =>
      useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev(status, { epoch: 'E1', seq, ...over }))
    apply('running', 1)
    apply('idle', 2, { raw_event_name: 'PdxNotification' }) // a transition, but not actionable
    apply('running', 3)
    apply('idle', 4, { from_proxy: true }) // a transition and actionable, but a proxy subagent's stop
    expect(reportRead).not.toHaveBeenCalled()
  })

  it('a status that did not change is not news, on screen or not', () => {
    showInActiveTab('aaaaaa')
    news('aaaaaa', 'E1', 7)
    vi.mocked(reportRead).mockClear()
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('idle', { epoch: 'E1', seq: 8 }))
    expect(reportRead).not.toHaveBeenCalled()
  })

  it('an agent.snapshot entry that changed status on screen is reported too', () => {
    showInActiveTab('aaaaaa')
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('running', { epoch: 'E1', seq: 1 }))
    useAgentStore.getState().applyAgentSnapshot(H, [{ session: 'aaaaaa', event: ev('idle', { epoch: 'E1', seq: 5, snapshot: true }) }])
    expect(reportRead).toHaveBeenCalledExactlyOnceWith(H, 'aaaaaa', { epoch: 'E1', seq: 5 })
  })

  it('the report is made after the state is written (a reporter that reads the store sees the event)', () => {
    showInActiveTab('aaaaaa')
    let seenStatus: string | undefined
    vi.mocked(reportRead).mockImplementationOnce(() => { seenStatus = useAgentStore.getState().statuses[key('aaaaaa')] })
    news('aaaaaa', 'E1', 7)
    expect(seenStatus).toBe('idle')
  })
})
