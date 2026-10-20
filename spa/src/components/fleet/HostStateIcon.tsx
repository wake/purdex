// spa/src/components/fleet/HostStateIcon.tsx — what stands in for a host's readings when it is not ok (monitoring tabs M17–M19): wwd_down a bold yellow broken link, host_down a bold red power symbol,
// unknown a bold grey question mark. The wide cell puts it where the indicator was, the narrow block (M16) in the middle of its square.
import { LinkBreak, Power, Question } from '@phosphor-icons/react'
import type { PoolHostState } from '../../lib/pool/host-state'

export function HostStateIcon({ state, size }: { state: Exclude<PoolHostState['state'], 'ok'>; size: number }) {
  return state === 'wwd_down' ? <LinkBreak weight="bold" size={size} className="text-status-warning" />
    : state === 'host_down' ? <Power weight="bold" size={size} className="text-status-error" />
    : <Question weight="bold" size={size} className="text-text-muted" />
}
