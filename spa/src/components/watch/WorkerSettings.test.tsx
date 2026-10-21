import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { WorkerSettings, PENDING_NOTICE_MS } from './WorkerSettings'
import { usePoolStore } from '../../lib/pool/store'
import { clearControlDenied } from '../../lib/pool/control-api'
import { clearWorkerDrafts } from '../../lib/pool/worker-draft-memory'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import type { PoolHost, PoolWorker } from '../../lib/pool/types'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const hostFetch = vi.fn()
vi.mock('../../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../../lib/host-api')>()),
  hostFetch: (...a: unknown[]) => hostFetch(...a),
}))

const CFG = 'cfg-a'
const facts = parseFacts(darwinJson)!
const WORKER: PoolWorker = { enabled: true, slots: { heavy: 1, light: 2 }, owner_rule: 'hid<5m' }
/** The pool's picture of the host `mlab`; `worker` is what its facts carry. */
function mlab(worker: PoolWorker = WORKER, over: Partial<PoolHost> = {}): PoolHost {
  const base = parseHostsAnswer(hostsJson)![0]
  return { ...base, alias: 'mlab', reachable: true, worker, labels: ['os:darwin', 'gpu'], facts: { ...facts, worker: { ...facts.worker, ...worker } }, ...over }
}
/** The same host read `dt` ms later. */
const later = (h: PoolHost, dt = 5000): PoolHost => ({ ...h, facts: { ...h.facts!, at: h.facts!.at + dt } })
const setHost = (h: PoolHost) => act(() => { usePoolStore.setState({ hosts: [h], sourceHostId: CFG }) })
const reply = (status: number, body: unknown = null) => new Response(body === null ? '' : JSON.stringify(body), { status })
const lastPut = () => {
  const call = hostFetch.mock.calls.at(-1)!
  return { path: call[1] as string, init: call[2] as RequestInit, body: JSON.parse((call[2] as RequestInit).body as string) }
}
const click = async (id: string) => { await act(async () => { fireEvent.click(screen.getByTestId(id)) }) }

let readOnce: ReturnType<typeof vi.fn>
beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  hostFetch.mockReset()
  hostFetch.mockResolvedValue(reply(200, { ok: true }))
  clearControlDenied()
  clearWorkerDrafts()
  readOnce = vi.fn()
  usePoolStore.getState().reset()
  usePoolStore.setState({ readOnce } as never)
  useHostStore.setState({ hosts: { [CFG]: { id: CFG, name: CFG, ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [CFG], activeHostId: CFG, runtime: { [CFG]: { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1', 'pool.control.v1'] } } } as never)
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('WorkerSettings', () => {
  it('label chips can wrap and break a long unbroken label instead of overflowing the row', () => {
    setHost(mlab(WORKER, { labels: ['os:darwin', 'task:' + 'x'.repeat(200)] }))
    render(<WorkerSettings alias="mlab" />)
    const chips = Array.from(screen.getByTestId('pool-worker-labels').children)
    expect(chips).toHaveLength(2)
    for (const c of chips) {
      expect(c.className).toContain('max-w-full')
      expect(c.className).toContain('break-all')
    }
  })

  it('shows what the facts carry: the switch, both slot counts, the labels; the owner row only for a host with an owner', () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-enabled')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('pool-worker-slots-heavy-value')).toHaveTextContent('1')
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('2')
    expect(screen.getByTestId('pool-worker-owner-rule')).toHaveValue('5')
    expect(screen.getByTestId('pool-worker-owner-state')).toHaveTextContent(t('pool.worker.owner_active', { reason: 'hid' }))
    expect(screen.getByTestId('pool-worker-labels')).toHaveTextContent('os:darwin')
    // many/long labels must not squeeze the title: the row is stacked and the chips wrap from the left
    expect(screen.getByTestId('pool-worker-labels').closest('[data-layout="stacked"]')).not.toBeNull()
    expect(screen.getByTestId('pool-worker-labels').className).not.toContain('justify-end')
    cleanup()
    setHost(mlab(WORKER, { facts: { ...facts, owner: null, worker: { ...facts.worker, ...WORKER } } }))
    render(<WorkerSettings alias="mlab" />)
    expect(screen.queryByTestId('pool-worker-owner-rule')).toBeNull()
  })

  it('renders nothing for a host the pool does not list, and says so when no daemon announces pool.control.v1', () => {
    setHost(mlab())
    const { container } = render(<WorkerSettings alias="nobody" />)
    expect(container).toBeEmptyDOMElement()
    cleanup()
    useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-unsupported')).toBeInTheDocument()
    expect(screen.queryByTestId('pool-worker-enabled')).toBeNull()
  })

  it('sends the whole settings object in one PUT to the host\'s alias', async () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-heavy-inc')
    const put = lastPut()
    expect(put.path).toBe('/api/pool/hosts/mlab/worker')
    expect(put.init.method).toBe('PUT')
    expect(put.body).toEqual({ enabled: true, slots: { heavy: 2, light: 2 }, owner_rule: 'hid<5m' })
  })

  it('turning the switch off with jobs running asks first and says that they finish; with none running it does not ask', async () => {
    setHost(mlab()) // the fixture runs one light job
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-enabled')
    expect(hostFetch).not.toHaveBeenCalled()
    expect(screen.getByTestId('pool-worker-confirm-off')).toHaveTextContent(t('pool.worker.confirm_off', { n: 1 }))
    await click('pool-worker-confirm-no')
    expect(hostFetch).not.toHaveBeenCalled()
    await click('pool-worker-enabled')
    await click('pool-worker-confirm-yes')
    expect(lastPut().body.enabled).toBe(false)
    cleanup()
    hostFetch.mockClear()
    setHost(mlab(WORKER, { facts: { ...facts, worker: { ...facts.worker, ...WORKER, running: { heavy: 0, light: 0 } } } }))
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-enabled')
    expect(screen.queryByTestId('pool-worker-confirm-off')).toBeNull()
    expect(lastPut().body.enabled).toBe(false)
  })

  it('keeps the new value pending until the facts carry it, then settles; after 15 s without it says the host has not reported', async () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-light-inc')
    expect(readOnce).toHaveBeenCalled()
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('3') // the sent value, not the stale facts
    expect(screen.getByTestId('pool-worker-pending')).toBeInTheDocument()
    expect(screen.queryByTestId('pool-worker-late')).toBeNull()
    act(() => { vi.advanceTimersByTime(PENDING_NOTICE_MS - 1) })
    expect(screen.queryByTestId('pool-worker-late')).toBeNull()
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.getByTestId('pool-worker-late')).toHaveTextContent(t('pool.worker.late'))
    setHost(later(mlab({ ...WORKER, slots: { heavy: 1, light: 3 } })))
    expect(screen.queryByTestId('pool-worker-pending')).toBeNull()
    expect(screen.queryByTestId('pool-worker-late')).toBeNull()
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('3')
  })

  it('facts that are not newer than the ones the page showed do not settle a change, even when they hold the sent value', async () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-light-inc')
    setHost(mlab({ ...WORKER, slots: { heavy: 1, light: 3 } })) // an older answer on its way: same reading time as before the PUT
    expect(screen.getByTestId('pool-worker-pending')).toBeInTheDocument()
    setHost(later(mlab({ ...WORKER, slots: { heavy: 1, light: 3 } })))
    expect(screen.queryByTestId('pool-worker-pending')).toBeNull()
  })

  it('a retry changes the one field on top of the settings as they are at the click, not as they were at the failure', async () => {
    hostFetch.mockResolvedValueOnce(reply(502, { error: 'wwd_error', detail: 'timeout' }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-light-inc') // fails; light would be 3
    setHost(later(mlab({ ...WORKER, slots: { heavy: 4, light: 2 }, owner_rule: 'hid<9m' }))) // meanwhile another App changed the others
    await click('pool-worker-retry-light')
    expect(lastPut().body).toEqual({ enabled: true, slots: { heavy: 4, light: 3 }, owner_rule: 'hid<9m' })
  })

  it('latest wins: three presses while a PUT is out show +3 at once and make two PUTs that never overlap, the last carrying +3', async () => {
    const answers: Array<(r: Response) => void> = []
    hostFetch.mockImplementation(() => new Promise<Response>((resolve) => { answers.push(resolve) }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await act(async () => {
      for (let i = 0; i < 3; i++) fireEvent.click(screen.getByTestId('pool-worker-slots-heavy-inc'))
    })
    expect(screen.getByTestId('pool-worker-slots-heavy-value')).toHaveTextContent('4') // drawn at once, not after the host answers
    expect(hostFetch).toHaveBeenCalledTimes(1) // the second and third presses wait for the one in the air
    expect(JSON.parse((hostFetch.mock.calls[0][2] as RequestInit).body as string).slots.heavy).toBe(2)
    await act(async () => { answers[0](reply(200, { ok: true })) })
    expect(hostFetch).toHaveBeenCalledTimes(2) // sent only after the first was answered
    expect(lastPut().body.slots.heavy).toBe(4)
    await act(async () => { answers[1](reply(200, { ok: true })) })
    expect(hostFetch).toHaveBeenCalledTimes(2) // nothing newer was asked: no third PUT
    expect(screen.getByTestId('pool-worker-slots-heavy-value')).toHaveTextContent('4')
  })

  it('a later PUT of a run that fails is shown under the row that asked for it last, and its retry asks again what that PUT changed', async () => {
    const answers: Array<(r: Response) => void> = []
    hostFetch.mockImplementation(() => new Promise<Response>((resolve) => { answers.push(resolve) }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await act(async () => {
      fireEvent.click(screen.getByTestId('pool-worker-slots-heavy-inc')) // PUT 1 goes out: heavy 2
      fireEvent.click(screen.getByTestId('pool-worker-slots-light-inc')) // queued: heavy 2, light 3
    })
    await act(async () => { answers[0](reply(200, { ok: true })) }) // PUT 2 goes out
    expect(lastPut().body.slots).toEqual({ heavy: 2, light: 3 })
    await act(async () => { answers[1](reply(502, { error: 'wwd_error', detail: 'timeout' })) })
    expect(screen.getByTestId('pool-worker-error-light')).toBeInTheDocument() // the row of the last press, not the first
    expect(screen.queryByTestId('pool-worker-error-heavy')).toBeNull()
    await act(async () => { fireEvent.click(screen.getByTestId('pool-worker-retry-light')) })
    expect(hostFetch).toHaveBeenCalledTimes(3)
    expect(lastPut().body.slots).toEqual({ heavy: 2, light: 3 }) // light 3 again on top of heavy 2, which the first PUT had already set
  })

  it('a refused PUT drops what was queued behind it and the rows go back to what the host has', async () => {
    const answers: Array<(r: Response) => void> = []
    hostFetch.mockImplementation(() => new Promise<Response>((resolve) => { answers.push(resolve) }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await act(async () => {
      fireEvent.click(screen.getByTestId('pool-worker-slots-heavy-inc'))
      fireEvent.click(screen.getByTestId('pool-worker-slots-heavy-inc'))
    })
    await act(async () => { answers[0](reply(400, { error: 'wwd_rejected', detail: 'too many' })) })
    expect(hostFetch).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('pool-worker-slots-heavy-value')).toHaveTextContent('1')
    expect(screen.getByTestId('pool-worker-error-heavy')).toHaveTextContent('too many')
  })

  it('a change wwd refuses goes back, with wwd\'s own words under that row', async () => {
    hostFetch.mockResolvedValue(reply(400, { error: 'wwd_rejected', detail: 'slots cannot be negative' }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-heavy-inc')
    expect(screen.getByTestId('pool-worker-error-heavy')).toHaveTextContent('slots cannot be negative')
    expect(screen.getByTestId('pool-worker-slots-heavy-value')).toHaveTextContent('1')
    expect(screen.queryByTestId('pool-worker-pending')).toBeNull()
    expect(screen.queryByTestId('pool-worker-error-light')).toBeNull()
  })

  it('a host that does not answer goes back with a retry that sends the same change again', async () => {
    hostFetch.mockResolvedValueOnce(reply(502, { error: 'wwd_error', detail: 'connection refused' }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-light-inc')
    expect(screen.getByTestId('pool-worker-error-light')).toHaveTextContent(t('pool.worker.no_answer', { alias: 'mlab', reason: 'connection refused' }))
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('2')
    await click('pool-worker-retry-light')
    expect(lastPut().body.slots).toEqual({ heavy: 1, light: 3 })
    expect(screen.queryByTestId('pool-worker-error-light')).toBeNull()
    expect(screen.getByTestId('pool-worker-slots-light-value')).toHaveTextContent('3')
  })

  it('a device token (403) turns the whole section read only with one notice, and stays so on the next visit', async () => {
    hostFetch.mockResolvedValue(reply(403, { error: 'forbidden', detail: 'changing a host\'s worker is for the admin token' }))
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    expect(screen.queryByTestId('pool-worker-readonly')).toBeNull()
    await click('pool-worker-slots-heavy-inc')
    expect(screen.getAllByTestId('pool-worker-readonly')).toHaveLength(1)
    expect(screen.queryByTestId('pool-worker-error-heavy')).toBeNull()
    cleanup()
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-readonly')).toHaveTextContent(t('pool.worker.read_only'))
    expect(screen.getByTestId('pool-worker-slots-heavy-value').closest('[inert]')).not.toBeNull()
  })

  it('read only belongs to the daemon that refused: when another daemon becomes the source, it is judged again', async () => {
    hostFetch.mockResolvedValueOnce(reply(403, { error: 'forbidden', detail: 'admin token only' }))
    useHostStore.setState({
      hosts: { [CFG]: { id: CFG, name: CFG, ip: '1', port: 1, token: 't', order: 0 }, 'cfg-b': { id: 'cfg-b', name: 'b', ip: '2', port: 1, token: 'u', order: 1 } },
      hostOrder: [CFG, 'cfg-b'], runtime: { [CFG]: { status: 'connected' }, 'cfg-b': { status: 'connected' } },
    } as never)
    useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1', 'pool.control.v1'] }, 'cfg-b': { phase: 'ready', daemonCapabilities: ['pool.control.v1'] } } } as never)
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-slots-heavy-inc')
    expect(screen.getByTestId('pool-worker-readonly')).toBeInTheDocument()
    act(() => { useHostStore.setState({ runtime: { [CFG]: { status: 'disconnected' }, 'cfg-b': { status: 'connected' } } } as never) })
    expect(screen.queryByTestId('pool-worker-readonly')).toBeNull()
    await click('pool-worker-slots-heavy-inc')
    expect(hostFetch.mock.calls.at(-1)![0]).toBe('cfg-b')
  })

  it('the owner rule: hid<N>m is edited as minutes, anything else as the text it is, and a bad number is not sent', async () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    const box = screen.getByTestId('pool-worker-owner-rule')
    fireEvent.change(box, { target: { value: 'x' } })
    await act(async () => { fireEvent.blur(box) })
    expect(screen.getByTestId('pool-worker-owner-rule-bad')).toBeInTheDocument()
    expect(hostFetch).not.toHaveBeenCalled()
    fireEvent.change(box, { target: { value: '10' } })
    await act(async () => { fireEvent.blur(box) })
    expect(lastPut().body.owner_rule).toBe('hid<10m')
    cleanup()
    hostFetch.mockClear()
    clearWorkerDrafts()
    setHost(mlab({ ...WORKER, owner_rule: 'when-quiet' }))
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-owner-rule')).toHaveValue('when-quiet')
    fireEvent.change(screen.getByTestId('pool-worker-owner-rule'), { target: { value: 'hid<3m' } })
    await act(async () => { fireEvent.blur(screen.getByTestId('pool-worker-owner-rule')) })
    expect(lastPut().body.owner_rule).toBe('hid<3m')
  })

  it('the owner rule has a minutes stepper: it moves from the stored minutes (5 when the rule is empty) and sends hid<N>m at once', async () => {
    setHost(mlab())
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-owner-rule-inc')
    expect(lastPut().body.owner_rule).toBe('hid<6m')
    cleanup()
    hostFetch.mockClear()
    setHost(mlab({ ...WORKER, owner_rule: '' }))
    render(<WorkerSettings alias="mlab" />)
    await click('pool-worker-owner-rule-dec')
    expect(lastPut().body.owner_rule).toBe('hid<4m')
    cleanup()
    setHost(mlab({ ...WORKER, owner_rule: 'hid<1m' }))
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-owner-rule-dec')).toBeDisabled()
    cleanup()
    setHost(mlab({ ...WORKER, owner_rule: 'when-quiet' }))
    render(<WorkerSettings alias="mlab" />)
    expect(screen.queryByTestId('pool-worker-owner-rule-inc')).toBeNull() // any other text is edited as the text it is
  })

  it('an unsent owner-rule value is kept when the component is remounted', () => {
    setHost(mlab())
    const first = render(<WorkerSettings alias="mlab" />)
    fireEvent.change(screen.getByTestId('pool-worker-owner-rule'), { target: { value: '7' } })
    first.unmount()
    render(<WorkerSettings alias="mlab" />)
    expect(screen.getByTestId('pool-worker-owner-rule')).toHaveValue('7')
  })
})
