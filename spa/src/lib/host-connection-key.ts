// spa/src/lib/host-connection-key.ts — the one definition of "which daemon, as which identity" a host config names.
// Shared by the event-WS connection (a change tears the connection down) and the roster forget (a change drops what
// the old daemon said about its teams), so the two can never disagree about when a host is "the same host".

/**
 * What a host's connection is negotiated from: its endpoint AND its token. A change to either tears the connection
 * down and starts a fresh one, exactly as a reload would. The token has to be part of it (#1360): a tokenless host
 * ends its negotiation in `auth-error`, which the state machine treats as final — so without a new connection, a
 * token added in-app was never tried.
 *
 * JSON, not a joined string: a token is user input and may contain any separator (and an IPv6 `ip` contains colons),
 * so a joined key could serialise two different configurations identically. `null` and an absent token are the same
 * (no token). The key carries the token, so it must never be logged.
 */
export function connectionKey(host: { ip: string; port: number; token?: string | null }): string {
  return JSON.stringify([host.ip, host.port, host.token ?? ''])
}
