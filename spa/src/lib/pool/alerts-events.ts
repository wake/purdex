// spa/src/lib/pool/alerts-events.ts — the daemon's `alerts.changed` host event (`pool.alerts.v1`, internal/module/pool/alerts.go): `{host}`, a notice that one host's alert list changed. It carries no alert: the alerts store reads that host again.
// Called from useMultiHostEventWs with the per-host closure's hostId, after its connection-key check. A frame that is not that shape is dropped.
import { alertsEvent } from './alerts-store'

export const ALERTS_EVENT_TYPE = 'alerts.changed'
export const isAlertsEvent = (type: string): boolean => type === ALERTS_EVENT_TYPE

export function parseAlertsChanged(value: unknown): { host: string } | null {
  let body: unknown = value
  if (typeof value === 'string') {
    try { body = JSON.parse(value) } catch { return null }
  }
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return null
  const host = (body as Record<string, unknown>).host
  return typeof host === 'string' && host !== '' ? { host } : null
}

export function handleAlertsEvent(hostId: string, value: unknown): void {
  const e = parseAlertsChanged(value)
  if (e) alertsEvent(hostId, e.host)
}
