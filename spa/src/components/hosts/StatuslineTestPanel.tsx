import { useEffect, useRef, useState } from 'react'
import { CheckCircle, XCircle, Minus, CircleNotch } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useStatuslineTest, type StageState } from '../../hooks/useStatuslineTest'

interface Props {
  hostId: string
  autoRun?: boolean
}

const STAGE_KEYS: Array<[1 | 2 | 3 | 4 | 5, string]> = [
  [1, 'hosts.extensions.test.stage1'],
  [2, 'hosts.extensions.test.stage2'],
  [3, 'hosts.extensions.test.stage3'],
  [4, 'hosts.extensions.test.stage4'],
  [5, 'hosts.extensions.test.stage5'],
]

export function StatuslineTestPanel({ hostId, autoRun = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const { state, run } = useStatuslineTest(hostId)
  const [showLog, setShowLog] = useState(false)
  const autoRanRef = useRef(false)

  useEffect(() => {
    if (autoRun && !autoRanRef.current) {
      autoRanRef.current = true
      void run()
    }
  }, [autoRun, run])

  const lastFailure = (Object.entries(state.stages) as Array<[string, StageState]>).find(
    ([, v]) => v.status === 'failed',
  )

  return (
    <div className="pl-4 pr-2 py-2 text-xs border-l border-border/40">
      <div className="flex items-center justify-between mb-2">
        <span className="font-medium">{t('hosts.extensions.test.heading')}</span>
        <button
          onClick={() => void run()}
          disabled={state.running}
          className="px-2 py-1 rounded text-xs bg-surface-muted border border-border cursor-pointer disabled:opacity-50"
        >
          {state.running ? t('hosts.extensions.test.running') : t('hosts.extensions.test.run_again')}
        </button>
      </div>
      <ul className="space-y-1">
        {STAGE_KEYS.map(([n, key]) => {
          const s = state.stages[n]
          return (
            <li key={n} className="flex items-center gap-2">
              <StageIcon state={s} />
              <span className="flex-1">{t(key)}</span>
              {s.status === 'passed' && s.elapsedMs != null && (
                <span className="text-text-muted">{s.elapsedMs}{t('hosts.extensions.test.passed_suffix')}</span>
              )}
            </li>
          )
        })}
      </ul>
      {lastFailure && (
        <div className="mt-2">
          <button
            onClick={() => setShowLog((v) => !v)}
            className="text-xs text-accent cursor-pointer"
          >
            {showLog ? t('hosts.extensions.test.hide_log') : t('hosts.extensions.test.show_log')}
          </button>
          {showLog && (
            <pre className="mt-1 p-2 rounded bg-surface-muted text-text-muted whitespace-pre-wrap">
              stage {lastFailure[0]}: {lastFailure[1].error ?? 'unknown error'}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

function StageIcon({ state }: { state: StageState }) {
  switch (state.status) {
    case 'passed':
      return <CheckCircle size={13} weight="fill" className="text-green-400" aria-hidden="true" />
    case 'failed':
      return <XCircle size={13} weight="fill" className="text-red-400" aria-hidden="true" />
    case 'running':
      return <CircleNotch size={13} className="text-accent animate-spin" aria-hidden="true" />
    case 'skipped':
    case 'untested':
    default:
      return <Minus size={13} className="text-text-muted" aria-hidden="true" />
  }
}
