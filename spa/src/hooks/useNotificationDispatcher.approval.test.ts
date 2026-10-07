// spa/src/hooks/useNotificationDispatcher.approval.test.ts — the click on an approval notification (lead-team spec
// §6.3): it focuses the window, where the dialog already is, and restores the dialog when this window minimized it
// (U22 (b); plan P9 open question 4: the person's own click, not an automatic expansion). It must not fall into the
// open-session path (no session code to route to) nor open the Hosts page.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { handleNotificationClick, useNotificationDispatcher } from './useNotificationDispatcher'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useApprovalStore } from '../stores/useApprovalStore'
import type { Approval } from '../lib/team/types'

type ClickPayload = { sessionCode: string; action?: { kind: string; hostId: string; sessionCode?: string } }

describe('useNotificationDispatcher open-approval', () => {
  let clickHandler: ((payload: ClickPayload) => void) | null
  const focusMyWindow = vi.fn()

  beforeEach(() => {
    clickHandler = null
    focusMyWindow.mockClear()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useHostStore.setState({ hostOrder: ['host-a', 'host-b'], activeHostId: 'host-a' })
    // Both shown: the open-session fallback's "hidden host → Hosts page" branch must not be what makes this pass.
    useShownHostsStore.setState({ ids: ['host-a', 'host-b'] })
    Object.defineProperty(window, 'electronAPI', {
      value: {
        onNotificationClicked: (cb: (payload: ClickPayload) => void) => { clickHandler = cb; return () => { clickHandler = null } },
        focusMyWindow,
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('a click with action open-approval focuses the window and nothing else', () => {
    const { unmount } = renderHook(() => useNotificationDispatcher())
    expect(clickHandler).not.toBeNull()
    clickHandler!({ sessionCode: '', action: { kind: 'open-approval', hostId: 'host-b' } })
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabOrder).toEqual([])
    expect(useHostStore.getState().activeHostId).toBe('host-a')
    unmount()
  })

  it('handleNotificationClick({kind: open-approval}) is the same no-op-plus-focus', () => {
    handleNotificationClick({ kind: 'open-approval', hostId: 'host-b' })
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabOrder).toEqual([])
  })

  it('an open-approval click restores a minimized dialog and still focuses the window', () => {
    const request: Approval = {
      id: 'req-1', kind: 'self_relay', host_id: 'd1',
      origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
      payload: { op_id: 'op-1', used_percentage: 72, window: 1_000_000 },
      state: 'open', created_at: 1_000, deadline_at: 601_000, lease_until: 31_000,
    }
    useApprovalStore.getState().reset()
    useApprovalStore.getState().applyOpened('host-b', request)
    useApprovalStore.getState().setMinimized(true)
    expect(useApprovalStore.getState().minimized).toBe(true)

    const { unmount } = renderHook(() => useNotificationDispatcher())
    clickHandler!({ sessionCode: '', action: { kind: 'open-approval', hostId: 'host-b' } })
    expect(useApprovalStore.getState().minimized).toBe(false)
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabOrder).toEqual([])
    expect(useHostStore.getState().activeHostId).toBe('host-a')
    unmount()
    useApprovalStore.getState().reset()
  })
})
