export const HOST_SUB_PAGES = ['overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs'] as const

export type HostSubPage = (typeof HOST_SUB_PAGES)[number]

export function isHostSubPage(value: string): value is HostSubPage {
  return HOST_SUB_PAGES.includes(value as HostSubPage)
}

export function encodeHostRouteId(hostId: string): string {
  return encodeURIComponent(hostId)
}

export function decodeHostRouteId(segment: string): string | null {
  try {
    return decodeURIComponent(segment)
  } catch {
    return null
  }
}
