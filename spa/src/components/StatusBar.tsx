import { useCallback, useEffect } from 'react'
import { CircleNotch, CheckCircle, XCircle, LockSimple, ArrowsClockwise } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { useStatusTargetPane } from '../hooks/useStatusTargetPane'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useIsRefShown } from '../lib/shown-hosts'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useUploadStore } from '../stores/useUploadStore'
import { compositeKey } from '../lib/composite-key'
import { useI18nStore } from '../stores/useI18nStore'
import { usePeerInfo, type PeerInfo } from '../hooks/usePeerInfo'
import type { PeerRow } from '../stores/usePeerStore'
import { reasonText } from '../lib/peer-display'
import { CopySegment, HostSegment, Separator, StatusBarLayout } from './status/StatusSegments'
import { CcUsageSegments } from './status/UsageSegments'
import { WorkerStatusBar } from './status/WorkerStatusBar'
import { PaneModeButtons } from './status/PaneModeButtons'
import { useCopyFeedback } from './status/useCopyFeedback'
import { keepFocus } from '../lib/keep-focus'

type T = (key: string, params?: Record<string, string | number>) => string

/** The peer id's tooltip: every §6 state says here why it reads as it does. */
function peerIdTitle(peer: PeerInfo, t: T): string {
  if (!peer.connected) return t('peer.host_not_connected')
  if (peer.error) return t('peer.error', { error: peer.error })
  const { row } = peer
  // Not `row.title === ''`, which is what this guard used to read. A title is
  // free text that routes nothing and under v4 is usually unset, so keying the
  // whole segment off it hid rows that were perfectly addressable. A row is
  // absent when it has neither a ref nor an address — nothing to say and
  // nothing to copy.
  if (!row || (row.ref === '' && row.address === '')) {
    // No row from a partial answer is "undetermined", never "no peer": the row
    // may simply not have been resolved inside the daemon's 2 s budget.
    return peer.envelope.partial ? t('peer.undetermined') : t('peer.none')
  }
  const parts = [row.address]
  if (row.reason) parts.push(reasonText(row.reason, t))
  if (peer.envelope.titlesUnavailable) parts.push(t('peer.titles_unavailable_note'))
  if (peer.stale) parts.push(t('peer.stale', { seconds: Math.round((Date.now() - peer.fetchedAt) / 1000) }))
  return parts.join(' — ')
}

/**
 * The peer segment's two strings: what the row shows, and what a click copies.
 *
 * Display drops the host; the clipboard keeps it, and keeps the ref. What
 * gets copied is what gets pasted into a handoff and used hours later —
 * exactly the window in which a name drifts or is taken by someone else. The
 * prettier string is the weaker one; it does not belong on the clipboard.
 */
function peerIdText(row: PeerRow | null): { display: string; value: string } {
  const address = row?.address ?? ''
  if (address === '') return { display: '', value: '' }
  // The host is the address's first segment; the name is everything after it.
  const slash = address.indexOf('/')
  const name = slash === -1 ? address : address.slice(slash + 1)
  const ref = row?.ref ?? ''
  // A session whose registry name is not routable is addressed by its ref, so
  // the address already ends in it and there is no name to bracket: appending
  // would render `_q34psn [q34psn]`, which says the same thing twice.
  if (ref === '' || name === ref) return { display: name, value: address }
  const bracketed = ` [${ref.replace(/^_/, '')}]`
  return { display: name + bracketed, value: address + bracketed }
}

interface Props {
  activeTab: Tab | null
  onNavigateToHost?: (hostId: string) => void
  onStartRename?: (tab: Tab, anchor: Element | null) => void
}


function UploadStatus({ hostId, sessionCode, t }: { hostId: string | null; sessionCode: string | null; t: (key: string, params?: Record<string, string | number>) => string }) {
  const ck = hostId && sessionCode ? compositeKey(hostId, sessionCode) : null
  const uploadState = useUploadStore((s) => ck ? s.sessions[ck] : undefined)
  const setDone = useUploadStore((s) => s.setDone)
  const dismiss = useUploadStore((s) => s.dismiss)
  const uploadStatus = uploadState?.status

  // Auto-transition "typing" → "done" after 1.5 seconds
  useEffect(() => {
    if (uploadStatus !== 'typing' || !hostId || !sessionCode) return
    const timer = setTimeout(() => setDone(hostId, sessionCode), 1500)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, setDone])

  // Auto-dismiss "done" after 3 seconds
  useEffect(() => {
    if (uploadStatus !== 'done' || !hostId || !sessionCode) return
    const timer = setTimeout(() => dismiss(hostId, sessionCode), 3000)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, dismiss])

  // Auto-dismiss "error" after 30 seconds
  useEffect(() => {
    if (uploadStatus !== 'error' || !hostId || !sessionCode) return
    const timer = setTimeout(() => dismiss(hostId, sessionCode), 30000)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, dismiss])

  if (!uploadState || !hostId || !sessionCode) return null

  if (uploadState.status === 'uploading') {
    return (
      <span className="flex items-center gap-1 text-yellow-400" data-testid="upload-status">
        <CircleNotch size={12} className="animate-spin" />
        <span>{t('upload.uploading', { file: uploadState.currentFile, current: uploadState.completed + 1, total: uploadState.total })}</span>
      </span>
    )
  }

  if (uploadState.status === 'typing') {
    return (
      <span className="flex items-center gap-1 text-blue-400" data-testid="upload-status">
        <CircleNotch size={12} className="animate-spin" />
        <span>{t('upload.typing')}</span>
      </span>
    )
  }

  if (uploadState.status === 'done') {
    const key = uploadState.total === 1 ? 'upload.done_one' : 'upload.done_many'
    return (
      <span className="flex items-center gap-1 text-green-400" data-testid="upload-status">
        <CheckCircle size={12} />
        <span>{t(key, { count: uploadState.total })}</span>
      </span>
    )
  }

  if (uploadState.status === 'error') {
    const message = uploadState.completed > 0
      ? t('upload.partial', { uploaded: uploadState.completed, failed: uploadState.failed })
      : t('upload.failed', { file: uploadState.error ?? '' })
    return (
      <span
        className="flex items-center gap-1 text-red-400 cursor-pointer"
        data-testid="upload-status"
        onClick={() => dismiss(hostId!, sessionCode!)}
      >
        <XCircle size={12} />
        <span>{message}</span>
      </span>
    )
  }

  return null
}

export function StatusBar({ activeTab, onNavigateToHost, onStartRename }: Props) {
  const t = useI18nStore((s) => s.t)

  // The whole bar shows one pane of the tab, the status target (spec §9.1, rule D.4), never simply the primary pane:
  // clicking a plain terminal or an editor beside an agent pane does not move it. Everything below reads this pane.
  // (Hooks must be called unconditionally, so the derivations tolerate a null tab.)
  const target = useStatusTargetPane(activeTab)
  const targetContent = target?.content ?? null
  const agentHostId = targetContent && targetContent.kind === 'tmux-session' ? targetContent.hostId : null
  const agentSessionCode = targetContent && 'sessionCode' in targetContent ? targetContent.sessionCode : null
  const agentCk = agentHostId && agentSessionCode ? compositeKey(agentHostId, agentSessionCode) : null

  const session = useSessionStore((s) =>
    agentHostId && agentSessionCode
      ? (s.sessions[agentHostId] ?? []).find((sess) => sess.code === agentSessionCode) ?? null
      : null,
  )
  const hostRuntime = useHostStore((s) => agentHostId ? s.runtime[agentHostId] : null)
  const agentLabel = useAgentStore((s) => agentCk ? s.models[agentCk] ?? null : null)
  const agentType = useAgentStore((s) => agentCk ? s.agentTypes[agentCk] ?? null : null)
  const showAgentTitleInStatusBar = useUISettingsStore((s) => s.showAgentTitleInStatusBar)

  // Peer data for the target pane. The hook owns *when* anything is fetched
  // (spec §3.3); passing nulls — an editor tab, a dashboard, no tab at all —
  // is how this component says "nothing here needs peer data".
  // A terminated pane has no peer (spec §6), and its session code may already
  // have been handed to a different session by a tmux restart — so asking for
  // it would not merely be pointless, it could render and copy a stranger's
  // address. Nulls here are how this component declines to ask.
  //
  // The same restart threatens a *live* pane too, from the other side: a peers
  // answer cached before it carries another session's address under this code.
  // The third argument is this pane's tmux generation, read from the session the
  // daemon most recently described; the hook returns a row only when the two
  // agree. `undefined` — a session not reconciled yet — is unknown, not a match.
  //
  // A pane on a host hidden in this workbench (host ownership H2d-4, §0.21) renders a placeholder and opens no
  // connection; the bar declines to ask about it in the same way, live.
  const targetTerminated = !!(targetContent && targetContent.kind === 'tmux-session' && targetContent.terminated)
  const targetHostShown = useIsRefShown(agentHostId)
  const noPeer = targetTerminated || !targetHostShown
  const peer = usePeerInfo(
    noPeer ? null : agentHostId,
    noPeer ? null : agentSessionCode,
    session?.tmux_instance ?? '',
  )

  // One confirmation for five copy buttons, in a fixed slot, so a copy never
  // reflows the row (spec §4.2).
  const { feedback: copyFeedback, failed: copyFailed, copy: handleCopy } = useCopyFeedback()

  const handleNameDoubleClick = useCallback((e: React.MouseEvent<HTMLSpanElement>) => {
    if (!activeTab || !onStartRename) return
    onStartRename(activeTab, e.currentTarget)
  }, [activeTab, onStartRename])

  if (!activeTab || !target) {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        {t('status.no_active')}
      </div>
    )
  }

  const { content } = target

  if (content.kind === 'editor') {
    return null
  }

  if (content.kind === 'execution') {
    return <WorkerStatusBar tabId={activeTab.id} pane={{ id: target.id, content }} onNavigateToHost={onNavigateToHost} />
  }

  if (content.kind !== 'tmux-session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  // Session pane — show host, session name, status
  // A conversation's rebuild tab (conversation entity spec §13.4) has no session code: it is named by the session it
  // would create, its cached name. Every pane with a code shows what it showed before.
  const sessionName = session?.name ?? (content.sessionCode || content.cachedName)
  const paneTitle = showAgentTitleInStatusBar && agentType && !content.terminated ? session?.pane_title : null
  const status = hostRuntime?.status ?? 'disconnected'

  // The peer id shows the name with its ref and copies the full address with
  // its ref (`peerIdText`): a name alone is not addressable without its host,
  // and someone copying "the peer id" means to paste something `pdx msg send`
  // accepts.
  //
  // A failed refresh shows nothing at all (spec §6): the cache still holds the
  // last answer, but that is precisely the answer the daemon has just failed to
  // confirm, and these segments are click-to-copy — a stale address pasted into
  // `pdx msg send` reaches the wrong agent. The error stays in the tooltip, and
  // the refresh control is right there. (Being *not connected* is different: the
  // status segment already says so, and §6 keeps the last rows, dimmed.)
  const peerRow = peer.error ? null : peer.row
  const peerId = peerIdText(peerRow)
  // The title sits beside the name, not in place of it: it is a human's note
  // about the conversation, and losing the name would lose the thing that both
  // identifies the row and matches what the clipboard carries.
  const peerTitle = peerRow?.title ?? ''
  const peerIdDisplay = peerId.display && peerTitle ? `${peerId.display} · ${peerTitle}` : peerId.display
  const peerUncertain = peerRow?.reason === 'inbox_dead' || peerRow?.reason === 'ambiguous'
  const peerDim = !peer.connected || peer.stale || peerUncertain
  const peerName = peerRow?.agent?.peerName ?? ''

  return (
    <StatusBarLayout
      copyFeedback={copyFeedback}
      copyFailed={copyFailed}
      segments={<>
        <HostSegment hostId={agentHostId} onCopy={handleCopy} onNavigateToHost={onNavigateToHost} />
        <Separator />
        <span
          data-testid="status-seg-session-name"
          className="min-w-0 max-w-[20ch] truncate text-text-secondary select-none"
          title={t('status.rename_hint')}
          onDoubleClick={handleNameDoubleClick}
        >
          {sessionName}
        </span>
        <Separator className="max-[600px]:hidden" />
        <CopySegment
          testId="status-seg-cwd"
          display={peer.cwd || '\u2014'}
          value={peer.cwd}
          what={t('peer.label.cwd')}
          title={peer.cwdError ? t('peer.error', { error: peer.cwdError }) : (peer.cwd || t('peer.copy_hint'))}
          rtl
          className="max-w-[32ch] max-[600px]:hidden"
          onCopy={handleCopy}
        />
        <Separator className="max-[700px]:hidden" />
        <CopySegment
          testId="status-seg-agent"
          display={peerName || '\u2014'}
          value={peerName}
          what={t('peer.label.agent')}
          title={peerName ? t('peer.copy_hint') : peerIdTitle(peer, t)}
          dim={peerDim}
          className="max-w-[20ch] max-[700px]:hidden"
          onCopy={handleCopy}
        />
        <Separator />
        <CopySegment
          testId="status-seg-peer-id"
          display={peerIdDisplay || '\u2014'}
          value={peerId.value}
          what={t('peer.label.peer_id')}
          title={peerIdTitle(peer, t)}
          dim={peerDim}
          className="max-w-[24ch]"
          onCopy={handleCopy}
        />
        {/* The only control that starts a fetch. Clicking a segment always
            copies, however stale it is (spec \u00a73.4). */}
        <button
          type="button"
          data-testid="status-peer-refresh"
          title={peerDim ? peerIdTitle(peer, t) : t('peer.refresh')}
          aria-label={t('peer.refresh')}
          data-stale={peerDim ? 'true' : undefined}
          disabled={!peer.connected || peer.loading}
          // A mouse press leaves focus on the pane (shell polish spec §4).
          onMouseDown={keepFocus}
          onClick={() => peer.refresh()}
          // This icon carries the uncertainty the text used to carry: amber
          // when the answer beside it may no longer hold, neutral otherwise.
          // It sits directly after the peer id, it is the thing that fixes the
          // condition it reports, and colouring it costs nothing to read.
          className={`ml-1.5 flex shrink-0 items-center rounded p-0.5 transition-colors hover:bg-surface-hover disabled:opacity-40 cursor-pointer disabled:cursor-default ${peerDim ? 'text-status-warning' : 'text-text-muted'}`}
        >
          <ArrowsClockwise size={10} className={peer.loading ? 'animate-spin' : ''} />
        </button>
        <Separator />
        <span
          data-testid="status-seg-status"
          className={`shrink-0 ${
            status === 'auth-error' ? 'text-red-400 cursor-pointer flex items-center gap-1'
              : status === 'connected' && hostRuntime?.tmuxState === 'unavailable' ? 'text-yellow-400'
              : status === 'connected' ? 'text-green-500'
              : status === 'reconnecting' ? 'text-yellow-400'
              : 'text-red-400'
          }`}
          onClick={status === 'auth-error' && agentHostId ? () => onNavigateToHost?.(agentHostId) : undefined}
        >
          {status === 'auth-error' && <LockSimple size={10} weight="fill" />}
          {status === 'auth-error' ? t('hosts.auth_error')
            : status === 'connected' && hostRuntime?.tmuxState === 'unavailable'
              ? t('hosts.error_tmux_down')
              : status}
        </span>
        {/* Context window and 5h / weekly limits from the agent's latest statusLine snapshot. */}
        <CcUsageSegments hostId={agentHostId} sessionCode={agentSessionCode} />
      </>}
      controls={<>
        {/* The model badge. It sits in the `shrink-0` controls group, so
            without a rule of its own a long model name ("Claude Opus 4") takes
            its full width off the segments on the left rather than yielding.
            Spec §4.3 puts it with the other agent-identity decorations — the
            peer name and the pane title — truncating first and dropping below
            700 px, above which the row still has room for all three. */}
        {agentLabel && (
          <span
            className="inline-block max-w-[16ch] truncate px-[7px] rounded-[3px] border text-[10px] leading-4 bg-[rgba(154,96,56,0.15)] text-[#e8956a] border-[rgba(180,110,65,0.3)] max-[700px]:hidden"
            data-testid="agent-label"
            title={agentLabel}
          >
            {agentLabel}
          </span>
        )}
        <UploadStatus hostId={agentHostId} sessionCode={agentSessionCode} t={t} />
        {paneTitle && (
          <span
            data-testid="agent-pane-title"
            className="max-w-[40ch] truncate text-text-muted max-[700px]:hidden"
            title={paneTitle}
          >
            {paneTitle}
          </span>
        )}
        {/* Where the split buttons were (spec D.3, §9.6; splitting stays in the title bar and the pane menu). */}
        <PaneModeButtons tabId={activeTab.id} pane={target} />
      </>}
    />
  )
}
