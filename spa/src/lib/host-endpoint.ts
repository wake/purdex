// Pure endpoint derivation for host connections. A host is an explicit
// {scheme, ip(host), port} endpoint; HTTP/WS bases and the identity key are
// derived here so the SPA connects the same way whether served by the daemon
// (same-origin) or from a hosted client. No relative URLs, no origin assumptions.
import type { HostConfig } from '../stores/useHostStore'

const DEFAULT_PORTS = { http: 80, https: 443 } as const

export function hostScheme(host: Pick<HostConfig, 'scheme'>): 'http' | 'https' {
  return host.scheme ?? 'http'
}

function portSuffix(port: number, scheme: 'http' | 'https'): string {
  return port === DEFAULT_PORTS[scheme] ? '' : `:${port}`
}

export function deriveDaemonBase(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  const scheme = hostScheme(host)
  return `${scheme}://${host.ip}${portSuffix(host.port, scheme)}`
}

export function deriveWsBase(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  const scheme = hostScheme(host)
  const wsScheme = scheme === 'https' ? 'wss' : 'ws'
  return `${wsScheme}://${host.ip}${portSuffix(host.port, scheme)}`
}

// Endpoint identity — any change here (including scheme) means "different
// endpoint"; callers use it to trigger reconnect and token re-auth.
export function hostEndpointKey(host: Pick<HostConfig, 'scheme' | 'ip' | 'port'>): string {
  return `${hostScheme(host)}:${host.ip}:${host.port}`
}
