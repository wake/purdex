// spa/src/lib/names/persona-api.ts — `GET/PUT /api/hostconfig/personas` (name pool v2 spec §4.2). Same CAS shape as
// `host-config-api.ts`; the daemon is the source of truth and nothing here caches.
import { hostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import type { Persona, PersonaCollection } from './persona-types'

/** Non-2xx (status = HTTP status) or a refused request (status = 0). */
export class PersonaApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'PersonaApiError'
    this.status = status
  }
}

/** PUT lost the compare-and-set: `current` is the daemon's copy. */
export class PersonaConflictError extends Error {
  readonly current: PersonaCollection
  constructor(current: PersonaCollection) {
    super('personas changed elsewhere')
    this.name = 'PersonaConflictError'
    this.current = current
  }
}

function assertKnownHost(hostId: string): void {
  if (!useHostStore.getState().hosts[hostId]) throw new PersonaApiError(0, `host ${hostId} is not configured`)
}

async function failure(res: Response): Promise<PersonaApiError> {
  let text = ''
  try { text = (await res.text()).trim() } catch { /* body unreadable */ }
  return new PersonaApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
}

export async function fetchPersonas(hostId: string, signal?: AbortSignal): Promise<PersonaCollection> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig/personas', { signal })
  if (!res.ok) throw await failure(res)
  return (await res.json()) as PersonaCollection
}

export async function putPersonas(hostId: string, items: Persona[], baseRevision: number): Promise<{ revision: number }> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig/personas', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) throw new PersonaConflictError((await res.json()) as PersonaCollection)
  if (!res.ok) throw await failure(res)
  return (await res.json()) as { revision: number }
}
