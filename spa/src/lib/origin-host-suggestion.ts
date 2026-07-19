// Decides whether a plain-browser (non-Electron), https-served SPA should
// suggest adding the current origin as a host. On an https page, only https
// hosts can connect (http hosts are mixed-content-blocked), so we suggest
// when there is no usable https host at all. The suggestion is an explicit
// host draft, never an implicit same-origin connection (spec §6).
import type { HostConfig } from '../stores/useHostStore'
import { hostScheme } from './host-endpoint'

export function shouldSuggestOriginHost(opts: {
  isElectron: boolean
  protocol: string
  hosts: HostConfig[]
}): boolean {
  if (opts.isElectron) return false
  if (opts.protocol !== 'https:') return false
  return !opts.hosts.some((h) => hostScheme(h) === 'https')
}

export function originHostDraft(opts: { hostname: string; port: string }): {
  scheme: 'https'
  ip: string
  port: string
  useToken: boolean
} {
  return { scheme: 'https', ip: opts.hostname, port: opts.port || '443', useToken: false }
}
