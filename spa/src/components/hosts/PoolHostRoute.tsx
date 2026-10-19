import { useEffect } from 'react'
import { usePoolHostRows } from '../../lib/pool/use-pool-host-rows'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { PoolHostPage } from './PoolHostPage'

/**
 * The pool host page of a host with a daemon (no style editor: its style is set in the overview). Once the pool has answered and the
 * host is no longer in its list, the selection is dropped, so the Hosts page falls back to the host it showed before instead of staying
 * on an empty page; before the pool has answered the selection is kept.
 */
export function PoolHostRoute({ id }: { id: string }) {
  const { rows, active } = usePoolHostRows()
  const row = rows.find((r) => r.id === id)
  const gone = active && rows.length > 0 && !row
  useEffect(() => {
    if (gone && useFleetDraftStore.getState().selectedPool === id) useFleetDraftStore.getState().selectPool(null)
  }, [gone, id])
  return row ? <PoolHostPage key={id} row={row} /> : null
}
