// The host tab's 資源 section: five gauges from the latest frame, a chart row per reading from the in-memory history, the range switch.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { fleetHistory } from '../../lib/fleet/history'
import { clearViewMemory, readHostRange } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { fast, host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const st = () => useFleetStore.getState()

function load(hosts: ReturnType<typeof host>[]) {
  const gen = st().begin('cfg-a')
  st().setSupport('cfg-a', gen, 'yes')
  st().applyFetched('cfg-a', gen, primaryAnswer(1, hosts))
}
const renderPane = (id: string) => render(<WatchHostPane pane={{ id: `watch-host-${id}.p`, content: { kind: 'watch-host', fleetHostId: id } }} isActive />)

beforeEach(() => {
  st().reset()
  fleetHistory.clear()
  clearViewMemory()
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: {} } as never)
})

describe('WatchHostPane — 資源', () => {
  it('the section sits above 行程 and is titled with the range', () => {
    load([host('d-a', { alias: 'air26' })])
    renderPane('d-a')
    const buttons = screen.getAllByRole('button', { expanded: true }).map((b) => b.textContent)
    expect(buttons[0]).toBe(t('watch.host.resources', { range: t('watch.host.range.5m') }))
    expect(buttons[1]).toContain(t('watch.host.procs'))
  })

  it('five gauges and a chart row per reading', () => {
    const now = Date.now()
    for (let i = 60; i >= 0; i--) fleetHistory.record('d-a', fast({ at: now - i * 1000, cpu: 20 + (i % 7) }))
    load([host('d-a', { alias: 'air26', fast: fast({ cpu: 77, mem: 40, level: 2, page_rate: 10, disk: 90 }) })])
    renderPane('d-a')
    const gauges = within(screen.getByTestId('gauges'))
    expect(['cpu', 'mem', 'level', 'page_rate', 'disk'].map((k) => gauges.getByTestId(`gauge-${k}`).getAttribute('data-tone'))).toEqual(['warn', 'ok', 'warn', 'ok', 'warn'])
    for (const k of ['cpu', 'mem', 'level', 'page_rate', 'disk']) expect(screen.getByTestId(`metric-${k}`)).toBeInTheDocument()
    expect(within(screen.getByTestId('metric-cpu')).getAllByTestId('chart-line').length).toBeGreaterThan(0)
    expect(screen.getByTestId('axis-collecting')).not.toHaveTextContent(/^$/)
  })

  it('offline: gauges show — and no arc', () => {
    load([host('d-a'), host('p1', { kind: 'probe', alias: 'air19', status: 'offline', error: 'unreachable', fast: null })])
    renderPane('p1')
    expect(screen.queryByTestId('gauge-arc')).toBeNull()
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('—')
  })

  it('the range switch (5 分／1 時／6 時／24 時) retitles the section and is remembered per host', () => {
    load([host('d-a', { alias: 'air26' })])
    renderPane('d-a')
    const sw = within(screen.getByTestId('range-switch'))
    expect(sw.getAllByRole('button').map((b) => b.textContent)).toEqual(['5m', '1h', '6h', '24h'].map((r) => t(`watch.host.range.${r}`)))
    fireEvent.click(sw.getByRole('button', { name: t('watch.host.range.6h') }))
    expect(screen.getByRole('button', { name: t('watch.host.resources', { range: t('watch.host.range.6h') }) })).toBeInTheDocument()
    expect(screen.getByTestId('axis')).toHaveTextContent(t('watch.host.axis_hour', { n: 6 }))
    expect(readHostRange('d-a')).toBe('6h')
    expect(readHostRange('other')).toBe('30m')
  })

  it('has no 狀態｜設定 switch yet', () => {
    load([host('d-a')])
    renderPane('d-a')
    expect(screen.queryByTestId('view-switch')).toBeNull()
  })

  describe('a stale fleet (no snapshot arrives)', () => {
    afterEach(() => vi.useRealTimers())
    it('the axis keeps moving: a 5 s tick re-reads now', () => {
      vi.useFakeTimers()
      const now = Date.now()
      fleetHistory.record('d-a', fast({ at: now - 130_000 }))
      load([host('d-a', { alias: 'air26' })])
      renderPane('d-a')
      expect(screen.getByTestId('axis-collecting')).toHaveTextContent(t('watch.host.collecting', { n: 2 }))
      act(() => { vi.advanceTimersByTime(65_000) })
      expect(screen.getByTestId('axis-collecting')).toHaveTextContent(t('watch.host.collecting', { n: 3 }))
    })
    it('the interval is cleared on unmount', () => {
      vi.useFakeTimers()
      load([host('d-a')])
      const { unmount } = renderPane('d-a')
      unmount()
      expect(vi.getTimerCount()).toBe(0)
    })
  })
})
