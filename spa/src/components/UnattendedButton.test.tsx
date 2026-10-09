// spa/src/components/UnattendedButton.test.tsx — the title-bar 無人值守模式 toggle (unattended spec D-U23-5, D-U23-6;
// plan PU-2b): the real button over the real host / shown-hosts / unattended stores; only the API is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { UnattendedButton } from './UnattendedButton'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useUnattendedStore, type UnattendedHostEntry } from '../stores/useUnattendedStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError } from '../lib/team/approval-api'
import { getUnattended, putUnattended } from '../lib/team/unattended-api'
import { handleUnattendedEvent } from '../lib/team/unattended-ws'
import type { Approval, UnattendedView } from '../lib/team/types'

vi.mock('../lib/team/unattended-api', () => ({ putUnattended: vi.fn(), getUnattended: vi.fn() }))
const mockedPut = vi.mocked(putUnattended)
const mockedGet = vi.mocked(getUnattended)

const A = 'host-a'
const B = 'host-b'
const C = 'host-c'
const D = 'host-d'
const ON = { on: true, since: 1_000, changed_at: 1_000 }
const OFF = { on: false, since: 0, changed_at: 0 }
const up: HostRuntime = { status: 'connected' }
const yes = (state: typeof ON): UnattendedHostEntry => ({ support: 'yes', state })
const view = (on: boolean): UnattendedView => ({ on, since: 1, changed_at: 1, approved: [], truncated: false })

const toggle = () => screen.getByTestId('unattended-toggle')
const list = () => screen.getByTestId('unattended-list')
const approved = (id: string, decidedAt: number): Approval => ({
  id, kind: 'self_relay', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: `_${id}`, name: `sess-${id}`, pid: 1, proc_start: 'x', cwd: '/w', tmux: 'p:@1.%1' },
  payload: {}, state: 'approved', created_at: decidedAt - 1_000, deadline_at: decidedAt + 540_000, lease_until: decidedAt + 30_000,
  decided_by: { kind: 'unattended', label: '無人值守模式' }, decided_at: decidedAt,
})
const rowTexts = () => screen.queryAllByTestId('unattended-row').map((r) => r.textContent)
const flush = () => act(async () => { await new Promise<void>((r) => setTimeout(r, 0)) })

function setup(shown: string[], runtime: Record<string, HostRuntime>, byHost: Record<string, UnattendedHostEntry>) {
  useShownHostsStore.setState({ ids: shown })
  useHostStore.setState({ runtime })
  useUnattendedStore.setState({ byHost })
}

beforeEach(() => {
  // The panel's rows show the date unless it is today: pin today to the fixtures' day.
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 9, 8, 12, 0))
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: {
      [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 },
      [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 },
      [C]: { id: C, name: 'air19', ip: '3', port: 3, token: 't', order: 2 },
      [D]: { id: D, name: 'old', ip: '4', port: 4, token: 't', order: 3 },
    },
    hostOrder: [A, B, C, D], activeHostId: A, runtime: {},
  })
  useUnattendedStore.getState().reset()
  useUndoToast.setState({ toast: null, notice: null })
  mockedPut.mockReset()
  mockedGet.mockReset()
  mockedGet.mockResolvedValue({ ...view(true), approved: [] })
  mockedPut.mockImplementation(async (_hostId, on) => view(on))
})
afterEach(() => { vi.useRealTimers(); useHostStore.getState().reset() })

describe('UnattendedButton', () => {
  // An unreachable or unsupported shown host makes the button partial by definition (D-U23-5), so "nothing written to
  // them" is pinned by the partial case below; here every shown host is reachable and off.
  it('off: no accent, no label, no ring; pressing PUTs on to every reachable shown host', async () => {
    setup([A, B], { [A]: up, [B]: up }, { [A]: yes(OFF), [B]: yes(OFF) })
    render(<UnattendedButton />)
    expect(toggle()).toHaveAttribute('data-state', 'off')
    expect(toggle()).toHaveAttribute('aria-pressed', 'false')
    expect(toggle()).toHaveAccessibleName('無人值守模式：關')
    expect(toggle().className).not.toContain('text-accent-base')
    expect(toggle().className).not.toContain('ring-')
    expect(toggle().getAttribute('title')).not.toContain('\n')
    expect(screen.queryByText('無人值守中')).toBeNull()
    fireEvent.click(toggle())
    await flush()
    expect(mockedPut.mock.calls).toEqual([[A, true], [B, true]])
  })

  it('on: accent and 無人值守中; pressing PUTs off', async () => {
    setup([A, B], { [A]: up, [B]: up }, { [A]: yes(ON), [B]: yes(ON) })
    render(<UnattendedButton />)
    expect(toggle()).toHaveAttribute('aria-pressed', 'true')
    expect(toggle()).toHaveAttribute('data-state', 'on')
    expect(toggle().className).toContain('text-accent-base')
    expect(screen.getByText('無人值守中')).toBeInTheDocument()
    fireEvent.click(toggle())
    await flush()
    expect(mockedPut.mock.calls).toEqual([[A, false], [B, false]])
  })

  it('partial: the ring and data-state; the tooltip names the off, unreachable and unsupported hosts; pressing turns the reachable ones on', async () => {
    setup([A, B, C, D], { [A]: up, [B]: up, [C]: { status: 'reconnecting' }, [D]: up }, { [A]: yes(ON), [B]: yes(OFF), [C]: yes(ON), [D]: { support: 'no' } })
    render(<UnattendedButton />)
    expect(toggle()).toHaveAttribute('data-state', 'partial')
    expect(toggle()).toHaveAttribute('aria-pressed', 'false')
    expect(toggle().className).toContain('ring-status-warning')
    const lines = toggle().getAttribute('title')!.split('\n')
    expect(lines).toContain('未開啟：air26')
    expect(lines).toContain('無法連線：air19')
    expect(lines).toContain('daemon 版本過舊：old')
    expect(lines.some((l) => l.includes('mlab'))).toBe(false)
    fireEvent.click(toggle())
    await flush()
    expect(mockedPut.mock.calls).toEqual([[A, true], [B, true]])
  })

  it('a hidden host is neither counted nor written', async () => {
    // B is on but hidden on this workbench: the shown A alone decides, and only A is written.
    setup([A], { [A]: up, [B]: up }, { [A]: yes(OFF), [B]: yes(ON) })
    render(<UnattendedButton />)
    expect(toggle()).toHaveAttribute('data-state', 'off')
    fireEvent.click(toggle())
    await flush()
    expect(mockedPut.mock.calls).toEqual([[A, true]])
  })

  it('a changed event (another window\'s press) turns the button on without a press here', () => {
    setup([A], { [A]: up }, { [A]: yes(OFF) })
    render(<UnattendedButton />)
    expect(toggle()).toHaveAttribute('data-state', 'off')
    act(() => handleUnattendedEvent(A, JSON.stringify({ op: 'changed', state: { ...ON, changed_by: { kind: 'app', label: 'Purdex.app @ air26' } } })))
    expect(toggle()).toHaveAttribute('data-state', 'on')
    expect(screen.getByText('無人值守中')).toBeInTheDocument()
    expect(mockedPut).not.toHaveBeenCalled()
  })

  it('a press does not write the store itself: the button follows the daemon\'s event', async () => {
    setup([A], { [A]: up }, { [A]: yes(OFF) })
    render(<UnattendedButton />)
    fireEvent.click(toggle())
    await flush()
    expect(toggle()).toHaveAttribute('data-state', 'off')
    act(() => handleUnattendedEvent(A, JSON.stringify({ op: 'changed', state: ON })))
    expect(toggle()).toHaveAttribute('data-state', 'on')
  })

  it('none: disabled', () => {
    setup([], { [A]: up }, { [A]: yes(ON) })
    render(<UnattendedButton />)
    expect(toggle()).toBeDisabled()
    expect(toggle()).toHaveAttribute('data-state', 'none')
    expect(toggle().getAttribute('title')).toBe('這個工作台沒有顯示中的主機')
  })

  it('a failed PUT toasts once naming the host', async () => {
    setup([A, B, C], { [A]: up, [B]: up, [C]: up }, { [A]: yes(OFF), [B]: yes(OFF), [C]: yes(OFF) })
    mockedPut.mockImplementation(async (hostId, on) => {
      if (hostId === B) throw new ApprovalApiError(0, 'network', 'Failed to fetch')
      if (hostId === C) throw new ApprovalApiError(503, 'not_ready')
      return view(on)
    })
    const show = vi.spyOn(useUndoToast.getState(), 'show')
    render(<UnattendedButton />)
    fireEvent.click(toggle())
    await waitFor(() => expect(show).toHaveBeenCalledTimes(1))
    const message = show.mock.calls[0][0]
    expect(message).toContain('air26')
    expect(message).toContain('air19')
    expect(message).not.toContain('mlab')
    expect(message).toBe('無法切換無人值守模式：air26 (network), air19 (not_ready)')
    show.mockRestore()
  })

  it('every PUT answering 200 toasts nothing', async () => {
    setup([A], { [A]: up }, { [A]: yes(OFF) })
    render(<UnattendedButton />)
    fireEvent.click(toggle())
    await flush()
    expect(mockedPut).toHaveBeenCalledTimes(1)
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('a second press while the first is in flight sends nothing more', async () => {
    setup([A], { [A]: up }, { [A]: yes(OFF) })
    let release: () => void = () => {}
    mockedPut.mockImplementation((_hostId, on) => new Promise((res) => { release = () => res(view(on)) }))
    render(<UnattendedButton />)
    fireEvent.click(toggle())
    fireEvent.click(toggle())
    expect(mockedPut).toHaveBeenCalledTimes(1)
    release()
    await flush()
    fireEvent.click(toggle())
    expect(mockedPut).toHaveBeenCalledTimes(2)
  })

  it('a mouse press keeps focus where it was', () => {
    setup([A], { [A]: up }, { [A]: yes(OFF) })
    render(<UnattendedButton />)
    const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    toggle().dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
  })

  describe('the ▾ list (plan PU-2c)', () => {
    it('▾ opens the panel and it fetches every reachable shown host once; the unreachable, the too old and the hidden are not asked', async () => {
      setup([A, B, C, D], { [A]: up, [B]: up, [C]: { status: 'reconnecting' }, [D]: up }, { [A]: yes(ON), [B]: yes(OFF), [C]: yes(ON), [D]: { support: 'no' } })
      mockedGet.mockImplementation(async (hostId) => ({ ...view(true), approved: hostId === A ? [approved('a1', new Date(2026, 9, 8, 9, 5).getTime())] : [] }))
      render(<UnattendedButton />)
      expect(screen.queryByTestId('unattended-panel')).toBeNull()
      expect(mockedGet).not.toHaveBeenCalled()
      fireEvent.click(list())
      expect(await screen.findByTestId('unattended-panel')).toBeInTheDocument()
      await waitFor(() => expect(rowTexts()).toEqual(['mlab：sess-a1 · 接力申請 · 09:05']))
      expect(mockedGet.mock.calls.map(([h]) => [h])).toEqual([[A], [B]])
      expect(mockedPut).not.toHaveBeenCalled()
    })

    it('a shown host that is unreachable is named in the panel; one whose daemon is too old is not', async () => {
      setup([A, C, D], { [A]: up, [C]: { status: 'reconnecting' }, [D]: up }, { [A]: yes(ON), [C]: yes(ON), [D]: { support: 'no' } })
      render(<UnattendedButton />)
      fireEvent.click(list())
      const lines = await screen.findAllByTestId('unattended-host-unreachable')
      expect(lines.map((l) => l.textContent)).toEqual(['air19：無法連線，可能仍在自動通過'])
      expect(mockedGet.mock.calls.map(([h]) => [h])).toEqual([[A]])
    })

    it('no host reachable: the panel lists the unreachable and shows no empty state', async () => {
      setup([A, B], { [A]: { status: 'reconnecting' }, [B]: { status: 'reconnecting' } }, { [A]: yes(ON), [B]: yes(ON) })
      render(<UnattendedButton />)
      fireEvent.click(list())
      await screen.findAllByTestId('unattended-host-unreachable')
      await flush()
      expect(screen.getAllByTestId('unattended-host-unreachable')).toHaveLength(2)
      expect(screen.queryByTestId('unattended-empty')).toBeNull()
      expect(mockedGet).not.toHaveBeenCalled()
    })

    it('the ▾ sits in the same no-drag wrapper as the toggle, with the accessible name and aria-expanded', async () => {
      setup([A], { [A]: up }, { [A]: yes(OFF) })
      render(<UnattendedButton />)
      expect(list().closest('[data-testid="unattended-buttons"]')).toBe(toggle().closest('[data-testid="unattended-buttons"]'))
      expect(list()).toHaveAccessibleName('無人值守：額度與自動通過的申請')
      expect(list()).toHaveAttribute('aria-expanded', 'false')
      fireEvent.click(list())
      expect(list()).toHaveAttribute('aria-expanded', 'true')
      await screen.findByTestId('unattended-empty')
    })

    it('▾ again closes the panel', async () => {
      setup([A], { [A]: up }, { [A]: yes(OFF) })
      render(<UnattendedButton />)
      fireEvent.click(list())
      await screen.findByTestId('unattended-panel')
      fireEvent.mouseDown(list())
      fireEvent.click(list())
      expect(screen.queryByTestId('unattended-panel')).toBeNull()
    })

    it('switching off opens nothing', async () => {
      setup([A, B], { [A]: up, [B]: up }, { [A]: yes(ON), [B]: yes(ON) })
      render(<UnattendedButton />)
      fireEvent.click(toggle())
      await flush()
      expect(mockedPut.mock.calls).toEqual([[A, false], [B, false]])
      act(() => { handleUnattendedEvent(A, JSON.stringify({ op: 'changed', state: OFF })); handleUnattendedEvent(B, JSON.stringify({ op: 'changed', state: OFF })) })
      await flush()
      expect(screen.queryByTestId('unattended-panel')).toBeNull()
      expect(mockedGet).not.toHaveBeenCalled()
    })

    it('switching on opens nothing either', async () => {
      setup([A], { [A]: up }, { [A]: yes(OFF) })
      render(<UnattendedButton />)
      fireEvent.click(toggle())
      await flush()
      act(() => handleUnattendedEvent(A, JSON.stringify({ op: 'changed', state: ON })))
      await flush()
      expect(screen.queryByTestId('unattended-panel')).toBeNull()
      expect(mockedGet).not.toHaveBeenCalled()
    })

    it('every open fetches afresh: what the daemon approved in between shows on the next open', async () => {
      setup([A], { [A]: up }, { [A]: yes(ON) })
      mockedGet.mockResolvedValueOnce({ ...view(true), approved: [approved('a1', new Date(2026, 9, 8, 9, 0).getTime())] })
      render(<UnattendedButton />)
      fireEvent.click(list())
      await waitFor(() => expect(rowTexts()).toEqual(['mlab：sess-a1 · 接力申請 · 09:00']))
      fireEvent.mouseDown(list())
      fireEvent.click(list())
      expect(screen.queryByTestId('unattended-panel')).toBeNull()
      mockedGet.mockResolvedValueOnce({ ...view(true), approved: [approved('a2', new Date(2026, 9, 8, 10, 0).getTime()), approved('a1', new Date(2026, 9, 8, 9, 0).getTime())] })
      fireEvent.click(list())
      await waitFor(() => expect(rowTexts()).toEqual(['mlab：sess-a2 · 接力申請 · 10:00', 'mlab：sess-a1 · 接力申請 · 09:00']))
      expect(mockedGet).toHaveBeenCalledTimes(2)
    })

    it('none: the ▾ is disabled', () => {
      setup([], { [A]: up }, { [A]: yes(ON) })
      render(<UnattendedButton />)
      expect(list()).toBeDisabled()
    })

    it('a mouse press on the ▾ keeps focus where it was', () => {
      setup([A], { [A]: up }, { [A]: yes(OFF) })
      render(<UnattendedButton />)
      const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
      list().dispatchEvent(ev)
      expect(ev.defaultPrevented).toBe(true)
    })
  })
})
