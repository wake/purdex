// spa/src/components/watch/host/AlertSettings.tsx — the 設定 tab's 告警 section (monitoring tabs spec M49, P9, W8; plan MON-9a-2a). Read only: the thresholds are constants in wwd (and a few in its TOML), with no API that
// reads a host's real values, so the table is the defaults wwd's spec §13.3 names and says so. The two "unreachable" alerts live only in each wwd's TOML (`[alert] alert_unreachable`): they are neither on nor off here
// until A's settings API (S9) exists. When the thresholds can be read per host, this becomes a read of each host's own values.
import { LinkBreak, Power } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { usePoolStore } from '../../../lib/pool/store'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { Section } from '../../hosts/form-fields'
import { SettingItem } from '../../settings/SettingItem'

export interface AlertRule {
  id: string
  /** `push` goes to the phone; `digest` is only in the 事件 tab and the summary. */
  level: 'push' | 'digest'
  /** Only a host that reports PSI has this rule (the mserv service rule). */
  needsPsi?: boolean
}

// Defaults of wwd (internal/wwd/advisor/rules.go:12-25, :130-131; rules_mem.go:16-17, :110-112; rules_mserv.go:15-17). The watch-level rules S3-2 added (`pressure_watch`, `pressure_danger`, `swap_growth`) are not alerts.
export const ALERT_RULES: readonly AlertRule[] = [
  { id: 'disk_low', level: 'push' },
  { id: 'disk_trend', level: 'digest' },
  { id: 'pressure_sustained', level: 'push' },
  { id: 'mem_thrash', level: 'digest' },
  { id: 'daemon_big', level: 'digest' },
  { id: 'wwd_restart', level: 'push' },
  { id: 'mserv_service', level: 'push', needsPsi: true },
]
// `[observe] min_cpu_seconds / min_mem_mb` (internal/wwd/daemon/config.go:148-149).
const OBSERVE = ['observe_cpu', 'observe_mem'] as const

const INPUT = 'w-96 px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary disabled:opacity-50'

function RuleRow({ id, level }: { id: string; level?: AlertRule['level'] }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid={`alert-rule-${id}`}>
      <SettingItem label={t(`watch.alert.rule.${id}`)} description={level ? t(`watch.alert.level.${level}`) : undefined}>
        <input type="text" readOnly disabled aria-label={t(`watch.alert.rule.${id}`)} className={INPUT} value={t(`watch.alert.th.${id}`)} />
      </SettingItem>
    </div>
  )
}

function Unreachable({ which }: { which: 'wwd' | 'host' }) {
  const t = useI18nStore((s) => s.t)
  const Icon = which === 'wwd' ? LinkBreak : Power
  const tone = which === 'wwd' ? 'text-status-warning' : 'text-status-error'
  return (
    <div data-testid={`alert-unreach-${which}`}>
      <SettingItem label={t(`watch.alert.unreach_${which}`)} description={t('watch.alert.unreach_d')} disabled>
        <span className="flex items-center gap-2">
          <Icon size={14} className={tone} />
          <span data-testid={`alert-unreach-${which}-state`} aria-disabled="true" className="text-xs text-text-muted">{t('watch.alert.unreach_pending')}</span>
        </span>
      </SettingItem>
    </div>
  )
}

export function AlertSettings({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const hasPsi = usePoolStore((s) => s.hosts.find((h) => h.alias === host.alias)?.facts?.mem.psi != null)
  return (
    <Section title={t('watch.alert.title')}>
      <div data-testid="alert-settings">
        <p data-testid="alert-defaults-note" className="mb-2 text-xs text-text-muted">{t('watch.alert.defaults_note')}</p>
        {ALERT_RULES.filter((r) => !r.needsPsi || hasPsi).map((r) => <RuleRow key={r.id} id={r.id} level={r.level} />)}
        <Unreachable which="wwd" />
        <Unreachable which="host" />
        {OBSERVE.map((id) => <RuleRow key={id} id={id} />)}
      </div>
    </Section>
  )
}
