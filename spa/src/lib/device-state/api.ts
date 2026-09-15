import { hostFetch } from '../host-api'
import { isWellFormedSnapshotV1 } from '../snapshot/storage'
import type { WorkspaceSnapshot } from '../snapshot/types'

export interface DeviceStateSummary {
  clientId: string
  deviceName: string
  appVersion: string
  capturedAt: number
  updatedAt: number
  workspaceCount: number
  tabCount: number
}

export interface DeviceStateRecord extends DeviceStateSummary {
  payload: WorkspaceSnapshot
}

export interface PutDeviceStateBody {
  deviceName: string
  appVersion: string
  capturedAt: number
  payload: WorkspaceSnapshot
}

/** Non-2xx response (status = HTTP status) or malformed response data (status = 0). */
export class DeviceStateApiError extends Error {
  readonly status: number
  constructor(status: number, message?: string) {
    super(message ?? `device state request failed (${status})`)
    this.name = 'DeviceStateApiError'
    this.status = status
  }
}

const BASE = '/api/device-state'

function recordPath(clientId: string): string {
  return `${BASE}/${encodeURIComponent(clientId)}`
}

async function ensureOk(res: Response): Promise<void> {
  if (res.ok) return
  let text = ''
  try {
    text = (await res.text()).trim()
  } catch {
    // body unreadable — fall back to status-only message
  }
  throw new DeviceStateApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
}

export async function putDeviceState(
  hostId: string,
  clientId: string,
  body: PutDeviceStateBody,
): Promise<{ stored: boolean }> {
  const res = await hostFetch(hostId, recordPath(clientId), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  await ensureOk(res)
  const data = (await res.json()) as { stored: boolean }
  return { stored: data.stored }
}

export async function listDeviceStates(hostId: string): Promise<DeviceStateSummary[]> {
  const res = await hostFetch(hostId, BASE)
  await ensureOk(res)
  return (await res.json()) as DeviceStateSummary[]
}

export async function getDeviceState(hostId: string, clientId: string): Promise<DeviceStateRecord> {
  const res = await hostFetch(hostId, recordPath(clientId))
  await ensureOk(res)
  const data = (await res.json()) as Partial<DeviceStateRecord> | null
  if (!data || !isWellFormedSnapshotV1(data.payload)) {
    throw new DeviceStateApiError(0, 'malformed payload')
  }
  return data as DeviceStateRecord
}

export async function deleteDeviceState(hostId: string, clientId: string): Promise<void> {
  const res = await hostFetch(hostId, recordPath(clientId), { method: 'DELETE' })
  await ensureOk(res)
}
