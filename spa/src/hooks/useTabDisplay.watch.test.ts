import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabDisplay } from './useTabDisplay'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { WATCH_GUARDIAN_TAB_ID } from '../lib/watch/ids'
import { createTab } from '../types/tab'
import { usePoolStore } from '../lib/pool/store'

beforeEach(() => {
  useI18nStore.setState({ t: (k: string) => k })
})

describe('useTabDisplay for a watch-host tab', () => {
  const tab = { ...createTab({ kind: 'watch-host' }), id: 'watch-host-h1' }

  it('a tab from before the pool (no poolHostId) has no host to name: its label is empty', () => {
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('')
  })
})

describe('useTabDisplay for a watch-host tab with poolHostId (MON-6a-1)', () => {
  const tab = { ...createTab({ kind: 'watch-host', poolHostId: 'wh1' }), id: 'watch-host-wh1' }

  it('the label follows the pool alias', () => {
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('wh1')
    act(() => usePoolStore.setState({ hosts: [{ host_id: 'wh1', alias: 'mserv' }] as never, sourceHostId: 'cfg-a' } as never))
    expect(result.current.displayTitle).toBe('mserv')
    act(() => usePoolStore.setState({ hosts: [{ host_id: 'wh1', alias: 'mserv-2' }] as never, sourceHostId: 'cfg-a' } as never))
    expect(result.current.displayTitle).toBe('mserv-2')
    expect(result.current.displayTitle).toBe('mserv-2')
  })
})

describe('useTabDisplay for the guardian tab (journal v5 §3.7, U10)', () => {
  // While the watcher lives the guardian tab's pane is its ordinary tmux session (watch-area.ts `guardianWish`).
  const guardian = {
    ...createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'w1', mode: 'terminal', cachedName: 'pdx-watch', tmuxInstance: '' }),
    id: WATCH_GUARDIAN_TAB_ID,
  }

  beforeEach(() => {
    useSessionStore.setState({ sessions: { h1: [{ code: 'w1', name: 'pdx-watch', pane_title: 'reading the queue' }] as never } })
    useAgentStore.setState({ agentTypes: { 'h1:w1': 'cc' } })
    useUISettingsStore.setState({ dynamicTabName: true, stripAgentTitleMarker: true })
  })
  afterEach(() => {
    useSessionStore.setState({ sessions: {} })
    useAgentStore.setState({ agentTypes: {} })
  })

  it('is labelled 守門員 while it shows the watcher session, not by the session name or its title', () => {
    const { result } = renderHook(() => useTabDisplay(guardian))
    expect(result.current.displayTitle).toBe('watch.pane.guardian')
  })

  it('the same session in any other tab keeps its own label: the override goes by the tab id', () => {
    const { result } = renderHook(() => useTabDisplay({ ...guardian, id: 't-plain' }))
    expect(result.current.displayTitle).toBe('reading the queue - pdx-watch')
  })
})
