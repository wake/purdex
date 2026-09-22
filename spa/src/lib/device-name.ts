// spa/src/lib/device-name.ts — this computer's device name: normalization, the
// effective-name fallback chain, and the default name (Electron's hostname when
// available, else "<Browser> · <OS>").

export const DEVICE_NAME_MAX_CODE_POINTS = 64

/** The two fields the effective name is derived from (structural on purpose:
 *  passing a store state without them is a compile error, not a silent 'Browser'). */
export interface DeviceNameParts {
  /** User override; null = use `defaultDeviceName`. */
  deviceName: string | null
  /** Resolved at startup (Electron hostname or UA). */
  defaultDeviceName: string
}

/**
 * Trim; blank → null; longer than 64 code points → first 64 code points
 * (split by code point, so surrogate pairs such as emoji are never cut).
 * Mirrors the daemon's validation (trim, then 1–64 runes).
 */
export function normalizeDeviceName(name: string): string | null {
  const trimmed = name.trim()
  if (trimmed === '') return null
  const points = Array.from(trimmed)
  return points.length > DEVICE_NAME_MAX_CODE_POINTS
    ? points.slice(0, DEVICE_NAME_MAX_CODE_POINTS).join('')
    : trimmed
}

export function effectiveDeviceName(parts: DeviceNameParts): string {
  return (
    normalizeDeviceName(parts.deviceName ?? '') ?? normalizeDeviceName(parts.defaultDeviceName) ?? 'Browser'
  )
}

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
