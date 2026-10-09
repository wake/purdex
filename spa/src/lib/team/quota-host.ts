// spa/src/lib/team/quota-host.ts — is this host still configured on this device (the writer asks before it applies an
// answer or re-reads a host: a host removed while a request was out must not get the old daemon's numbers back).
import { useHostStore } from '../../stores/useHostStore'

export const hostExistsNow = (hostId: string): boolean => Object.hasOwn(useHostStore.getState().hosts, hostId)
