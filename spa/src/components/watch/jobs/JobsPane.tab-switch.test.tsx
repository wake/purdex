// Regression (CLAUDE.md tab-hosted checklist): the 工作 tab's mode (佇列與執行中 / 歷史) survives switching to another tab and back. The pane really unmounts (keepAliveCount 0; watch-jobs is not a "light" kind),
// so this mounts the real TabContent.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

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
})
