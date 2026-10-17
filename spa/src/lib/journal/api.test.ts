// spa/src/lib/journal/api.test.ts — the four journal routes and their outcomes (J-2a T-2a.3).
import { describe, it, expect, beforeEach, vi } from 'vitest'

const pinned = vi.fn<(hostId: string, path: string, init?: RequestInit) => Promise<Response>>()
vi.mock('../host-api', () => ({ pinnedHostFetch: (h: string, p: string, i?: RequestInit) => pinned(h, p, i) }))

import { getJournal, getRoadmap, getTasks, setConfirmItem } from './api'
import { useHostStore } from '../../stores/useHostStore'
import { wirePage, wireRoadmap, wireTasks } from './fixtures'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const LEAD = { lead_host_id: 'mini-lab:abc', lead_session_id: 'lead-sess' }

beforeEach(() => {
  pinned.mockReset()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: ['h1'], runtime: {} })
})

describe('getJournal', () => {
  it('asks with the session, the cursor and the limit, and reads the page', async () => {
    pinned.mockResolvedValue(json(wirePage()))
    const r = await getJournal('h1', 'sess/1', '2026-09:wb:7:1')
    expect(pinned.mock.calls[0][1]).toBe('/api/journal?session_id=sess%2F1&before=2026-09%3Awb%3A7%3A1&limit=50')
    expect(r.kind === 'ok' && r.value.unit.id).toBe('tm1')
  })
  it('omits before on the first page', async () => {
    pinned.mockResolvedValue(json(wirePage()))
    await getJournal('h1', 's')
    expect(pinned.mock.calls[0][1]).toBe('/api/journal?session_id=s&limit=50')
  })
  it('404 → none, for the daemon\'s JSON and the mux\'s plain text alike', async () => {
    pinned.mockResolvedValueOnce(json({ error: 'not_found' }, 404))
    expect(await getJournal('h1', 's')).toEqual({ kind: 'none' })
    pinned.mockResolvedValueOnce(new Response('404 page not found', { status: 404 }))
    expect(await getJournal('h1', 's')).toEqual({ kind: 'none' })
  })
  it('409 with a lead → remote', async () => {
    pinned.mockResolvedValue(json({ error: 'member_remote', ...LEAD }, 409))
    expect(await getJournal('h1', 's')).toEqual({ kind: 'remote', leadHostId: 'mini-lab:abc', leadSessionId: 'lead-sess' })
  })
  it('a 409 without a usable lead still throws; so do 500 and a broken 200', async () => {
    pinned.mockResolvedValueOnce(json({ error: 'conflict' }, 409))
    await expect(getJournal('h1', 's')).rejects.toMatchObject({ status: 409 })
    pinned.mockResolvedValueOnce(json({ error: 'internal' }, 500))
    await expect(getJournal('h1', 's')).rejects.toMatchObject({ status: 500 })
    pinned.mockResolvedValueOnce(json({ nope: 1 }))
    await expect(getJournal('h1', 's')).rejects.toMatchObject({ code: 'bad_response' })
  })
  it('an unconfigured host is host_removed', async () => {
    await expect(getJournal('gone', 's')).rejects.toMatchObject({ code: 'host_removed' })
  })
})

describe('tasks, roadmap, pin', () => {
  it('reads tasks and roadmap, with the same none / remote outcomes', async () => {
    pinned.mockResolvedValueOnce(json(wireTasks()))
    const t = await getTasks('h1', 's')
    expect(pinned.mock.calls[0][1]).toBe('/api/journal/tasks?session_id=s')
    expect(t.kind === 'ok' && t.value).toHaveLength(3)
    pinned.mockResolvedValueOnce(json(wireRoadmap()))
    const r = await getRoadmap('h1', 's')
    expect(pinned.mock.calls[1][1]).toBe('/api/journal/roadmap?session_id=s')
    expect(r.kind === 'ok' && r.value[0].name).toBe('Journal')
    pinned.mockResolvedValueOnce(json({}, 404))
    expect(await getTasks('h1', 's')).toEqual({ kind: 'none' })
    pinned.mockResolvedValueOnce(json(LEAD, 409))
    expect((await getRoadmap('h1', 's')).kind).toBe('remote')
  })
  it('PUTs a hand check with the session id, the entry id, the item index and the flag', async () => {
    pinned.mockResolvedValue(json({}))
    expect(await setConfirmItem('h1', 's', 'wb:1:1', 2, true)).toEqual({ kind: 'ok', value: true })
    const [, path, init] = pinned.mock.calls[0]
    expect(path).toBe('/api/journal/confirm')
    expect(init?.method).toBe('PUT')
    expect(JSON.parse(String(init?.body))).toEqual({ session_id: 's', entry_id: 'wb:1:1', idx: 2, done: true })
  })
  it('a hand check goes to a remote lead as a result too', async () => {
    pinned.mockResolvedValue(json(LEAD, 409))
    expect((await setConfirmItem('h1', 's', 'e', 0, false)).kind).toBe('remote')
  })
})
    const [, path, init] = pinned.mock.calls[0]
    expect(path).toBe('/api/journal/pin')
    expect(init?.method).toBe('PUT')
    expect(JSON.parse(String(init?.body))).toEqual({ session_id: 's', entry_id: 'wb:1:1', pinned: true })
  })
  it('the pin goes to a remote lead as a result too', async () => {
    pinned.mockResolvedValue(json(LEAD, 409))
    expect((await setPinned('h1', 's', 'e', false)).kind).toBe('remote')
  })
})
