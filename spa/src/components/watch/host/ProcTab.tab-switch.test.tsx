// Regression (CLAUDE.md tab-hosted checklist, plan MON-8a): the host page's 行程 tab and its scroll survive switching to another tab and back. The pane really unmounts (keepAliveCount 0), so this mounts the real TabContent.
// 8a has no reader choice of its own to keep (the sub-tab and the scroll are 6f-1's memory); 8b and 8c add their cases to this file.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'

vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
vi.mock('../../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/host-api')>()), hostFetch: () => new Promise(() => {}) }))
const fetchPoolHosts = vi.fn()
const fetchFactsHistory = vi.fn()
const fetchUndeclared = vi.fn()
const fetchObservations = vi.fn()
vi.mock('../../../lib/pool/observations-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/observations-api')>()), fetchUndeclared: (...a: unknown[]) => fetchUndeclared(...a), fetchObservations: (...a: unknown[]) => fetchObservations(...a) }))
vi.mock('../../../lib/pool/api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/api')>()), fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a), fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a) }))

import { TabContent } from '../../TabContent'
import { WatchHostPane } from '../WatchHostPane'
import { poolHostOf, seedPool, SEED_HOST_ID } from '../host-pane-seed'
import { registerModule, clearModuleRegistry } from '../../../lib/module-registry'
import { clearViewMemory, readObsExpanded, readProcAt } from '../../../lib/watch/view-memory'
import { clearProcHistory } from '../../../lib/pool/proc-history'
import { clearHostObservations } from '../../../lib/pool/host-observations'
import { parseFacts } from '../../../lib/pool/parse'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { createTab } from '../../../types/tab'
import type { Tab } from '../../../types/tab'
import { fingerprintOf } from '../../../lib/nex/nex-host-effects'
import { useI18nStore } from '../../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const Other = () => <div data-testid="other-tab" />
const hostTab: Tab = { ...createTab({ kind: 'watch-host', poolHostId: SEED_HOST_ID }), id: `watch-host-${SEED_HOST_ID}` }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearViewMemory(); clearProcHistory(); clearHostObservations(); fetchFactsHistory.mockReset(); fetchUndeclared.mockReset(); fetchObservations.mockReset(); fetchObservations.mockReturnValue(new Promise(() => {}))
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-host', component: WatchHostPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useHostStore.setState({ hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } }, hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: Date.now(), daemonCapabilities: ['pool.v1', 'pool.observations.v1'], wwd: { hostId: SEED_HOST_ID }, fingerprint: fingerprintOf('cfg-a') } } } as never)
  const hosts = [poolHostOf()]
  fetchPoolHosts.mockReset()
  fetchPoolHosts.mockResolvedValue(hosts)
  seedPool(hosts)
})
afterEach(() => clearViewMemory())

describe('行程 across tab switches', () => {
  it('stays on 行程, at the place it was scrolled to, when the reader switches to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.procs') }))
    expect(screen.getByTestId('proc-tab')).toBeInTheDocument()
    const scroller = screen.getByTestId(`watch-host-${SEED_HOST_ID}`)
    scroller.scrollTop = 180
    fireEvent.scroll(scroller)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('proc-tab')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(screen.getByTestId('proc-tab')).toBeInTheDocument()
    expect(screen.getByTestId(`watch-host-${SEED_HOST_ID}`).scrollTop).toBe(180)
  }, 30_000)

  it('keeps the picked record of the hour across the switch, even when live records joined meanwhile; a pick that has left the hour is the live one again (MON-8b)', async () => {
    const t0 = Date.now()
    const facts = parseFacts(darwinJson)!
    const records = []
    for (let at = t0 - 3600_000; at <= t0; at += 5000) records.push({ ...facts, at, procs: [{ pid: 1, name: 'x', cpu_pct: 1, mem_mb: 1, mem_kind: 'footprint', owner: null }] })
    fetchFactsHistory.mockResolvedValue({ step: '5s', records })
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.procs') }))
    await act(async () => { fireEvent.change(screen.getByTestId('proc-timeline'), { target: { value: '200' } }) })
    const note = screen.getByTestId('proc-note').textContent
    expect(note).toContain(':')
    const picked = readProcAt(SEED_HOST_ID)
    expect(picked).not.toBeNull()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('proc-timeline')).toBeNull() // really unmounted, not hidden

    // 20 s later (the host's facts are still fresh): the live end has moved on, the picked record has not, so it sits a step further from the live end.
    const real = Date.now
    const clock = vi.spyOn(Date, 'now').mockImplementation(() => real() + 20_000)
    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    await act(async () => { await Promise.resolve() })
    expect(readProcAt(SEED_HOST_ID)).toBe(picked)
    expect(screen.getByTestId('proc-note').textContent).toBe(note)
    expect((screen.getByTestId('proc-timeline') as HTMLInputElement).value).toBe('199')
    clock.mockRestore()
  }, 30_000)

  it('keeps the groups it read when the reader switches away and back: they are drawn at once, before any new read answers (MON-8c)', async () => {
    const groups = { host: 'air26', known: true, truncated: false, groups: [{ signature: 'go test', count: 2, cpuSeconds: 30, maxMemMb: 900, lastAt: Date.now() - 60_000, lastSession: 'ab12', lastVia: 'session', lastCommand: 'go test ./...' }] }
    fetchUndeclared.mockResolvedValueOnce(groups)
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.procs') }))
    await act(async () => { await Promise.resolve() })
    expect(screen.getByTestId('undeclared-row-go test')).toBeInTheDocument()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('undeclared')).toBeNull() // really unmounted, not hidden

    fetchUndeclared.mockReturnValue(new Promise(() => {})) // the next read never answers
    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(screen.getByTestId('undeclared-row-go test')).toBeInTheDocument()
  }, 30_000)
  it('keeps the expansion and the older page the reader loaded when they switch away and back: the older page is not asked again (MON-8d)', async () => {
    const at = Date.now()
    const rec = (id: string, startedAt: number) => ({ id, host: 'air26', session: 'ab12', via: 'session', command: id, signature: `sig-${id}`, startedAt, endedAt: startedAt + 1000, cpuSeconds: 1, cpuPeak: 100, memPeakMb: 1, procsPeak: 1 })
    const newest = Array.from({ length: 12 }, (_, i) => rec(`n${i}`, at - 60_000 - i * 1000))
    fetchObservations.mockReset()
    fetchObservations.mockImplementation((_s: string, _a: string, q: { until?: string }) => Promise.resolve(q.until
      ? { host: 'air26', known: true, observations: [rec('old1', at - 3600_000)], truncated: false, nextUntil: null }
      : { host: 'air26', known: true, observations: newest, truncated: false, nextUntil: 'CURSOR-1' }))
    const { rerender } = render(<TabContent activeTab={hostTab} allTabs={all} />)
    fireEvent.click(within(screen.getByTestId('page-switch')).getByRole('button', { name: t('watch.host.page.procs') }))
    await act(async () => { await Promise.resolve() })
    expect(screen.getAllByTestId(/^obs-row-/)).toHaveLength(10)
    fireEvent.click(screen.getByTestId('obs-toggle'))
    await act(async () => { fireEvent.click(screen.getByTestId('obs-older')) })
    expect(screen.getByTestId('obs-row-old1')).toBeInTheDocument()
    expect(readObsExpanded(SEED_HOST_ID)).toBe(true)
    const asked = fetchObservations.mock.calls.filter((c) => c[2].until !== undefined).length
    expect(asked).toBe(1)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('obs-records')).toBeNull() // really unmounted, not hidden

    fetchObservations.mockReset()
    fetchObservations.mockReturnValue(new Promise(() => {})) // no read answers from here on
    rerender(<TabContent activeTab={hostTab} allTabs={all} />)
    expect(screen.getAllByTestId(/^obs-row-/)).toHaveLength(13) // still expanded: all 12 and the older one, at once
    expect(screen.getByTestId('obs-row-old1')).toBeInTheDocument()
    expect(screen.getByTestId('obs-toggle')).toHaveTextContent(t('watch.host.obs.show_newest', { n: 10 }))
    expect(fetchObservations.mock.calls.filter((c) => c[2].until !== undefined)).toHaveLength(0) // the older page was not read again
  }, 30_000)
})
