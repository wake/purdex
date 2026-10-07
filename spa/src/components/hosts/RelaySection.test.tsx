import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { RelaySection } from './RelaySection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { RelaySwitches } from '../../lib/host-config-api'

const H = 'h1'
const saveRelay = vi.fn()

function entry(relay: RelaySwitches, supported = true, status: HostConfigEntry['status'] = 'ready'): HostConfigEntry {
  const e = emptyHostConfigEntry(status)
  return { ...e, relay, relaySupported: supported, revisions: { ...e.revisions, relay: 1 } }
}

function seed(e: HostConfigEntry) {
  useHostConfigStore.setState({ byHost: { [H]: e }, load: vi.fn(async () => {}), saveRelay })
}

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  saveRelay.mockReset().mockImplementation(async (hostId: string, items: RelaySwitches) => {
    useHostConfigStore.setState((s) => {
      const cur = s.byHost[hostId]
      return { byHost: { ...s.byHost, [hostId]: { ...cur, relay: items, revisions: { ...cur.revisions, relay: cur.revisions.relay + 1 } } } }
    })
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
})

describe('RelaySection', () => {
  it('shows the two switches with the stored values and the member line (spec §8.7 (a))', () => {
    seed(entry({ self_solo: true, self_lead: false }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByText('接力')).toBeTruthy()
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('true')
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('relay-member-note').textContent).toBe('member 的接力一律由 lead 安排')
  })

  it('a toggle PUTs the whole relay object with the one field changed', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H, { self_solo: false, self_lead: true }))
    await waitFor(() => expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('false'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenLastCalledWith(H, { self_solo: false, self_lead: false }))
  })

  it('two clicks in one tick serialize: the second saves on top of the first, not over it', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(2))
    expect(saveRelay.mock.calls[1][1]).toEqual({ self_solo: false, self_lead: false })
  })

  it('a save failure is shown and the switch keeps the daemon copy', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    saveRelay.mockRejectedValueOnce(new Error('boom'))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(screen.getByTestId('relay-save-error').textContent).toContain('boom'))
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('true')
  })

  it('an older daemon (no relay field) shows the unsupported note and no switches', () => {
    seed(entry({ self_solo: true, self_lead: true }, false))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('relay-unsupported').textContent).toContain('太舊')
    expect(screen.queryByTestId('relay-self-solo')).toBeNull()
  })

  it('offline: the notice shows and a click saves nothing', () => {
    seed(entry({ self_solo: true, self_lead: true }))
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status: 'disconnected' } } }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('host-config-notice').dataset.notice).toBe('host_config.offline')
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    expect(saveRelay).not.toHaveBeenCalled()
  })
})
