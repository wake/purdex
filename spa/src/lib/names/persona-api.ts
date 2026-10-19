// spa/src/lib/names/persona-api.ts — `GET/PUT /api/hostconfig/personas` (name pool v2 spec §4.2). Same CAS shape as
// `host-config-api.ts`; the daemon is the source of truth and nothing here caches.
import { hostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import type { Persona, PersonaCollection } from './persona-types'

/** Non-2xx (status = HTTP status) or a refused request (status = 0). */
export class PersonaApiError extends Error {
  readonly status: number
  /** A 400 body's `index`: the item at fault, or `-1` for the collection as a whole (a stored code was removed). */
  readonly index?: number
  readonly field?: string
  constructor(status: number, message: string, detail: { index?: number; field?: string } = {}) {
    super(message)
    this.name = 'PersonaApiError'
    this.status = status
    this.index = detail.index
    this.field = detail.field
  }

  /** The row to point at, or `null` when the error belongs to the whole collection and not to one row. */
  rowIndex(): number | null {
    return this.index !== undefined && this.index >= 0 ? this.index : null
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
  // A 400 is `{error, index, field}`; anything else is plain text.
  try {
    const body = JSON.parse(text) as { error?: unknown; index?: unknown; field?: unknown }
    if (typeof body === 'object' && body !== null && typeof body.error === 'string') {
      return new PersonaApiError(res.status, body.error, {
        index: typeof body.index === 'number' ? body.index : undefined,
        field: typeof body.field === 'string' ? body.field : undefined,
      })
    }
  } catch { /* not JSON */ }
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
