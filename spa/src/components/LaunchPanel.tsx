// spa/src/components/LaunchPanel.tsx — what a live session with no agent shows in the deck and the chat (no-agent launch panel
// spec §3): the daemon's launch plan as rows, and two quick buttons. The daemon owns every rule; this renders its plan and
// posts the choice. Never switches to the terminal by itself, and shows nothing on success (the agent's hooks switch the view).
import { useEffect, useReducer, useState } from 'react'
import { latchKey, latchRelease, latchRemaining, latchSettled, latchStart, subscribeLatch } from '../lib/launch-latch'
import { LaunchError, postLaunch, previewLine, type LaunchKind, type LaunchPlan } from '../lib/launch-plan'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  hostId: string
  sessionCode: string
  /** The generation the pane is bound to; the plan's is used when the pane has none. */
  tmuxInstance: string
  /** From the sessions list, shown after the path. */
  branch?: string
  plan: LaunchPlan
  /** Read the plan again (button, and after a refusal that means the plan changed). */
  onRefresh: () => void
  onSwitchToTerminal?: () => void
}

type Failure = { code: string; status: string }

const btn = 'cursor-pointer rounded border border-border-subtle px-3 py-1 hover:bg-surface-secondary disabled:cursor-not-allowed disabled:opacity-50'

export function LaunchPanel({ hostId, sessionCode, tmuxInstance, branch, plan, onRefresh, onSwitchToTerminal }: Props) {
  const t = useI18nStore((s) => s.t)
  const [cd, setCd] = useState(true)
  // The cd choice belongs to the plan it was made for: adjust it while rendering (never in an effect, which would leave one
  // render holding the old value) whenever the plan's (generation, directory, state) triple changes.
  const planKey = `${plan.tmuxInstance}\0${plan.dir?.path ?? ''}\0${plan.dir?.state ?? ''}`
  const [seenKey, setSeenKey] = useState(planKey)
  let cdNow = cd
  if (seenKey !== planKey) {
    setSeenKey(planKey)
    setCd(true)
    cdNow = true
  }
  const [failure, setFailure] = useState<Failure | null>(null)
  const generation = tmuxInstance || plan.tmuxInstance
  // The launch latch is module-level (it survives a tab switch); re-render on any change and when its window ends.
  const key = latchKey(hostId, sessionCode, generation)
  const [tick, bump] = useReducer((n: number) => n + 1, 0)
  useEffect(() => subscribeLatch(bump), [])
  const remaining = latchRemaining(key)
  const sending = remaining > 0
  useEffect(() => {
    if (remaining <= 0 || remaining === Infinity) return
    const id = setTimeout(bump, remaining + 1)
    return () => clearTimeout(id)
  }, [key, remaining, tick])

  const move = plan.dir?.state === 'move'
  const busy = plan.busy !== null
  const resumeLine = 'line' in plan.resume ? plan.resume.line : null
  const resumeReason = resumeLine === null && 'reason' in plan.resume ? plan.resume.reason : null
  const freshLine = plan.fresh?.line ?? null
  const withCd = cdNow && move
  const noGeneration = generation === ''

  const launch = async (kind: LaunchKind) => {
    if (latchRemaining(key) > 0 || noGeneration) return
    latchStart(key)
    setFailure(null)
    try {
      await postLaunch(hostId, sessionCode, { kind, cd: withCd, expectedTmuxInstance: generation })
      latchSettled(key) // stays latched for the claim lifetime; the agent's hooks switch the view
    } catch (e) {
      // A LaunchError is the daemon's own refusal: nothing was typed, so free the buttons at once. Anything else (network,
      // aborted, ...) has an unknown outcome — the line may have been typed — so hold the latch for the claim lifetime.
      if (e instanceof LaunchError) latchRelease(key)
      else latchSettled(key)
      const code = e instanceof LaunchError ? e.code : 'unknown_outcome'
      setFailure({ code, status: e instanceof LaunchError ? String(e.status) : '' })
      if (code === 'busy' || code === 'agent_running' || code === 'unknown_outcome') onRefresh()
    }
  }

  const errorText = (f: Failure): string => {
    switch (f.code) {
      case 'generation_conflict': return t('launch.err.generation_conflict')
      case 'busy': return plan.busy ? t('launch.err.busy', { command: plan.busy.command }) : t('launch.err.busy_unknown')
      case 'already_launching': return t('launch.err.already_launching')
      case 'not_found': return t('launch.err.not_found')
      case 'unknown_outcome': return t('launch.err.unknown_outcome')
      case 'agent_running': return ''
      default: return t('launch.err.other', { status: f.status })
    }
  }
  const shownError = failure ? errorText(failure) : ''

  return (
    <div data-testid="launch-panel" className="flex h-full flex-col items-center justify-center gap-3 p-6 text-sm text-text-secondary">
      <p className="text-text-primary">{t('launch.title')}</p>
      <label className="flex items-center gap-2 opacity-70">
        <input data-testid="launch-in-tmux" type="checkbox" checked disabled readOnly />
        <span>{t('launch.in_tmux')}</span>
      </label>
      {plan.dir && (
        <label data-testid="launch-cd-row" className="flex max-w-full items-center gap-2">
          <input
            data-testid="launch-cd"
            type="checkbox"
            checked={withCd}
            disabled={!move || sending}
            onChange={(e) => setCd(e.target.checked)}
          />
          <span data-testid="launch-dir" className="min-w-0 truncate">
            {move ? t('launch.cd') : t('launch.here')}
            <span className="ml-2 font-mono text-xs text-text-muted">{plan.dir.path}</span>
            {branch && <span className="ml-1 text-xs text-text-muted">({branch})</span>}
          </span>
        </label>
      )}
      <div className="flex gap-2">
        <button
          type="button"
          data-testid="launch-resume"
          title={previewLine(resumeLine, plan.dir, withCd)}
          disabled={resumeLine === null || busy || sending || noGeneration}
          onClick={() => void launch('resume')}
          className={btn}
        >
          {t('launch.resume')}
        </button>
        <button
          type="button"
          data-testid="launch-fresh"
          title={previewLine(freshLine, plan.dir, withCd)}
          disabled={freshLine === null || busy || sending || noGeneration}
          onClick={() => void launch('fresh')}
          className={btn}
        >
          {t('launch.fresh')}
        </button>
      </div>
      {resumeReason && !busy && (
        <p data-testid="launch-resume-hint" className="text-xs text-text-muted">{t(`launch.resume.${resumeReason}`)}</p>
      )}
      {noGeneration && (
        <p data-testid="launch-no-generation" className="text-xs text-text-muted">{t('launch.no_generation')}</p>
      )}
      {plan.busy && (
        <p data-testid="launch-busy" className="text-xs text-text-primary">{t('launch.busy', { command: plan.busy.command })}</p>
      )}
      {shownError && (
        <p data-testid="launch-error" data-code={failure?.code} role="alert" className="text-xs text-red-400">{shownError}</p>
      )}
      <div className="flex items-center gap-3">
        {onSwitchToTerminal && (
          <button
            type="button"
            data-testid="launch-to-terminal"
            data-emphasis={busy ? 'true' : 'false'}
            onClick={onSwitchToTerminal}
            className={`${btn} ${busy ? 'border-text-primary font-semibold text-text-primary' : ''}`}
          >
            {t('launch.to_terminal')}
          </button>
        )}
        <button type="button" data-testid="launch-refresh" onClick={onRefresh} className={btn}>{t('launch.refresh')}</button>
      </div>
    </div>
  )
}
