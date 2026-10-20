// The 守門員 section of a host's 設定 page — writes the whole `watch` row with its revision, a 409 reloads, a 400 shows the daemon's text and puts the field back, offline locks it.
// Only the start settings are drawn (MON-9a-1); the fields that are not drawn stay in the row and are written back as they were.
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

import { GuardianSection } from './WatchSettings'

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

const daemonPage = (over: Partial<Parameters<typeof GuardianSection>[0]> = {}) =>
  render(<GuardianSection hostId="d-a" hostConfigId="A" offline={false} {...over} />)
const edit = (key: string, value: string) => {
  const el = screen.getByTestId(`watch-set-${key}`)
  fireEvent.change(el, { target: { value } })
  fireEvent.blur(el)
}
const puts = () => hostFetch.mock.calls.filter((c) => (c[2] as RequestInit | undefined)?.method === 'PUT')

describe('GuardianSection', () => {
  it('loads the row: values from it, and each value says where it comes from', async () => {
    daemons.A = { items: { enabled: true, patrol_idle_min: 90, workdir: '~/w' }, revision: 3 }
    daemonPage()
    expect(await screen.findByTestId('watch-set-patrol_idle_min')).toHaveValue('90')
    expect(screen.getByTestId('watch-set-source-patrol_idle_min')).toHaveTextContent(t('watch.set.source_set'))
    expect(screen.getByTestId('watch-set-source-mode')).toHaveTextContent(t('watch.set.source_default'))
    // The source sits in the row's right-hand control cell, with the control (spec §6.2), not under the row.
    const cell = screen.getByTestId('watch-set-cell-patrol_idle_min')
    expect(cell).toContainElement(screen.getByTestId('watch-set-patrol_idle_min'))
    expect(cell).toContainElement(screen.getByTestId('watch-set-source-patrol_idle_min'))
    expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/w')
    expect(screen.getByTestId('watch-set-agent_command')).toHaveValue('claude --dangerously-skip-permissions --model opus')
  })

  it('draws the eight start settings and none of the trigger fields', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-workdir')
    for (const k of ['enabled', 'mode', 'patrol_idle_min', 'auto_wake', 'agent_command', 'workdir', 'start_prompt', 'wake_prompt']) expect(screen.getByTestId(`watch-set-row-${k}`)).toBeInTheDocument()
    for (const k of ['x_cores', 'y_mb', 't_s', 'pressure_level', 'pressure_t_s', 'page_rate', 'page_t_s', 'targets']) expect(screen.queryByTestId(`watch-set-row-${k}`)).toBeNull()
    expect(screen.queryByTestId('watch-set-x_cores')).toBeNull()
    expect(screen.queryByTestId('watch-set-targets')).toBeNull()
  })

  it('a write sends the whole row with the revision: the fields that are not drawn go back as they were (unknown fields too)', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-workdir')
    edit('workdir', '~/x')
    await waitFor(() => expect(puts()).toHaveLength(1))
    const [id, path, init] = puts()[0] as [string, string, RequestInit]
    expect([id, path]).toEqual(['A', '/api/hostconfig/watch'])
    const sent = JSON.parse(init.body as string)
    expect(sent).toEqual({ items: { ...FULL, workdir: '~/x' }, baseRevision: 3 })
    expect(sent.items).toMatchObject({ x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60, targets: ['air26', 'mlab'], future_field: { a: 1 } })
    await waitFor(() => expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/x'))
    // The PUT's own answer is the new row (same shape as the GET): no second read.
    expect(hostFetch.mock.calls.filter((c) => c[0] === 'A' && (c[2] as RequestInit | undefined)?.method !== 'PUT')).toHaveLength(1)
    // The next write is built on the stored row and its new revision.
    edit('patrol_idle_min', '90')
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(JSON.parse((puts()[1][2] as RequestInit).body as string)).toEqual({ items: { ...FULL, workdir: '~/x', patrol_idle_min: 90 }, baseRevision: 4 })
  })

  it('a switch writes at once', async () => {
    daemonPage()
    fireEvent.click(await screen.findByTestId('watch-set-auto_wake'))
    await waitFor(() => expect(puts()).toHaveLength(1))
    const sent = JSON.parse((puts()[0][2] as RequestInit).body as string).items
    expect(sent.auto_wake).toBe(false)
    expect(sent.targets).toEqual(['air26', 'mlab'])
  })

  it('a 409 shows the daemon\'s copy and says so; the change is not sent again by itself', async () => {
    putReply = () => json(409, { items: { ...FULL, agent_command: 'claude --other' }, revision: 4 })
    daemonPage()
    await screen.findByTestId('watch-set-patrol_idle_min')
    edit('patrol_idle_min', '240')
    expect(await screen.findByTestId('watch-set-conflict')).toHaveTextContent(t('watch.set.conflict'))
    await waitFor(() => expect(screen.getByTestId('watch-set-agent_command')).toHaveValue('claude --other'))
    expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('120')
    expect(puts()).toHaveLength(1)
  })

  it('a 400 shows the daemon\'s text under the field and puts the field back', async () => {
    putReply = () => new Response('patrol_idle_min must be between 1 and 1440', { status: 400 })
    daemonPage()
    await screen.findByTestId('watch-set-patrol_idle_min')
    edit('patrol_idle_min', '5')
    expect(await screen.findByTestId('watch-set-error-patrol_idle_min')).toHaveTextContent('patrol_idle_min must be between 1 and 1440')
    expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('120')
    expect(screen.queryByTestId('watch-set-conflict')).toBeNull()
  })

  it('an answer that does not read is a failed write: the error shows and the field goes back', async () => {
    putReply = () => json(200, { nonsense: true })
    daemonPage()
    await screen.findByTestId('watch-set-patrol_idle_min')
    edit('patrol_idle_min', '240')
    expect(await screen.findByTestId('watch-set-error-patrol_idle_min')).toBeInTheDocument()
    expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('120')
  })

  describe('unsent text', () => {
    const key = (field: string) => watchDraftKey('A', 'd-a', field)
    beforeEach(() => clearWatchDrafts())

    it('a 400 and a 409 both end the written field\'s draft', async () => {
      putReply = () => new Response('patrol_idle_min must be between 1 and 1440', { status: 400 })
      daemonPage()
      await screen.findByTestId('watch-set-patrol_idle_min')
      fireEvent.change(screen.getByTestId('watch-set-patrol_idle_min'), { target: { value: '5' } })
      expect(readWatchDraft(key('patrol_idle_min'))).toBe('5')
      fireEvent.blur(screen.getByTestId('watch-set-patrol_idle_min'))
      await screen.findByTestId('watch-set-error-patrol_idle_min')
      expect(readWatchDraft(key('patrol_idle_min'))).toBeUndefined()

      putReply = () => json(409, { items: { ...FULL }, revision: 4 })
      fireEvent.change(screen.getByTestId('watch-set-workdir'), { target: { value: '~/mine' } })
      fireEvent.blur(screen.getByTestId('watch-set-workdir'))
      await screen.findByTestId('watch-set-conflict')
      expect(readWatchDraft(key('workdir'))).toBeUndefined()
      await waitFor(() => expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/w'))
    })

    it('a 409 restores only the field that was written; another field\'s draft stays, marked as updated on the host', async () => {
      putReply = () => json(409, { items: { ...FULL, patrol_idle_min: 999, workdir: '~/host' }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-patrol_idle_min')
      fireEvent.change(screen.getByTestId('watch-set-patrol_idle_min'), { target: { value: '500' } }) // typed, not sent
      fireEvent.change(screen.getByTestId('watch-set-workdir'), { target: { value: '~/mine' } })
      fireEvent.blur(screen.getByTestId('watch-set-workdir'))
      await screen.findByTestId('watch-set-conflict')
      await waitFor(() => expect(screen.getByTestId('watch-set-workdir')).toHaveValue('~/host')) // the written field shows the host's copy
      expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('500') // the other keeps what the reader typed
      expect(readWatchDraft(key('patrol_idle_min'))).toBe('500')
      expect(await screen.findByTestId('watch-set-stale-patrol_idle_min')).toHaveTextContent(t('watch.set.stale'))
      // The reader decides: take the host's value.
      fireEvent.click(screen.getByTestId('watch-set-stale-use-patrol_idle_min'))
      await waitFor(() => expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('999'))
      expect(readWatchDraft(key('patrol_idle_min'))).toBeUndefined()
      expect(screen.queryByTestId('watch-set-stale-patrol_idle_min')).toBeNull()
    })

    // A stale draft in a FOCUSED box, then the reader chooses 「改用主機上的值」: the box must not send its text on the way.
    const staleFocused = async () => {
      putReply = () => json(409, { items: { ...FULL, patrol_idle_min: 999, workdir: '~/host' }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-patrol_idle_min')
      fireEvent.change(screen.getByTestId('watch-set-patrol_idle_min'), { target: { value: '500' } })
      fireEvent.change(screen.getByTestId('watch-set-workdir'), { target: { value: '~/mine' } })
      fireEvent.blur(screen.getByTestId('watch-set-workdir'))
      await screen.findByTestId('watch-set-stale-use-patrol_idle_min')
      const box = screen.getByTestId('watch-set-patrol_idle_min')
      act(() => box.focus())
      expect(document.activeElement).toBe(box)
      expect(puts()).toHaveLength(1)
      return { box, use: screen.getByTestId('watch-set-stale-use-patrol_idle_min') }
    }

    it('the mouse: pressing the button does not take focus from the box, so nothing is sent, and the box shows the host\'s value', async () => {
      const { box, use } = await staleFocused()
      // A browser moves focus (and so blurs the box) on mousedown unless the press is cancelled.
      if (fireEvent.mouseDown(use)) act(() => use.focus())
      fireEvent.click(use)
      await waitFor(() => expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('999'))
      expect(puts()).toHaveLength(1)
      expect(readWatchDraft(key('patrol_idle_min'))).toBeUndefined()
      expect(box).toBe(screen.getByTestId('watch-set-patrol_idle_min'))
    })

    it('the keyboard: Tab onto the button blurs the box towards it, and nothing is sent; Enter then takes the host\'s value', async () => {
      const { use } = await staleFocused()
      act(() => use.focus()) // Tab: the box blurs with the button as relatedTarget
      fireEvent.click(use) // Enter / Space on a button
      await waitFor(() => expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('999'))
      expect(puts()).toHaveLength(1)
      expect(readWatchDraft(key('patrol_idle_min'))).toBeUndefined()
    })

    it('an untouched field follows the host\'s copy quietly', async () => {
      putReply = () => json(409, { items: { ...FULL, patrol_idle_min: 999 }, revision: 4 })
      daemonPage()
      await screen.findByTestId('watch-set-patrol_idle_min')
      fireEvent.change(screen.getByTestId('watch-set-workdir'), { target: { value: '~/mine' } })
      fireEvent.blur(screen.getByTestId('watch-set-workdir'))
      await screen.findByTestId('watch-set-conflict')
      await waitFor(() => expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('999'))
      expect(screen.queryByTestId('watch-set-stale-patrol_idle_min')).toBeNull()
    })
  })

  it('a value that is not a number is refused here, nothing is sent', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-patrol_idle_min')
    edit('patrol_idle_min', 'abc')
    expect(await screen.findByTestId('watch-set-error-patrol_idle_min')).toHaveTextContent(t('watch.set.not_int'))
    expect(screen.getByTestId('watch-set-patrol_idle_min')).toHaveValue('120')
    expect(puts()).toHaveLength(0)
  })

  it('offline: nothing is read, nothing can be edited', async () => {
    daemonPage({ offline: true })
    expect(await screen.findByTestId('watch-set-offline')).toHaveTextContent(t('watch.set.offline'))
    expect(hostFetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('watch-set-patrol_idle_min')).toBeNull()
  })
})
