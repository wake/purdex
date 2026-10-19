import { useState } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PoolHostRow } from '../../lib/pool/use-pool-host-rows'
import { ProbeBadge } from './probe-rows'
import { PoolStatusDot } from './PoolStatusDot'

/**
 * 「資源池主機」 in the Hosts page's sidebar (plan WWD-7, 7a-3): every pool host with its badge, name, a small 「不接」 while its worker is
 * off, and its light. No `+`: adding a host is installing wwd on it and editing the peers, which the App cannot do.
 */
export function PoolHostGroup({ rows, selectedId, onOpen }: { rows: PoolHostRow[]; selectedId: string | null; onOpen: (row: PoolHostRow) => void }) {
  const t = useI18nStore((s) => s.t)
  const [open, setOpen] = useState(true)
  return (
    <div data-testid="pool-group" className="mt-2">
      <div className="flex items-center px-1">
        <button type="button" data-testid="pool-group-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}
          className="flex-1 flex items-center gap-1.5 px-1 py-1 text-xs uppercase text-text-secondary cursor-pointer text-left">
          {open ? <CaretDown size={10} /> : <CaretRight size={10} />}
          <span>{t('pool_hosts.group')}</span>
        </button>
      </div>
      {open && rows.map((r) => {
        const selected = selectedId === r.id
        return (
          <button key={r.id} type="button" data-testid={`pool-item-${r.id}`} aria-current={selected ? 'true' : undefined} onClick={() => onOpen(r)}
            className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-1.5 ${selected ? 'bg-accent text-white' : 'text-text-secondary hover:bg-surface-secondary/50'}`}>
            <ProbeBadge row={r.look as never} />
            <span className="truncate flex-1">{r.name}</span>
            {r.worker && !r.worker.enabled && <span data-testid={`pool-item-off-${r.id}`} className={`shrink-0 text-[10px] ${selected ? 'text-white/80' : 'text-text-muted'}`}>{t('pool.card.worker_off')}</span>}
            <PoolStatusDot dot={r.dot} />
          </button>
        )
      })}
    </div>
  )
}
