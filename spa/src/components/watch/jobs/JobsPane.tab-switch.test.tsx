// Regression (CLAUDE.md tab-hosted checklist): the 工作 tab's mode (佇列與執行中 / 歷史) survives switching to another tab and back. The pane really unmounts (keepAliveCount 0; watch-jobs is not a "light" kind),
// so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'

vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
vi.mock('../../../lib/pool/use-cell-models', () => ({ useRunPool: vi.fn() }))

import { TabContent } from '../../TabContent'
import { JobsPane } from './JobsPane'
import { registerModule, clearModuleRegistry } from '../../../lib/module-registry'
import { clearJobsViewMemory } from '../../../lib/pool/jobs-view-memory'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { createTab } from '../../../types/tab'
import type { Tab } from '../../../types/tab'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import { readHistoryScroll } from '../../../lib/pool/jobs-view-memory'

const t = (k: string) => useI18nStore.getState().t(k)
const Other = () => <div data-testid="other-tab" />
const jobsTab: Tab = { ...createTab({ kind: 'watch-jobs' }), id: 'watch-jobs' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [jobsTab, dashTab]

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearJobsViewMemory()
  registerModule({ id: 'watch', name: 'Watch', panes: [{ kind: 'watch-jobs', component: JobsPane }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useJobsStore.getState().reset()
  usePoolStore.getState().reset()
})
afterEach(() => clearJobsViewMemory())

const ended = (id: string, finished: number, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'succeeded', claim: null,
  result: { exit: 0, summary: null, log: '', host: 'mserv', started_at: finished - 1000, finished_at: finished, cache_hit: false, reason: '', superseded_by: null },
  manual: '', manual_seq: 0, ...over,
})

describe('the jobs tab across tab switches', () => {
  it('keeps the mode the reader picked when they switch to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={jobsTab} allTabs={all} />)
    expect(screen.getByTestId('job-lanes')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.mode.history') }))
    expect(screen.getByTestId('job-history-none')).toBeInTheDocument()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('jobs-pane')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={jobsTab} allTabs={all} />)
    expect(screen.getByTestId('job-history-none')).toBeInTheDocument()
    expect(screen.queryByTestId('job-lanes')).toBeNull()
    expect(screen.getByRole('button', { name: t('watch.jobs.mode.history') })).toHaveAttribute('aria-pressed', 'true')
  }, 30_000) // the first TabContent render pulls in its lazy modules, which is slow on a loaded host
  it('keeps the history\'s filters, the selected job and the scroll position', () => {
    const jobs = [ended('ok', 5000), ended('bad', 4000, { state: 'failed' }), ended('bad2', 3000, { state: 'failed', repo: { name: 'mlab', root: '', remote: '' } })]
    useJobsStore.setState({ byOrigin: { mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])), cursor: '1', epoch: 'e', via: 'v' } } })
    usePoolStore.setState({ hosts: [{ host_id: 'id-mlab', alias: 'mlab', version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: null, reachable: true, labels: [], worker: { enabled: true, slots: { heavy: 1, light: 1 }, owner_rule: '' } }] })
    const { rerender } = render(<TabContent activeTab={jobsTab} allTabs={all} />)
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.mode.history') }))
    fireEvent.click(within(screen.getByTestId('job-filter-group')).getByRole('button', { name: t('watch.jobs.filter.group.failed') }))
    fireEvent.change(screen.getByLabelText(t('watch.jobs.filter.repo')), { target: { value: 'purdex' } })
    expect(screen.getAllByTestId('job-history-row').map((r) => r.getAttribute('data-job-id'))).toEqual(['bad'])
    fireEvent.click(screen.getByTestId('job-history-row'))
    const box = screen.getByTestId('job-history-scroll')
    box.scrollTop = 64
    fireEvent.scroll(box)
    expect(readHistoryScroll()).toBe(64)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('jobs-pane')).toBeNull()

    rerender(<TabContent activeTab={jobsTab} allTabs={all} />)
    expect(screen.getAllByTestId('job-history-row').map((r) => r.getAttribute('data-job-id'))).toEqual(['bad'])
    expect(within(screen.getByTestId('job-filter-group')).getByRole('button', { name: t('watch.jobs.filter.group.failed') })).toHaveAttribute('aria-pressed', 'true')
    expect((screen.getByLabelText(t('watch.jobs.filter.repo')) as HTMLSelectElement).value).toBe('purdex')
    expect(screen.getByTestId('job-history-row')).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByTestId('job-history-scroll').scrollTop).toBe(64)
  }, 30_000)
})
