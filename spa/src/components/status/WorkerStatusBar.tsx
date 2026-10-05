// spa/src/components/status/WorkerStatusBar.tsx — the status bar for an `execution` (worker) target pane (shell
// cleanup spec §9.2): the host segment the tmux bar uses, the worker name, its cwd, then the controls block. No
// refresh / peer-id / upload segments: those are tmux-specific.
import { useHostStore } from '../../stores/useHostStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { selectSessionTitleSupported, useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { liveWorkerSummary } from '../../lib/nex/worker-summary'
import { workerTabTitle } from '../../lib/nex/worker-tab-title'
import type { ExecutionContent } from '../../types/tab'
import { CopySegment, HostSegment, Separator, StatusBarLayout } from './StatusSegments'
import { useCopyFeedback } from './useCopyFeedback'

export function WorkerStatusBar({ content, onNavigateToHost }: {
  content: ExecutionContent
  onNavigateToHost?: (hostId: string) => void
}) {
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
        </>
      )}
      // The mode buttons (spec §9.3) go in the controls block; until then it stays empty, but it is still the one
      // `ml-auto` container of the row.
    />
  )
}
