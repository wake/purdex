// spa/src/hooks/useMultiHostEventWs.approval.test.ts — the `approval.request` host event (lead-team spec §6.2–§6.3):
// the snapshot a new subscriber gets populates the host's open set, `opened` adds, `closed` removes — and a close
// decided on another client toasts who handled it (U6). Harness as useMultiHostEventWs.worker-exited.test.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useApprovalStore, approvalKey } from '../stores/useApprovalStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const frame = (value: unknown) => JSON.stringify({ type: 'approval.request', session: '', value: typeof value === 'string' ? value : JSON.stringify(value) })
const held = () => Object.values(useApprovalStore.getState().entries).map((e) => e.approval.id).sort()

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useApprovalStore.getState().reset()
  useUndoToast.setState({ toast: null, notice: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

async function connected() {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  return { view, ws: sockets[0] }
}

describe('useMultiHostEventWs approval.request', () => {
  it('snapshot populates the host\'s set, keyed by this host id; opened adds; closed removes', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' }), approval({ id: 'b', created_at: 2_000 })] })) })
    expect(held()).toEqual(['a', 'b'])
    expect(useApprovalStore.getState().entries[approvalKey(HOST, 'a')]).toMatchObject({ hostId: HOST, approval: { id: 'a', host_id: 'd1' } })
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'c', created_at: 3_000 }) })) })
    expect(held()).toEqual(['a', 'b', 'c'])
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'b', state: 'timeout', decided_at: 4_000 }) })) })
    expect(held()).toEqual(['a', 'c'])
    view.unmount()
  })

  it('a snapshot replaces the set: a request closed while this client was away disappears, and nothing is duplicated', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' }), approval({ id: 'gone' })] })) })
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' })] })) })
    expect(held()).toEqual(['a'])
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: [] })) })
    expect(held()).toEqual([])
    view.unmount()
  })

  it('a duplicate opened is ignored', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    expect(held()).toEqual(['req-1'])
    view.unmount()
  })

  it('unknown ops, malformed approvals and non-JSON values are ignored; a malformed frame with a known op warns once', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'nope', approval: approval() })) })
    act(() => { ws.emit(frame({ op: 'opened', approval: { id: '' } })) })
    act(() => { ws.emit(frame({ op: 'opened' })) })
    act(() => { ws.emit(frame('not json')) })
    act(() => { ws.emit(frame({ op: 'snapshot', approvals: null })) })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast).toBeNull()
    expect(warn).toHaveBeenCalledTimes(3)
    warn.mockRestore()
    view.unmount()
  })

  it('closed by another client → toast "<主機>：<session> 的 lead 申請 已由 <client> 核准"', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    act(() => {
      ws.emit(frame({ op: 'closed', approval: approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ air26', addr: '100.64.0.4:5' }, decided_at: 5_000 }) }))
    })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
    view.unmount()
  })

  it('denied elsewhere says 拒絕; a timeout says it ended; a close for a request never shown toasts nothing', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'a' }) })) })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'a', state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ a19' } }) })) })
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ a19 拒絕')
    act(() => { ws.emit(frame({ op: 'opened', approval: approval({ id: 'b' }) })) })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'b', state: 'timeout' }) })) })
    expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請已結束（逾時）')
    useUndoToast.setState({ toast: null })
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ id: 'never-held', state: 'approved', decided_by: { kind: 'app', label: 'x' } }) })) })
    expect(useUndoToast.getState().toast).toBeNull()
    view.unmount()
  })

  it('a close for a request this app decided itself does not toast', async () => {
    const { view, ws } = await connected()
    act(() => { ws.emit(frame({ op: 'opened', approval: approval() })) })
    useApprovalStore.getState().markDecidedHere(HOST, 'req-1')
    act(() => { ws.emit(frame({ op: 'closed', approval: approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app @ mlab' } }) })) })
    expect(held()).toEqual([])
    expect(useUndoToast.getState().toast).toBeNull()
    view.unmount()
  })

  // A frame is bound to the host identity its connection was made for (#1978, as PU-2a's team.unattended). Between a
  // host-store change and the effect that closes the old socket, a frame still queued on it must not reach the store.
  // These tests do NOT forget the host themselves (lib/host-lifecycle.ts does that in the app): the guard has to
  // hold on its own, and the entries seeded below are what a dropped snapshot / closed would otherwise change.
  describe('a frame from a connection the host no longer has', () => {
    const seeded = async () => {
      const { view, ws } = await connected()
      act(() => { ws.emit(frame({ op: 'snapshot', approvals: [approval({ id: 'a' })] })) })
      expect(held()).toEqual(['a'])
      return { view, old: ws }
    }
    const repoint = () => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))

    it('after a re-point, opened / closed / snapshot queued on the old socket do not write; the new connection\'s do', async () => {
      const { view, old } = await seeded()
      act(() => {
        repoint()
        old.emit(frame({ op: 'opened', approval: approval({ id: 'late' }) })) // before the effect closes the old socket
        old.emit(frame({ op: 'closed', approval: approval({ id: 'a', state: 'denied', decided_by: { kind: 'app', label: 'x' } }) }))
        old.emit(frame({ op: 'snapshot', approvals: [] }))
      })
      expect(held()).toEqual(['a'])
      expect(useUndoToast.getState().toast).toBeNull()
      await waitFor(() => expect(sockets).toHaveLength(2))
      act(() => { sockets[1].emit(frame({ op: 'snapshot', approvals: [approval({ id: 'n' })] })) })
      expect(held()).toEqual(['n'])
      view.unmount()
    })

    it('a token change is a re-point too', async () => {
      const { view, old } = await seeded()
      act(() => {
        useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], token: 'rotated' } } }))
        old.emit(frame({ op: 'opened', approval: approval({ id: 'late' }) }))
      })
      expect(held()).toEqual(['a'])
      view.unmount()
    })

    it('after a removal, a frame queued on the old socket does not write, nor one after the effect closed it', async () => {
      const { view, old } = await seeded()
      act(() => {
        useHostStore.setState({ hosts: {}, hostOrder: [] })
        old.emit(frame({ op: 'opened', approval: approval({ id: 'late' }) }))
      })
      expect(held()).toEqual(['a'])
      act(() => { old.emit(frame({ op: 'opened', approval: approval({ id: 'later' }) })) })
      expect(held()).toEqual(['a'])
      view.unmount()
    })

    it('removed and added again with the same settings in one batch: the one connection is kept and its frames write', async () => {
      const { view, old } = await seeded()
      const config = useHostStore.getState().hosts[HOST]
      act(() => {
        useHostStore.setState({ hosts: {}, hostOrder: [] })
        useHostStore.setState({ hosts: { [HOST]: config }, hostOrder: [HOST] })
        old.emit(frame({ op: 'opened', approval: approval({ id: 'b', created_at: 2_000 }) }))
      })
      expect(held()).toEqual(['a', 'b'])
      expect(sockets).toHaveLength(1)
      view.unmount()
    })
  })
})
