// spa/src/hooks/usePushPresence.ts — the Mac window's presence report to push-capable daemons (PU-4); mounted once in
// App.tsx. All the logic is in lib/presence/reporter.ts.
import { useEffect } from 'react'
import { startPushPresence } from '../lib/presence/reporter'

export function usePushPresence(): void {
  useEffect(() => startPushPresence(), [])
}
