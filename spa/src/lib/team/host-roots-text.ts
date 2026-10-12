// spa/src/lib/team/host-roots-text.ts — the words for a refused grant change (XR-3). `unknown_host` is also what the input says for
// `x:/…` when no paired host is called x.
import type { HostRootsError } from './host-roots'

type T = (key: string, params?: Record<string, string | number>) => string

export function hostRootsErrorText(e: HostRootsError, t: T): string {
  switch (e.kind) {
    case 'in_use': return t('unattended.hostroots.in_use', { who: e.who.join('、') })
    case 'unknown_host': return t('unattended.hostroots.err_unknown_host', { name: e.name })
    case 'bad_root': return t('unattended.hostroots.err_bad_root', { detail: e.detail })
    case 'not_a_root': return t('unattended.hostroots.err_not_a_root')
    case 'failed': return t('unattended.hostroots.err_failed', { code: e.code })
  }
}
