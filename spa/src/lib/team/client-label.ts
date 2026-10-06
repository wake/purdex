// spa/src/lib/team/client-label.ts — the audit label this app signs its decisions with (lead-team spec §6.5):
// `Purdex.app @ <hostname>`, the hostname from Electron's local-daemon status (device-name.ts reads the same
// field the same way), else `Purdex.app`. Resolved once per renderer: the daemon adds the remote address.
// U14: there is no browser label — the App is the only client.
import type { Client } from './types'

const HOSTNAME_TIMEOUT_MS = 1500
const APP = 'Purdex.app'

let cached: Promise<Client> | null = null

async function resolve(): Promise<Client> {
  const status = window.electronAPI?.localDaemonStatus
  if (!status) return { kind: 'app', label: APP }
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((done) => {
    timer = setTimeout(() => done(null), HOSTNAME_TIMEOUT_MS)
  })
  try {
    const result = await Promise.race([Promise.resolve().then(() => status()), timeout])
    const hostname: unknown = result?.hostname
    const name = typeof hostname === 'string' ? hostname.trim() : ''
    return { kind: 'app', label: name !== '' ? `${APP} @ ${name}` : APP }
  } catch {
    return { kind: 'app', label: APP }
  } finally {
    clearTimeout(timer)
  }
}

/** This app's `Client` descriptor, resolved on first use and then reused. */
export function clientDescriptor(): Promise<Client> {
  if (cached === null) cached = resolve()
  return cached
}

export function __resetClientDescriptorForTests(): void {
  cached = null
}
