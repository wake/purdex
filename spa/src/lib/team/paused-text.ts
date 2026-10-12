// spa/src/lib/team/paused-text.ts — the text of the 被擋下 light (UP-5b contract): 「被擋下：接力額度用完」 when the causes hold
// `quota`, 「被擋下：lead 已暫停」 when they are only `lead`, then 「・退回 N 則」 when messages were bounced to it.
import type { PauseCause } from './roster'

type T = (key: string, params?: Record<string, string | number>) => string

export function pausedLightText(t: T, causes: readonly PauseCause[], bounced: number): string {
  const base = causes.includes('quota') ? t('paused.light.quota') : t('paused.light.lead')
  return bounced > 0 ? `${base}${t('paused.light.bounced', { n: bounced })}` : base
}
