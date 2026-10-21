// The 狀態 tab's order and its follow-the-host behaviour (monitoring tabs spec M44; plan MON-7b-2).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

vi.mock('../../../lib/pool/control-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/control-api')>()), fetchPauses: () => Promise.resolve([]), controlSource: () => 'cfg-a' }))

import { StatusTab } from './StatusTab'
import { poolHostOf } from '../host-pane-seed'
import { poolHostState } from '../../../lib/pool/host-state'
import { usePoolStore } from '../../../lib/pool/store'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'

const t = (k: string, o?: Record<string, string | number>) => useI18nStore.getState().t(k, o)
const NOW = Date.now()
const hostOf = (alias: string, over = {}): MonitorHost => {
  const pool = poolHostOf({ alias, host_id: `id-${alias}`, ...over })
  return { hostId: pool.host_id, alias, daemonHostId: null, state: poolHostState(pool, NOW, NOW), look: { indicator: { shape: 'ring', data: ['load'] } }, poolFailure: null, count: null, local: false }
}

beforeEach(() => { cleanup(); usePoolStore.getState().reset(); useAlertsStore.getState().reset() })
afterEach(() => cleanup())

describe('StatusTab', () => {
  it('the six cards come first, the facts table after them', () => {
    render(<StatusTab host={hostOf('air26')} />)
    const cards = screen.getByTestId('status-cards')
    const facts = screen.getByTestId('facts-table')
    expect(cards.compareDocumentPosition(facts) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('another host is another content', () => {
    const { rerender } = render(<StatusTab host={hostOf('air26')} />)
    expect(screen.getByText('id-air26')).toBeInTheDocument()
    rerender(<StatusTab host={hostOf('mserv')} />)
    expect(screen.getByText('id-mserv')).toBeInTheDocument()
    expect(screen.queryByText('id-air26')).toBeNull()
  })

  it('a host that is not ok: no reading in the cards and in the facts table', () => {
    render(<StatusTab host={hostOf('air26', { liveness: 'host_down', facts: null })} />)
    expect(screen.getByTestId('status-card-judge')).toHaveTextContent(t('watch.host.status.no_reading', { state: t('pool.liveness.host_down') }))
    expect(screen.getByTestId('facts-table')).toHaveTextContent(t('watch.host.status.facts.none'))
  })
})
