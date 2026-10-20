// spa/src/components/watch/jobs/JobsPane.cancel.test.tsx — M33 through the real JobDetail and JobMenu: cancelling a queued job from the queue flow is at once, a running one asks first.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'

vi.mock('../../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/host-api')>()), hostFetch: vi.fn() }))
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [], liveTopology: () => ({ origins: ['mlab'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] }) }))
vi.mock('../../../lib/pool/jobs-api', () => ({ fetchJob: vi.fn(() => new Promise(() => {})), fetchJobLog: vi.fn() }))
vi.mock('../../../lib/pool/use-cell-models', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/use-cell-models')>()), useRunPool: vi.fn() }))

import { JobsPane } from './JobsPane'
import { hostFetch } from '../../../lib/host-api'
import { clearControlDenied } from '../../../lib/pool/control-api'
import { clearJobsViewMemory } from '../../../lib/pool/jobs-view-memory'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import type { PaneRendererProps } from '../../../lib/module-registry'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import { parseFacts } from '../../../lib/pool/parse'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: Date.now() - 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})

beforeEach(() => {
  cleanup(); clearJobsViewMemory(); clearControlDenied()
  useJobsStore.getState().reset(); usePoolStore.getState().reset()
  useNexHostStore.setState({ byHost: { 'd-mlab': { daemonCapabilities: ['jobs.control.v1'], fetchedAt: 1 } } } as never)
  vi.mocked(hostFetch).mockReset()
  vi.mocked(hostFetch).mockResolvedValue(new Response('{}', { status: 200 }))
})
afterEach(() => cleanup())

const withJobs = (...jobs: PoolJob[]) => {
  useJobsStore.setState({ byOrigin: { mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])), cursor: '1', epoch: 'e', via: 'v' } } })
  usePoolStore.setState({ hosts: [{ host_id: 'id-mlab', alias: 'mlab', version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: { ...parseFacts(darwinJson)!, at: Date.now() }, reachable: true, labels: [], worker: { enabled: true, slots: { heavy: 1, light: 1 }, owner_rule: '' } }], sourceHostId: 'd-mlab' })
}

describe('cancelling from the queue flow (M33)', () => {
  it('a queued job is cancelled at once, with no question', async () => {
    withJobs(job('q1'))
    render(<JobsPane {...({} as PaneRendererProps)} />)
    fireEvent.click(document.querySelector('[data-q="mlab/q1"]')!)
    fireEvent.click(screen.getByTestId('job-menu-trigger'))
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(vi.mocked(hostFetch).mock.calls[0][1]).toBe('/api/pool/jobs/q1/cancel?host=mlab')
    expect(screen.queryByTestId('job-menu-confirm-yes')).toBeNull()
  })

  it('a running job asks first; nothing is sent until the answer is yes', async () => {
    withJobs(job('r1', { state: 'running', claim: { worker: 'mlab', attempt: 1, since: 1 } }))
    render(<JobsPane {...({} as PaneRendererProps)} />)
    fireEvent.click(document.querySelector('[data-j="mlab/r1"]')!)
    fireEvent.click(screen.getByTestId('job-menu-trigger'))
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(hostFetch).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('job-menu-confirm-yes'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(vi.mocked(hostFetch).mock.calls[0][1]).toBe('/api/pool/jobs/r1/cancel?host=mlab')
  })
})
