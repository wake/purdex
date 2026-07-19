import { useEffect } from 'react'
import { registerDeeplinkResolver } from '../lib/deeplink/deeplinkResolver'

/**
 * Registers the purdex:// execution deeplink resolver (Task P.12). Must be
 * called BEFORE useElectronIpc (which sends `spa:ready`): the electron main
 * process buffers a cold-start deeplink and flushes it on the first
 * `spa:ready`, so a listener attached afterwards would miss it. Placing this
 * hook ahead of useElectronIpc in App guarantees the mount effect subscribes
 * first.
 */
export function useDeeplinkResolver(): void {
  useEffect(() => registerDeeplinkResolver(), [])
}
