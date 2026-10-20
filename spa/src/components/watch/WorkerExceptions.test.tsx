import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { WorkerExceptions } from './WorkerExceptions'
import { PENDING_NOTICE_MS } from './WorkerSettings'
import { usePoolStore } from '../../lib/pool/store'
import { clearControlDenied } from '../../lib/pool/control-api'
import { clearWorkerDrafts } from '../../lib/pool/worker-draft-memory'
import { untilClock } from '../../lib/pool/pause'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import type { PoolHost } from '../../lib/pool/types'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
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
const NOW = new Date(2026, 9, 19, 12, 0, 0, 0).getTime()
const facts = parseFacts(darwinJson)! // exceptions: app-dev-package (ends at a time), dev-server (no end time: it follows a process)
const reply = (status: number, body: unknown = null) => new Response(body === null ? '' : JSON.stringify(body), { status })
function mlab(names?: string[]): PoolHost {
  const base = parseHostsAnswer(hostsJson)![0]
  const kept = names === undefined ? facts.exceptions : facts.exceptions.filter((e) => names.includes(e.name))
  return { ...base, alias: 'mlab', reachable: true, labels: [], facts: { ...facts, exceptions: kept } }
}
const setHost = (h: PoolHost) => act(() => { usePoolStore.setState({ hosts: [h], sourceHostId: CFG }) })
/** The calls that changed something, in order. */
const changes = () => hostFetch.mock.calls.filter((c) => (c[2] as RequestInit | undefined)?.method !== undefined)
const lastChange = () => { const c = changes().at(-1)!; return { path: c[1] as string, method: (c[2] as RequestInit).method, body: (c[2] as RequestInit).body ? JSON.parse((c[2] as RequestInit).body as string) : undefined } }
const click = async (id: string) => { await act(async () => { fireEvent.click(screen.getByTestId(id)) }) }

let details: unknown // what GET …/exceptions answers; undefined → 404 (an older host)
beforeEach(() => {
  cleanup()
  localStorage.clear()
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  hostFetch.mockReset()
  details = undefined
  hostFetch.mockImplementation(async (_id: string, path: string, init?: RequestInit) => {
    if (init?.method === undefined && path.endsWith('/exceptions')) return details === undefined ? reply(404, { error: 'not_found' }) : reply(200, details)
    return reply(200, { ok: true })
  })
  clearControlDenied()
  clearWorkerDrafts()
  usePoolStore.getState().reset()
  usePoolStore.setState({ readOnce: vi.fn() } as never)
  useHostStore.setState({ hosts: { [CFG]: { id: CFG, name: CFG, ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [CFG], activeHostId: CFG, runtime: { [CFG]: { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1', 'pool.control.v1'] } } } as never)
})
afterEach(() => { cleanup(); vi.useRealTimers() })

const mount = async () => { render(<WorkerExceptions alias="mlab" />); await act(async () => {}) }

describe('the list', () => {
  it('shows the host\'s pauses from its facts; an older host (404) leaves what they hold unknown', async () => {
    setHost(mlab())
    await mount()
    expect(screen.getByTestId('pool-pause-row-app-dev-package')).toHaveTextContent('Paused until')
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.follows'))
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.holds_unknown'))
  })

  it('with the list route: what each holds, and 「跟著 pid」 for one that follows a process', async () => {
    details = { host: 'mlab', known: true, exceptions: [
      { name: 'app-dev-package', until: '2026-10-19T18:30:00+08:00', pid: null, holds: ['heavy', 'light'] },
      { name: 'dev-server', until: null, pid: 4242, holds: ['heavy'], allow: { classes: ['release'] }, flags: ['release'] },
    ] }
    setHost(mlab())
    await mount()
    expect(screen.getByTestId('pool-pause-row-app-dev-package')).toHaveTextContent(t('pool.pause.holds_both'))
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.pid', { pid: 4242 }))
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.holds_heavy'))
  })

  it('an older wwd answers 200 with known:false: what they hold stays unknown (not "holds nothing")', async () => {
    details = { host: 'mlab', known: false, exceptions: [] }
    setHost(mlab())
    await mount()
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.holds_unknown'))
  })

  it.each([[404, 'wwd_rejected'], [502, 'wwd_error'], [503, 'wwd_unavailable']])('a %i (%s) on the list route leaves what they hold unknown', async (status, error) => {
    hostFetch.mockImplementation(async (_id: string, path: string, init?: RequestInit) => (init?.method === undefined && path.endsWith('/exceptions') ? reply(status, { error }) : reply(200, { ok: true })))
    setHost(mlab())
    await mount()
    expect(screen.getByTestId('pool-pause-row-dev-server')).toHaveTextContent(t('pool.pause.holds_unknown'))
  })

  it('says so when there is none, and renders nothing for a host the pool does not list', async () => {
    setHost(mlab([]))
    await mount()
    expect(screen.getByTestId('pool-pause-none')).toBeInTheDocument()
    cleanup()
    usePoolStore.setState({ hosts: [], sourceHostId: CFG })
    const { container } = render(<WorkerExceptions alias="mlab" />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('adding', () => {
  it('1 / 3 / 8 小時 end that long from now, holding heavy work', async () => {
    setHost(mlab([]))
    await mount()
    for (const h of [1, 3, 8]) {
      const now = Date.now()
      await click(`pool-pause-add-${h}h`)
      const c = lastChange()
      expect(c.method).toBe('POST')
      expect(c.path).toBe('/api/pool/hosts/mlab/exceptions')
      expect(c.body.name).toMatch(/^app-pause-[0-9a-z]+-[0-9a-z]{4}$/)
      expect(c.body.until).toBe(new Date(now + h * 3_600_000).toISOString())
      expect(c.body).toMatchObject({ pid: null, holds: ['heavy'] })
      act(() => { vi.advanceTimersByTime(5) }) // the next pause is made a moment later: a different name
    }
    expect(new Set(changes().map((c) => JSON.parse((c[2] as RequestInit).body as string).name)).size).toBe(3)
  })

  it('"also hold light work" makes it hold both', async () => {
    setHost(mlab([]))
    await mount()
    fireEvent.click(screen.getByTestId('pool-pause-light'))
    await click('pool-pause-add-1h')
    expect(lastChange().body.holds).toEqual(['heavy', 'light'])
  })

  it('picks no class to let through: the body has no `allow` key (an empty allow would hold everything)', async () => {
    setHost(mlab([]))
    await mount()
    await click('pool-pause-add-1h')
    expect(lastChange().body).not.toHaveProperty('allow')
    expect(lastChange().body).not.toHaveProperty('flags')
  })

  it('classes picked go as allow.classes; marks picked go as flags', async () => {
    setHost(mlab([]))
    await mount()
    fireEvent.click(screen.getByTestId('pool-pause-allow-release'))
    fireEvent.click(screen.getByTestId('pool-pause-allow-merge'))
    fireEvent.click(screen.getByTestId('pool-pause-flag-release'))
    await click('pool-pause-add-1h')
    expect(lastChange().body).toMatchObject({ allow: { classes: ['release', 'merge'], repos: [] }, flags: ['release'] })
  })

  it('"expect pressure" is offered only when the host reports PSI', async () => {
    setHost(mlab([]))
    await mount()
    expect(screen.queryByTestId('pool-pause-flag-expect_pressure')).toBeNull()
    cleanup()
    const base = mlab([])
    setHost({ ...base, facts: { ...base.facts!, mem: { ...base.facts!.mem, psi: { some10: 1, full10: 0, some60: 2 } } } })
    await mount()
    fireEvent.click(screen.getByTestId('pool-pause-flag-expect_pressure'))
    await click('pool-pause-add-1h')
    expect(lastChange().body.flags).toEqual(['expect_pressure'])
  })

  it('a custom end time ends at the next time the clock reads it, and is remembered and offered again', async () => {
    setHost(mlab([]))
    await mount()
    expect(screen.queryByTestId('pool-pause-recent')).toBeNull()
    expect(screen.getByTestId('pool-pause-add-clock')).toBeDisabled()
    fireEvent.change(screen.getByTestId('pool-pause-clock'), { target: { value: '18:30' } })
    await click('pool-pause-add-clock')
    expect(lastChange().body.until).toBe(new Date(untilClock(NOW, '18:30')!).toISOString())
    expect(screen.getByTestId('pool-pause-recent-18:30')).toBeInTheDocument()
    expect(screen.getByTestId('pool-pause-clock')).toHaveValue('')
    act(() => { vi.advanceTimersByTime(5) })
    fireEvent.change(screen.getByTestId('pool-pause-clock'), { target: { value: '07:15' } })
    await click('pool-pause-add-clock')
    expect(screen.getAllByTestId(/^pool-pause-recent-/).map((b) => b.textContent)).toEqual(['07:15', '18:30'])
    // another visit, same device: the remembered times are there, and pressing one adds that pause
    cleanup()
    hostFetch.mockClear()
    await mount()
    await click('pool-pause-recent-18:30')
    expect(lastChange().body.until).toBe(new Date(untilClock(NOW, '18:30')!).toISOString())
  })

  it('shows the new pause at once, waiting for the host to report it, and settles when its facts list it', async () => {
    setHost(mlab([]))
    await mount()
    await click('pool-pause-add-3h')
    const name = lastChange().body.name as string
    expect(screen.getByTestId(`pool-pause-row-${name}`)).toBeInTheDocument()
    expect(screen.getByTestId(`pool-pause-pending-${name}`)).toBeInTheDocument()
    expect(screen.queryByTestId('pool-pause-late')).toBeNull()
    act(() => { vi.advanceTimersByTime(PENDING_NOTICE_MS) })
    expect(screen.getByTestId('pool-pause-late')).toHaveTextContent(t('pool.worker.late'))
    const h = mlab([])
    setHost({ ...h, facts: { ...h.facts!, exceptions: [{ name, since: NOW, until: NOW + 3 * 3_600_000 }] } })
    expect(screen.queryByTestId(`pool-pause-pending-${name}`)).toBeNull()
    expect(screen.queryByTestId('pool-pause-late')).toBeNull()
    expect(screen.getAllByTestId(`pool-pause-row-${name}`)).toHaveLength(1)
  })

  it('a pause wwd refuses says why under the form', async () => {
    hostFetch.mockImplementation(async (_i: string, _p: string, init?: RequestInit) => (init?.method === 'POST' ? reply(400, { error: 'wwd_rejected', detail: 'invalid hold' }) : reply(404, { error: 'not_found' })))
    setHost(mlab([]))
    await mount()
    await click('pool-pause-add-1h')
    expect(screen.getByTestId('pool-pause-error')).toHaveTextContent('invalid hold')
    expect(screen.queryByTestId('pool-pause-none')).not.toBeNull() // nothing was added
  })
})

describe('ending', () => {
  it('DELETEs that pause by name, waits for the host to drop it, then it is gone', async () => {
    setHost(mlab())
    await mount()
    await click('pool-pause-end-dev-server')
    expect(lastChange()).toMatchObject({ method: 'DELETE', path: '/api/pool/hosts/mlab/exceptions/dev-server' })
    expect(screen.getByTestId('pool-pause-pending-dev-server')).toBeInTheDocument()
    expect(screen.getByTestId('pool-pause-end-dev-server')).toBeDisabled()
    setHost(mlab(['app-dev-package']))
    expect(screen.queryByTestId('pool-pause-row-dev-server')).toBeNull()
    expect(screen.getByTestId('pool-pause-row-app-dev-package')).toBeInTheDocument()
  })

  it('a host that does not answer keeps the pause and says so under it', async () => {
    hostFetch.mockImplementation(async (_i: string, _p: string, init?: RequestInit) => (init?.method === 'DELETE' ? reply(502, { error: 'wwd_error', detail: 'timeout' }) : reply(404, { error: 'not_found' })))
    setHost(mlab())
    await mount()
    await click('pool-pause-end-dev-server')
    expect(screen.getByTestId('pool-pause-error-dev-server')).toHaveTextContent(t('pool.worker.no_answer', { alias: 'mlab', reason: 'timeout' }))
    expect(screen.getByTestId('pool-pause-row-dev-server')).toBeInTheDocument()
    expect(screen.queryByTestId('pool-pause-pending-dev-server')).toBeNull()
  })

  it('a device token (403) makes it read only with the one notice', async () => {
    hostFetch.mockImplementation(async (_i: string, _p: string, init?: RequestInit) => (init?.method === 'DELETE' ? reply(403, { error: 'forbidden' }) : reply(404, { error: 'not_found' })))
    setHost(mlab())
    await mount()
    await click('pool-pause-end-dev-server')
    expect(screen.getAllByTestId('pool-pause-readonly')).toHaveLength(1)
    expect(screen.getByTestId('pool-pause-end-dev-server')).toBeDisabled()
  })
})

describe('no daemon can change it', () => {
  it('still lists the pauses, with nothing to press', async () => {
    useNexHostStore.setState({ byHost: { [CFG]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
    setHost(mlab())
    await mount()
    expect(screen.getByTestId('pool-pause-row-dev-server')).toBeInTheDocument()
    expect(screen.getByTestId('pool-pause-end-dev-server')).toBeDisabled()
    expect(screen.getByTestId('pool-pause-add-1h').closest('[inert]')).not.toBeNull()
  })
})

describe('the word', () => {
  it('例外 is in no string of the App, in either language, and not on the page', async () => {
    for (const [lang, table] of [['en', en], ['zh-TW', zhTW]] as const) {
      const hits = Object.entries(table as Record<string, string>).filter(([, v]) => typeof v === 'string' && v.includes('例外')).map(([k]) => k)
      expect(hits, lang).toEqual([])
    }
    setHost(mlab())
    await mount()
    expect(document.body.textContent).not.toContain('例外')
  })
})

describe('what is typed and not sent', () => {
  it('the half-typed end time and the "light too" box survive a remount', async () => {
    setHost(mlab([]))
    const first = render(<WorkerExceptions alias="mlab" />)
    await act(async () => {})
    fireEvent.change(screen.getByTestId('pool-pause-clock'), { target: { value: '21:45' } })
    fireEvent.click(screen.getByTestId('pool-pause-light'))
    first.unmount()
    await mount()
    expect(screen.getByTestId('pool-pause-clock')).toHaveValue('21:45')
    expect(screen.getByTestId('pool-pause-light')).toBeChecked()
  })

  it('the classes and marks picked survive a remount', async () => {
    setHost(mlab([]))
    const first = render(<WorkerExceptions alias="mlab" />)
    await act(async () => {})
    fireEvent.click(screen.getByTestId('pool-pause-allow-release'))
    fireEvent.click(screen.getByTestId('pool-pause-flag-release'))
    first.unmount()
    await mount()
    expect(screen.getByTestId('pool-pause-allow-release')).toBeChecked()
    expect(screen.getByTestId('pool-pause-allow-merge')).not.toBeChecked()
    expect(screen.getByTestId('pool-pause-flag-release')).toBeChecked()
  })
})

describe('the list shows what a pause lets through and carries', () => {
  it('release, merge and the 發版中 mark are in the row text', async () => {
    details = { host: 'mlab', known: true, exceptions: [
      { name: 'app-dev-package', until: '2026-10-19T18:30:00+08:00', pid: null, holds: ['heavy'], allow: { classes: ['release', 'merge'], repos: [] }, flags: ['release'] },
    ] }
    setHost(mlab(['app-dev-package']))
    await mount()
    const row = screen.getByTestId('pool-pause-row-app-dev-package')
    expect(row).toHaveTextContent(t('pool.pause.allows', { classes: 'release, merge' }))
    expect(row).toHaveTextContent(t('pool.pause.marks', { flags: t('pool.pause.flag_short.release') }))
  })
})
