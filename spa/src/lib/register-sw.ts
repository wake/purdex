// spa/src/lib/register-sw.ts
// Registers the web PWA service worker. Web-only: skipped in Electron and on
// non-http(s) protocols. On an updated SW taking control, reloads once so the
// running SPA can't drift from the newly-served bundle. Pure decision helpers
// are unit-tested; the navigator glue is thin and reviewed.
import { getPlatformCapabilities } from './platform'

export function shouldRegisterServiceWorker(opts: {
  isElectron: boolean
  protocol: string
  hasServiceWorker: boolean
}): boolean {
  if (opts.isElectron) return false
  if (!opts.hasServiceWorker) return false
  return opts.protocol === 'https:' || opts.protocol === 'http:'
}

// Only reload when an updated SW takes over a page that already had a
// controller. The first-ever control acquisition (no prior controller) must
// NOT reload, or every first load would refresh once.
export function shouldReloadOnControllerChange(hadController: boolean): boolean {
  return hadController
}

export function registerServiceWorker(): void {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') return
  const ok = shouldRegisterServiceWorker({
    isElectron: getPlatformCapabilities().isElectron,
    protocol: window.location.protocol,
    hasServiceWorker: 'serviceWorker' in navigator,
  })
  if (!ok) return

  const hadController = !!navigator.serviceWorker.controller
  let reloading = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (reloading) return
    if (!shouldReloadOnControllerChange(hadController)) return
    reloading = true
    window.location.reload()
  })
  navigator.serviceWorker.register('/sw.js').catch(() => {
    // Best-effort: any http origin is attempted, but non-trustworthy origins
    // (non-loopback http, e.g. http://100.64.0.2 dev) reject per Secure
    // Contexts — swallowed intentionally.
  })
}
