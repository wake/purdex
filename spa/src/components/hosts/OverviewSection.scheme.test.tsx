import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { OverviewSection } from './OverviewSection'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn().mockResolvedValue({ ok: false }),
  fetchInfo: vi.fn().mockResolvedValue({ ok: false }),
  fetchHealth: vi.fn().mockResolvedValue({ ok: false }),
}))

describe('OverviewSection — scheme edit', () => {
  beforeEach(() => { useHostStore.getState().reset() })

  it('切換 scheme → updateHost 生效、getDaemonBase 導出 https', () => {
    const id = useHostStore.getState().addHost({ name: 'h', ip: 'purdex.mlab.host', port: 443 })
    useHostStore.getState().setActiveHost(id)
    render(<OverviewSection hostId={id} />)
    fireEvent.change(screen.getByRole('combobox', { name: /scheme/i }), { target: { value: 'https' } })
    expect(useHostStore.getState().hosts[id].scheme).toBe('https')
    expect(useHostStore.getState().getDaemonBase(id)).toBe('https://purdex.mlab.host')
  })
})
