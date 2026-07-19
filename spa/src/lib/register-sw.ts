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

// Stateful transition for each 'controllerchange' event. The first event
// observed on an uncontrolled page is the initial SW claim — it must NOT
// reload (or every first load would refresh once). Every subsequent event
// (or the first event on an already-controlled page) is a real update
// takeover and must reload.
export function nextControllerChange(controlled: boolean): { controlled: boolean; reload: boolean } {
  if (!controlled) return { controlled: true, reload: false }
  return { controlled: true, reload: true }
}

export function registerServiceWorker(): void {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') return
  const ok = shouldRegisterServiceWorker({
    isElectron: getPlatformCapabilities().isElectron,
    protocol: window.location.protocol,
    hasServiceWorker: 'serviceWorker' in navigator,
  })
  if (!ok) return

  let controlled = !!navigator.serviceWorker.controller
  let reloading = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    const res = nextControllerChange(controlled)
    controlled = res.controlled
    if (res.reload && !reloading) {
      reloading = true
      window.location.reload()
    }
  })
  navigator.serviceWorker.register('/sw.js').catch(() => {
    // Best-effort: any http origin is attempted, but non-trustworthy origins
    // (non-loopback http, e.g. http://100.64.0.2 dev) reject per Secure
    // Contexts — swallowed intentionally.
  })
}
