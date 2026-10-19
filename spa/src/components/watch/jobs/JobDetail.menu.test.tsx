// spa/src/components/watch/jobs/JobDetail.menu.test.tsx — the 「⋯」 menu inside the detail: it sits in the header only where a daemon announces jobs.control.v1, and what a refused change says is shown under the header for that job only.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

vi.mock('../../../lib/pool/jobs-api', () => ({ fetchJob: vi.fn(() => new Promise(() => {})), fetchJobLog: vi.fn() }))
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ liveTopology: vi.fn() }))
vi.mock('../../../lib/pool/jobs-control-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/jobs-control-api')>()), cancelJob: vi.fn(), setJobPosition: vi.fn() }))

import { JobDetail } from './JobDetail'
import { cancelJob } from '../../../lib/pool/jobs-control-api'
import { liveTopology } from '../../../lib/pool/jobs-driver-live'
import { PoolChangeError } from '../../../lib/pool/control-api'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { clearJobsViewMemory } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const job = (id: string): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0,
})
const announce = (...ids: string[]) => useNexHostStore.setState({ byHost: Object.fromEntries(ids.map((id) => [id, { daemonCapabilities: ['jobs.control.v1'], fetchedAt: 1 }])) } as never)

beforeEach(() => {
  cleanup(); clearJobsViewMemory()
  vi.mocked(cancelJob).mockReset()
  vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] })
  announce('d-mlab')
})
afterEach(() => cleanup())

describe('the menu in the detail', () => {
  it('is in the header where the capability is announced, and not where it is not', () => {
    const view = render(<JobDetail job={job('j1')} now={1000} onClose={() => {}} />)
    expect(screen.getByTestId('job-menu-trigger')).toBeInTheDocument()
    act0(() => announce())
    view.rerender(<JobDetail job={job('j1')} now={1000} onClose={() => {}} />)
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
  })

  it('a refused change is told under the header, for that job only', async () => {
    vi.mocked(cancelJob).mockRejectedValue(new PoolChangeError('POST', '/x', 409, { code: 'wwd_rejected', action: 'revert' }, 'job ended'))
    const view = render(<JobDetail job={job('j1')} now={1000} onClose={() => {}} />)
    fireEvent.click(screen.getByTestId('job-menu-trigger'))
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(await screen.findByTestId('job-menu-notice')).toHaveTextContent(t('watch.jobs.menu.ended'))
    view.rerender(<JobDetail job={job('j2')} now={1000} onClose={() => {}} />)
    expect(screen.queryByTestId('job-menu-notice')).toBeNull()
  })
})

function act0(fn: () => void) { fn() }
