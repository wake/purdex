// spa/src/lib/team/quota-host.ts — which daemon a host id means right now (the writer asks before it sends, applies an answer
// or re-reads a host: a host removed or re-pointed (same id, another endpoint or token) while a request was out is another
// daemon, and must not get the old one's numbers - or a write meant for it).
import { connectionKey } from '../host-connection-key'
import { useHostStore } from '../../stores/useHostStore'

/** The host's endpoint + token as one string, or null when the host is not configured (any change of it is a re-point). */
export function hostIdentityNow(hostId: string): string | null {
  const host = useHostStore.getState().hosts[hostId]
  return host ? connectionKey(host) : null
}
