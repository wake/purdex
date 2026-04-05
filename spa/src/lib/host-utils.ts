// spa/src/lib/host-utils.ts — Shared host utility functions
import type { HostRuntime } from '../stores/useHostStore'

/**
 * Returns a user-facing error message for the current host connection state,
 * or null if there is no error.
 */
export function connectionErrorMessage(
  runtime: HostRuntime | undefined,
  t: (key: string) => string,
): string | null {
  if (runtime?.status === 'auth-error') return t('hosts.error_auth')
  if (!runtime || runtime.status !== 'connected') {
    if (runtime?.daemonState === 'unreachable') return t('hosts.error_unreachable')
    if (runtime?.daemonState === 'refused') return t('hosts.error_refused')
    return null
  }
  if (runtime.tmuxState === 'unavailable') return t('hosts.error_tmux_down')
  return null
}
