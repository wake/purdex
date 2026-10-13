// spa/src/lib/team/paused-text.ts — the text of the 被擋下 light (UP-5b contract): 「被擋下：接力申請沒有回應」 when the causes hold
// `relay_unanswered` (a member reads 「被擋下：等待 lead 確認接力」: its lead is the one asked), else 「被擋下：接力額度用完」 when they
// hold `quota`, else 「被擋下：lead 已暫停」; then 「・退回 N 則」 when messages were bounced to it. relay_unanswered wins over the
// others, as in the daemon's bounce text (relay v2 plan, App contract 4).
import type { PauseCause } from './roster'
import type { PausedRole } from './paused-api'

type T = (key: string, params?: Record<string, string | number>) => string

export function pausedLightText(t: T, causes: readonly PauseCause[], bounced: number, role: PausedRole): string {
  const base = causes.includes('relay_unanswered')
    ? t(role === 'member' ? 'paused.light.unanswered_member' : 'paused.light.unanswered')
    : causes.includes('quota') ? t('paused.light.quota') : t('paused.light.lead')
  return bounced > 0 ? `${base}${t('paused.light.bounced', { n: bounced })}` : base
}
