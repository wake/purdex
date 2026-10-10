// spa/src/components/status/WorkerStatusBar.tsx — the status bar for an `execution` (worker) target pane (shell
// cleanup spec §9.2): the host segment the tmux bar uses, the worker name, its cwd, then the mode buttons (§9.3) in the
// controls block. The peer id appears only when the summary carries an address. No refresh / upload segments: those are tmux-specific.
import { useHostStore } from '../../stores/useHostStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { selectSessionTitleSupported, useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { liveWorkerSummary } from '../../lib/nex/worker-summary'
import { workerTabTitle } from '../../lib/nex/worker-tab-title'
import type { ExecutionContent } from '../../types/tab'
import { CopySegment, HostSegment, Separator, StatusBarLayout } from './StatusSegments'
import { PaneModeButtons } from './PaneModeButtons'
import { HostQuotaSegments } from './UsageSegments'
import { useCopyFeedback } from './useCopyFeedback'

export function WorkerStatusBar({ tabId, pane, onNavigateToHost }: {
  tabId: string
  /** The status target: a worker pane. */
  pane: { id: string; content: ExecutionContent }
  onNavigateToHost?: (hostId: string) => void
}) {
  const { content } = pane
  const t = useI18nStore((s) => s.t)
  // The host resolves like the worker pane's own (`resolveExecutionHostId`: the hint, else the first host), but
  // through a selector so a host list that loads after mount still lands here.
  const hostId = useHostStore((s) => content.host || s.hostOrder[0] || '')
  const summary = useExecutionStore((s) => liveWorkerSummary(s.executions, hostId, content.executionId))
  const titleSupported = useNexHostStore(selectSessionTitleSupported(hostId))
  const { feedback, failed, copy } = useCopyFeedback()

  // The same primary as the worker tab's title (session title → handed-off terminal title → first line of the
  // brief), without the tab's cwd suffix: the cwd has its own segment here.
  const name = workerTabTitle({
    sessionTitle: titleSupported ? summary?.session_title?.text : undefined,
    fromTitle: content.fromTitle,
    brief: summary?.brief,
  }) ?? t('page.pane.execution')
  const cwd = summary?.cwd ?? ''
  // Rendered only when the daemon supplies the worker's address; nothing is derived or guessed here.
  const peerAddress = summary?.peer_address ?? ''

  return (
    <StatusBarLayout
      copyFeedback={feedback}
      copyFailed={failed}
      segments={(
        <>
          <HostSegment hostId={hostId} onCopy={copy} onNavigateToHost={onNavigateToHost} />
          <Separator />
          <span
            data-testid="status-seg-worker-name"
            className="min-w-0 max-w-[32ch] truncate text-text-secondary"
            title={name}
          >
            {name}
          </span>
          <Separator className="max-[600px]:hidden" />
          <CopySegment
            testId="status-seg-cwd"
            display={cwd || '—'}
            value={cwd}
            what={t('peer.label.cwd')}
            title={cwd || t('peer.copy_hint')}
            rtl
            className="max-w-[32ch] max-[600px]:hidden"
            onCopy={copy}
          />
          {peerAddress && (
            <>
              <Separator className="max-[700px]:hidden" />
              <CopySegment
                testId="status-seg-peer-id"
                display={peerAddress.slice(peerAddress.indexOf('/') + 1)}
                value={peerAddress}
                what={t('peer.label.peer_id')}
                title={t('peer.copy_hint')}
                className="max-w-[24ch] max-[700px]:hidden"
                onCopy={copy}
              />
            </>
          )}
        </>
      )}
      controls={(
        <>
          {/* The host's 5h / weekly quota. Per-worker context % waits on Nexen exposing it. */}
          <HostQuotaSegments hostId={hostId} />
          <PaneModeButtons tabId={tabId} pane={pane} />
        </>
      )}
    />
  )
}
