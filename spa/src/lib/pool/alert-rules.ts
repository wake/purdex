// spa/src/lib/pool/alert-rules.ts — the alert rules the 設定 tab lists (MON-9a-2a; spec M49): wwd's defaults, which the App cannot read per host yet (S9). The texts of each rule are in the locale files, `watch.alert.rule.<id>`
// and `watch.alert.th.<id>`; this is only which rules exist and how loud each is.
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
export const OBSERVE_RULES = ['observe_cpu', 'observe_mem'] as const
