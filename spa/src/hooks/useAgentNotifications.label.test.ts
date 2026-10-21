// spa/src/hooks/useAgentNotifications.label.test.ts — the Mac's notification title follows the daemon's `notify_label` (notify-title spec §3.4, plan N-3 #4): the phone push's rule, with THIS device's name for the host.
// An event without it (an older daemon), and a worker, keep today's title.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { act } from 'react'
import { useAgentNotifications } from './useAgentNotifications'
import { __resetDebounceStateForTests } from '../lib/notification-gate'
import { STORAGE_KEYS } from '../lib/storage'
import { useAgentStore } from '../stores/useAgentStore'
import type { NormalizedEvent } from '../stores/useAgentStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'

const H = 'host-a'
const CODE = 'aaaaaa'

let ts = 1
const ev = (over: Partial<NormalizedEvent>): NormalizedEvent => ({
  agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', broadcast_ts: ++ts, detail: { tool_name: 'Bash' }, ...over,
})
const send = (code: string, event: NormalizedEvent) => act(() => useAgentStore.getState().handleNormalizedEvent(H, code, event))

describe('useAgentNotifications — the daemon\'s notify_label titles the notification', () => {
  let showNotification: ReturnType<typeof vi.fn>
  const titles = () => showNotification.mock.calls.map((c) => (c[0] as { title: string }).title)

  beforeEach(() => {
    ts = 1
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [`${H}:${CODE}`]: 1, [`${H}:exec-w1`]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useAgentStore.setState({ lastEvents: {}, statuses: { [`${H}:${CODE}`]: 'running', [`${H}:exec-w1`]: 'running' }, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} })
    useNotificationSettingsStore.getState().setNotifyWithoutTab('cc', true)
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab-local', ip: '1.1.1.1', port: 7860, token: 't' } } as never })
    useHostLookStore.setState({ looks: {} })
    useSessionStore.setState({ sessions: { [H]: [{ code: CODE, name: 'aigora4', cwd: '/', mode: 'terminal' }] }, activeHostId: null, activeCode: null })
    useTeamRosterStore.getState().reset()
    useTeamRosterStore.getState().markSessionsLive(H)
    useI18nStore.setState({ activeLocaleId: 'zh-TW' } as never)
    showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  })
  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('a name and a group → "{name} — {group}"', () => {
    renderHook(() => useAgentNotifications())
    send(CODE, ev({ notify_label: { name: 'Mei', group: 'Purdex A team', tmux: 'aigora4' } }))
    expect(titles()).toEqual(['Mei — Purdex A team'])
  })

  it('a name without a group → "{name} — {this device\'s host name}：{tmux}"', () => {
    renderHook(() => useAgentNotifications())
    send(CODE, ev({ notify_label: { name: 'Mei', tmux: 'aigora4' } }))
    expect(titles()).toEqual(['Mei — mlab-local：aigora4'])
  })

  it('English UI → the English separator', () => {
    useI18nStore.setState({ activeLocaleId: 'en' } as never)
    renderHook(() => useAgentNotifications())
    send(CODE, ev({ notify_label: { name: 'Mei', tmux: 'aigora4' } }))
    expect(titles()).toEqual(['Mei — mlab-local: aigora4'])
  })

  it('a name is never cut, however long', () => {
    renderHook(() => useAgentNotifications())
    const name = 'N'.repeat(150)
    send(CODE, ev({ notify_label: { name, group: 'g', tmux: 'aigora4' } }))
    expect(titles()).toEqual([name])
  })

  it('no notify_label (an older daemon) → today\'s title, the session\'s name', () => {
    renderHook(() => useAgentNotifications())
    send(CODE, ev({}))
    expect(titles()).toEqual(['aigora4'])
  })

  it('a worker keeps its title even if a label rides on the event', () => {
    renderHook(() => useAgentNotifications())
    send('exec-w1', ev({ notify_label: { name: 'Mei', group: 'Purdex A team', tmux: 'x' } }))
    expect(titles()).toEqual(['w1']) // the execution id, as before
  })
})
