import { usePoolHostRows } from '../../lib/pool/use-pool-host-rows'
import { PoolHostPage } from './PoolHostPage'

/** The pool host page of a host with a daemon (no style editor: its style is set in the overview). A host that left the list shows nothing. */
export function PoolHostRoute({ id }: { id: string }) {
  const { rows } = usePoolHostRows()
  const row = rows.find((r) => r.id === id)
  return row ? <PoolHostPage key={id} row={row} /> : null
}
