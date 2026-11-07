// spa/src/components/team/CompactionLine.tsx — one session's compaction settings (compaction-control spec §8): 討論 / 開發 modes,
// threshold, idle compaction, 跟主機預設. Read when shown and again after our own write (a lead's `pdx compact` shows up the next
// time the panel opens). Panels are side panels, not tab panes, so component state is fine.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { getCompaction, putCompaction, type CompactionOverride, type CompactionView } from '../../lib/team/compaction-api'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { ThresholdStepper, clampThreshold } from '../settings/ThresholdStepper'
import { keepFocus } from './panel-focus'

const ROW = 'flex items-center gap-2 px-2 min-h-7 flex-wrap'
const LABEL = 'text-[11px] text-text-muted'
const BTN = 'px-1.5 py-0.5 rounded text-[11px] border border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer aria-pressed:bg-surface-hover aria-pressed:text-text-primary aria-pressed:border-accent'
const DEV_THRESHOLD = 70

export function CompactionLine({ hostId, sessionId }: { hostId: string; sessionId: string }) {
  const t = useI18nStore((s) => s.t)
  const key = `${hostId}\n${sessionId}`
  const keyRef = useRef(key)
  keyRef.current = key
  const [shown, setShown] = useState<{ key: string; view: CompactionView } | null>(null)
  const [failedKey, setFailedKey] = useState<string | null>(null)
  const viewRef = useRef<CompactionView | null>(null)
  const queues = useRef(new Map<string, Promise<void>>())

  const apply = useCallback((forKey: string, view: CompactionView) => {
    if (keyRef.current !== forKey) return // a late answer for another session
    viewRef.current = view
    setShown({ key: forKey, view })
  }, [])

  useEffect(() => {
    viewRef.current = null
    const ctl = new AbortController()
    getCompaction(hostId, sessionId, ctl.signal).then((v) => apply(key, v), () => {})
    return () => ctl.abort()
  }, [hostId, sessionId, key, apply])

  // Saves run one after another, each computed from the view current when it starts (two quick presses add up).
  const save = (build: (cur: CompactionView) => CompactionOverride) => {
    const forKey = key
    // One chain per session, kept for the component's life: another session's hung save does not hold this one back, and
    // coming back to a session chains onto its earlier saves (its writes stay in order).
    queues.current.set(forKey, (queues.current.get(forKey) ?? Promise.resolve()).then(async () => {
      const cur = viewRef.current
      if (cur === null || keyRef.current !== forKey) return
      setFailedKey(null)
      try {
        const put = await putCompaction(hostId, sessionId, build(cur))
        apply(forKey, put)
        try { apply(forKey, await getCompaction(hostId, sessionId)) } catch { /* the PUT answer stays */ }
      } catch {
        if (keyRef.current === forKey) setFailedKey(forKey)
      }
    }))
  }

  const view = shown?.key === key ? shown.view : null
  if (view === null) return null
  const { effective: eff, override: ov } = view
  if (!eff.managed) {
    return <p data-testid="compaction-unmanaged" className="px-2 text-[11px] text-text-muted">{t(`compaction.unmanaged.${eff.reason ?? 'self_relay'}`)}</p>
  }
  const discuss = !eff.auto && !eff.idle
  const dev = eff.auto && eff.threshold === DEV_THRESHOLD && eff.idle
  const hasOverride = ov.auto !== null || ov.threshold !== null || ov.idle !== null
  const mode = (id: string, on: boolean, text: string, o: (cur: CompactionView) => CompactionOverride) => (
    <button type="button" data-testid={id} aria-pressed={on} onMouseDown={keepFocus} onClick={() => save(o)} className={BTN}>{text}</button>
  )
  return (
    <div className="flex flex-col">
      <div className={ROW}>
        <span className={LABEL}>{t('compaction.label')}</span>
        {!discuss && !dev && <span data-testid="compaction-custom" className={LABEL}>{t('compaction.custom')}</span>}
        {mode('compaction-discuss', discuss, t('compaction.discuss'), (c) => ({ auto: false, threshold: c.override.threshold, idle: false }))}
        {mode('compaction-dev', dev, t('compaction.dev'), () => ({ auto: true, threshold: DEV_THRESHOLD, idle: true }))}
        {hasOverride && (
          <button type="button" data-testid="compaction-follow-host" onMouseDown={keepFocus} className={BTN}
            onClick={() => save(() => ({ auto: null, threshold: null, idle: null }))}>{t('compaction.follow_host')}</button>
        )}
      </div>
      <div className={ROW}>
        <span className={LABEL}>{t('compaction.threshold')}</span>
        <span onMouseDown={keepFocus}>
          <ThresholdStepper testId="compaction-threshold" label={t('compaction.threshold')} value={eff.threshold} disabled={!eff.auto}
            onStep={(d) => save((c) => ({ ...c.override, threshold: clampThreshold((c.override.threshold ?? c.effective.threshold) + d) }))} />
        </span>
        <span className={LABEL}>{t('compaction.idle')}</span>
        <span onMouseDown={keepFocus}>
          <ToggleSwitch testId="compaction-idle" label={t('compaction.idle')} checked={eff.idle}
            onChange={() => save((c) => ({ ...c.override, idle: !c.effective.idle }))} />
        </span>
      </div>
      {failedKey === key && <p data-testid="compaction-error" role="status" className="px-2 text-[11px] text-status-warning-text">{t('compaction.error')}</p>}
    </div>
  )
}
