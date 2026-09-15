// spa/src/lib/device-state/device-name.ts — the default name for this computer
// (spec §3.5): Electron's hostname when available, else "<Browser> · <OS>".

const HOSTNAME_TIMEOUT_MS = 1500

function detectBrowser(ua: string): string | null {
  // Order matters: Edge UAs contain "Chrome", Chrome UAs contain "Safari".
  if (/Edg\//.test(ua)) return 'Edge'
  if (/Firefox\//.test(ua)) return 'Firefox'
  if (/Chrome\//.test(ua)) return 'Chrome'
  if (/Safari\//.test(ua)) return 'Safari'
  return null
}

function detectOS(ua: string): string | null {
  // Order matters: iOS UAs say "like Mac OS X", Android UAs say "Linux".
  if (/iPhone|iPad/.test(ua)) return 'iOS'
  if (/Android/.test(ua)) return 'Android'
  if (/Mac OS X|Macintosh/.test(ua)) return 'macOS'
  if (/Windows/.test(ua)) return 'Windows'
  if (/Linux/.test(ua)) return 'Linux'
  return null
}

export function parseUserAgentName(ua: string): string {
  const browser = detectBrowser(ua) ?? 'Browser'
  const os = detectOS(ua)
  return os ? `${browser} · ${os}` : browser
}

async function electronHostname(): Promise<string | null> {
  const status = window.electronAPI?.localDaemonStatus
  if (!status) return null
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((resolve) => {
    timer = setTimeout(() => resolve(null), HOSTNAME_TIMEOUT_MS)
  })
  try {
    const result = await Promise.race([Promise.resolve().then(() => status()), timeout])
    const hostname: unknown = result?.hostname
    return typeof hostname === 'string' && hostname.trim() !== '' ? hostname.trim() : null
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

export async function resolveDefaultDeviceName(): Promise<string> {
  return (await electronHostname()) ?? parseUserAgentName(navigator.userAgent)
}
