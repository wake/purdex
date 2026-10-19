// spa/src/lib/names/rename-preview.ts — `POST /api/peers/rename-preview` (#3520): how many live conversations a first
// persona save would rename, counted by the daemon that does the renaming. A read, nothing is written.
import { hostFetch } from '../host-api'
import type { Persona } from './persona-types'

/**
 * The daemon's exact count, or `null` for anything else: a daemon without the route (404/405), one that is not ready
 * (503), a refused request (400), a lost connection, a body that is not the expected shape. The caller then falls back
 * to the floor it can work out from the inventory, so every failure looks the same here on purpose.
 */
export async function fetchRenamePreview(hostId: string, personas: Persona[], signal?: AbortSignal): Promise<number | null> {
  try {
    const res = await hostFetch(hostId, '/api/peers/rename-preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ codes: personas.map((p) => p.code), pickable: personas.filter((p) => !p.retired).map((p) => p.code) }),
      signal,
    })
    if (!res.ok) return null
    const body = (await res.json()) as { would_rename?: unknown }
    const n = body.would_rename
    return typeof n === 'number' && Number.isInteger(n) && n >= 0 ? n : null
  } catch {
    return null
  }
}
