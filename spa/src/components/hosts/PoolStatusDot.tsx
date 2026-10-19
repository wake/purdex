// spa/src/components/hosts/PoolStatusDot.tsx — the light of a resource pool host: green live, yellow wwd silent (read over SSH), grey nobody reads it, red an error.
import { useI18nStore } from '../../stores/useI18nStore'
import type { PoolHostDot } from '../../lib/pool/use-pool-host-rows'

const DOT: Record<PoolHostDot, string> = { live: 'bg-green-400', warn: 'bg-yellow-400', off: 'bg-text-muted', error: 'bg-red-400' }
const LABEL: Record<PoolHostDot, string> = { live: 'pool_hosts.dot.live', warn: 'pool_hosts.dot.warn', off: 'pool_hosts.dot.off', error: 'pool_hosts.dot.error' }

export function PoolStatusDot({ dot }: { dot: PoolHostDot }) {
  const label = useI18nStore((s) => s.t)(LABEL[dot])
  return <span role="img" aria-label={label} title={label} data-testid="pool-status" data-dot={dot} className={`inline-block w-2 h-2 rounded-full shrink-0 ${DOT[dot]}`} />
}
