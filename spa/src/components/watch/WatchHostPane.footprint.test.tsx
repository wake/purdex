import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import type { FleetProc } from '../../lib/fleet/types'

const t = (k: string) => useI18nStore.getState().t(k)
const st = () => useFleetStore.getState()
const proc = (pid: number, name: string, cpu: number, rss_mb: number): FleetProc => ({ pid, name, cpu, rss_mb })

function memRows(mem: FleetProc[], cpu: FleetProc[] = []) {
  const gen = st().begin('cfg-a')
  st().setSupport('cfg-a', gen, 'yes')
  st().applyFetched('cfg-a', gen, primaryAnswer(1, [host('d-a', { alias: 'air26', slow: { at: 1, top_cpu: cpu, top_mem: mem } })]))
  render(<WatchHostPane pane={{ id: 'watch-host-d-a.p', content: { kind: 'watch-host', fleetHostId: 'd-a' } }} isActive />)
  return (id: string) => within(screen.getByTestId(id)).getAllByRole('row').slice(1)
}

beforeEach(() => {
  st().reset()
  useHostStore.setState({
    hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } },
    hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: {},
  } as never)
})

describe('WatchHostPane — memory footprint (#3057)', () => {
  it('shows mem_mb over rss_mb, at most one decimal, in the given order', () => {
    const rows = memRows([
      { ...proc(1, 'low', 1, 9000), mem_mb: 12.34, mem_kind: 'footprint' },
      { ...proc(2, 'high', 1, 10), mem_mb: 4096, mem_kind: 'footprint' },
    ])('procs-mem')
    expect(rows.map((r) => r.textContent)).toEqual(['1low12.3 MB', '2high4,096 MB'])
  })

  it('falls back to rss_mb without a marker for an old daemon', () => {
    const rows = memRows([proc(1, 'old', 1, 300)])('procs-mem')
    expect(rows[0].textContent).toBe('1old300 MB')
  })

  it('marks only mem_kind rss, with a tooltip and an accessible label', () => {
    const rows = memRows([
      { ...proc(1, 'fp', 1, 1), mem_mb: 10, mem_kind: 'footprint' },
      { ...proc(2, 'rs', 1, 1), mem_mb: 20, mem_kind: 'rss' },
      proc(3, 'legacy', 1, 30),
    ])('procs-mem')
    const hint = t('watch.host.mem_rss_hint')
    expect(hint).not.toBe('watch.host.mem_rss_hint')
    expect(within(rows[0]).queryByLabelText(hint)).toBeNull()
    expect(within(rows[2]).queryByLabelText(hint)).toBeNull()
    const marker = within(rows[1]).getByLabelText(hint)
    expect(marker).toHaveAttribute('title', hint)
    expect(marker).toHaveTextContent('RSS')
  })

  it('the CPU table is unaffected by the new fields', () => {
    const rows = memRows([], [{ ...proc(11, 'node', 91.5, 100), mem_mb: 5, mem_kind: 'rss' }])('procs-cpu')
    expect(rows.map((r) => r.textContent)).toEqual(['11node91.5%'])
  })
})
