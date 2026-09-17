import { useState, useRef, useEffect, useLayoutEffect, useMemo } from 'react'
import { ArrowsClockwise, CircleNotch } from '@phosphor-icons/react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'
import { useResumeTemplateLookup } from '../lib/resume-templates'
import { isValidSessionName } from '../lib/session-name'
import { resolveResumeCommand } from '../lib/rebuild/composer'
import { EditableValue, type RebuildEditableField } from './RebuildActionSet'
import { collectRenameTargets, type RenameTargetPane } from '../features/workspace/hooks'
import { usePeerInfo } from '../hooks/usePeerInfo'
import { usePeerStore, type PeerEnvelopeFlags } from '../stores/usePeerStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { copyText } from '../lib/copy-text'
import { reasonText } from '../lib/peer-display'
import type { Tab } from '../types/tab'

interface Props {
  anchorRect: DOMRect
  currentName: string
  initialValue?: string
  allowUnchangedSubmit?: boolean
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  error?: string
  onClearError?: () => void
  placeholder?: string
  validateName?: (trimmedDraft: string, currentName: string) => string | undefined
  /**
   * Tab mode (spec §4.10). When the tab holds at least one terminal
   * `tmux-session` pane, the single rename input is replaced by one detail
   * block per pane — name, working directory, resume command — so the three
   * rebuild fields can be read and corrected on a live session as well as a
   * dead one. Omitted by the editor and storage callers, which keep the
   * single-input popover unchanged.
   */
  tab?: Tab
  /** A live pane's name row: the daemon rename that already exists. */
  onRenamePane?: (target: RenameTargetPane, name: string) => void | Promise<void>
  /** Record-only edits: a dead pane's name, and cwd / resume on any pane. */
  onEditRebuildField?: (target: RenameTargetPane, field: RebuildEditableField, value: string) => void
}

const POPOVER_WIDTH = 240
/** The detail blocks need room for a path and a resume command. */
const PANE_POPOVER_WIDTH = 380
const PADDING = 4

/** Label + value, matching `RebuildActionSet`'s row rhythm minus the checkbox. */
function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-28 shrink-0 truncate text-[11px] text-text-secondary">{label}</span>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  )
}

type T = (key: string, params?: Record<string, string | number>) => string

/**
 * Which of `partial`'s three causes to name.
 *
 * It is set by an owner lookup that did not finish, unreadable registry files,
 * or a failed title snapshot — so "some other session's owner" is not a
 * description of it, and the note has to say which one happened.
 */
function partialCause(envelope: PeerEnvelopeFlags, t: T): string {
  if (envelope.unknownRegistryFiles.length > 0) return t('peer.partial.registry')
  if (envelope.titlesUnavailable) return t('peer.partial.labels')
  return t('peer.partial.owners')
}

/**
 * One pane's peer row: address, agent, deliverability (spec §5).
 *
 * Loading and errors live here rather than on the panel because a tab can hold
 * panes on several hosts, and one host being unreachable must not blank
 * another pane's block.
 */
function PanePeerSection({ target }: { target: RenameTargetPane }) {
  const t = useI18nStore((s) => s.t)
  // A pane whose session the store has not reconciled yet is not a pane with
  // no peer — it is a pane whose answer has not arrived.
  const reconciled = useSessionStore((s) => (s.sessions[target.hostId] ?? []).some((sess) => sess.code === target.sessionCode))
  // This pane's tmux generation, as the daemon last described the session —
  // not `target.tmuxInstance`, which is the generation the *record* was written
  // in and is exactly what a rebuild is meant to change. A peer row is this
  // pane's only if it names the same tmux server; see `usePeerInfo`.
  const paneGeneration = useSessionStore((s) => (s.sessions[target.hostId] ?? []).find((sess) => sess.code === target.sessionCode)?.tmux_instance ?? '')
  const peer = usePeerInfo(target.hostId, target.sessionCode, paneGeneration)
  const [feedback, setFeedback] = useState('')
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (feedbackTimer.current) clearTimeout(feedbackTimer.current) }, [])

  const pid = target.paneId
  const row = peer.row

  const copyAddress = async () => {
    if (!row?.address) return
    let failed = false
    try {
      await copyText(row.address)
    } catch {
      failed = true
    }
    setFeedback(failed ? t('peer.copy_failed') : t('peer.copied', { what: t('peer.label.peer_id') }))
    if (feedbackTimer.current) clearTimeout(feedbackTimer.current)
    feedbackTimer.current = setTimeout(() => { setFeedback(''); feedbackTimer.current = null }, 1500)
  }

  const note = (testId: string, text: string) => (
    <p data-testid={testId} className="px-1 py-0.5 text-[11px] text-text-muted">{text}</p>
  )

  const rows = row && (
    <>
      {row.address !== '' && (
        <DetailRow label={t('peer.address')}>
          <span className="flex min-w-0 items-center gap-1">
            <button
              type="button"
              data-testid={`peer-address-${pid}`}
              title={t('peer.copy_hint')}
              onClick={() => void copyAddress()}
              className="min-w-0 cursor-pointer truncate text-left font-mono text-[11px] text-text-primary"
            >
              {row.address}
            </button>
          </span>
        </DetailRow>
      )}
      {row.agent && (
        <DetailRow label={t('peer.agent')}>
          <span data-testid={`peer-agent-${pid}`} className="block truncate text-[11px] text-text-primary">
            {[row.agent.type, row.agent.peerName, row.agent.status].filter(Boolean).join(' · ')}
          </span>
        </DetailRow>
      )}
      <DetailRow label={t('peer.deliverable')}>
        <span data-testid={`peer-deliverable-${pid}`} className={`block truncate text-[11px] ${row.deliverable ? 'text-text-primary' : 'text-status-warning'}`}>
          {row.deliverable ? t('peer.deliverable_yes') : reasonText(row.reason, t)}
        </span>
      </DetailRow>
      {peer.envelope.partial && note(`peer-partial-${pid}`, t('peer.partial_note', { cause: partialCause(peer.envelope, t) }))}
      {peer.envelope.titlesUnavailable && note(`peer-titles-${pid}`, t('peer.titles_unavailable_note'))}
    </>
  )

  let body: React.ReactNode
  if (!peer.connected) {
    // Nothing was fetched and nothing will be; whatever was last known stays,
    // dimmed, because a stale address is still a better start than none.
    body = <>{note(`peer-status-${pid}`, t('peer.host_not_connected'))}{rows}</>
  } else if (peer.error) {
    body = (
      <p data-testid={`peer-error-${pid}`} className="px-1 py-0.5 text-[11px] text-status-error">
        {t('peer.error', { error: peer.error })}
      </p>
    )
  } else if (!row && (!reconciled || peer.loading)) {
    body = (
      <span data-testid={`peer-spinner-${pid}`} className="flex items-center gap-1 px-1 py-0.5 text-[11px] text-text-muted">
        <CircleNotch size={11} className="animate-spin" />
        {t('peer.loading')}
      </span>
    )
  } else if (!row) {
    // "no peer" is only safe to say about a complete answer.
    body = note(`peer-status-${pid}`, peer.envelope.partial ? t('peer.undetermined') : t('peer.none'))
  } else {
    body = rows
  }

  return (
    <div
      data-testid={`peer-section-${pid}`}
      data-dim={!peer.connected || peer.stale ? 'true' : undefined}
      className={`mt-1 border-t border-border-subtle pt-1 ${!peer.connected || peer.stale ? 'opacity-60' : ''}`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[10px] uppercase tracking-wide text-text-muted">{t('peer.section')}</span>
        <span className="flex items-center gap-1">
          {feedback && <span className="text-[10px] text-text-muted">{feedback}</span>}
          {/* Inside the popover container on purpose: `useClickOutside` binds
              mousedown, so a control outside it would close the panel before
              its own click landed. */}
          <button
            type="button"
            data-testid={`peer-refresh-${pid}`}
            title={t('peer.refresh')}
            aria-label={t('peer.refresh')}
            disabled={!peer.connected || peer.loading}
            onClick={() => peer.refresh()}
            className="flex shrink-0 cursor-pointer items-center rounded p-0.5 text-text-muted transition-colors hover:bg-surface-hover disabled:cursor-default disabled:opacity-40"
          >
            <ArrowsClockwise size={11} className={peer.loading ? 'animate-spin' : ''} />
          </button>
        </span>
      </div>
      {body}
    </div>
  )
}

/**
 * One terminal pane's three rebuild fields.
 *
 * The name row is the only one that can reach the daemon, and only when the
 * session is still there: a terminated pane's name row writes
 * `rebuild.sessionName` instead, because renaming a session that is gone is
 * not a request worth sending. cwd and resume never leave the record.
 */
function PaneDetailBlock({
  target,
  autoFocus,
  onRenamePane,
  onEditRebuildField,
  onClearError,
}: {
  target: RenameTargetPane
  autoFocus: boolean
  onRenamePane?: (target: RenameTargetPane, name: string) => void | Promise<void>
  onEditRebuildField?: (target: RenameTargetPane, field: RebuildEditableField, value: string) => void
  onClearError?: () => void
}) {
  const t = useI18nStore((s) => s.t)
  // Subscribed: the row shows the resolved command, so editing a template
  // repaints it here too (spec §4.2).
  const templates = useResumeTemplateLookup(target.hostId)
  const live = !target.terminated
  const currentName = live
    ? (target.cachedName || target.sessionCode)
    : (target.record.sessionName || target.cachedName || target.sessionCode)
  const [draft, setDraft] = useState(currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!autoFocus) return
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [autoFocus])

  const trimmed = draft.trim()
  const invalid = trimmed && trimmed !== currentName && !isValidSessionName(trimmed)
    ? t('tab.rename_invalid_format')
    : undefined

  const commitName = () => {
    if (!trimmed || trimmed === currentName || submitting || invalid) return
    if (!live) {
      onEditRebuildField?.(target, 'sessionName', trimmed)
      return
    }
    setSubmitting(true)
    void Promise.resolve(onRenamePane?.(target, trimmed)).finally(() => setSubmitting(false))
  }

  const handleNameKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // An IME Enter confirms a candidate; it is not a submit.
    if (e.nativeEvent.isComposing) return
    if (e.key !== 'Enter') return
    // The popover's own Enter handler submits the legacy single-input rename.
    // Each block owns its target, so it must not also fire that.
    e.preventDefault()
    e.stopPropagation()
    commitName()
  }

  return (
    <div data-testid={`rename-pane-block-${target.paneId}`} className="rounded-md border border-border-subtle bg-surface-secondary px-2 py-1.5">
      <div className="flex items-center justify-between gap-2 pb-1">
        <span className="truncate font-mono text-[10px] text-text-muted">{target.sessionCode}</span>
        {!live && (
          <span data-testid={`rename-pane-terminated-${target.paneId}`} className="shrink-0 text-[10px] text-status-warning">
            {t('rebuild.pane_terminated')}
          </span>
        )}
      </div>

      <DetailRow label={t('rebuild.session_name')}>
        <input
          ref={inputRef}
          type="text"
          value={draft}
          onChange={(e) => { setDraft(e.target.value); onClearError?.() }}
          onKeyDown={handleNameKeyDown}
          disabled={submitting}
          placeholder={t('tab.rename_placeholder')}
          className="w-full rounded border border-border-default bg-surface-input px-1.5 py-0.5 font-mono text-xs text-text-primary focus:border-border-active focus:outline-none disabled:opacity-50"
        />
      </DetailRow>

      {/* Enter inside these editors commits the cell, nothing else. */}
      <div onKeyDown={(e) => { if (e.key === 'Enter') e.stopPropagation() }}>
        <DetailRow label={t('rebuild.working_directory')}>
          <EditableValue
            value={target.record.cwd}
            placeholder="—"
            disabled={false}
            testId={`rename-pane-cwd-${target.paneId}`}
            onCommit={(value) => onEditRebuildField?.(target, 'cwd', value)}
          />
        </DetailRow>
        <DetailRow label={t('rebuild.run_resume')}>
          <EditableValue
            value={resolveResumeCommand(target.record, templates)}
            placeholder="—"
            disabled={false}
            testId={`rename-pane-resume-${target.paneId}`}
            onCommit={(value) => onEditRebuildField?.(target, 'resumeCommandOverride', value)}
          />
        </DetailRow>
      </div>

      {/* A terminated pane has no peer: the section is omitted, as the panel
          already does for its other live-only rows. */}
      {live && <PanePeerSection target={target} />}

      {invalid && <p className="px-1 pt-1 text-[11px] text-red-400">{invalid}</p>}
    </div>
  )
}

export function RenamePopover({ anchorRect, currentName, initialValue, allowUnchangedSubmit = false, onConfirm, onCancel, error, onClearError, placeholder, validateName, tab, onRenamePane, onEditRebuildField }: Props) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initialValue ?? currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useClickOutside(containerRef, onCancel)

  const targets = useMemo(() => (tab ? collectRenameTargets(tab) : []), [tab])
  const paneMode = targets.length > 0
  const width = paneMode ? PANE_POPOVER_WIDTH : POPOVER_WIDTH

  // Refresh peers once per distinct host when the panel opens (spec §3.3).
  //
  // Keyed on the host *set* rather than on `targets`: this component recomputes
  // its targets on every render, and the peers call costs ~2 s, so an effect
  // that depended on the array would hammer the daemon. `\0` cannot appear in
  // a hostId, so the joined string is an honest identity for the set.
  //
  // The set is the hosts of the panes that actually render a peer section, so
  // terminated panes are left out: they show no peer section (spec §5), and a
  // tab holding nothing but dead panes would otherwise put a two-second,
  // tmux-heavy inventory call behind opening a panel with nothing to fill.
  //
  // The cwd store is refreshed too, by each block's own `usePeerInfo` as it
  // mounts — one cheap tmux call per pane, which is what opening the panel
  // means for that store.
  const hostKey = useMemo(
    () => Array.from(new Set(targets.filter((target) => !target.terminated).map((target) => target.hostId))).sort().join('\0'),
    [targets],
  )
  useEffect(() => {
    if (!hostKey) return
    const { runtime } = useHostStore.getState()
    for (const hostId of hostKey.split('\0')) {
      // A host that is not reachable would fail slowly, and the block already
      // says so.
      if (runtime[hostId]?.status !== 'connected') continue
      void usePeerStore.getState().refresh(hostId)
    }
  }, [hostKey])

  const trimmedDraft = draft.trim()
  const validationError = validateName
    ? validateName(trimmedDraft, currentName)
    : (trimmedDraft && trimmedDraft !== currentName && !isValidSessionName(trimmedDraft)
        ? t('tab.rename_invalid_format')
        : undefined)
  const displayError = paneMode ? error : (validationError ?? error)

  // Focus + select all on mount
  useEffect(() => {
    const input = inputRef.current
    if (input) {
      input.focus()
      input.select()
    }
  }, [])

  // Position: centered below anchor, clamped to viewport
  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    // Horizontal clamping (existing)
    let left = anchorRect.left + anchorRect.width / 2 - width / 2
    left = Math.max(PADDING, Math.min(left, window.innerWidth - width - PADDING))
    // Vertical clamping
    const popoverHeight = el.offsetHeight
    let top = anchorRect.bottom + PADDING
    if (top + popoverHeight > window.innerHeight - PADDING) {
      top = anchorRect.top - PADDING - popoverHeight
    }
    if (top < PADDING) {
      top = PADDING
    }
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [anchorRect, displayError, width, targets.length])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const trimmed = draft.trim()
      if (!trimmed || (!allowUnchangedSubmit && trimmed === currentName) || submitting || validationError) return
      setSubmitting(true)
      onConfirm(trimmed).finally(() => setSubmitting(false))
    }
  }

  return (
    <div
      ref={containerRef}
      onKeyDown={handleKeyDown}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl p-2"
      style={{ width }}
    >
      {paneMode
        ? (
          <div className="space-y-2">
            {targets.map((target, i) => (
              <PaneDetailBlock
                key={target.paneId}
                target={target}
                autoFocus={i === 0}
                onRenamePane={onRenamePane}
                onEditRebuildField={onEditRebuildField}
                onClearError={onClearError}
              />
            ))}
          </div>
        )
        : (
          <input
            ref={inputRef}
            type="text"
            value={draft}
            onChange={(e) => { setDraft(e.target.value); onClearError?.() }}
            disabled={submitting}
            placeholder={placeholder ?? t('tab.rename_placeholder')}
            className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none disabled:opacity-50"
          />
        )}
      {displayError && (
        <p className="text-xs text-red-400 mt-1 px-1">{displayError}</p>
      )}
    </div>
  )
}
