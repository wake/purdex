// spa/src/hooks/useNotificationClickListener.test.ts — the contract between Electron's notification-click payload and
// `handleNotificationClick` (#1690): every action kind decodes to the action the router takes, and a payload without an
// action (no host to route to) is dropped. The router itself is mocked; what each action does is tested in
// useNotificationDispatcher*.test.ts.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { handleNotificationClick } from '../lib/notification-click'
import { useNotificationClickListener } from './useNotificationClickListener'

vi.mock('../lib/notification-click', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/notification-click')>()),
  handleNotificationClick: vi.fn(),
}))

type ClickCallback = Parameters<NonNullable<Window['electronAPI']>['onNotificationClicked']>[0]
type ClickPayload = Parameters<ClickCallback>[0]

describe('useNotificationClickListener — Electron payload → handleNotificationClick', () => {
  const route = vi.mocked(handleNotificationClick)
  let deliver: ClickCallback | null

  /** Mounts the listener, delivers one click, unmounts; returns the actions routed. */
  const click = (payload: ClickPayload) => {
    const { unmount } = renderHook(() => useNotificationClickListener())
    expect(deliver).not.toBeNull()
    deliver!(payload)
    unmount()
    return route.mock.calls.map(([action]) => action)
  }

  beforeEach(() => {
    deliver = null
    route.mockReset()
    Object.defineProperty(window, 'electronAPI', {
      value: { onNotificationClicked: (cb: ClickCallback) => { deliver = cb; return () => { deliver = null } } },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('open-session takes the session code from the action when it carries one', () => {
    expect(click({ sessionCode: 'from-payload', action: { kind: 'open-session', hostId: 'host-a', sessionCode: 'from-action' } }))
      .toEqual([{ kind: 'open-session', hostId: 'host-a', sessionCode: 'from-action' }])
  })

  it("open-session falls back to the payload's top-level session code when the action has none", () => {
    expect(click({ sessionCode: 'from-payload', action: { kind: 'open-session', hostId: 'host-a' } }))
      .toEqual([{ kind: 'open-session', hostId: 'host-a', sessionCode: 'from-payload' }])
  })

  it('open-host routes the host only — a session code on the payload or the action is not carried', () => {
    const actions = click({ sessionCode: 'ses001', action: { kind: 'open-host', hostId: 'host-b', sessionCode: 'ses001' } })
    expect(actions).toEqual([{ kind: 'open-host', hostId: 'host-b' }])
    expect(Object.keys(actions[0])).toEqual(['kind', 'hostId'])
  })

  it('open-approval carries the request id when there is one', () => {
    expect(click({ sessionCode: '', action: { kind: 'open-approval', hostId: 'host-b', requestId: 'req-1' } }))
      .toEqual([{ kind: 'open-approval', hostId: 'host-b', requestId: 'req-1' }])
  })

  it('open-approval without a request id routes no requestId key at all', () => {
    const actions = click({ sessionCode: '', action: { kind: 'open-approval', hostId: 'host-b' } })
    expect(actions).toEqual([{ kind: 'open-approval', hostId: 'host-b' }])
    expect(Object.keys(actions[0])).toEqual(['kind', 'hostId'])
  })

  it('a payload without an action is ignored', () => {
    expect(click({ sessionCode: 'ses001' })).toEqual([])
  })

  it('unmounting removes the listener', () => {
    const { unmount } = renderHook(() => useNotificationClickListener())
    expect(deliver).not.toBeNull()
    unmount()
    expect(deliver).toBeNull()
  })
})
