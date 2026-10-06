import { useI18nStore } from '../stores/useI18nStore'

export type RebuildMode = 'terminal' | 'worker'

export interface RebuildModeChoiceProps {
  value: RebuildMode
  onChange: (m: RebuildMode) => void
  terminalAvailable: boolean
  workerAvailable: boolean
  workerUnavailableHint?: string
}

export function RebuildModeChoice({
  value,
  onChange,
  terminalAvailable,
  workerAvailable,
  workerUnavailableHint,
}: RebuildModeChoiceProps) {
  const t = useI18nStore((s) => s.t)
  const options: { mode: RebuildMode; label: string; available: boolean; hint?: string }[] = [
    { mode: 'terminal', label: t('worker.rebuild.terminal'), available: terminalAvailable },
    { mode: 'worker', label: t('worker.rebuild.worker'), available: workerAvailable, hint: workerUnavailableHint },
  ]
  return (
    <div
      role="radiogroup"
      aria-label={t('worker.rebuild.mode_label')}
      className="inline-flex items-center gap-2 text-sm"
    >
      <span className="text-zinc-500">{t('worker.rebuild.mode_label')}</span>
      {options.map((o) => {
        const checked = value === o.mode
        return (
          <button
            key={o.mode}
            type="button"
            role="radio"
            aria-checked={checked}
            data-testid={`rebuild-mode-${o.mode}`}
            disabled={!o.available}
            title={!o.available ? o.hint : undefined}
            onClick={() => onChange(o.mode)}
            className={`px-3 py-1 rounded border transition-colors ${
              checked
                ? 'border-zinc-400 bg-zinc-700 text-zinc-100'
                : 'border-zinc-700 text-zinc-400 hover:text-zinc-200'
            } disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:text-zinc-400`}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
