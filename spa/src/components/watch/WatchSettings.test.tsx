// W5c1: the 設定 page of a host tab — the 守門員 section writes the whole `watch` row with its revision, a 409 reloads, a 400 shows the
// daemon's text and puts the field back, the two daemons' `targets` are compared, offline locks it; every host gets the worker settings and the pause list (MON-6f-1: the fleet's probe page is gone).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { clearWatchDrafts, readWatchDraft, watchDraftKey } from '../../lib/watch/draft-memory'

const hostFetch = vi.fn()
vi.mock('../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../lib/host-api')>()), hostFetch: (...a: unknown[]) => hostFetch(...a) }))
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () => Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 38, size: 38 })),
  }),
}))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10', isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(),
}))
vi.mock('./WorkerSettings', () => ({ WorkerSettings: ({ alias }: { alias: string }) => <div data-testid="mock-worker-settings">{alias}</div> }))
vi.mock('./WorkerExceptions', () => ({ WorkerExceptions: ({ alias }: { alias: string }) => <div data-testid="mock-worker-exceptions">{alias}</div> }))

import { WatchSettings } from './WatchSettings'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const FULL = {
  enabled: true, x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60,
  targets: ['air26', 'mlab'], mode: 'patrol', patrol_idle_min: 120, auto_wake: true,
  agent_command: 'claude --model opus', workdir: '~/w', start_prompt: '', wake_prompt: '', future_field: { a: 1 },
}
let daemons: Record<string, { items: Record<string, unknown>; revision: number }>
let putReply: (hostId: string, body: { items: Record<string, unknown>; baseRevision: number }) => Response
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  cleanup()
  hostFetch.mockReset()
  daemons = { A: { items: { ...FULL }, revision: 3 }, B: { items: { ...FULL, targets: ['mlab', 'air26'] }, revision: 5 } }
  putReply = (id, body) => { daemons[id] = { items: body.items, revision: body.baseRevision + 1 }; return json(200, daemons[id]) }
  hostFetch.mockImplementation(async (id: string, path: string, init?: RequestInit) => {
    if (path === '/api/hostconfig' && init?.method !== 'PUT') return json(200, { watch: daemons[id] })
    if (path === '/api/hostconfig/watch' && init?.method === 'PUT') return putReply(id, JSON.parse(init.body as string))
    return json(404, {})
  })
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({ hosts: { A: host('A'), B: host('B') } as never, hostOrder: ['A', 'B'], runtime: {} as never })
})
afterEach(() => cleanup())

const daemonPage = (over: Partial<Parameters<typeof WatchSettings>[0]> = {}) =>
  render(<WatchSettings hostId="d-a" hostConfigId="A" offline={false} peerIds={[]} {...over} />)
const edit = (key: string, value: string) => {
  const el = screen.getByTestId(`watch-set-${key}`)
  fireEvent.change(el, { target: { value } })
  fireEvent.blur(el)
}
const puts = () => hostFetch.mock.calls.filter((c) => (c[2] as RequestInit | undefined)?.method === 'PUT')

describe('WatchSettings — the 守門員 section', () => {
  it('loads the row: values from it, and each value says where it comes from', async () => {
    daemons.A = { items: { enabled: true, x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60 }, revision: 3 }
    daemonPage()
    expect(await screen.findByTestId('watch-set-x_cores')).toHaveValue('2')
    expect(screen.getByTestId('watch-set-source-x_cores')).toHaveTextContent(t('watch.set.source_set'))
    expect(screen.getByTestId('watch-set-source-mode')).toHaveTextContent(t('watch.set.source_default'))
    // The source sits in the row's right-hand control cell, with the control (spec §6.2), not under the row.
    const cell = screen.getByTestId('watch-set-cell-x_cores')
    expect(cell).toContainElement(screen.getByTestId('watch-set-x_cores'))
    expect(cell).toContainElement(screen.getByTestId('watch-set-source-x_cores'))
    expect(screen.getByTestId('watch-set-targets')).toHaveValue('air26, mlab')
    expect(screen.getByTestId('watch-set-agent_command')).toHaveValue('claude --dangerously-skip-permissions --model opus')
  })

  it('a write sends the whole row (unknown fields too) with the revision, and shows the stored answer', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    await waitFor(() => expect(puts()).toHaveLength(1))
    const [id, path, init] = puts()[0] as [string, string, RequestInit]
    expect([id, path]).toEqual(['A', '/api/hostconfig/watch'])
    expect(JSON.parse(init.body as string)).toEqual({ items: { ...FULL, y_mb: 2048 }, baseRevision: 3 })
    await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('2048'))
    // The PUT's own answer is the new row (same shape as the GET): no second read.
    expect(hostFetch.mock.calls.filter((c) => c[0] === 'A' && (c[2] as RequestInit | undefined)?.method !== 'PUT')).toHaveLength(1)
    // The next write is built on the stored row and its new revision.
    edit('t_s', '90')
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(JSON.parse((puts()[1][2] as RequestInit).body as string)).toEqual({ items: { ...FULL, y_mb: 2048, t_s: 90 }, baseRevision: 4 })
  })

  it('a switch writes at once, and the targets are sent as a list of aliases', async () => {
    daemonPage()
    fireEvent.click(await screen.findByTestId('watch-set-auto_wake'))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(JSON.parse((puts()[0][2] as RequestInit).body as string).items.auto_wake).toBe(false)
    edit('targets', 'mlab, air26 ,a19')
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(JSON.parse((puts()[1][2] as RequestInit).body as string).items.targets).toEqual(['mlab', 'air26', 'a19'])
  })

  it('a 409 shows the daemon\'s copy and says so; the change is not sent again by itself', async () => {
    putReply = () => json(409, { items: { ...FULL, x_cores: 9 }, revision: 4 })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    expect(await screen.findByTestId('watch-set-conflict')).toHaveTextContent(t('watch.set.conflict'))
    await waitFor(() => expect(screen.getByTestId('watch-set-x_cores')).toHaveValue('9'))
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(puts()).toHaveLength(1)
  })

  it('a 400 shows the daemon\'s text under the field and puts the field back', async () => {
    putReply = () => new Response('y_mb must be between 64 and 1048576', { status: 400 })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '5')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toHaveTextContent('y_mb must be between 64 and 1048576')
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(screen.queryByTestId('watch-set-conflict')).toBeNull()
  })

  it('an answer that does not read is a failed write: the error shows and the field goes back', async () => {
    putReply = () => json(200, { nonsense: true })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toBeInTheDocument()
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
  })

  describe('unsent text', () => {
    const key = (field: string) => watchDraftKey('A', 'd-a', field)
    beforeEach(() => clearWatchDrafts())

    it('a 400 and a 409 both end the written field\'s draft', async () => {
      putReply = () => new Response('y_mb must be between 64 and 1048576', { status: 400 })
      daemonPage()
      await screen.findByTestId('watch-set-y_mb')
      fireEvent.change(screen.getByTestId('watch-set-y_mb'), { target: { value: '5' } })
      expect(readWatchDraft(key('y_mb'))).toBe('5')
      fireEvent.blur(screen.getByTestId('watch-set-y_mb'))
      await screen.findByTestId('watch-set-error-y_mb')
      expect(readWatchDraft(key('y_mb'))).toBeUndefined()

      putReply = () => json(409, { items: { ...FULL }, revision: 4 })
      fireEvent.change(screen.getByTestId('watch-set-t_s'), { target: { value: '90' } })
      fireEvent.blur(screen.getByTestId('watch-set-t_s'))
      await screen.findByTestId('watch-set-conflict')
      expect(readWatchDraft(key('t_s'))).toBeUndefined()
      await waitFor(() => expect(screen.getByTestId('watch-set-t_s')).toHaveValue('60'))
    })

    it('a 409 restores only the field that was written; another field\'s draft stays, marked as updated on the host', async () => {
      putReply = () => json(409, { items: { ...FULL, y_mb: 999, t_s: 77 }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-y_mb')
      fireEvent.change(screen.getByTestId('watch-set-y_mb'), { target: { value: '5000' } }) // typed, not sent
      fireEvent.change(screen.getByTestId('watch-set-t_s'), { target: { value: '90' } })
      fireEvent.blur(screen.getByTestId('watch-set-t_s'))
      await screen.findByTestId('watch-set-conflict')
      await waitFor(() => expect(screen.getByTestId('watch-set-t_s')).toHaveValue('77')) // the written field shows the host's copy
      expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('5000') // the other keeps what the reader typed
      expect(readWatchDraft(key('y_mb'))).toBe('5000')
      expect(await screen.findByTestId('watch-set-stale-y_mb')).toHaveTextContent(t('watch.set.stale'))
      // The reader decides: take the host's value.
      fireEvent.click(screen.getByTestId('watch-set-stale-use-y_mb'))
      await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('999'))
      expect(readWatchDraft(key('y_mb'))).toBeUndefined()
      expect(screen.queryByTestId('watch-set-stale-y_mb')).toBeNull()
    })

    // A stale draft in a FOCUSED box, then the reader chooses 「改用主機上的值」: the box must not send its text on the way.
    const staleFocused = async () => {
      putReply = () => json(409, { items: { ...FULL, y_mb: 999, t_s: 77 }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-y_mb')
      fireEvent.change(screen.getByTestId('watch-set-y_mb'), { target: { value: '5000' } })
      fireEvent.change(screen.getByTestId('watch-set-t_s'), { target: { value: '90' } })
      fireEvent.blur(screen.getByTestId('watch-set-t_s'))
      await screen.findByTestId('watch-set-stale-use-y_mb')
      const box = screen.getByTestId('watch-set-y_mb')
      act(() => box.focus())
      expect(document.activeElement).toBe(box)
      expect(puts()).toHaveLength(1)
      return { box, use: screen.getByTestId('watch-set-stale-use-y_mb') }
    }

    it('the mouse: pressing the button does not take focus from the box, so nothing is sent, and the box shows the host\'s value', async () => {
      const { box, use } = await staleFocused()
      // A browser moves focus (and so blurs the box) on mousedown unless the press is cancelled.
      if (fireEvent.mouseDown(use)) act(() => use.focus())
      fireEvent.click(use)
      await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('999'))
      expect(puts()).toHaveLength(1)
      expect(readWatchDraft(key('y_mb'))).toBeUndefined()
      expect(box).toBe(screen.getByTestId('watch-set-y_mb'))
    })

    it('the keyboard: Tab onto the button blurs the box towards it, and nothing is sent; Enter then takes the host\'s value', async () => {
      const { use } = await staleFocused()
      act(() => use.focus()) // Tab: the box blurs with the button as relatedTarget
      fireEvent.click(use) // Enter / Space on a button
      await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('999'))
      expect(puts()).toHaveLength(1)
      expect(readWatchDraft(key('y_mb'))).toBeUndefined()
    })

    it('an untouched field follows the host\'s copy quietly', async () => {
      putReply = () => json(409, { items: { ...FULL, y_mb: 999 }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-y_mb')
      fireEvent.change(screen.getByTestId('watch-set-t_s'), { target: { value: '90' } })
      fireEvent.blur(screen.getByTestId('watch-set-t_s'))
      await screen.findByTestId('watch-set-conflict')
      await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('999'))
      expect(screen.queryByTestId('watch-set-stale-y_mb')).toBeNull()
    })
  })

  it('a value that is not a number is refused here, nothing is sent', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', 'abc')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toHaveTextContent(t('watch.set.not_int'))
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(puts()).toHaveLength(0)
  })

  it('warns when the other daemon\'s targets differ, and not when they match', async () => {
    daemonPage({ peerIds: ['B'] })
    expect(await screen.findByTestId('watch-set-targets-differ')).toBeInTheDocument()
    cleanup()
    daemons.B = { items: { ...FULL }, revision: 5 }
    daemonPage({ peerIds: ['B'] })
    await screen.findByTestId('watch-set-y_mb')
    await waitFor(() => expect(hostFetch.mock.calls.filter((c) => c[0] === 'B')).toHaveLength(2))
    expect(screen.queryByTestId('watch-set-targets-differ')).toBeNull()
  })

  it('a peer that cannot be read is said so, and is not taken for the same order', async () => {
    daemons.B = { items: { ...FULL }, revision: 5 } // would match, but cannot be read
    const inner = hostFetch.getMockImplementation()!
    hostFetch.mockImplementation(async (id: string, path: string, init?: RequestInit) => (id === 'B' ? new Response('nope', { status: 500 }) : inner(id, path, init)))
    daemonPage({ peerIds: ['B'] })
    const line = await screen.findByTestId('watch-set-compare-unreadable')
    expect(line).toHaveTextContent(t('watch.set.compare_unreadable', { host: 'B' }))
    expect(screen.queryByTestId('watch-set-targets-differ')).toBeNull()
  })

  it('offline: nothing is read, nothing can be edited', async () => {
    daemonPage({ offline: true })
    expect(await screen.findByTestId('watch-set-offline')).toHaveTextContent(t('watch.set.offline'))
    expect(hostFetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('watch-set-y_mb')).toBeNull()
  })

  it('a host the App has no config for says so', () => {
    daemonPage({ hostConfigId: undefined })
    expect(screen.getByTestId('watch-set-unmapped')).toBeInTheDocument()
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

describe('WatchSettings — every host is a pool host (MON-6f-1)', () => {
  it('a host with no daemon the App has a config for: the worker settings and the pause list are there, the 守門員 section says unmapped, and no probe editor', () => {
    daemonPage({ hostConfigId: undefined, poolAlias: 'mserv' })
    expect(screen.getByTestId('mock-worker-settings')).toHaveTextContent('mserv')
    expect(screen.getByTestId('mock-worker-exceptions')).toHaveTextContent('mserv')
    expect(screen.getByTestId('watch-set-unmapped')).toBeInTheDocument()
    expect(screen.queryByTestId('watch-set-probe')).toBeNull()
    expect(hostFetch).not.toHaveBeenCalled()
  })
  it('without an alias in the pool there are no worker sections, still no probe editor', () => {
    daemonPage({ hostConfigId: undefined })
    expect(screen.queryByTestId('mock-worker-settings')).toBeNull()
    expect(screen.queryByTestId('watch-set-probe')).toBeNull()
  })
})
