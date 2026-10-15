// spa/src/lib/fleet/api.ts — `GET /api/fleet` (fleet spec §6). Answers a primary, a standby or an off daemon.
import { hostFetch } from '../host-api'
import { parseFleetAnswer } from './parse'
import type { FleetAnswer } from './types'

export async function fetchFleet(hostId: string): Promise<FleetAnswer> {
  const res = await hostFetch(hostId, '/api/fleet')
  if (!res.ok) throw new Error(`GET /api/fleet: ${res.status}`)
  const answer = parseFleetAnswer(await res.json())
  if (!answer) throw new Error('GET /api/fleet: unreadable answer')
  return answer
}
