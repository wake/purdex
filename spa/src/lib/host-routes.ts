import { listContributions } from './settings-contribution-registry'

/**
 * Canonical order of built-in host sub-pages.
 * Not the type source for `HostSubPage` (widened to `string` in commit 3) and
 * not consulted by `isHostSubPage` (which queries the contribution registry).
 * Retained for: (a) test reference in host-routes.test.ts C1, (b) ordering
 * annotation in register-modules.tsx where these are registered as built-in
 * host contributions.
 */
export const HOST_SUB_PAGES = ['overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs'] as const

// Commit 3: loosened from the six-literal union to plain string so that
// module-contributed host sub-pages can navigate via URL without type casts.
export type HostSubPage = string

export function isHostSubPage(value: string): boolean {
  return listContributions('host').some((c) => c.localId === value)
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
