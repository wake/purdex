// spa/src/components/ApprovalPill.test.tsx — the corner pill a minimized approval dialog becomes (lead-team spec U22 (b)):
// `● 待核准 N · m:ss`, N across hosts and the nearest deadline's countdown, ticking each second; a click restores the
// dialog; a request it has not shown yet raises `data-flash` by one and runs one background flash, a close never does.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalPill } from './ApprovalPill'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useI18nStore } from '../stores/useI18nStore'
import type { Approval } from '../lib/team/types'

const lead = (id: string, deadline: number, created = 1_000): Approval => ({
  id, kind: 'lead', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w'] },
  state: 'open', created_at: created, deadline_at: deadline, lease_until: created + 30_000,
})
const relay = (id: string, deadline: number, created = 1_000): Approval => ({
  ...lead(id, deadline, created),
  kind: 'self_relay',
  payload: { op_id: 'op-1', used_percentage: 72, window: 1_000_000 },
})

const pill = () => screen.getByTestId('approval-pill')
const opened = (hostId: string, a: Approval) => act(() => { useApprovalStore.getState().applyOpened(hostId, a) })
const closed = (hostId: string, a: Approval) => act(() => { useApprovalStore.getState().applyClosed(hostId, { ...a, state: 'denied' }) })

beforeEach(() => {
  vi.useFakeTimers({ now: 1_000 })
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
})
afterEach(() => {
  vi.useRealTimers()
})

describe('ApprovalPill', () => {
  it('shows N across hosts and the nearest deadline as m:ss, and ticks each second', () => {
    useApprovalStore.getState().applyOpened('h1', lead('a', 1_000 + 300_000))
    useApprovalStore.getState().applyOpened('h2', relay('b', 1_000 + 65_000, 2_000))
    render(<ApprovalPill />)
    expect(pill().textContent).toBe('待核准 2 · 1:05')
    expect(pill().getAttribute('aria-label')).toBe('還原核准對話框')
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(pill().textContent).toBe('待核准 2 · 1:00')
    act(() => { vi.advanceTimersByTime(120_000) })
    expect(pill().textContent).toBe('待核准 2 · 0:00')
  })

  it('a click restores the dialog (minimized → false); a mousedown does not take the keyboard from where it is', () => {
    useApprovalStore.getState().applyOpened('h1', lead('a', 600_000))
    useApprovalStore.getState().setMinimized(true)
    render(<ApprovalPill />)
    expect(fireEvent.mouseDown(pill())).toBe(false) // default prevented: the focused terminal keeps focus
    fireEvent.click(pill())
    expect(useApprovalStore.getState().minimized).toBe(false)
  })

  it('what was open when it appeared is not new: data-flash starts at 0', () => {
    useApprovalStore.getState().applyOpened('h1', lead('a', 600_000))
    render(<ApprovalPill />)
    expect(pill().dataset.flash).toBe('0')
  })

  for (const [kind, make] of [['lead', lead], ['self_relay', relay]] as const) {
    it(`a new ${kind} request raises data-flash by one and N; the same request again does not`, () => {
      useApprovalStore.getState().applyOpened('h1', lead('a', 600_000))
      render(<ApprovalPill />)
      opened('h2', make('b', 400_000, 2_000))
      expect(pill().dataset.flash).toBe('1')
      expect(pill().textContent).toBe('待核准 2 · 6:39')
      opened('h2', make('b', 400_000, 2_000)) // a duplicate opened: the store ignores it
      expect(pill().dataset.flash).toBe('1')
    })
  }

  it('a close updates N and the countdown without a flash', () => {
    useApprovalStore.getState().applyOpened('h1', lead('a', 600_000))
    useApprovalStore.getState().applyOpened('h2', lead('b', 120_000, 2_000))
    render(<ApprovalPill />)
    closed('h2', lead('b', 120_000, 2_000))
    expect(pill().textContent).toBe('待核准 1 · 9:59')
    expect(pill().dataset.flash).toBe('0')
  })

  it('runs one 600 ms background flash through element.animate when the platform has it', () => {
    const animate = vi.fn()
    Object.defineProperty(HTMLElement.prototype, 'animate', { value: animate, configurable: true, writable: true })
    try {
      useApprovalStore.getState().applyOpened('h1', lead('a', 600_000))
      render(<ApprovalPill />)
      expect(animate).not.toHaveBeenCalled()
      opened('h1', relay('b', 700_000, 2_000))
      expect(animate).toHaveBeenCalledTimes(1)
      expect(animate.mock.instances[0]).toBe(pill())
      expect(animate.mock.calls[0][1]).toMatchObject({ duration: 600 })
      closed('h1', relay('b', 700_000, 2_000))
      expect(animate).toHaveBeenCalledTimes(1)
    } finally {
      delete (HTMLElement.prototype as { animate?: unknown }).animate
    }
  })
})
