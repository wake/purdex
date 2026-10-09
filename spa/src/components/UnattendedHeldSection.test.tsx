import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { UnattendedHeldSection } from './UnattendedHeldSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import type { Approval } from '../lib/team/types'

// 「額度用完，等你核准」: the open requests the daemon holds. A self relay, and (RQ-2) a member relay.

const H = 'h1'
const origin = { session_id: 'S1', ref: '_aaaaaa', name: 'lead-name', cwd: '/w', tmux: '', pid: 1, proc_start: 'p', title: 'iface-lead' }
const selfRelay = (id: string, at: number): Approval => ({
  id, kind: 'self_relay', host_id: 'd', origin, payload: { op_id: 'o', used_percentage: 70, window: 1 },
  state: 'open', created_at: at, deadline_at: at + 1, lease_until: at + 1,
})
const memberRelay = (id: string, at: number, over: Record<string, unknown> = {}): Approval => ({
  id, kind: 'member_relay', host_id: 'd', origin,
  payload: { op_id: 'o', team_id: 't', lead_ref: '_a', lead_title: 'iface-lead', member_session_id: 'M', member_ref: '_b', member_title: 'iface-solo', used_percentage: 40, ...over },
  state: 'open', created_at: at, deadline_at: at + 1, lease_until: at + 1,
}) as Approval

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [H], activeHostId: H, runtime: {} })
})

describe('UnattendedHeldSection', () => {
  it('shows nothing without rows', () => {
    const { container } = render(<UnattendedHeldSection rows={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('a self relay keeps its line', () => {
    render(<UnattendedHeldSection rows={[{ hostId: H, a: selfRelay('a1', 1_000) }]} />)
    expect(screen.getByTestId('held-row').textContent).toContain('mlab：iface-lead · 接力申請')
  })

  it('a member relay names the lead and the member, beside the self relay rows, newest first', () => {
    render(<UnattendedHeldSection rows={[{ hostId: H, a: selfRelay('a1', 1_000) }, { hostId: H, a: memberRelay('m1', 2_000) }]} />)
    const rows = screen.getAllByTestId('held-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('mlab：iface-lead 要幫 member iface-solo 接力')
    expect(rows[1].textContent).toContain('接力申請')
  })

  it('falls back to the origin\'s label and the member ref when the payload has no titles', () => {
    render(<UnattendedHeldSection rows={[{ hostId: H, a: memberRelay('m1', 1_000, { lead_title: '', member_title: '' }) }]} />)
    expect(within(screen.getByTestId('held-row')).getByText(/iface-lead 要幫 member _b 接力/)).toBeInTheDocument()
  })

  it('the English line says the same', () => {
    useI18nStore.getState().setLocale('en')
    render(<UnattendedHeldSection rows={[{ hostId: H, a: memberRelay('m1', 1_000) }]} />)
    expect(screen.getByTestId('held-row').textContent).toContain('mlab: iface-lead wants to relay member iface-solo')
  })

  it('an over-long title is clipped', () => {
    render(<UnattendedHeldSection rows={[{ hostId: H, a: memberRelay('m1', 1_000, { member_title: 'M'.repeat(300) }) }]} />)
    expect((screen.getByTestId('held-row').textContent ?? '').length).toBeLessThan(200)
  })
})
