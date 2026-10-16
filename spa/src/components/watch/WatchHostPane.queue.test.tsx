// The host tab's queue section as the pane mounts it: daemon hosts only, offline sentence, placed between 資源 and 行程.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { fleetHistory } from '../../lib/fleet/history'
import { clearViewMemory } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import type { FleetQueue } from '../../lib/fleet/types'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const st = () => useFleetStore.getState()
const q: FleetQueue = {
  heavy_used: 1, heavy_total: 2, light_used: 0, light_total: 3, holders: [{ class: 'heavy', slot: 1, command: 'go test', project: 'purdex', session: '', since: Date.now() - 5000, pid: 9 }],
  waiters: [], longest_wait_s: 0, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false,
}
function load(hosts: ReturnType<typeof host>[]) {
  const gen = st().begin('cfg-a')
  st().setSupport('cfg-a', gen, 'yes')
  st().applyFetched('cfg-a', gen, primaryAnswer(1, hosts))
}
const renderPane = (id: string) => render(<WatchHostPane pane={{ id: `watch-host-${id}.p`, content: { kind: 'watch-host', fleetHostId: id } }} isActive />)

beforeEach(() => {
  st().reset(); fleetHistory.clear(); clearViewMemory()
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: {} } as never)
})

describe('WatchHostPane — 重指令排隊', () => {
  it('a daemon host with a queue shows the section between 資源 and 行程', () => {
    load([host('d-a', { slow: { at: 1, top_cpu: [], top_mem: [], queue: q } })])
    renderPane('d-a')
    const titles = screen.getAllByRole('button', { expanded: true }).map((b) => b.textContent)
    expect(titles[1]).toBe(t('watch.queue.title'))
    expect(titles[2]).toContain(t('watch.host.procs'))
    expect(screen.getByTestId('q-holder')).toHaveTextContent('go test')
  })

  it('a probe host has no queue section', () => {
    load([host('d-a'), host('p1', { kind: 'probe', alias: 'air19' })])
    renderPane('p1')
    expect(screen.queryByText(t('watch.queue.title'))).toBeNull()
  })

  it('a probe host never shows one, even if a frame carried a queue', () => {
    load([host('d-a'), host('p1', { kind: 'probe', alias: 'air19', slow: { at: 1, top_cpu: [], top_mem: [], queue: q } })])
    renderPane('p1')
    expect(screen.queryByText(t('watch.queue.title'))).toBeNull()
    expect(screen.queryByTestId('q-holder')).toBeNull()
  })

  it('a daemon host that sends no queue (an older daemon) has none either', () => {
    load([host('d-a', { slow: { at: 1, top_cpu: [], top_mem: [] } })])
    renderPane('d-a')
    expect(screen.queryByText(t('watch.queue.title'))).toBeNull()
  })

  it('an offline daemon host: the section with the one sentence', () => {
    load([host('d-a', { status: 'offline', error: 'unreachable', fast: null, slow: { at: 1, top_cpu: [], top_mem: [], queue: q } })])
    renderPane('d-a')
    expect(screen.getByText(t('watch.queue.offline'))).toBeInTheDocument()
    expect(screen.queryByTestId('q-holder')).toBeNull()
  })
})
