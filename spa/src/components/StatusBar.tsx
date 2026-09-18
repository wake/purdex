import { useState, useRef, useCallback, useEffect } from 'react'
import { CircleNotch, CheckCircle, XCircle, LockSimple, Columns, Rows, ArrowsClockwise } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useUploadStore } from '../stores/useUploadStore'
import { compositeKey } from '../lib/composite-key'
import { useI18nStore } from '../stores/useI18nStore'
import { usePeerInfo, type PeerInfo } from '../hooks/usePeerInfo'
import type { PeerRow } from '../stores/usePeerStore'
import { copyText } from '../lib/copy-text'
import { reasonText } from '../lib/peer-display'

type T = (key: string, params?: Record<string, string | number>) => string

/** How long a copy confirmation stays in the fixed slot. */
const COPY_FEEDBACK_MS = 1500

/**
 * How long a segment that also has a double-click gesture waits before copying.
 *
 * A browser dispatches two `click`s *before* `dblclick`, so an element carrying
 * both gestures runs the single-click action twice on the way to the double one.
 * The host segment carries both — click copies the host name, double-click opens
 * host settings, which it did before this feature existed — and the collision is
 * silent: the user navigates and finds the clipboard overwritten. So the copy is
 * deferred by one double-click window and cancelled if `dblclick` arrives.
 *
 * The alternative was to separate the gestures, which means taking one of them
 * off the segment: the double-click is muscle memory that predates this feature,
 * and the single click is what §4.2 promises for every segment. A quarter-second
 * on a clipboard write whose confirmation lands in a fixed slot anyway is the
 * cheaper side of that trade; the delay applies only to segments that have a
 * second gesture, which today is the host alone.
 */
const DOUBLE_CLICK_GRACE_MS = 250

/**
 * A rule between two segments.
 *
 * A `|` glyph would be selected and copied along with the text the user is
 * trying to grab, so the separator carries no text at all (spec §4.1). It
 * takes the drop class of the segment it introduces, or the row would keep a
 * dangling rule where a dropped segment used to be.
 */
function Separator({ className = '' }: { className?: string }) {
  return <span aria-hidden="true" data-testid="status-separator" className={`mx-1 h-3 shrink-0 self-center border-l border-border-subtle ${className}`} />
}

/**
 * One click-to-copy segment.
 *
 * A `<button>`, not a `<span>` with a handler: the status bar had no keyboard
 * path at all, and these are the first things in it worth reaching. The
 * displayed text and the copied value differ for the peer id — see
 * `peerIdText`.
 */
function CopySegment({ testId, display, value, what, title, dim, rtl, className = '', onCopy, onDoubleClick }: {
  testId: string
  /** What the row shows; '—' stands in for a value that is not there. */
  display: string
  /** What a click puts on the clipboard. Empty disables the button. */
  value: string
  /** The segment's name, already translated, for the confirmation message. */
  what: string
  title?: string
  dim?: boolean
  /** Truncate from the left instead of the right (paths: the tail informs). */
  rtl?: boolean
  className?: string
  onCopy: (what: string, value: string) => void
  /** The host segment keeps its pre-existing double-click to host settings. */
  onDoubleClick?: () => void
}) {
  // Only set while a copy is waiting out the double-click window; see
  // DOUBLE_CLICK_GRACE_MS. A segment without a second gesture copies at once.
  const pendingCopy = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (pendingCopy.current) clearTimeout(pendingCopy.current) }, [])

  const handleClick = () => {
    if (!onDoubleClick) {
      onCopy(what, value)
      return
    }
    if (pendingCopy.current) clearTimeout(pendingCopy.current)
    pendingCopy.current = setTimeout(() => {
      pendingCopy.current = null
      onCopy(what, value)
    }, DOUBLE_CLICK_GRACE_MS)
  }

  const handleDoubleClick = () => {
    if (pendingCopy.current) {
      clearTimeout(pendingCopy.current)
      pendingCopy.current = null
    }
    onDoubleClick?.()
  }

  return (
    <button
      type="button"
      data-testid={testId}
      data-dim={dim ? 'true' : undefined}
      disabled={value === ''}
      title={title}
      onClick={handleClick}
      onDoubleClick={onDoubleClick ? handleDoubleClick : undefined}
      // `bdi` keeps the path itself left-to-right inside an RTL box, so the
      // ellipsis lands at the start without reordering the text.
      style={rtl ? { direction: 'rtl', textAlign: 'left' } : undefined}
      // `text-text-secondary` is on the base, not left to each call site: the
      // row's container is `text-text-muted`, so a segment that forgets the
      // class inherits a dimmer colour than the host and session name beside
      // it — which is exactly what shipped in alpha.365 and read as three
      // greyed-out segments next to two normal ones.
      //
      // `dim` no longer changes the text at all. Two rounds of trying to make
      // "uncertain" a *brightness* landed on the same complaint both times:
      // the row reads as one line, and a segment that is darker than its
      // neighbours looks broken rather than provisional. The uncertainty is
      // signalled beside the value instead (the refresh control), where it
      // costs no legibility; `data-dim` stays as the tested state.
      className={`min-w-0 truncate text-left text-text-secondary ${value === '' ? 'cursor-default' : 'cursor-pointer'} ${className}`}
    >
      {rtl ? <bdi>{display}</bdi> : display}
    </button>
  )
}

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

  // Read agent event for the active session (hooks must be called unconditionally)
  const primaryContent = activeTab?.layout
    ? getPrimaryPane(activeTab.layout).content
    : null
  const agentHostId = primaryContent && primaryContent.kind === 'tmux-session' ? primaryContent.hostId : null
  const agentSessionCode = primaryContent && 'sessionCode' in primaryContent ? primaryContent.sessionCode : null
  const agentCk = agentHostId && agentSessionCode ? compositeKey(agentHostId, agentSessionCode) : null

  const session = useSessionStore((s) =>
    agentHostId && agentSessionCode
      ? (s.sessions[agentHostId] ?? []).find((sess) => sess.code === agentSessionCode) ?? null
      : null,
  )
  const hostConfig = useHostStore((s) => agentHostId ? s.hosts[agentHostId] : null)
  const hostRuntime = useHostStore((s) => agentHostId ? s.runtime[agentHostId] : null)
  const agentLabel = useAgentStore((s) => agentCk ? s.models[agentCk] ?? null : null)
  const agentType = useAgentStore((s) => agentCk ? s.agentTypes[agentCk] ?? null : null)
  const showAgentTitleInStatusBar = useUISettingsStore((s) => s.showAgentTitleInStatusBar)

  // Peer data for the primary pane. The hook owns *when* anything is fetched
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
  const primaryTerminated = !!(primaryContent && primaryContent.kind === 'tmux-session' && primaryContent.terminated)
  const peer = usePeerInfo(
    primaryTerminated ? null : agentHostId,
    primaryTerminated ? null : agentSessionCode,
    session?.tmux_instance ?? '',
  )

  // One confirmation for five copy buttons, in a fixed slot, so a copy never
  // reflows the row (spec §4.2).
  const [copyFeedback, setCopyFeedback] = useState('')
  const [copyFailed, setCopyFailed] = useState(false)
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (feedbackTimer.current) clearTimeout(feedbackTimer.current) }, [])

  const handleCopy = useCallback(async (what: string, value: string) => {
    let failed = false
    try {
      // `copyText` genuinely rejects: the Electron window over plain http has
      // no `navigator.clipboard`, which is the reason that helper exists.
      await copyText(value)
    } catch {
      failed = true
    }
    setCopyFailed(failed)
    setCopyFeedback(failed ? t('peer.copy_failed') : t('peer.copied', { what }))
    if (feedbackTimer.current) clearTimeout(feedbackTimer.current)
    feedbackTimer.current = setTimeout(() => { setCopyFeedback(''); feedbackTimer.current = null }, COPY_FEEDBACK_MS)
  }, [t])

  const handleNameDoubleClick = useCallback((e: React.MouseEvent<HTMLSpanElement>) => {
    if (!activeTab || !onStartRename) return
    onStartRename(activeTab, e.currentTarget)
  }, [activeTab, onStartRename])

  if (!activeTab) {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        {t('status.no_active')}
      </div>
    )
  }

  const primary = getPrimaryPane(activeTab.layout)
  const { content } = primary

  if (content.kind === 'editor') {
    return null
  }

  if (content.kind !== 'tmux-session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  // Session pane — show host, session name, status
  const sessionName = session?.name ?? content.sessionCode
  const paneTitle = showAgentTitleInStatusBar && agentType && !content.terminated ? session?.pane_title : null
  const hostName = hostConfig?.name ?? 'Unknown'
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
    <div data-testid="status-bar" className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0 relative z-10">
      {/* Left: the segment group. Everything in here may truncate. */}
      <div data-testid="status-segments" className="flex min-w-0 items-center">
        <CopySegment
          testId="status-seg-host"
          display={hostName}
          value={hostName}
          what={t('peer.label.host')}
          title={`${t('peer.copy_hint')} \u00b7 ${t('status.open_host_hint')}`}
          className="max-[500px]:max-w-[8ch] text-text-secondary"
          onCopy={handleCopy}
          onDoubleClick={agentHostId ? () => onNavigateToHost?.(agentHostId) : undefined}
        />
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
      </div>

      {/* Middle: the slack. The feedback slot sits at its start with a fixed
          width, so a copy confirmation never moves anything. */}
      <div className="flex min-w-0 flex-1 items-center">
        <span
          data-testid="status-copy-feedback"
          aria-live="polite"
          className={`ml-2 w-[14ch] shrink-0 truncate ${copyFailed ? 'text-status-error' : 'text-text-muted'}`}
        >
          {copyFeedback}
        </span>
      </div>

      {/* Right: the controls. `ml-auto` lives here and nowhere else. */}
      <div data-testid="status-controls" className="ml-auto flex shrink-0 items-center gap-3">
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
        <span data-testid="status-split-buttons" className="flex items-center gap-1 max-[500px]:hidden">
          <button
            title={t('pane.split_horizontal')}
            onClick={() => useTabStore.getState().splitPaneBlank(activeTab.id, getPrimaryPane(activeTab.layout).id, 'h')}
            className="flex items-center px-1 py-0.5 rounded border border-border-default text-text-secondary cursor-pointer transition-colors hover:bg-surface-hover"
          >
            <Columns size={12} />
          </button>
          <button
            title={t('pane.split_vertical')}
            onClick={() => useTabStore.getState().splitPaneBlank(activeTab.id, getPrimaryPane(activeTab.layout).id, 'v')}
            className="flex items-center px-1 py-0.5 rounded border border-border-default text-text-secondary cursor-pointer transition-colors hover:bg-surface-hover"
          >
            <Rows size={12} />
          </button>
        </span>
      </div>
    </div>
  )
}
