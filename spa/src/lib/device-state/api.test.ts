import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceSnapshot } from '../snapshot/types'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({
  hostFetch: (...args: unknown[]) => hostFetch(...args),
}))

import {
  DeviceStateApiError,
  deleteDeviceState,
  getDeviceState,
  listDeviceStates,
  putDeviceState,
} from './api'

function snap(overrides?: Record<string, unknown>): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 1000,
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: {},
    ...overrides,
  } as WorkspaceSnapshot
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const summary = {
  clientId: 'c_0123456789ab',
  deviceName: 'mlab',
  appVersion: '1.0.0',
  capturedAt: 1000,
  updatedAt: 2000,
  workspaceCount: 1,
  tabCount: 2,
}

beforeEach(() => {
  hostFetch.mockReset()
})

describe('putDeviceState', () => {
  it('PUTs JSON body to the encoded client path and returns stored', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ stored: true }))
    const body = { deviceName: 'mlab', appVersion: '1.0.0', capturedAt: 1000, payload: snap() }
    const res = await putDeviceState('h1', 'c_0123456789ab', body)
    expect(res).toEqual({ stored: true })
    expect(hostFetch).toHaveBeenCalledTimes(1)
    const [hostId, path, init] = hostFetch.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(path).toBe('/api/device-state/c_0123456789ab')
    expect(init.method).toBe('PUT')
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual(body)
  })

  it('returns stored:false for a stale write', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ stored: false }))
    const res = await putDeviceState('h1', 'c_0123456789ab', {
      deviceName: 'x', appVersion: '', capturedAt: 1, payload: snap(),
    })
    expect(res).toEqual({ stored: false })
  })

  it('URL-encodes clientId', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ stored: true }))
    await putDeviceState('h1', 'a/b c?', {
      deviceName: 'x', appVersion: '', capturedAt: 1, payload: snap(),
    })
    expect(hostFetch.mock.calls[0][1]).toBe('/api/device-state/a%2Fb%20c%3F')
  })

  it.each([400, 413, 500])('throws DeviceStateApiError on %i with response text', async (status) => {
    hostFetch.mockResolvedValue(new Response('bad thing', { status }))
    const err = await putDeviceState('h1', 'c_0123456789ab', {
      deviceName: 'x', appVersion: '', capturedAt: 1, payload: snap(),
    }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(status)
    expect((err as DeviceStateApiError).message).toContain('bad thing')
  })
})

describe('listDeviceStates', () => {
  it('GETs the collection and returns the array', async () => {
    hostFetch.mockResolvedValue(jsonResponse([summary]))
    const res = await listDeviceStates('h1')
    expect(res).toEqual([summary])
    const [hostId, path, init] = hostFetch.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(path).toBe('/api/device-state')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('returns an empty array', async () => {
    hostFetch.mockResolvedValue(jsonResponse([]))
    expect(await listDeviceStates('h1')).toEqual([])
  })

  it('throws DeviceStateApiError on 500', async () => {
    hostFetch.mockResolvedValue(new Response('boom', { status: 500 }))
    const err = await listDeviceStates('h1').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(500)
  })
})

describe('getDeviceState', () => {
  it('GETs one record by encoded clientId and returns it', async () => {
    const record = { ...summary, payload: snap() }
    hostFetch.mockResolvedValue(jsonResponse(record))
    const res = await getDeviceState('h1', 'c_0123456789ab')
    expect(res).toEqual(record)
    const [hostId, path, init] = hostFetch.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(path).toBe('/api/device-state/c_0123456789ab')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('URL-encodes clientId', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ ...summary, payload: snap() }))
    await getDeviceState('h1', 'x/y')
    expect(hostFetch.mock.calls[0][1]).toBe('/api/device-state/x%2Fy')
  })

  it('throws DeviceStateApiError 404 when missing', async () => {
    hostFetch.mockResolvedValue(new Response('not found', { status: 404 }))
    const err = await getDeviceState('h1', 'c_0123456789ab').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(404)
  })

  it('throws on payload missing tabs', async () => {
    const payload = snap()
    delete (payload as Partial<WorkspaceSnapshot>).tabs
    hostFetch.mockResolvedValue(jsonResponse({ ...summary, payload }))
    const err = await getDeviceState('h1', 'c_0123456789ab').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(0)
    expect((err as DeviceStateApiError).message).toBe('malformed payload')
  })

  it('throws on payload version 2', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ ...summary, payload: snap({ version: 2 }) }))
    const err = await getDeviceState('h1', 'c_0123456789ab').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(0)
    expect((err as DeviceStateApiError).message).toBe('malformed payload')
  })

  it('throws when payload is absent', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ ...summary }))
    const err = await getDeviceState('h1', 'c_0123456789ab').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(0)
  })
})

describe('deleteDeviceState', () => {
  it('DELETEs the encoded client path and resolves on 204', async () => {
    hostFetch.mockResolvedValue(new Response(null, { status: 204 }))
    await expect(deleteDeviceState('h1', 'c_0123456789ab')).resolves.toBeUndefined()
    const [hostId, path, init] = hostFetch.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(path).toBe('/api/device-state/c_0123456789ab')
    expect(init.method).toBe('DELETE')
  })

  it('URL-encodes clientId', async () => {
    hostFetch.mockResolvedValue(new Response(null, { status: 204 }))
    await deleteDeviceState('h1', 'a b')
    expect(hostFetch.mock.calls[0][1]).toBe('/api/device-state/a%20b')
  })

  it('throws DeviceStateApiError on 400', async () => {
    hostFetch.mockResolvedValue(new Response('invalid clientId', { status: 400 }))
    const err = await deleteDeviceState('h1', 'bad').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DeviceStateApiError)
    expect((err as DeviceStateApiError).status).toBe(400)
  })
})
