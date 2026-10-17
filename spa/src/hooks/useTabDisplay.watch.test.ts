import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabDisplay } from './useTabDisplay'
import { useFleetStore, type FleetSnapshot } from '../lib/fleet/store'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { WATCH_GUARDIAN_TAB_ID } from '../lib/watch/ids'
import { createTab } from '../types/tab'

const snapshot = (alias: string): FleetSnapshot => ({
  hostId: 'p', term: 1, order: ['h1'], hosts: { h1: { id: 'h1', alias } as never },
})

beforeEach(() => {
  useFleetStore.getState().reset()
  useI18nStore.setState({ t: (k: string) => k })
})

describe('useTabDisplay for a watch-host tab', () => {
  const tab = { ...createTab({ kind: 'watch-host', fleetHostId: 'h1' }), id: 'watch-host-h1' }

  it('the label follows the fleet: the snapshot arrives after mount, and a rename is picked up', () => {
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('h1') // no snapshot yet: the raw fleet id
    act(() => useFleetStore.setState({ snapshot: snapshot('mlab') }))
    expect(result.current.displayTitle).toBe('mlab')
    act(() => useFleetStore.setState({ snapshot: snapshot('mlab-2') }))
    expect(result.current.displayTitle).toBe('mlab-2')
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
