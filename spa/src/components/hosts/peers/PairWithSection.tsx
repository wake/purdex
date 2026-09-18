// spa/src/components/hosts/peers/PairWithSection.tsx — "Pair with…" (Phase D
// spec §7.1): one line per App host X has no entry for. Pair runs `pairHosts`
// (create on Y → create on X → push to Y; the repair path rotates Y's
// existing entry instead and finishes with dial → read → commit). A 409 on
// either create opens an inline alias field for that side and the retry
// keeps whatever the other side was already given.
//
// A candidate is not offered Pair when its entries could not be read
// (codex F4 — an unread Y may already hold an entry for X and belong on the
// repair path) or when its entry for X has a rotation pending (codex F3 —
// that rotation must be committed or cancelled first; the line offers the
// button by the ordinary row rule, "as of the peer's last dial", since X has
// no entry to dial Y with).
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { pairHosts } from '../../../lib/peer-pairing-actions'
import type { PairCandidate } from '../../../lib/peer-pairing-load'
import { actionApi, candidateKey, rowKey, type FlowResult, type FlowState, type RunFlow } from './flow'
import { FlowNote } from './FlowNote'
import { RotationControls } from './RotationControls'

interface Props {
  hostId: string
  self: { host_id: string; self_alias: string }
  /** `getDaemonBase(hostId)`: the address the counterpart is told to dial (spec §7.1's stated assumption). */
  xUrl: string
  candidates: PairCandidate[]
  busy: boolean
  flow: FlowState | null
  runFlow: RunFlow
  onChanged: () => void
}

export function PairWithSection({ hostId, self, xUrl, candidates, busy, flow, runFlow, onChanged }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="peers-pair" className="mt-6">
      <h3 className="text-sm font-semibold text-text-primary mb-2">{t('peers.pair_heading')}</h3>
      {candidates.length === 0 ? (
        <p data-testid="peers-no-candidates" className="text-xs text-text-muted">{t('peers.no_candidates')}</p>
      ) : (
        <div className="space-y-2">
          {candidates.map((c) => (
            <CandidateLine key={`${hostId}:${c.hostId}`} hostId={hostId} self={self} xUrl={xUrl} candidate={c}
              busy={busy} flow={flow} runFlow={runFlow} onChanged={onChanged} />
          ))}
        </div>
      )}
    </div>
  )
}

type Aliases = { onY?: string; onX?: string }

interface LineProps extends Omit<Props, 'candidates'> { candidate: PairCandidate }

function CandidateLine({ hostId, self, xUrl, candidate: c, busy, flow, runFlow, onChanged }: LineProps) {
  const t = useI18nStore((s) => s.t)
  // The alias prompt after a 409: which side asked, and the aliases the
  // failed attempt already carried (the other side's typed alias is kept).
  const [prompt, setPrompt] = useState<{ side: 'x' | 'y'; kept: Aliases } | null>(null)
  const [typed, setTyped] = useState('')

  const pendingReturn = !!c.returnEntry?.rotation_pending
  const blocked = c.listError !== '' || pendingReturn

  const pair = (aliases: Aliases) => void runFlow(candidateKey(c.hostId), async (report): Promise<FlowResult> => {
    const out = await pairHosts(
      { hostId, url: xUrl, selfAlias: self.self_alias },
      { hostId: c.hostId, url: c.url, returnEntry: c.returnEntry },
      aliases, actionApi(), report,
    )
    if (out.kind === 'alias-conflict') {
      setPrompt({ side: out.side, kept: aliases })
      setTyped('')
      return { error: out.error }
    }
    setPrompt(null)
    switch (out.kind) {
      case 'paired':
        return {}
      case 'return-failed':
        // Both entries exist: the row (one-way) owns the note and offers Retry return path.
        return { key: rowKey(out.aliasOnX), hint: t('peers.return_failed_hint'),
          error: t('peers.flow_error', { step: t('peers.step.push-to-y'), error: out.error }) }
      case 'repair-pending':
        // Y's rotation is still pending; the fresh row on the refresh offers Commit/Cancel by the rule.
        return { key: rowKey(out.aliasOnX),
          error: out.commitError ? t('peers.flow_error', { step: t('peers.step.commit'), error: out.commitError }) : '' }
      case 'step-failed': {
        const main = t('peers.flow_error', { step: t(`peers.step.${out.step}`), error: out.error })
        return { error: out.undoError ? `${main} ${t('peers.flow_undo_error', { error: out.undoError })}` : main }
      }
    }
  })

  const retryAliases = (): Aliases => {
    const alias = typed.trim()
    return prompt?.side === 'y' ? { ...prompt.kept, onY: alias } : { ...prompt?.kept, onX: alias }
  }

  return (
    <div data-testid={`peers-cand-${c.hostId}`} className="border border-border-subtle rounded-lg px-4 py-3 text-sm">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-semibold text-text-primary">{c.name}</span>
        <span className="font-mono text-xs text-text-muted">{c.url}</span>
        <button type="button" data-testid={`peers-pair-${c.hostId}`} disabled={busy || blocked} onClick={() => pair({})}
          className="ml-auto text-xs px-2 py-0.5 rounded bg-accent text-white cursor-pointer disabled:opacity-50 disabled:cursor-default">
          {flow?.running && flow.key === candidateKey(c.hostId) ? t('peers.pairing') : t('peers.pair')}
        </button>
      </div>
      {c.listError !== '' && (
        <p className="text-xs text-status-warning mt-1">{t('peers.pair_blocked_unread', { name: c.name, cause: c.listError })}</p>
      )}
      {c.returnEntry && !pendingReturn && (
        <p className="text-xs text-text-muted mt-1">{t('peers.pair_repair_note', { name: c.name })}</p>
      )}
      {c.returnEntry && pendingReturn && (
        <div className="flex items-center gap-2 flex-wrap mt-1">
          <span className="text-xs text-status-warning">{t('peers.pair_blocked_pending', { name: c.name })}</span>
          <RotationControls holder={{ hostId: c.hostId, alias: c.returnEntry.alias }} row={c.returnEntry}
            stale={false} evidenceDialled={false} push={null} label="rotate" testId={`peers-cand-${c.hostId}`}
            busy={busy} onDone={onChanged} runFlow={(fn) => runFlow(candidateKey(c.hostId), fn)} />
        </div>
      )}
      {prompt && (
        <div className="flex items-center gap-2 flex-wrap mt-2">
          <label htmlFor={`peers-alias-input-${c.hostId}`} className="text-xs text-text-secondary">
            {t(prompt.side === 'y' ? 'peers.alias_prompt_y' : 'peers.alias_prompt_x', { name: c.name })}
          </label>
          <input id={`peers-alias-input-${c.hostId}`} data-testid={`peers-alias-input-${c.hostId}`} type="text" value={typed}
            onChange={(e) => setTyped(e.target.value)} disabled={busy}
            className="text-xs px-2 py-0.5 rounded bg-surface-secondary border border-border-default text-text-primary font-mono w-40" />
          <button type="button" data-testid={`peers-alias-retry-${c.hostId}`} disabled={busy || blocked || typed.trim() === ''}
            onClick={() => pair(retryAliases())}
            className="text-xs px-2 py-0.5 rounded bg-accent text-white cursor-pointer disabled:opacity-50 disabled:cursor-default">
            {t('peers.alias_retry')}
          </button>
        </div>
      )}
      <FlowNote flow={flow} flowKey={candidateKey(c.hostId)} />
    </div>
  )
}
