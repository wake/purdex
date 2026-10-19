// spa/src/components/watch/jobs/JobMenu.test.tsx — the 「⋯」 menu of a job's detail: present only where a daemon announces jobs.control.v1, the places only for an origin the App has its own daemon for, a cancel that asks first only for a job that is going,
// and a refusal told in words. The requests are the real ones, seen at `hostFetch`; the topology is mocked.
import { useState } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react'

vi.mock('../../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../../lib/host-api')>()), hostFetch: vi.fn() }))
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ liveTopology: vi.fn() }))

import { JobMenu, type MenuNotice } from './JobMenu'
import { hostFetch } from '../../../lib/host-api'
import { liveTopology } from '../../../lib/pool/jobs-driver-live'
import { clearControlDenied } from '../../../lib/pool/control-api'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const CAP = 'jobs.control.v1'
const job = (over: Partial<PoolJob> = {}): PoolJob => ({
  id: 'j1', origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const announce = (...ids: string[]) => useNexHostStore.setState({ byHost: Object.fromEntries(ids.map((id) => [id, { daemonCapabilities: [CAP], fetchedAt: 1 }])) } as never)
const reply = (status: number, body: unknown = {}) => vi.mocked(hostFetch).mockResolvedValue(new Response(JSON.stringify(body), { status }))
const calls = () => vi.mocked(hostFetch).mock.calls.map(([id, path, init]) => ({ id, path, method: (init as RequestInit | undefined)?.method, body: (init as RequestInit | undefined)?.body }))

/** What the detail does around the menu: it shows the notice. */
function Harness({ j }: { j: PoolJob }) {
  const [notice, setNotice] = useState<MenuNotice | null>(null)
  return (
    <div>
      <JobMenu job={j} onNotice={setNotice} />
      {notice && <p data-testid="notice">{notice.text}{notice.retry && <button type="button" data-testid="retry" onClick={notice.retry} />}</p>}
    </div>
  )
}
const open = () => fireEvent.click(screen.getByTestId('job-menu-trigger'))
const items = () => screen.queryAllByRole('menuitem').map((e) => e.getAttribute('data-testid'))

beforeEach(() => {
  cleanup(); clearControlDenied()
  vi.mocked(hostFetch).mockReset(); reply(200, {})
  useJobsStore.getState().reset()
  vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab', 'mserv'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] })
  announce('d-mlab')
})
afterEach(() => { cleanup(); clearControlDenied() })

describe('whether the menu is there', () => {
  it('absent while no daemon announces jobs.control.v1, present once one does (the same mounted menu, the capability flipped)', () => {
    announce()
    render(<Harness j={job()} />)
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
    act(() => announce('d-mlab'))
    expect(screen.getByTestId('job-menu-trigger')).toBeInTheDocument()
    act(() => announce())
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
  })

  it('a job that has ended has no menu', () => {
    render(<Harness j={job({ state: 'failed' })} />)
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
  })
})

describe('the items', () => {
  it('a queued job of an origin with its own daemon: the places and cancel', () => {
    render(<Harness j={job()} />)
    open()
    expect(items()).toEqual(['job-menu-front', 'job-menu-back', 'job-menu-hold', 'job-menu-cancel'])
  })

  it('a held job offers 恢復 instead of 暫緩', () => {
    render(<Harness j={job({ manual: 'hold' })} />)
    open()
    expect(items()).toEqual(['job-menu-front', 'job-menu-back', 'job-menu-normal', 'job-menu-cancel'])
  })

  it('an origin the App has no daemon for (mserv): cancel only', () => {
    render(<Harness j={job({ origin: 'mserv' })} />)
    open()
    expect(items()).toEqual(['job-menu-cancel'])
  })
})

describe('the requests', () => {
  it('a place goes to the origin\'s own daemon, naming the origin, with the place in the body', async () => {
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-front'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(calls()[0]).toEqual({ id: 'd-mlab', path: '/api/pool/jobs/j1/position?host=mlab', method: 'POST', body: JSON.stringify({ position: 'front' }) })
  })

  it('恢復 sends `normal`', async () => {
    render(<Harness j={job({ manual: 'back' })} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-normal'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(calls()[0].body).toBe(JSON.stringify({ position: 'normal' }))
  })

  it('cancelling a queued job is at once, with no question, to …/cancel?host=<origin>', async () => {
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(calls()[0]).toMatchObject({ id: 'd-mlab', path: '/api/pool/jobs/j1/cancel?host=mlab', method: 'POST' })
    expect(screen.queryByTestId('job-menu-confirm-yes')).toBeNull()
  })

  it('a cancel for an origin the App has no daemon for goes through a forwarder that announces it, naming the origin', async () => {
    render(<Harness j={job({ origin: 'mserv' })} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(calls()[0]).toMatchObject({ id: 'd-mlab', path: '/api/pool/jobs/j1/cancel?host=mserv' })
  })

  it('cancelling a running job asks first: 不取消 sends nothing, 取消工作 sends the cancel', async () => {
    render(<Harness j={job({ state: 'running', claim: { worker: 'mlab', attempt: 1, since: 1 } })} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(hostFetch).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('job-menu-confirm-no'))
    expect(hostFetch).not.toHaveBeenCalled()
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    fireEvent.click(screen.getByTestId('job-menu-confirm-yes'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(1))
    expect(calls()[0].path).toBe('/api/pool/jobs/j1/cancel?host=mlab')
  })
})

describe('a refusal', () => {
  it('a job that has ended (wwd 409) says so', async () => {
    reply(409, { error: 'wwd_rejected', detail: 'job ended' })
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(await screen.findByTestId('notice')).toHaveTextContent(t('watch.jobs.menu.ended'))
    expect(screen.queryByTestId('retry')).toBeNull()
  })

  it('another wwd 4xx says wwd\'s own words', async () => {
    reply(404, { error: 'wwd_rejected', detail: 'no such job' })
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-front'))
    expect(await screen.findByTestId('notice')).toHaveTextContent('no such job')
  })

  it('a device token (403): the notice says it, and the menu is gone', async () => {
    reply(403, { error: 'forbidden' })
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-hold'))
    expect(await screen.findByTestId('notice')).toHaveTextContent(t('pool.error.forbidden'))
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
  })

  it('a host that does not answer (502) says so with the reason, and 重試 sends the same change again', async () => {
    reply(502, { error: 'host_unreachable', detail: 'dial tcp: timeout' })
    render(<Harness j={job({ origin: 'mserv' })} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(await screen.findByTestId('notice')).toHaveTextContent(t('watch.jobs.menu.no_answer', { host: 'mserv', reason: 'dial tcp: timeout' }))
    reply(200, {})
    fireEvent.click(screen.getByTestId('retry'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(2))
    expect(calls()[1]).toEqual(calls()[0])
    await waitFor(() => expect(screen.queryByTestId('notice')).toBeNull())
  })

  it('a connection that drops (no answer at all) says nothing was sent, with 重試; the retry asks the daemon that is connected then', async () => {
    vi.mocked(hostFetch).mockRejectedValue(new Error('network down'))
    render(<Harness j={job({ origin: 'mserv' })} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-cancel'))
    expect(await screen.findByTestId('notice')).toHaveTextContent(t('watch.jobs.menu.unreachable'))
    // the first daemon is gone and another has come: the retry goes there
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab', 'mserv'], ownDaemon: {}, forwarders: ['d-air26'] })
    act(() => announce('d-air26'))
    reply(200, {})
    fireEvent.click(screen.getByTestId('retry'))
    await waitFor(() => expect(hostFetch).toHaveBeenCalledTimes(2))
    expect(calls()[0].id).toBe('d-mlab')
    expect(calls()[1]).toMatchObject({ id: 'd-air26', path: '/api/pool/jobs/j1/cancel?host=mserv' })
  })

  it('a daemon that is too busy (429) says so, with 重試', async () => {
    reply(429, { error: 'busy' })
    render(<Harness j={job()} />)
    open()
    fireEvent.click(screen.getByTestId('job-menu-back'))
    expect(await screen.findByTestId('notice')).toHaveTextContent(t('pool.error.busy'))
    expect(screen.getByTestId('retry')).toBeInTheDocument()
  })

  it('nothing goes out when the daemon is gone by the time of the press (no daemon, no request)', () => {
    announce()
    render(<Harness j={job()} />)
    expect(screen.queryByTestId('job-menu-trigger')).toBeNull()
    expect(hostFetch).not.toHaveBeenCalled()
  })
})
