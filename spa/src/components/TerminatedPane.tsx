import { useEffect, useRef, useState } from 'react'
import { SmileySad } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useI18nStore } from '../stores/useI18nStore'
import { closeTab } from '../lib/tab-lifecycle'
import { rebuildPane, paneOwner, type RebuildPlan } from '../lib/rebuild/engine'
import { useRebuildStore, withOperationLock } from '../stores/useRebuildStore'
import { useNexHostStore, selectHandoffReady } from '../stores/useNexHostStore'
import { rebuildAsWorker, rebuildErrorMessage, announceRebuildOutcome } from '../lib/nex/worker-rebuild'
import { RebuildScreen } from './RebuildScreen'
import { RebuildModeChoice, type RebuildMode } from './RebuildModeChoice'
import { RebuildActionSet, type RebuildEditableField } from './RebuildActionSet'
import { SessionPickerList, type SessionSelection } from './SessionPickerList'
import type { PaneContent, PaneRebuildRecord, TerminatedReason } from '../types/tab'

interface Props {
  content: Extract<PaneContent, { kind: 'tmux-session' }>
  tabId: string
  paneId: string
}

const REASON_KEYS: Record<TerminatedReason, { title: string; desc: string }> = {
  'session-closed': { title: 'terminated.session_closed', desc: 'terminated.session_closed_desc' },
  'tmux-restarted': { title: 'terminated.tmux_restarted', desc: 'terminated.tmux_restarted_desc' },
  'host-removed': { title: 'terminated.host_removed', desc: 'terminated.host_removed_desc' },
}

export function TerminatedPane({ content, tabId, paneId }: Props) {
  const t = useI18nStore((s) => s.t)
  const setPaneContent = useTabStore((s) => s.setPaneContent)
  const setPaneRebuildForPane = useTabStore((s) => s.setPaneRebuildForPane)
  const reason = content.terminated!
  const keys = REASON_KEYS[reason]

  // Re-pointing drops `terminated` and takes the generation the picker read off
  // the selected session's own payload (spec §4.5) — so the freshly attached
  // pane is not immediately marked dead by the next reconciliation.
  const handleSelect = (sel: SessionSelection) => {
    setPaneContent(tabId, paneId, {
      kind: 'tmux-session',
      hostId: sel.hostId,
      sessionCode: sel.sessionCode,
      mode: 'terminal',
      cachedName: sel.cachedName,
      tmuxInstance: sel.tmuxInstance,
    })
  }

  // A pane that never accumulated a record still has a name and a generation:
  // the same shape `applyRebuildPatch` seeds a first write with.
  const record: PaneRebuildRecord = content.rebuild ?? {
    sessionName: content.cachedName,
    tmuxInstance: content.tmuxInstance,
    capturedAt: 0,
  }

  // Worker rebuild (conversation entity spec Q4 / D12): offered only when Nexen is
  // ready and the record knows both the cc session and its cwd. Terminal stays preselected.
  const handoffReady = useNexHostStore(selectHandoffReady(content.hostId))
  useEffect(() => {
    // A failed check leaves handoffReady false: the terminal screen, as without Nexen.
    useNexHostStore.getState().ensure(content.hostId).catch(() => {})
  }, [content.hostId])
  const sid = record.agent?.type === 'cc' ? record.agent.sessionId : undefined
  const cwd = record.cwd
  const showChoice = handoffReady && !!sid && !!cwd
  const [choice, setChoice] = useState<RebuildMode>('terminal')
  const mode: RebuildMode = showChoice ? choice : 'terminal'
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const inFlight = useRef(false)

  // Any held operation lock (a terminal rebuild of this pane, or a batch) freezes the choice.
  const locked = useRebuildStore((s) => s.lockedBy !== null)
  // A rebuild error belongs to the mode it was tried in.
  const changeMode = (m: RebuildMode) => {
    if (m !== mode) setError(null)
    setChoice(m)
  }

  const rebuildWorker = async () => {
    if (inFlight.current || !sid || !cwd) return
    inFlight.current = true
    setBusy(true)
    setError(null)
    try {
      // The same pane-scoped lock `rebuildPane` takes: a terminal and a worker
      // rebuild of one pane never run together. Held → refused, no request.
      await withOperationLock(
        paneOwner(paneId),
        async () => {
          const outcome = await rebuildAsWorker({
            hostId: content.hostId, sessionId: sid, cwd, tabId, paneId,
            expect: (c) => c.kind === 'tmux-session' && c.hostId === content.hostId && c.sessionCode === content.sessionCode,
          })
          announceRebuildOutcome(t, content.hostId, outcome)
        },
        () => undefined,
      )
    } catch (err) {
      setError(rebuildErrorMessage(err, t))
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const handleRebuild = (plan: RebuildPlan) => {
    void rebuildPane(content.hostId, tabId, paneId, plan)
  }

  // Pane-scoped, never the session-scoped writer: an edit made here must not
  // rewrite the record of a split sibling bound to the same session (§4.10).
  const handleEdit = (field: RebuildEditableField, value: string) => {
    setPaneRebuildForPane(
      tabId,
      paneId,
      { hostId: content.hostId, sessionCode: content.sessionCode, tmuxInstance: content.tmuxInstance },
      { kind: 'field', field, value },
    )
  }

  return (
    <RebuildScreen
      icon={<SmileySad size={48} className="text-zinc-500 mb-4" />}
      title={t(keys.title)}
      description={t(keys.desc, { name: content.cachedName })}
      closeLabel={t('terminated.close_tab')}
      onClose={() => {
        closeTab(tabId)
      }}
    >
      {showChoice && (
        <div className="mb-6">
          <RebuildModeChoice value={mode} onChange={changeMode} terminalAvailable workerAvailable disabled={locked || busy} />
        </div>
      )}
      {mode === 'worker' ? (
        <div className="flex flex-col items-center gap-3 w-full max-w-lg">
          <p className="text-sm text-zinc-400 font-mono break-all">{cwd}</p>
          <button
            type="button"
            data-testid="terminated-rebuild-worker"
            disabled={busy || locked}
            onClick={() => { void rebuildWorker() }}
            className="px-4 py-1.5 text-sm rounded bg-zinc-700 text-zinc-100 hover:bg-zinc-600 disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {t('worker.rebuild.button')}
          </button>
          {error && <p data-testid="terminated-rebuild-error" role="alert" className="text-sm text-red-400">{error}</p>}
        </div>
      ) : (<>
      <div className="w-full max-w-lg mb-8">
        <RebuildActionSet
          tabId={tabId}
          paneId={paneId}
          record={record}
          terminated={reason}
          binding={{ hostId: content.hostId, sessionCode: content.sessionCode, tmuxInstance: content.tmuxInstance }}
          onRebuild={handleRebuild}
          onEdit={handleEdit}
        />
      </div>
      <div className="w-full max-w-sm">
        <SessionPickerList onSelect={handleSelect} />
      </div>
      </>)}
    </RebuildScreen>
  )
}
