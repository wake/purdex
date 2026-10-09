// spa/src/lib/team/appearance-api.test.ts — the appearance write's wire and how each answer maps to a result.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const send = vi.hoisted(() => vi.fn())
vi.mock('./approval-api', async (orig) => ({ ...(await orig<typeof import('./approval-api')>()), send }))
vi.mock('./unattended-api', () => ({ descriptorFor: async () => ({ kind: 'app', label: 'Purdex.app @ test' }) }))

import { ApprovalApiError } from './approval-api'
import { fieldOfDetail, saveAppearance } from './appearance-api'

beforeEach(() => send.mockReset())

describe('saveAppearance', () => {
  it('PUTs all of the fields to the host, colour null for automatic', async () => {
    send.mockResolvedValue({})
    const r = await saveAppearance('h1', 't1', { name: 'Release', label: '發版', color: null })
    expect(r).toEqual({ ok: true })
    expect(send).toHaveBeenCalledTimes(1)
    const [host, path, init] = send.mock.calls[0]
    expect([host, path, init.method]).toEqual(['h1', '/api/team/appearance', 'PUT'])
    expect(JSON.parse(init.body)).toEqual({
      team_id: 't1', team_name: 'Release', team_label: '發版', team_color: null, client: { kind: 'app', label: 'Purdex.app @ test' },
    })
  })

  it('a chosen colour goes as the number', async () => {
    send.mockResolvedValue({})
    await saveAppearance('h1', 't1', { name: '', label: '', color: 0 })
    expect(JSON.parse(send.mock.calls[0][2].body)).toMatchObject({ team_name: '', team_label: '', team_color: 0 })
  })

  it('400 names its field by the detail prefix; an unnamed 400 is form-level', async () => {
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_label: too wide'))
    expect(await saveAppearance('h', 't', { name: 'a', label: 'b', color: null })).toEqual({ ok: false, kind: 'field', field: 'label', message: 'team_label: too wide' })
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_name: has a control character'))
    expect(await saveAppearance('h', 't', { name: 'a', label: 'b', color: null })).toMatchObject({ kind: 'field', field: 'name' })
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_color must be an integer from 0 to 7, or null'))
    expect(await saveAppearance('h', 't', { name: 'a', label: 'b', color: null })).toMatchObject({ kind: 'field', field: 'color' })
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'invalid JSON: x'))
    expect(await saveAppearance('h', 't', { name: 'a', label: 'b', color: null })).toEqual({ ok: false, kind: 'form', message: 'invalid JSON: x' })
  })

  it('409 not_live and 404 are "ended"; a network failure is form-level', async () => {
    send.mockRejectedValueOnce(new ApprovalApiError(409, 'not_live', 'the team has ended'))
    expect(await saveAppearance('h', 't', { name: '', label: '', color: null })).toEqual({ ok: false, kind: 'ended' })
    send.mockRejectedValueOnce(new ApprovalApiError(404, 'not_found', 'no team with that id'))
    expect(await saveAppearance('h', 't', { name: '', label: '', color: null })).toEqual({ ok: false, kind: 'ended' })
    send.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    expect(await saveAppearance('h', 't', { name: '', label: '', color: null })).toEqual({ ok: false, kind: 'form', message: 'Failed to fetch' })
  })

  it('fieldOfDetail', () => {
    expect(fieldOfDetail('team_id is required')).toBeNull()
  })
})
