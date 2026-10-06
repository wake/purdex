// spa/src/components/execution/ExecutionView.tsx — the {kind:'execution'}
// pane (spec §4.3.3). Composes the observe subscription, the lazy control
// lease, the pane's actions (useExecutionActions: send/interrupt,
// the only writes), the room transcript, the worker dock and WorkerInput. It also
// owns "Take to terminal": confirm when a turn is running, then
// `lib/nex/handoff.ts` does the request, the lease forget and the pane swap
// (which unmounts this view) — `takeBack` to the origin session when the
// execution came from one (`from`, P-C.3 spec §4.4), else `takeToTerminal`
// into a fresh session in the execution's cwd (exec-to-terminal spec §4.2).
// The view (R2 plan T1.4): the same state renders as the room or as chat
// (`mode`, from the pane content). Chat has no dock and chat's header (see
// ExecutionHeader); switching is local — the subscription, the store and the
// lease are untouched, so nothing is refetched.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, DragEvent } from 'react'
import { UploadSimple } from '@phosphor-icons/react'
import RoomTranscript from '../room/RoomTranscript'
import ChatTranscript from '../chat/ChatTranscript'
import { ChatUserBubble } from '../chat/ChatBubble'
import { FoldContext, useFoldMemory } from '../room/fold-context'
import RoomUserLine from '../room/RoomUserLine'
import { PendingAttachmentThumbs } from '../room/AttachmentThumbs'
import { AttachmentSourceContext } from '../room/attachment-source'
import WorkerDock from '../room/WorkerDock'
import WorkerInput from '../room/WorkerInput'
import QuickReplyDock from '../room/QuickReplyDock'
import TranscriptSearch from '../room/TranscriptSearch'
import { isFindShortcut } from '../../lib/find-shortcut'
import type { TranscriptScrollControl } from '../../hooks/useTranscriptScroll'
import { useQuickReplies } from '../../lib/quick-replies'
import ExecutionHeader from './ExecutionHeader'
import { ConfirmDialog } from '../ConfirmDialog'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { getWorkerTheme, workerThemeStyle } from '../../lib/worker-theme/registry'
import { useUndoToast } from '../../stores/useUndoToast'
import { useExecutionSubscription } from '../../hooks/useExecutionSubscription'
import { useExecutionPrelude } from '../../hooks/useExecutionPrelude'
import { derivePrelude } from '../../lib/nex/prelude'
import PreludeSection from '../room/prelude/PreludeSection'
import { useExecutionLease } from '../../hooks/useExecutionLease'
import { useExecutionActions, type SendOptions } from '../../hooks/useExecutionActions'
import { useWorkerUploads } from '../../hooks/useWorkerUploads'
import {
  canSend, composeWithAttachments, encodeImage, isAttachmentError, planAttachments, requestBytes, uploadErrorKey,
  PER_IMAGE_ERRORS, type Chip, type WireImageAttachment,
} from '../../lib/nex/worker-upload'
import { selectImageAttachments, selectTranscriptPrelude, useNexHostStore } from '../../stores/useNexHostStore'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import { defaultExecutionState } from '../../lib/nex/event-reducer'
import { costSummary } from '../../lib/nex/cost-summary'
import { anyRunningSubagent, runningTasks, subagentTasksByToolUse } from '../../lib/nex/tasks'
import { indexOperations } from '../../lib/nex/operations'
import { toolUseUnit } from '../../lib/nex/transcript-search'
import { partialHasChatContent, partialHasVisibleContent } from '../../lib/nex/partial'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import { exitWorker, exitErrorMessage } from '../../lib/nex/exit-worker'
import { isLiveRow } from '../../lib/nex/live-workers'
import { takeBack, takeToTerminal, handoffErrorMessage, manualResumeHint } from '../../lib/nex/handoff'
import { registerTakeToTerminal } from '../../lib/nex/take-to-terminal-registry'
import type { ExecutionFrom, ExecutionViewMode } from '../../types/tab'

export interface ExecutionViewProps {
  hostId: string
  executionId: string
  isActive: boolean
  /** The pane is its tab's focus target (shell cleanup spec §8.2); the reply box focuses itself only then. Absent reads as false. */
  isFocusTarget?: boolean
  /** The pane this view lives in — the take-back swaps its content. */
  tabId: string
  paneId: string
  /** Set when the execution was handed off from a tmux session; "Take to terminal" then returns to that session. */
  from?: ExecutionFrom
  /** The pane's view; absent reads as room (spec §1, D3). */
  mode?: ExecutionViewMode
  /** Writes the chosen view back to the pane content (ExecutionPaneWrapper). */
  onModeChange: (mode: ExecutionViewMode) => void
}

const EMPTY = defaultExecutionState()
const TERMINAL_STATES = new Set(['rejected', 'failed', 'terminated'])
/**
 * States the daemon's take-to-terminal can settle (spec §4.2): never `queued`
 * (it would answer `execution_not_settled`) nor `rejected`.
 */
const TAKEABLE_STATES = new Set(['running', 'idle', 'failed', 'terminated'])
/**
 * The worker panes listening for Mod+F right now (their tab is active), and
 * the one the reader last pressed or focused in (R1-2). `isActive` is the
 * tab's, so in a split every worker pane listens; a Mod+F with nothing
 * focused (the body) goes to the lone listener when there is one, else to
 * the one last interacted with, else to none. The record only tells split
 * panes apart: a lone pane needs none — an ended worker's input is disabled
 * and never takes focus, so nothing would record it (#1495 re-review P2-1).
 * Module state: one reader, one keyboard.
 */
const findListeners = new Set<string>()
let lastInteractedPane: string | null = null

const KNOWN_ERROR_KEYS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

export default function ExecutionView({ hostId, executionId, isActive, isFocusTarget = false, tabId, paneId, from, mode = 'room', onModeChange }: ExecutionViewProps) {
  const t = useI18nStore((s) => s.t)
  const key = executionKey(hostId, executionId)
  const st = useExecutionStore((s) => s.executions[key] ?? EMPTY)
  // Worker pane theme spec §4.1: the pane root carries the theme id and its
  // `--wt-*` vars as inline style; room and chat read only `var(--wt-*)`.
  const workerThemeId = useWorkerSettingsStore((s) => s.theme)
  const workerTheme = getWorkerTheme(workerThemeId)
  const workerThemeVars = useMemo(() => workerThemeStyle(workerTheme), [workerTheme])
  const { problem } = useExecutionSubscription(hostId, executionId, isActive)
  // Spec D4: a worker with no prelude to draw gets no prelude markup at all.
  const preludeEligible = useNexHostStore(selectTranscriptPrelude(hostId)) !== null && !!st.summary?.resume_session_id
  const preludeApi = useExecutionPrelude(hostId, executionId)
  const preludeView = useMemo(() => derivePrelude(st.prelude.items), [st.prelude.items])
  const lease = useExecutionLease(hostId, executionId)
  const { draft, actionPending, handleSend, handleInterrupt, restoreDraft } = useExecutionActions(hostId, executionId, lease)
  // Spec §9.2 (phase E): native images only when the host's capability
  // exists AND lists this execution's provider — null otherwise (an older
  // daemon, a codex execution, the summary not in yet), and then every image
  // goes by path exactly as in phase D. The result is reference-stable.
  const imageCaps = useNexHostStore(selectImageAttachments(hostId, st.summary?.provider ?? ''))
  useEffect(() => { void useNexHostStore.getState().ensure(hostId) }, [hostId])
  // The typed text, for planning an image added mid-draft against the request budget.
  const draftText = useRef('')
  const onTextChange = useCallback((text: string) => { draftText.current = text }, [])
  const getDraftText = useCallback(() => draftText.current, [])
  // Spec §9.1: attachments. The chips live here, not in WorkerInput — the
  // input is re-keyed on the restored draft and remounts after a failed send —
  // and the whole pane is the drop target (TerminalView's overlay pattern).
  const uploads = useWorkerUploads(hostId, executionId, { caps: imageCaps, getText: getDraftText })
  // A send refused before going out (phase E: the body would exceed
  // max_request_bytes); cleared by the next send or a removed chip.
  const [sendBlock, setSendBlock] = useState<'request_too_large' | null>(null)
  // Encoding the images is async; this keeps a second submit meanwhile from
  // posting the same chips twice (handleSend's own lock starts after it).
  // The ref is the same-tick guard; the state renders it, so the input and
  // the quick replies show disabled instead of dropping a click (PR #1527 A3).
  const encoding = useRef(false)
  const [encodingBusy, setEncodingBusy] = useState(false)
  // Phase E: where the transcript's replayed image attachments are fetched from.
  const attachmentSource = useMemo(() => ({ hostId, executionId }), [hostId, executionId])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)

  // Take-back: `takeBack` is single-flight per execution, but the busy flag
  // is what the header shows; the ref keeps a same-tick second click from
  // reaching it before React commits the state. While it is pending every
  // write to the execution (send / interrupt / exit) is frozen too: the
  // daemon is interrupting and archiving it, and a write racing that would
  // land on an execution that is about to be gone.
  const [takeBackBusy, setTakeBackBusy] = useState(false)
  const takeBackInFlight = useRef(false)
  const [confirmTakeBack, setConfirmTakeBack] = useState(false)
  const runTakeBack = useCallback(async () => {
    if (takeBackInFlight.current) return
    const exec = useExecutionStore.getState().executions[key]
    // Without `from` the request needs the execution's cwd; the control is
    // only offered once the summary is in, so this is a same-tick race guard.
    const cwd = from ? undefined : exec?.summary?.cwd
    if (!from && !cwd) return
    takeBackInFlight.current = true
    setTakeBackBusy(true)
    const toast = useUndoToast.getState()
    try {
      const leaseId = exec?.lease?.leaseId
      const common = { hostId, executionId, leaseId, tabId, paneId, forgetLease: lease.forget }
      const { result, swapped } = from
        ? await takeBack({ ...common, from })
        : await takeToTerminal({ ...common, cwd: cwd! })
      // On `swapped` this view is already unmounted (the pane is a terminal
      // again); the toast is global, so it still lands.
      toast.show(swapped ? t('takeback.success') : t('takeback.archived_no_pane'))
      // The terminal took over, but the worker is still live: say so until dismissed.
      if (result.exited === false) toast.show(t('worker.exit.after_transfer_failed'), undefined, undefined, { persistent: true })
    } catch (err) {
      if (err instanceof HandoffApiError) {
        const id = manualResumeHint(err)
        const message = handoffErrorMessage(t, err)
        toast.show(id ? `${message}\n${t('takeback.manual_resume', { id })}` : message)
      } else {
        toast.show(t('handoff.error.generic', { code: 'unknown' }))
      }
    } finally {
      takeBackInFlight.current = false
      setTakeBackBusy(false)
    }
  }, [from, hostId, executionId, key, tabId, paneId, lease.forget, t])
  // 退出: single-flight in `exitWorker`; the ref stops a same-tick second click.
  const [exitBusy, setExitBusy] = useState(false)
  const exitInFlight = useRef(false)
  const [confirmExit, setConfirmExit] = useState(false)
  const runExit = useCallback(async () => {
    if (exitInFlight.current) return
    exitInFlight.current = true
    setExitBusy(true)
    try {
      const result = await exitWorker({ hostId, executionId, leaseId: useExecutionStore.getState().executions[key]?.lease?.leaseId, forgetLease: lease.forget })
      // Patch the summary at once from the result (the SSE confirms later): no longer live,
      // so exit stays disabled and the ended handling takes over.
      const cur = useExecutionStore.getState().executions[key]?.summary
      if (cur) useExecutionStore.getState().setSummary(hostId, executionId, { ...cur, state: result.state, archived: result.archived })
    } catch (err) {
      useUndoToast.getState().show(exitErrorMessage(err, t))
    } finally {
      exitInFlight.current = false
      setExitBusy(false)
    }
  }, [hostId, executionId, key, lease.forget, t])
  const onExit = useCallback(() => {
    if (exitInFlight.current) return
    if (useExecutionStore.getState().executions[key]?.summary?.state === 'running') setConfirmExit(true)
    else void runExit()
  }, [key, runExit])
  const exitable = !!st.summary && isLiveRow(st.summary)
  // A write already on its way to the daemon (send, interrupt)
  // could land after the daemon's settled check and before its archive;
  // the daemon re-verifies (#1171), and the SPA refuses to start the race.
  const writeInFlight = st.pendingSend || actionPending
  const onTakeBack = useCallback(() => {
    if (takeBackInFlight.current || exitInFlight.current || writeInFlight) return
    if (useExecutionStore.getState().executions[key]?.summary?.state === 'running') setConfirmTakeBack(true)
    else void runTakeBack()
  }, [key, runTakeBack, writeInFlight])
  // Spec §4.2: every claude execution with a session id can go to a terminal
  // — a fresh one when there is no origin session to return to.
  // Archived is excluded too: the daemon refuses it (`execution_archived`) —
  // whoever archived it already resumed that transcript elsewhere.
  const canTakeToTerminal = !from && !!st.summary && st.summary.provider === 'claude' && !st.summary.archived
    && !!(st.summary.session_id || st.summary.resume_session_id) && TAKEABLE_STATES.has(st.summary.state)
  // Shell cleanup §9.5: the status bar's terminal button runs this same flow
  // (confirm, lease forget, busy guards) through the registry. Offered only
  // where the header offers it — a problem pane has no header (and no confirm
  // dialog) — and busy under the header's own busy condition. The entry calls
  // through a ref, so a re-render that leaves canTake / busy alone still runs
  // the latest closure without re-registering.
  const takeOffered = !problem && (!!from || canTakeToTerminal)
  const takeBusy = takeBackBusy || exitBusy || writeInFlight
  const onTakeBackRef = useRef(onTakeBack)
  useLayoutEffect(() => { onTakeBackRef.current = onTakeBack })
  useEffect(() => registerTakeToTerminal(paneId, {
    canTake: takeOffered, busy: takeBusy, takeToTerminal: () => onTakeBackRef.current(),
  }), [paneId, takeOffered, takeBusy])

  const isMine = useCallback((p: string | undefined) => !!p && p.endsWith(`/${getNexClientId()}`), [])
  // P-B4 spec §4.2: null until history is loaded so the header shows `$…`
  // rather than a partial sum.
  const cost = useMemo(() => (st.historyLoaded ? costSummary(st.messages) : null), [st.messages, st.historyLoaded])
  // Spec §4.2: the 1 s clock only runs while some tool is running.
  const anyRunning = useMemo(() => Object.values(st.tools).some((tool) => tool.status === 'running'), [st.tools])
  // R4 T3.3: a running subagent's close-out line ticks too (a background one
  // has no running tool). Only the clock — `anyRunning` also gates thinking.
  const subagentRunning = useMemo(() => anyRunningSubagent(st.tasks), [st.tasks])
  const subagentTasks = useMemo(() => subagentTasksByToolUse(st.tasks), [st.tasks])
  const now = useElapsedTicker(anyRunning || subagentRunning)
  // Spec §3.2: one fold memory per pane. It lives here, above the view
  // switch, because room ⇄ chat remounts the transcript (F2).
  const foldStore = useFoldMemory()
  const quickReplies = useQuickReplies(hostId)

  // R3 T3.3: the search bar. Open state lives here; the bar is the
  // transcript's (TranscriptSearch). `focusRequest` refocuses its input on a
  // repeated Mod+F; `restoreFocus` is where Escape sends focus back to.
  const [searchOpen, setSearchOpen] = useState(false)
  const [focusRequest, setFocusRequest] = useState(0)
  const restoreFocus = useRef<HTMLElement | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  // The transcript's scroll box, as state (a callback ref): room ⇄ chat
  // mounts a new one, and the open bar must re-mark and re-scroll in it (R1-1).
  const [scrollBox, setScrollBox] = useState<HTMLDivElement | null>(null)
  // The transcript's bottom-follow: every jump to a match releases it (A F4).
  const scrollControl = useRef<TranscriptScrollControl>(null)
  const onSearchJump = useCallback(() => scrollControl.current?.release(), [])

  // R4 T3.2: the dock's running tasks, and its "inspect" — the search reveal
  // path: find the call's unit (`toolUseUnit`, the same ids and reveal keys
  // as a search match), open the folds that hide it, and once that commit is
  // on screen scroll its `data-search-unit` into view and release the
  // bottom-follow, as a search jump does (the release holds with the search
  // bar closed too, until the reader is back at the bottom). The target waits in a ref so a later
  // re-render (a view switch handing over a new scroll box) never replays it.
  const dockTasks = useMemo(() => runningTasks(st.tasks), [st.tasks])
  const inspectTarget = useRef<string | null>(null)
  const [inspectRequest, setInspectRequest] = useState(0)
  const onInspectTask = useCallback((toolUseId: string) => {
    const unit = toolUseUnit({ messages: st.messages, index: indexOperations(st.messages), tools: st.tools }, toolUseId)
    if (!unit) return
    foldStore.expand(unit.reveal)
    inspectTarget.current = unit.id
    setInspectRequest((n) => n + 1)
  }, [st.messages, st.tools, foldStore])
  useLayoutEffect(() => {
    const id = inspectTarget.current
    if (id === null || !scrollBox) return
    inspectTarget.current = null
    for (const el of scrollBox.querySelectorAll('[data-search-unit]')) {
      if (el.getAttribute('data-search-unit') !== id) continue
      el.scrollIntoView?.({ block: 'center' })
      scrollControl.current?.release()
      return
    }
  }, [inspectRequest, scrollBox])
  const closeSearch = useCallback(() => {
    setSearchOpen(false)
    const el = restoreFocus.current
    restoreFocus.current = null
    if (el?.isConnected) el.focus()
  }, [])
  // No focusable pane root (plan review #4): a click-to-focus root would
  // fight the input's auto-focus, the header's refocus and every fold button.
  // Instead, while this pane is active, Mod+F anywhere in it — or with
  // nothing focused (the body) when it is the only pane listening, or, in a
  // split, the one last interacted with — opens the bar and keeps the
  // browser's own find (the web build) from opening. A target anywhere else
  // (another pane, a dialog, Monaco) keeps its own Mod+F, and so does the
  // body in a split where no listening pane has been interacted with. Electron registers no Cmd+F accelerator (electron/keybindings.ts),
  // so the renderer gets the key.
  const markInteracted = useCallback(() => { lastInteractedPane = paneId }, [paneId])
  useEffect(() => () => {
    if (lastInteractedPane === paneId) lastInteractedPane = null
  }, [paneId])
  useEffect(() => {
    if (!isActive) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (!isFindShortcut(e)) return
      const root = rootRef.current
      const target = e.target
      if (!root) return
      if (target === document.body) {
        if (findListeners.size > 1 && lastInteractedPane !== paneId) return
      } else if (!(target instanceof Node && root.contains(target))) return
      // A dialog is modal: its Mod+F is its own, even rendered inside the pane (R1-3).
      if (target instanceof Element && target.closest('[role="dialog"]')) return
      if (root.querySelector('[aria-modal="true"]')) return
      // Nothing to search until the history is in.
      if (!useExecutionStore.getState().executions[key]?.historyLoaded) return
      e.preventDefault()
      const active = document.activeElement
      // The first open remembers where focus was; a repeat only refocuses.
      if (!restoreFocus.current && active instanceof HTMLElement && active !== document.body && !active.closest('[data-testid="transcript-search"]')) {
        restoreFocus.current = active
      }
      setSearchOpen(true)
      setFocusRequest((n) => n + 1)
    }
    findListeners.add(paneId)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      findListeners.delete(paneId)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [isActive, key, paneId])

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex items-center justify-center h-full text-sm text-text-muted">{text}</div>
  }

  const terminal = !!st.summary && TERMINAL_STATES.has(st.summary.state)
  const ended = terminal || !!st.summary?.archived
  // The SSE handle can die terminally (401/403, or a non-retryable
  // structured error) after history has loaded, with no reconnect ever
  // coming — the pane looks live but a send would 2xx into the void with
  // no message_accepted/result ever arriving. Gate input on
  // it same as `ended`; `sse` flips back off 'closed' the moment the pane
  // is reactivated (see useExecutionSubscription's activation effect), so
  // this clears itself without redesigning the reconnect path.
  const streamDead = st.historyLoaded && st.sse === 'closed' && !!st.sseError
  // One gate for everything that sends: the input and the quick replies.
  const inputDisabled = st.pendingSend || encodingBusy || ended || !st.historyLoaded || streamDead || takeBackBusy || exitBusy
  const placeholder = st.summary?.archived ? t('execution.input.archived')
    : ended ? t('execution.input.terminal')
    : streamDead ? t('execution.input.disconnected')
    : undefined
  const leaseHeld = st.leaseError?.code === 'lease_held'

  // Every user send — typed or a quick reply — goes through here (PR #1522
  // A1): an uploading or failed chip blocks it, and otherwise the done chips
  // ride along as `[file: …]` lines. The chips go only once the daemon
  // accepted it; only the chips that went out are cleared — one added
  // meanwhile stays for the next message. A typed send's failure restores
  // just the typed text as the draft (the chips stay where they are); a quick
  // reply passes `restoreDraft: false` and leaves the input alone.
  //
  // Phase E: the native image chips go as base64 `attachments`, the rest as
  // `[file: …]` lines. Before anything goes out the planned set is checked
  // once more against the final text: over `max_request_bytes` the send is
  // refused with a visible reason (returns false: the input keeps the text);
  // an image the capability no longer admits (the daemon changed under us)
  // is re-uploaded by path first. A per-image refusal from the daemon fails
  // that chip (`attachment_index`, in send order).
  const attachGate = canSend(uploads.chips)
  const sendWithAttachments = (text: string, opts: SendOptions): boolean => {
    if (encoding.current || !canSend(uploads.chips).ok) return false
    const sent = uploads.chips.filter((c) => c.status === 'done')
    const finalText = composeWithAttachments(text, sent.filter((c) => c.kind === 'path'))
    const natives = uploads.nativeFiles(sent.filter((c) => c.kind === 'image').map((c) => c.key))
    if (natives.length > 0) {
      const planned = natives.map((n) => ({ key: n.key, size: n.file.size, type: n.file.type }))
      if (imageCaps && requestBytes(finalText, planned) > imageCaps.maxRequestBytes) {
        setSendBlock('request_too_large')
        return false
      }
      const plan = planAttachments(planned, imageCaps, finalText)
      if (plan.path.length > 0) {
        uploads.demote(plan.path)
        return false
      }
    }
    setSendBlock(null)
    if (natives.length === 0) {
      // No await without images: a text-only send takes handleSend's lock in
      // the same tick as the submit, as in phase D.
      void sendNow(finalText, opts, sent.map((c) => c.key), [], [])
      return true
    }
    void encodeThenSend(text, opts, sent, natives)
    return true
  }
  // Encoding is async and a chip can be removed meanwhile (PR #1527 A1): once
  // the images are read, the snapshot is re-checked against the chips that
  // still exist — a removed image leaves the attachments (and gets no
  // optimistic preview), a removed path chip loses its `[file:]` line — and
  // with nothing left to say the send is dropped.
  const encodeThenSend = async (typed: string, opts: SendOptions, sent: Chip[], natives: { key: string; file: File }[]) => {
    encoding.current = true
    setEncodingBusy(true)
    try {
      const encoded = await Promise.allSettled(natives.map((n) => encodeImage(n.file)))
      const kept = natives.flatMap((n, i) => (uploads.isLive(n.key) ? [{ ...n, result: encoded[i] }] : []))
      const unreadable = kept.filter((n) => n.result.status === 'rejected')
      if (unreadable.length > 0) {
        for (const n of unreadable) uploads.markFailed(n.key)
        if (opts.restoreDraft !== false) restoreDraft(opts.draftText ?? typed)
        return
      }
      const liveSent = sent.filter((c) => uploads.isLive(c.key))
      const finalText = composeWithAttachments(typed, liveSent.filter((c) => c.kind === 'path'))
      if (!finalText && kept.length === 0) return
      const data = kept.map((n) => (n.result as PromiseFulfilledResult<string>).value)
      await sendNow(finalText, opts, liveSent.map((c) => c.key), kept, data)
    } finally {
      encoding.current = false
      setEncodingBusy(false)
    }
  }
  const sendNow = async (finalText: string, opts: SendOptions, sentKeys: string[], natives: { key: string; file: File }[], data: string[]) => {
    const attachments: WireImageAttachment[] = natives.map((n, i) => ({ type: 'image', media_type: n.file.type, data: data[i] }))
    // The optimistic line's thumbnails: its own object URLs (the chips' go
    // with the chips), made only for the images still live after encoding —
    // so none is ever made that isn't handed over. handleSend takes them
    // over: the execution store revokes them once pendingLocal drops them
    // (accepted, failed, replaced), whichever pane is showing them, and a
    // no-op send revokes them itself — so this pane never revokes them, not
    // even on unmount or on the `attachments_unsupported` demote below.
    const previews = natives.length > 0
      ? natives.map((n) => ({ previewUrl: URL.createObjectURL(n.file), media_type: n.file.type }))
      : undefined
    const ok = await handleSend(finalText, previews ? { ...opts, attachments, previews } : opts)
    if (ok) {
      uploads.clear(sentKeys)
      return
    }
    const err = useExecutionStore.getState().executions[key]?.sendError
    if (err && PER_IMAGE_ERRORS.has(err.code) && err.attachmentIndex !== undefined) {
      const failed = natives[err.attachmentIndex]
      if (failed) uploads.markFailed(failed.key, err.code)
    } else if (err?.code === 'attachments_unsupported' && natives.length > 0) {
      // The cached capability was wrong (PR #1527 A2): left `done`, every
      // retry would re-post the same refused body. Refetch it, and send
      // these images by path — the draft stays, and the reader resends once
      // the uploads finish. `request_too_large` keeps its chips: the reason
      // shows, and the reader decides what to drop.
      void useNexHostStore.getState().invalidate(hostId)
      uploads.demote(natives.map((n) => n.key))
    }
  }
  const removeChip = (k: string) => {
    setSendBlock(null)
    uploads.remove(k)
  }
  // A file dragged over the pane must never fall through to the browser's
  // default (Electron would navigate to it), so every file drag is claimed;
  // an ended execution just shows no overlay and takes nothing.
  const canAttach = !ended && !takeBackBusy && !exitBusy
  // The drop target matches the disabled `+` button (worker.upload.attach):
  // while sending, mid-take-back, history still loading or the stream dead,
  // neither offers to attach.
  const canDrop = canAttach && !inputDisabled
  const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files')
  const onDragEnter = (e: DragEvent) => {
    if (!hasFiles(e)) return
    e.preventDefault()
    if (!canDrop) return
    dragDepth.current++
    if (dragDepth.current === 1) setDragging(true)
  }
  const onDragOver = (e: DragEvent) => { if (hasFiles(e)) e.preventDefault() }
  const onDragLeave = (e: DragEvent) => {
    if (!hasFiles(e)) return
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragging(false)
  }
  const onDrop = (e: DragEvent) => {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth.current = 0
    setDragging(false)
    if (canDrop) uploads.add(Array.from(e.dataTransfer.files))
  }
  const errorText = st.sendError
    ? (KNOWN_ERROR_KEYS.has(st.sendError.code) ? t(`execution.error.${st.sendError.code}`)
      : isAttachmentError(st.sendError.code) ? t(uploadErrorKey(st.sendError.code))
      : t('execution.error.generic', { message: st.sendError.message }))
    : null
  // Spec §4.4 R3: dots while the model is silent (own delivered send, or an
  // observed live turn); the typewriter takes over once tokens flow, and a
  // running tool's spinner already shows activity, so no dots beside it.
  // Chat never draws a thought, so a streaming thought keeps its dots on
  // (R2 plan T1.3b); prose or a streaming call switches them off — the call
  // shows as the turn's "Using N tools…" line (R2-B). A running tool switches
  // them off in both views: the room's spinner, chat's tools line (F1,
  // revisited in R2-B — one signal per state, as in the room).
  const chat = mode === 'chat'
  const partialVisible = chat ? partialHasChatContent(st.partial) : partialHasVisibleContent(st.partial)
  const showThinking = (st.turnLive || (st.pendingSend && st.pendingLocal?.delivery !== 'queued'))
    && !partialVisible && !anyRunning
  const queuedTag = st.pendingLocal?.delivery === 'queued'
    && <span className="text-[10px] uppercase font-normal text-text-muted">{t('execution.queued')}</span>
  // The optimistic line's images: the local previews the execution store owns (phase E).
  const pendingPreviews = st.pendingLocal?.attachments
  const pendingThumbs = pendingPreviews && pendingPreviews.length > 0
    ? <PendingAttachmentThumbs items={pendingPreviews} />
    : undefined
  const preludeNode = (
    <PreludeSection view={preludeView} status={st.prelude.status} done={st.prelude.done} error={st.prelude.error}
      keyPrefix={executionId} now={now} mode={chat ? 'chat' : 'room'} pages={st.prelude.pages}
      onLoadOlder={preludeApi.loadOlder} onRetry={preludeApi.retry} />
  )
  const transcriptProps = {
    messages: st.messages, turnStarts: st.turnStarts, turnMeta: st.turnMeta, keyPrefix: executionId, showThinking,
    showEmptyHint: st.messages.length === 0 && !st.pendingLocal, emptyText: t('execution.empty'), scrollKey: st.pendingLocal ? 1 : 0,
    partial: st.partial, tools: st.tools, now, subagentTasks,
    // R3 T3.3: the search bar marks and scrolls inside the transcript, and
    // while it is open a new line never pulls the reader off a match (A4).
    scrollRef: setScrollBox, holdScroll: searchOpen, scrollControl,
    // Spec §6: the pane remembers where its reader was, across remounts and
    // view switches. Keyed by the execution too, not just the pane: a
    // handoff / take-back swaps a pane's content to a different execution
    // while keeping its paneId, and the old memo must not bleed into it
    // (fix round 1, finding 1).
    scrollMemoryKey: `${paneId}:${hostId}:${executionId}`,
    ...(preludeEligible ? { prelude: preludeNode, preludeVersion: `${st.prelude.pages}:${st.prelude.status}` } : {}),
  }

  return (
    <div ref={rootRef} data-testid="execution-view" data-worker-theme={workerTheme.id} style={workerThemeVars as CSSProperties}
      onPointerDownCapture={markInteracted} onFocusCapture={markInteracted}
      onDragEnter={onDragEnter} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}
      className="relative flex flex-col h-full">
      <ExecutionHeader summary={st.summary} cost={cost} hostId={hostId}
        onInterrupt={() => void handleInterrupt()} onExit={onExit} exitDisabled={!exitable || exitBusy || takeBackBusy || writeInFlight} busy={terminal || takeBackBusy || exitBusy}
        onTakeBack={takeOffered ? onTakeBack : undefined} takeBackBusy={takeBusy}
        mode={mode} onModeChange={onModeChange} />
      {confirmExit && (
        <ConfirmDialog testIdPrefix="exit" title={t('worker.exit.confirm_title')} body={t('worker.exit.confirm_running')}
          confirmLabel={t('worker.exit.button')} onCancel={() => setConfirmExit(false)}
          onConfirm={() => { setConfirmExit(false); void runExit() }} />
      )}
      {confirmTakeBack && (
        <ConfirmDialog testIdPrefix="takeback" title={t('takeback.confirm_title')} body={t('takeback.confirm_running')}
          confirmLabel={t('takeback.button')} onCancel={() => setConfirmTakeBack(false)}
          onConfirm={() => { setConfirmTakeBack(false); void runTakeBack() }} />
      )}
      {!st.historyLoaded ? (
        <div data-testid="execution-loading" className="flex-1 flex flex-col items-center justify-center gap-1 text-sm text-text-muted">
          <span>{t('execution.loading')}</span>
          {st.sse === 'closed' && st.sseError && (
            <span data-testid="execution-loading-error" className="text-xs text-status-error">
              {t('execution.loading_error', { message: st.sseError })}
            </span>
          )}
        </div>
      ) : (
        <FoldContext.Provider value={foldStore}>
        <AttachmentSourceContext.Provider value={attachmentSource}>
          {searchOpen && (
            <TranscriptSearch owner={paneId} container={scrollBox} messages={st.messages} tools={st.tools}
              view={chat ? 'chat' : 'room'} keyPrefix={executionId} turnStarts={st.turnStarts}
              onClose={closeSearch} focusRequest={focusRequest} onJump={onSearchJump}
              prelude={preludeEligible && st.prelude.status !== 'idle' && st.prelude.status !== 'none' ? preludeView : undefined}
              preludeDone={st.prelude.done} onLoadAll={() => { preludeApi.retry(); return preludeApi.loadAll() }} />
          )}
          {chat ? (
            <ChatTranscript {...transcriptProps}>
              {/* The optimistic line: your bubble like any other, dimmed until message_accepted (F8). */}
              {st.pendingLocal && (
                <ChatUserBubble text={st.pendingLocal.text} pending attachments={pendingThumbs}>{queuedTag}</ChatUserBubble>
              )}
            </ChatTranscript>
          ) : (
            <RoomTranscript {...transcriptProps}>
              {/* The optimistic line: a user line like any other, dimmed until message_accepted (spec §4.1). */}
              {st.pendingLocal && (
                <RoomUserLine text={st.pendingLocal.text} pending attachments={pendingThumbs}>{queuedTag}</RoomUserLine>
              )}
            </RoomTranscript>
          )}
        </AttachmentSourceContext.Provider>
        </FoldContext.Provider>
      )}
      {/* Worker pane spec §4.6 (Q3): the worker's current state, inside the pane, above the input. Chat has none (spec §5). */}
      {!chat && <WorkerDock sse={st.sse} observers={st.summary?.observers ?? 0} lease={st.summary?.lease} isMine={isMine} tasks={dockTasks} onInspect={onInspectTask} />}
      {leaseHeld && (
        <div data-testid="lease-held" className="mx-2 mb-1 text-xs text-status-warning">
          {t('execution.lease_held', { principal: st.leaseError?.heldBy ?? '' })}
        </div>
      )}
      {errorText && <div data-testid="send-error" className="mx-2 mb-1 text-xs text-status-error">{errorText}</div>}
      {sendBlock && (
        <div data-testid="send-block" role="status" aria-live="polite" className="mx-2 mb-1 text-xs text-status-error">
          {t(`worker.upload.${sendBlock}`)}
        </div>
      )}
      {/* R3 T2.1: part of the input, so in chat too. A tap sends at once and
          never restores a draft — that would remount the input over what is typed. */}
      <QuickReplyDock replies={quickReplies} onSend={(text) => { sendWithAttachments(text, { restoreDraft: false }) }}
        disabled={inputDisabled || !attachGate.ok} />
      <WorkerInput key={draft ?? ''} initialValue={draft ?? undefined} onSend={(text) => sendWithAttachments(text, { draftText: text })}
        disabled={inputDisabled} pendingSend={st.pendingSend} placeholder={placeholder} isActive={isActive} isFocusTarget={isFocusTarget} onTextChange={onTextChange}
        chips={uploads.chips} onRemoveChip={removeChip} onAddFiles={canAttach ? uploads.add : undefined} />
      {dragging && (
        <div data-testid="drop-overlay"
          className="absolute inset-0 flex flex-col items-center justify-center gap-2 pointer-events-none z-20"
          style={{ background: 'rgba(0, 0, 0, 0.6)' }}>
          <UploadSimple size={32} className="text-text-secondary" />
          <span className="text-text-secondary text-sm">{t('upload.drop_files')}</span>
        </div>
      )}
    </div>
  )
}
