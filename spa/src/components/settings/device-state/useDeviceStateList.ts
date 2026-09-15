// spa/src/components/settings/device-state/useDeviceStateList.ts — the device
// state list (spec §4.1): fetched on mount, on host change, on reload(), and
// whenever this computer's own upload succeeds.
import { useEffect, useState } from 'react'
import { listDeviceStates } from '../../../lib/device-state/api'
import type { DeviceStateSummary } from '../../../lib/device-state/api'
import { useDeviceStateStore } from '../../../stores/useDeviceStateStore'

export type DeviceStateListView =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'rows'; rows: DeviceStateSummary[] }

type Result =
  | { hostId: string; kind: 'error'; message: string }
  | { hostId: string; kind: 'rows'; rows: DeviceStateSummary[] }

export function useDeviceStateList(hostId: string | null): { view: DeviceStateListView | null; reload: () => void } {
  const [nonce, setNonce] = useState(0)
  const [result, setResult] = useState<Result | null>(null)
  const reload = () => setNonce((n) => n + 1)

  // A fresh successful upload changes what the list shows for this computer.
  // Subscribing (rather than depending on `status` in render) fires once per
  // new `ok`, and never on leaving `ok`.
  useEffect(
    () =>
      useDeviceStateStore.subscribe((state, prev) => {
        const next = state.status
        if (next.kind !== 'ok') return
        if (prev.status.kind === 'ok' && prev.status.at === next.at) return
        setNonce((n) => n + 1)
      }),
    [],
  )

  useEffect(() => {
    if (!hostId) return
    // `cancelled` drops a response that lands after the host changed (or the
    // section unmounted), so a slow old host can never overwrite the new list.
    let cancelled = false
    listDeviceStates(hostId).then(
      (rows) => {
        if (!cancelled) setResult({ hostId, kind: 'rows', rows })
      },
      (e: unknown) => {
        if (!cancelled) setResult({ hostId, kind: 'error', message: e instanceof Error ? e.message : String(e) })
      },
    )
    return () => {
      cancelled = true
    }
  }, [hostId, nonce])

  if (!hostId) return { view: null, reload }
  // A result for another host is stale → loading. A result for this host is
  // kept while a same-host refetch runs, so a routine refresh does not flash.
  if (!result || result.hostId !== hostId) return { view: { kind: 'loading' }, reload }
  return { view: result, reload }
}
