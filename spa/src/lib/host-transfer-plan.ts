// spa/src/lib/host-transfer-plan.ts — the pure half of the host transfer code (host ownership spec §6.4; H4b):
// what the sharer sends, how the receiver reads it, which status each received row gets, and the change the
// receiver commits. No store access: callers pass the local hosts in.

import { hostEndpoint, requestAtOf, type HostConfig } from '../stores/useHostStore'
import { isValidDaemonId } from './daemon-id'
import { sanitizeHostConfig } from './host-color'
import type { TransferLook, TransferRow } from './host-transfer-api'

const LOOK_FIELDS = ['colors', 'color', 'icon', 'iconWeight'] as const

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Keeps the look fields that pass the guards the host store itself applies to untrusted host configs
 * (`sanitizeHostConfig`: strict `#rrggbb`, a catalog icon name, a known weight, well-formed color sets).
 * Undefined when nothing survives.
 */
function parseLook(raw: unknown): TransferLook | undefined {
  if (!isPlainObject(raw)) return undefined
  const candidate: Record<string, unknown> = { id: '', name: '', ip: '', port: 0, order: 0 }
  for (const f of LOOK_FIELDS) if (raw[f] !== undefined) candidate[f] = raw[f]
  const clean = sanitizeHostConfig(candidate as unknown as HostConfig)
  const look: TransferLook = {}
  for (const f of LOOK_FIELDS) if (clean[f] !== undefined) (look as Record<string, unknown>)[f] = clean[f]
  return Object.keys(look).length > 0 ? look : undefined
}

const LABEL = /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/
const OCTET = /^(?:0|[1-9][0-9]{0,2})$/

/**
 * A payload row's `ip` as it may be spliced into `http://${ip}:${port}` — the probe's URL and the stored host
 * address are built from this one value, so it must not be able to change which URL is requested. Accepted: a
 * hostname (dot-separated labels of ASCII letters, digits and inner hyphens, ≤ 253 chars) or a dotted-quad IPv4 with
 * decimal octets 0–255 and no zero padding. Anything else is refused — `#` `?` `@` `\` `/` `:` `%`, whitespace,
 * control and non-ASCII characters, and IPv6: no address field in the app writes the `[…]` form a URL needs, so a
 * row carrying one could only be stored broken. A hostname whose last label is numeric (or `0x…`) is what a URL
 * parser reads as IPv4, so it must then be a strict dotted quad.
 *
 * The add-host dialog has no such validator (it only trims), so this one lives here.
 */
export function isTransferHost(ip: string): boolean {
  if (ip.length === 0 || ip.length > 253) return false
  const labels = ip.split('.')
  if (!labels.every((l) => LABEL.test(l))) return false
  const last = labels[labels.length - 1]
  if (/^[0-9]+$/.test(last) || /^0x/i.test(last)) {
    return labels.length === 4 && labels.every((l) => OCTET.test(l) && Number(l) <= 255)
  }
  return true
}

function parseRow(raw: unknown): TransferRow | null {
  if (!isPlainObject(raw)) return null
  const { ip, port, token, name, daemonId, look } = raw
  if (typeof ip !== 'string' || !isTransferHost(ip)) return null
  if (typeof port !== 'number' || !Number.isInteger(port) || port < 1 || port > 65535) return null
  if (typeof token !== 'string' || token === '') return null
  const row: TransferRow = { name: typeof name === 'string' && name.trim() !== '' ? name : ip, ip, port, token }
  if (isValidDaemonId(daemonId)) row.daemonId = daemonId
  const cleanLook = parseLook(look)
  if (cleanLook) row.look = cleanLook
  return row
}

/**
 * Reads a redeemed `hosts` array (spec §6.4.1, plan R5). A row survives only with `ip` (`isTransferHost`),
 * `port` (integer 1–65535) and `token` (non-empty); `name` falls back to `ip`; an invalid `daemonId` or look
 * field is dropped while the row stays. Unknown fields are ignored. `dropped` counts the rows that did not survive.
 */
export function parseTransferRows(hosts: unknown): { rows: TransferRow[]; dropped: number } {
  if (!Array.isArray(hosts)) return { rows: [], dropped: 0 }
  const rows: TransferRow[] = []
  let dropped = 0
  for (const raw of hosts) {
    const r = parseRow(raw)
    if (r) rows.push(r)
    else dropped++
  }
  return { rows, dropped }
}

/** The share side (spec §6.4.1): one row per host that has a token; `daemonId` / look fields only when set. */
export function payloadRowsOf(hosts: readonly HostConfig[]): TransferRow[] {
  const rows: TransferRow[] = []
  for (const h of hosts) {
    if (typeof h.token !== 'string' || h.token === '') continue
    const r: TransferRow = { name: h.name, ip: h.ip, port: h.port, token: h.token }
    if (h.daemonId) r.daemonId = h.daemonId
    const look: TransferLook = {}
    for (const f of LOOK_FIELDS) if (h[f] !== undefined) (look as Record<string, unknown>)[f] = h[f]
    if (Object.keys(look).length > 0) r.look = look
    rows.push(r)
  }
  return rows
}

/** What `/api/info` at a row's endpoint, with the row's own token, answered. */
export type Observation = { kind: 'ok'; hostId: string } | { kind: 'failed' }

export type ReceiveStatus = 'new' | 'existing' | 'mismatch' | 'unverified' | 'duplicate' | 'local-conflict'

export type ReceiveMode = 'add-only' | 'overwrite'

/** The local row an `existing` payload row would overwrite, as it was when the plan was made (R2). */
export interface OverwriteTarget {
  hostId: string
  endpoint: string
  token: string
  daemonId: string
}

export interface PreviewRow {
  index: number
  row: TransferRow
  status: ReceiveStatus
  /** The verified `host_id`; set for every status but `unverified`. */
  observed?: string
  /** Only with `existing`. */
  target?: OverwriteTarget
}

/**
 * Spec §6.4.3, in this order: `unverified` (no answer, or no valid id) → `mismatch` (payload id ≠ observed) →
 * `duplicate` (an earlier row observed the same id; the first wins) → `local-conflict` (two local rows claim the
 * id, or a local row at the same endpoint claims another id — or none yet) → `existing` (one local row claims it)
 * → `new`.
 */
export function planReceive(
  rows: readonly TransferRow[],
  observations: readonly Observation[],
  local: Record<string, HostConfig>,
): PreviewRow[] {
  const seen = new Set<string>()
  const localHosts = Object.values(local)
  return rows.map((row, index) => {
    const obs = observations[index]
    if (!obs || obs.kind !== 'ok' || !isValidDaemonId(obs.hostId)) return { index, row, status: 'unverified' }
    const observed = obs.hostId
    const first = !seen.has(observed)
    seen.add(observed)
    if (row.daemonId !== undefined && row.daemonId !== observed) return { index, row, status: 'mismatch', observed }
    if (!first) return { index, row, status: 'duplicate', observed }
    const claims = localHosts.filter((h) => h.daemonId === observed)
    const endpoint = hostEndpoint(row)
    const squatter = localHosts.some((h) => hostEndpoint(h) === endpoint && h.daemonId !== observed)
    if (claims.length > 1 || squatter) return { index, row, status: 'local-conflict', observed }
    if (claims.length === 1) {
      const h = claims[0]
      const at = requestAtOf(h)
      return { index, row, status: 'existing', observed, target: { hostId: h.id, endpoint: at.endpoint, token: at.token, daemonId: observed } }
    }
    return { index, row, status: 'new', observed }
  })
}

/** Only `new` and `existing` can be committed; `existing` only when overwriting (add-only skips it). */
export function isPickable(status: ReceiveStatus, mode: ReceiveMode): boolean {
  return status === 'new' || (status === 'existing' && mode === 'overwrite')
}

export interface TransferCreate {
  name: string
  ip: string
  port: number
  token: string
  /** The observed id — written through the store's verified-endpoint rule, never raw. */
  daemonId: string
  look?: TransferLook
}

export interface TransferOverwrite {
  hostId: string
  /** What the plan was built against; any difference refuses the whole change (R2). */
  expect: Omit<OverwriteTarget, 'hostId'>
  name: string
  ip: string
  port: number
  token: string
  look?: TransferLook
}

/** One `applyHostTransfer` call. */
export interface TransferChange {
  create: TransferCreate[]
  overwrite: TransferOverwrite[]
}

/** The change for the picked rows under `mode`; rows that are not pickable in that mode are left out. */
export function commitSet(preview: readonly PreviewRow[], picks: ReadonlySet<number>, mode: ReceiveMode): TransferChange {
  const change: TransferChange = { create: [], overwrite: [] }
  for (const p of preview) {
    if (!picks.has(p.index) || !isPickable(p.status, mode)) continue
    const { name, ip, port, token, look } = p.row
    if (p.status === 'new' && p.observed) {
      change.create.push({ name, ip, port, token, daemonId: p.observed, ...(look ? { look } : {}) })
    } else if (p.status === 'existing' && p.target) {
      const { hostId, endpoint, token: expectToken, daemonId } = p.target
      change.overwrite.push({ hostId, expect: { endpoint, token: expectToken, daemonId }, name, ip, port, token, ...(look ? { look } : {}) })
    }
  }
  return change
}
