// spa/src/components/LaunchPlanError.tsx — shown in the deck / chat when the daemon's launch plan could not be read: the user
// is never stuck on a view with no way out (retry, or go to the terminal).
import { useI18nStore } from '../stores/useI18nStore'

export function LaunchPlanError({ onRefresh, onSwitchToTerminal }: { onRefresh: () => void; onSwitchToTerminal: () => void }) {
  const t = useI18nStore((s) => s.t)
  const btn = 'rounded border border-border-default px-2 py-1 text-sm text-text-secondary hover:bg-surface-hover hover:text-text-primary'
  return (
    <div data-testid="launch-plan-error" className="flex h-full items-center justify-center p-6">
      <div className="flex max-w-full flex-col items-start gap-3 text-sm text-text-secondary">
        <p>{t('launch.plan_error')}</p>
        <div className="flex gap-2">
          <button type="button" data-testid="launch-plan-error-refresh" className={btn} onClick={onRefresh}>{t('launch.refresh')}</button>
          <button type="button" data-testid="launch-plan-error-terminal" className={btn} onClick={onSwitchToTerminal}>{t('launch.to_terminal')}</button>
        </div>
      </div>
    </div>
  )
}
