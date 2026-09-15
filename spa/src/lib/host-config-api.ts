// spa/src/lib/host-config-api.ts — the daemon `hostconfig` module's HTTP
// contract (B1 Task 4). The daemon is the source of truth; nothing here caches.
import { hostFetch } from './host-api'
import { useHostStore } from '../stores/useHostStore'

export type AgentIconValue = 'cc-bot' | 'cc-star' | 'openai' | 'codex' | 'opencode'

export type CommandIcon =
  | { kind: 'agent'; value: AgentIconValue }
  | { kind: 'phosphor'; value: string }

export interface HostProject { id: string; name: string; slug: string; path: string }
export interface HostCommand { id: string; name: string; command: string; icon: CommandIcon }
export type ResumeTemplateOverrides = Record<string, { exact: string; fallback: string }>

export interface Versioned<T> { items: T; revision: number }

export interface HostConfigPayload {
  projects: Versioned<HostProject[]>
  commands: Versioned<HostCommand[]>
  resumeTemplates: Versioned<ResumeTemplateOverrides>
}

export type PathCheckStatus = 'dir' | 'not_dir' | 'missing' | 'error' | 'unverifiable'
export interface PathCheck { status: PathCheckStatus; resolved: string; reason?: string }

export interface HostConfigCollectionItems {
  projects: HostProject[]
  commands: HostCommand[]
  'resume-templates': ResumeTemplateOverrides
}
export type HostConfigCollection = keyof HostConfigCollectionItems

/** Non-2xx (status = HTTP status) or a refused request (status = 0). */
export class HostConfigApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'HostConfigApiError'
    this.status = status
  }
}

/** PUT lost the compare-and-set: `current` is the daemon's copy. */
export class HostConfigConflictError extends Error {
  readonly current: Versioned<unknown>
  constructor(current: Versioned<unknown>) {
    super('host config changed elsewhere')
    this.name = 'HostConfigConflictError'
    this.current = current
  }
}

/**
 * `hostFetch` resolves an unknown host id to the ACTIVE host's address
 * (`useHostStore.getDaemonBase`). Host config written to the wrong daemon is
 * silent corruption, so an unknown id never reaches the network.
 */
function assertKnownHost(hostId: string): void {
  if (!useHostStore.getState().hosts[hostId]) {
    throw new HostConfigApiError(0, `host ${hostId} is not configured`)
  }
}

async function failure(res: Response): Promise<HostConfigApiError> {
  let text = ''
  try { text = (await res.text()).trim() } catch { /* body unreadable */ }
  return new HostConfigApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
}

export async function fetchHostConfig(hostId: string, signal?: AbortSignal): Promise<HostConfigPayload> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig', { signal })
  if (!res.ok) throw await failure(res)
  return (await res.json()) as HostConfigPayload
}

export async function putHostConfig<C extends HostConfigCollection>(
  hostId: string,
  collection: C,
  items: HostConfigCollectionItems[C],
  baseRevision: number,
): Promise<Versioned<HostConfigCollectionItems[C]>> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, `/api/hostconfig/${collection}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) {
    throw new HostConfigConflictError((await res.json()) as Versioned<unknown>)
  }
  if (!res.ok) throw await failure(res)
  return (await res.json()) as Versioned<HostConfigCollectionItems[C]>
}

const UNVERIFIABLE: PathCheck = { status: 'unverifiable', resolved: '' }

/** Advice only: never throws, so a check can never block a save. */
export async function checkHostPath(hostId: string, path: string, signal?: AbortSignal): Promise<PathCheck> {
  if (!useHostStore.getState().hosts[hostId]) return { ...UNVERIFIABLE }
  let res: Response
  try {
    res = await hostFetch(hostId, '/api/hostconfig/check-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
      signal,
    })
  } catch {
    return { ...UNVERIFIABLE }
  }
  if (res.status === 400) {
    const reason = (await res.text().catch(() => '')).trim()
    return { status: 'error', resolved: '', reason }
  }
  if (!res.ok) return { ...UNVERIFIABLE }
  try {
    return (await res.json()) as PathCheck
  } catch {
    return { ...UNVERIFIABLE }
  }
}
