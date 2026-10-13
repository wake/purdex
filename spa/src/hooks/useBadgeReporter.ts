// spa/src/hooks/useBadgeReporter.ts — once per window: report what this window counts toward the Mac Dock badge
// (app-badge spec §3.2). A no-op outside Electron. See lib/badge/badge-reporter.ts.
import { useEffect } from 'react'
import { startBadgeReporter } from '../lib/badge/badge-reporter'

export function useBadgeReporter(): void {
  useEffect(() => startBadgeReporter(), [])
}
