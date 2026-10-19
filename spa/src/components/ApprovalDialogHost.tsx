// spa/src/components/ApprovalDialogHost.tsx — the one approval dialog (lead-team spec §6.3, §8.7), mounted once with the
// app-level overlays, next to HandoffDialogHost. It renders the oldest open request across hosts from
// `useApprovalStore`, which the `approval.request` WS branch and the daemon's snapshot feed (P3b); several requests
// queue, one dialog at a time. The body switches on `kind`: `lead` (reason, editable grant) and `self_relay` (the
// session, its usage and what approving does — P5a).
//
// It cannot be dismissed: no Escape, no backdrop click. A request ends by a decision — 核准 / 拒絕, one click each on any
// Purdex.app (U5b, U13a) — or by the daemon closing it (decided elsewhere, timeout, cancel), which removes it from the
// store and unmounts this. While the host is not connected (spec §9.4) the buttons dim under `daemon 重啟中…`; a click
// then is queued in the store and sent when the reconnect snapshot re-adds the request (P3b). A send that fails on the
// network while the host is still connected is not queued (nothing would resend it): it toasts and the buttons come back.
//
// 核准 only approves; 「核准並檢視」 (shown when the request names a tmux session to look at) approves and then goes to
// the requester's tab (U22 (a)); 拒絕 and a decision queued while the daemon was down never switch.
//
// Focus: the dialog takes focus on open on 核准 (the panel itself when 核准 is disabled) and Tab stays inside, as
// ConfirmDialog does, so a stray keystroke never reaches the pane behind. The Enter a person presses at the terminal
// the moment the dialog appears would then approve: for 500 ms after the dialog shows, a click made with the keyboard
// (`detail === 0`) is ignored; a mouse click never is; focus that something behind takes anyway (a tab switch, a terminal's late focus) is pulled
// back; Escape is swallowed so a dialog beneath does not dismiss. The i18n strings are the spec's.
//
// 縮小 (U22 (b)): the dialog can be minimized to a corner pill (ApprovalPill). Minimized, it stays MOUNTED and hidden —
// the grant edits and the 不再詢問 tick live in this component's state — and it is not modal: the Escape swallow, the
// Tab trap and the focus guard are off, and the keyboard goes back to where it was before the dialog took it. Only a
// click restores it (the pill, or the approval notification); a new request never does. `minimized` is per window and
// not persisted (useApprovalStore).
import { useEffect, useId, useRef, useState } from 'react'
import { ArrowsClockwise, ArrowsInSimple } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { approvalKey, selectCurrent, selectOpenCount, useApprovalStore, type ApprovalEntry, type Decision } from '../stores/useApprovalStore'
import { ApprovalPill } from './ApprovalPill'
import { AdoptionWaitCard } from './AdoptionWaitCard'
import { adoptionAlias } from '../lib/team/adoption-wait'
import { hostLabel, useHostLook } from '../lib/host-look'
import { deriveTeamLabel, goTrim, labelProblem, TEAM_LABEL_MAX_WIDTH } from '../lib/team/label'
import { cellWidth } from '../lib/textwidth'
import { adoptPayloadOf, adoptTargetLabel, clipForDisplay, leadPayloadOf, maxMembersMax, selfRelayPayloadOf, DEFAULT_MAX_MEMBERS, type Grant, type LeadNameCandidate } from '../lib/team/types'
import { fetchLeadNames } from '../lib/team/approval-api'
import { LeadNameCard } from './LeadNameCard'
import { useCardUnanswered } from '../lib/team/approval-unanswered'
import { startUnansweredReread } from '../lib/team/approval-reread'
import { approvalSessionLabel, formatCountdown, formatOriginAddress, memberRelayNames } from '../lib/team/approval-format'
import { buttonBase, fieldClass, keyboardGuard, parseRoots, tabStops, teamNameOk } from './approval-dialog-helpers'
import { submitDecision, subscribeNameTaken } from '../lib/team/approval-decide'
import { parseOriginTmux } from '../lib/team/approval-goto'
import { RelayBrief, RelayTitle } from './approval/RelayBrief'


export function ApprovalDialogHost() {
  const current = useApprovalStore(selectCurrent)
  const minimized = useApprovalStore((s) => s.minimized)
  // A card that ran out while open is read again by this host (its daemon sends no event for that); see approval-reread.ts.
  useEffect(() => startUnansweredReread(), [])
  // The remote-adopt wait card outlives the (closed) approval, so it is mounted whether or not a request is open.
  if (!current) return <AdoptionWaitCard />
  // Keyed by the request: the next one is a fresh dialog (the payload's defaults, nothing in flight). Minimizing does
  // not change the key, so the dialog is hidden, not unmounted.
  return (
    <>
      <OpenApprovalDialog key={approvalKey(current.hostId, current.approval.id)} entry={current} minimized={minimized} />
      {minimized && <ApprovalPill />}
      <AdoptionWaitCard />
    </>
  )
}

function OpenApprovalDialog({ entry, minimized }: { entry: ApprovalEntry; minimized: boolean }) {
  const { hostId, approval } = entry
  const t = useI18nStore((s) => s.t)
  const hostName = hostLabel(hostId, useHostLook(hostId))
  const connected = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const queued = useApprovalStore((s) => s.queued[approvalKey(hostId, approval.id)])
  const openCount = useApprovalStore(selectOpenCount)
  const isSelfRelay = approval.kind === 'self_relay'
  // An adoption (U24): the lead asks to take a session in. One click, like a relay: no grant inputs.
  const isAdopt = approval.kind === 'adopt'
  // A member relay waiting because the lead's member pool is out (RQ-2): one click too, no grant, nothing to remember.
  const isMemberRelay = approval.kind === 'member_relay'
  const memberRelay = memberRelayNames(approval)
  const adopt = adoptPayloadOf(approval)
  // The member ceiling follows the host that asked: 9 against team.max_members.v2, else 8 (name pool v2 §5.1).
  const ceiling = maxMembersMax(useUnattendedStore((s) => s.byHost[hostId]?.maxMembersV2Support === 'yes'))
  const payload = leadPayloadOf(approval, ceiling)
  const relay = selfRelayPayloadOf(approval)
  const [maxMembers, setMaxMembers] = useState(String(DEFAULT_MAX_MEMBERS)) // U25: always 3; the lead's request is only named beside the field
  // Shown only when the payload has a team_name (a daemon that knows names, D-N10), prefilled with the requested one.
  // Kept here beside the member limit, so minimize / restore (the dialog stays mounted) keeps the edit.
  const [teamName, setTeamName] = useState(payload.team_name ?? '')
  // The short label (team-label D-L10): shown only when the payload has a team_label (a daemon that knows labels),
  // prefilled with the requested one; while it is empty the placeholder says what the daemon will take from the name.
  const [teamLabel, setTeamLabel] = useState(payload.team_label ?? '')
  const [rootsText, setRootsText] = useState(payload.roots.join('\n'))
  // The 「名字」 row (name pool v2 §5.3): only for a lead request that carries a candidate list. The stored list is the fallback; a fresh one is read when the dialog
  // opens and replaces it, and a `name_taken` answer brings another. The pick (null = 「保留」) is dropped when a refreshed list no longer has it.
  const [leadNames, setLeadNames] = useState<LeadNameCandidate[] | undefined>(payload.lead_names)
  const [pickedName, setPickedName] = useState<string | null>(null)
  const [nameTaken, setNameTaken] = useState(false)
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  // Nobody answered this card: no countdown, the session is paused (relay v2 App contract 3). Also true for a card held since
  // `opened` that ran out while open, once the pause data says so.
  const unanswered = useCardUnanswered(hostId, approval, now)
  // Ref, not state: two clicks in one event burst both see `busy === false` before React commits the first setBusy.
  const inFlight = useRef(false)
  const overlayRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const approveRef = useRef<HTMLButtonElement>(null)
  // True for `keyboardGuard.ms` after the dialog shows (or is restored): a keyboard-triggered click is ignored meanwhile.
  const keyboardGuarded = useRef(false)
  // Where the keyboard was when the panel last took it; null while the panel has not taken it (mounted minimized).
  const focusBefore = useRef<{ el: Element | null } | null>(null)

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  // Shown (on open, or restored from the pill): remember where the keyboard was, then take it. Minimized: give it back
  // to that element when it is still in the document, else drop it — a hidden panel must not keep the focus. A dialog
  // that mounts already minimized (the current request changed behind the pill) never took it, so it touches nothing.
  // Focus already inside the dialog is never "where it was": StrictMode (the dev server the app loads) runs this effect
  // twice, and the second run finds the panel focused by the first.
  useEffect(() => {
    const inDialog = (el: Element | null) => el !== null && overlayRef.current?.contains(el) === true
    if (!minimized) {
      const active = document.activeElement
      focusBefore.current = { el: inDialog(active) ? (focusBefore.current?.el ?? null) : active }
      // The keyboard guard runs from here (also from a restore): see the header.
      keyboardGuarded.current = keyboardGuard.ms > 0
      const guardTimer = setTimeout(() => { keyboardGuarded.current = false }, keyboardGuard.ms)
      // 核准 when it can be pressed, else the panel (a lead form that does not validate yet).
      const approve = approveRef.current
      ;(approve && !approve.disabled ? approve : panelRef.current)?.focus()
      return () => clearTimeout(guardTimer)
    }
    const took = focusBefore.current
    focusBefore.current = null
    if (!took) return
    const back = took.el
    if (back instanceof HTMLElement && back !== document.body && back.isConnected && !inDialog(back)) {
      back.focus()
      if (document.activeElement === back) return
    }
    const active = document.activeElement
    if (active instanceof HTMLElement && inDialog(active)) active.blur()
  }, [minimized])

  // Escape is swallowed, not handled: nothing dismisses this dialog, and nothing beneath it may be dismissed either
  // (ConfirmDialog and FloatingPanel both listen for Escape on `document`; a handoff confirm under this modal would
  // otherwise cancel). Capture phase on `window`, not `document`: capture listeners on one target run in registration
  // order, and the dialog beneath registered first — `window` capture runs before every `document` listener regardless.
  // Not while minimized: Escape then belongs to the tabs (U22 (b)), as do Tab and focus below.
  useEffect(() => {
    if (minimized) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      e.stopImmediatePropagation()
    }
    window.addEventListener('keydown', onKey, { capture: true })
    return () => window.removeEventListener('keydown', onKey, { capture: true })
  }, [minimized])

  // Tab stays inside the panel (ConfirmDialog's rule).
  useEffect(() => {
    if (minimized) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return
      const panel = panelRef.current
      if (!panel) return
      e.preventDefault()
      const stops = tabStops(panel)
      if (stops.length === 0) {
        panel.focus()
        return
      }
      const at = stops.indexOf(document.activeElement as HTMLElement)
      const next = at === -1
        ? (e.shiftKey ? stops.length - 1 : 0)
        : (at + (e.shiftKey ? stops.length - 1 : 1)) % stops.length
      stops[next].focus()
    }
    document.addEventListener('keydown', onKey, { capture: true })
    return () => document.removeEventListener('keydown', onKey, { capture: true })
  }, [minimized])

  // Focus stays inside the panel (P9b-1 review). Something behind this modal can still take focus: the switch to the
  // requester after a decision (U22, approval-goto.ts) shows a tab while the next request's dialog is already up, a
  // notification click does the same, and a terminal focuses in the NEXT animation frame (TerminalView's
  // `useActivationFocus(…, { raf: true })`) — after this panel took focus. Its keystrokes would reach the shell under
  // the overlay, so any focus that lands outside the panel is pulled back. `panel.focus()` fires a `focusin` inside the
  // panel, which this leaves alone, so the two cannot ping-pong. Same lifetime as the Escape swallow and the Tab trap:
  // off while minimized, where a terminal the person clicks keeps the focus (U22 (b)).
  useEffect(() => {
    if (minimized) return
    const panel = panelRef.current
    if (!panel) return
    const onFocusIn = (e: FocusEvent) => {
      if (e.target instanceof Node && panel.contains(e.target)) return
      panel.focus()
    }
    document.addEventListener('focusin', onFocusIn, { capture: true })
    return () => document.removeEventListener('focusin', onFocusIn, { capture: true })
  }, [minimized])

  const members =maxMembers.trim() === '' ? NaN : Number(maxMembers)
  const membersOk = Number.isInteger(members) && members >= 1 && members <= ceiling
  const membersErrorId = useId()
  const roots = parseRoots(rootsText)
  const rootsOk = roots.length > 0
  const leadNameShown = approval.kind === 'lead' && leadNames !== undefined && leadNames.length > 0
  // Every list that lands bumps the generation; a read that went out earlier and comes back after a newer list (the open-time read after a `name_taken`) is dropped.
  const namesGen = useRef(0)
  const refreshLeadNames = (names: LeadNameCandidate[]) => {
    namesGen.current += 1
    setLeadNames(names)
    setPickedName((p) => (p !== null && names.some((n) => n.code === p) ? p : null))
  }
  useEffect(() => {
    if (approval.kind !== 'lead' || payload.lead_names === undefined) return
    let live = true
    const gen = namesGen.current
    fetchLeadNames(hostId, approval.id).then((names) => { if (live && namesGen.current === gen) refreshLeadNames(names) }, () => {}) // a failed read keeps the stored list
    return () => { live = false }
    // once per dialog: it is keyed by the approval
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  // The card's answer to a `name_taken`: the list from the refusal (or read again when it came without one), the pick dropped when it is gone, one line above the pills.
  const onNameTaken = (names: LeadNameCandidate[] | undefined) => {
    setNameTaken(true)
    if (names !== undefined) refreshLeadNames(names)
    else {
      const gen = namesGen.current
      void fetchLeadNames(hostId, approval.id).then((fresh) => { if (namesGen.current === gen) refreshLeadNames(fresh) }, () => {})
    }
  }
  // A decision made offline is sent by the reconnect, not by this dialog: its refusal reaches the card the same way.
  const onTakenRef = useRef(onNameTaken)
  onTakenRef.current = onNameTaken
  useEffect(() => subscribeNameTaken((h, id, names) => { if (h === hostId && id === approval.id) onTakenRef.current(names) }), [hostId, approval.id])
  const nameShown = !isSelfRelay && !isAdopt && !isMemberRelay && payload.team_name !== undefined
  const trimmedName = goTrim(teamName)
  const nameOk = !nameShown || teamNameOk(trimmedName)
  const nameErrorId = useId()
  const labelShown = !isSelfRelay && !isAdopt && !isMemberRelay && payload.team_label !== undefined
  const trimmedLabel = goTrim(teamLabel)
  const labelBad = labelShown ? labelProblem(trimmedLabel) : null
  const labelOk = labelBad === null
  const labelErrorId = useId()
  const labelCountId = useId()
  // What an empty label becomes (D-L3): derived from the name as it stands in the dialog (edited or not), else 「（無）」.
  const derivedLabel = labelShown ? deriveTeamLabel(nameShown ? trimmedName : (payload.team_name ?? '')) : ''
  // A self relay and an adoption carry no grant (U13a: one click); only the lead kind validates its fields.
  const grantOk = isSelfRelay || isAdopt || isMemberRelay || (membersOk && rootsOk && nameOk && labelOk)
  const locked = busy || queued !== undefined

  // A button the person just pressed turns disabled while the decision is in flight or queued, and a disabled element
  // gives its focus up to the body — where the focus guard (it only reacts to a focus landing elsewhere) cannot see it
  // and the keys would reach the pane behind. Take it back to the panel.
  useEffect(() => {
    if (minimized || !locked) return
    const active = document.activeElement
    const lost = active === null || active === document.body || (active instanceof HTMLButtonElement && active.disabled)
    if (lost) panelRef.current?.focus()
  }, [locked, minimized])
  const session = approvalSessionLabel(approval.origin)

  // Only 核准並檢視 has somewhere to go: an origin outside tmux names no session to look at.
  const canReveal = parseOriginTmux(approval.origin.tmux) !== null
  // The click handler of all three buttons: the keyboard guard, then the decision.
  const onButton = (e: React.MouseEvent, decision: Decision, reveal = false) => {
    if (e.detail === 0 && keyboardGuarded.current) return
    void decide(decision, reveal)
  }

  const decide = async (decision: Decision, reveal = false) => {
    if (inFlight.current || locked) return
    if (decision === 'approve' && !grantOk) return
    const grant: Grant | undefined = !isSelfRelay && !isAdopt && !isMemberRelay && decision === 'approve' ? { max_members: members, roots, ...(nameShown ? { team_name: trimmedName } : {}), ...(labelShown ? { team_label: trimmedLabel } : {}), ...(leadNameShown && pickedName !== null ? { lead_name: pickedName } : {}) } : undefined
    if (!connected) {
      // A second click in the same tick still sees locked=false (React has not re-rendered the queued state):
      // the store is read synchronously so the FIRST queued decision wins (PR #1742 attacker A-2).
      if (useApprovalStore.getState().queued[approvalKey(hostId, approval.id)]) return
      // Spec §9.4: kept locally, sent on reconnect (the snapshot re-adds the request, or shows it gone).
      useApprovalStore.getState().queueDecision(hostId, approval, decision, grant)
      return
    }
    inFlight.current = true
    setBusy(true)
    const outcome = await submitDecision(hostId, approval, decision, grant, {
      ...(reveal ? { reveal } : {}),
      onNameTaken,
    })
    // 'closed' and 'decided_elsewhere' unmount this dialog through the store; the other two keep it.
    if (outcome === 'failed' || outcome === 'queued') {
      inFlight.current = false
      setBusy(false)
    }
  }

  const titleId = 'approval-dialog-title'
  return (
    <div
      ref={overlayRef}
      // Minimized: hidden, not unmounted (Tailwind's preflight keeps `[hidden]` at display:none over `flex`).
      hidden={minimized}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-testid="approval-dialog"
      data-kind={approval.kind}
      // The backdrop also covers the Electron title bar, a window drag region that would otherwise swallow clicks there.
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        data-testid="approval-panel"
        className="w-[480px] rounded-lg border border-border-default bg-surface-primary shadow-lg outline-none"
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <div className="flex items-start justify-between gap-3">
            <h3 id={titleId} className="break-words text-sm font-medium text-text-primary">
              {isAdopt
                ? t('approval.dialog.title_adopt', { host: hostName, lead: session, target: clipForDisplay(adoptTargetLabel(adopt), 60) })
                : isMemberRelay
                  ? t('approval.dialog.title_member_relay', { lead: clipForDisplay(memberRelay.lead, 60), member: clipForDisplay(memberRelay.member, 60), pct: memberRelay.pct })
                  : t(isSelfRelay ? 'approval.dialog.title_self_relay' : 'approval.dialog.title_lead', { host: hostName, session })}
            </h3>
            {/* Never disabled: minimizing during a send is harmless (the outcome lands in the store either way). */}
            <button
              type="button"
              data-testid="approval-minimize"
              onClick={() => useApprovalStore.getState().setMinimized(true)}
              className={`${buttonBase} shrink-0 text-text-secondary hover:bg-surface-hover`}
            >
              <ArrowsInSimple size={12} aria-hidden="true" />
              {t('approval.dialog.minimize')}
            </button>
          </div>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-text-muted">{t('approval.dialog.host')}</dt>
            <dd data-testid="approval-host" className="text-text-primary">{hostName}</dd>
            <dt className="text-text-muted">{t('approval.dialog.session')}</dt>
            <dd data-testid="approval-session" className="text-text-primary">{session}</dd>
            <dt className="text-text-muted">{t('approval.dialog.address')}</dt>
            <dd data-testid="approval-address" className="font-mono text-text-primary">{formatOriginAddress(hostName, approval.origin)}</dd>
            {isSelfRelay && (
              <>
                <dt className="text-text-muted">{t('approval.dialog.ref')}</dt>
                <dd className="text-text-primary"><span data-testid="approval-ref" className="font-mono">{approval.origin.ref}</span><RelayTitle title={approval.origin.title ?? ''} reask={relay.reask} /></dd>
              </>
            )}
            <dt className="text-text-muted">{t('approval.dialog.cwd')}</dt>
            <dd data-testid="approval-cwd" className="font-mono break-all text-text-primary">{approval.origin.cwd}</dd>
            {isAdopt ? (
              <>
                <dt className="text-text-muted">{t('approval.dialog.adopt_target')}</dt>
                {/* The title and name are written by the target session: an alias, never the identity. The ref and the
                    session id are the daemon's, shown beside it; every string is clipped and may wrap anywhere. */}
                <dd data-testid="approval-adopt-target" dir="auto" className="break-all text-text-primary">{clipForDisplay(adoptTargetLabel(adopt), 80)}</dd>
                {adopt.target_host_id !== '' && (
                  <>
                    <dt className="text-text-muted">{t('approval.dialog.adopt_target_host')}</dt>
                    <dd data-testid="approval-adopt-host" dir="auto" className="break-all text-text-primary">{t('approval.dialog.adopt_on_host', { alias: adoptionAlias(adopt) })}</dd>
                  </>
                )}
                <dt className="text-text-muted">{t('approval.dialog.adopt_target_ref')}</dt>
                <dd data-testid="approval-adopt-ref" className="font-mono break-all text-text-primary">{adopt.target_ref !== '' ? clipForDisplay(adopt.target_ref, 40) : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.adopt_target_session')}</dt>
                <dd data-testid="approval-adopt-session" className="font-mono break-all text-text-primary">{adopt.target_session_id !== '' ? clipForDisplay(adopt.target_session_id, 64) : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.adopt_target_address')}</dt>
                <dd data-testid="approval-adopt-address" dir="auto" className="font-mono break-all text-text-primary">{adopt.target_address !== '' ? clipForDisplay(adopt.target_address) : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.adopt_target_cwd')}</dt>
                <dd data-testid="approval-adopt-cwd" dir="auto" className="font-mono break-all text-text-primary">{adopt.target_cwd !== '' ? clipForDisplay(adopt.target_cwd) : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.adopt_target_tmux')}</dt>
                <dd data-testid="approval-adopt-tmux" dir="auto" className="font-mono break-all text-text-primary">{adopt.target_tmux !== '' ? clipForDisplay(adopt.target_tmux) : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.adopt_team')}</dt>
                <dd data-testid="approval-adopt-team" className="font-mono break-all text-text-primary">{adopt.team_id !== '' ? clipForDisplay(adopt.team_id, 64) : '—'}</dd>
              </>
            ) : isMemberRelay ? (
              <>
                {/* The titles are written by the sessions: aliases, never the identity (the ref and ids are the daemon's). */}
                <dt className="text-text-muted">{t('approval.dialog.member')}</dt>
                <dd data-testid="approval-member" dir="auto" className="break-all text-text-primary">{clipForDisplay(memberRelay.member, 80) || '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.member_ref')}</dt>
                <dd className="break-all text-text-primary"><span data-testid="approval-member-ref" className="font-mono">{memberRelay.payload.member_ref !== '' ? clipForDisplay(memberRelay.payload.member_ref, 40) : '—'}</span><RelayTitle title={memberRelay.payload.member_title} /></dd>
                <dt className="text-text-muted">{t('approval.dialog.usage')}</dt>
                <dd data-testid="approval-usage" className="text-text-primary">{t('approval.dialog.usage_value', { pct: memberRelay.pct })}</dd>
              </>
            ) : isSelfRelay ? (
              <>
                {/* An idle-relay ask (spec §6): why it is asked now, above the usage. Only for a well-formed payload. */}
                {relay.reason === 'idle' && relay.idle_minutes !== undefined && (
                  <div className="col-span-2">
                    <dt className="sr-only">{t('approval.dialog.idle_label')}</dt>
                    <dd data-testid="approval-idle-note" className="text-text-primary">{t('approval.dialog.idle_note', { n: relay.idle_minutes })}</dd>
                  </div>
                )}
                <dt className="text-text-muted">{t('approval.dialog.usage')}</dt>
                <dd data-testid="approval-usage" className="text-text-primary">{t('approval.dialog.usage_value', { pct: Math.round(relay.used_percentage) })}</dd>
              </>
            ) : (
              <>
                <dt className="text-text-muted">{t('approval.dialog.tmux')}</dt>
                <dd data-testid="approval-tmux" className="font-mono text-text-primary">{approval.origin.tmux !== '' ? approval.origin.tmux : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.reason')}</dt>
                <dd data-testid="approval-reason" className="whitespace-pre-wrap text-text-primary">{payload.reason}</dd>
              </>
            )}
            {unanswered ? (
              <>
                <dt className="text-text-muted">{t('approval.dialog.unanswered_label')}</dt>
                <dd data-testid="approval-unanswered" className="text-text-primary">{t('approval.dialog.unanswered')}</dd>
              </>
            ) : (
              <>
                <dt className="text-text-muted">{t('approval.dialog.deadline')}</dt>
                <dd data-testid="approval-countdown" className="font-mono text-text-primary">{formatCountdown(approval.deadline_at - now)}</dd>
              </>
            )}
          </dl>
          {isSelfRelay && <RelayBrief hostId={hostId} sessionId={approval.origin.session_id} />}
          {isMemberRelay && <RelayBrief hostId={hostId} sessionId={memberRelay.payload.member_session_id} />}
          {isAdopt ? (
            <p data-testid="approval-adopt-note" className="mt-3 text-xs text-text-secondary">{adopt.target_host_id !== '' ? t('approval.dialog.adopt_note_remote', { alias: adoptionAlias(adopt) }) : t('approval.dialog.adopt_note')}</p>
          ) : isMemberRelay ? (
            <p data-testid="approval-member-relay-note" className="mt-3 text-xs text-text-secondary">{t('approval.dialog.member_relay_note')}</p>
          ) : isSelfRelay ? (
            <p data-testid="approval-self-relay-note" className="mt-3 text-xs text-text-secondary">{t('approval.dialog.self_relay_note')}</p>
          ) : (
            <>
              {leadNameShown && leadNames !== undefined && (
                <LeadNameCard hostId={hostId} currentName={approval.origin.name} names={leadNames} pick={pickedName} onPick={(c) => { setPickedName(c); setNameTaken(false) }} taken={nameTaken} disabled={locked} />
              )}
              {nameShown && (
                <>
                  <label className="mt-3 flex items-center gap-2 text-xs text-text-secondary">
                    <span className="shrink-0">{t('approval.dialog.team_name')}</span>
                    <input
                      type="text"
                      value={teamName}
                      placeholder={t('approval.dialog.team_name_placeholder')}
                      disabled={locked}
                      onChange={(e) => setTeamName(e.target.value)}
                      aria-invalid={nameOk ? undefined : true}
                      aria-describedby={nameOk ? undefined : nameErrorId}
                      data-testid="approval-team-name"
                      className={`min-w-0 flex-1 ${fieldClass}`}
                    />
                  </label>
                  {!nameOk && (
                    <p id={nameErrorId} role="alert" data-testid="approval-team-name-error" className="mt-1 text-xs text-status-warning-text">{t('approval.dialog.team_name_invalid')}</p>
                  )}
                </>
              )}
              {labelShown && (
                <>
                  <div className={`${nameShown ? 'mt-2' : 'mt-3'} flex items-center gap-2 text-xs text-text-secondary`}>
                    <label className="flex min-w-0 flex-1 items-center gap-2">
                      <span className="shrink-0">{t('approval.dialog.team_label')}</span>
                      <input
                        type="text"
                        value={teamLabel}
                        placeholder={derivedLabel !== '' ? derivedLabel : t('approval.dialog.team_label_none')}
                        disabled={locked}
                        onChange={(e) => setTeamLabel(e.target.value)}
                        aria-invalid={labelOk ? undefined : true}
                        aria-describedby={labelOk ? labelCountId : `${labelCountId} ${labelErrorId}`}
                        data-testid="approval-team-label"
                        className={`min-w-0 flex-1 ${fieldClass}`}
                      />
                    </label>
                    <span id={labelCountId} data-testid="approval-team-label-width" className={`shrink-0 tabular-nums ${labelOk ? 'text-text-muted' : 'text-status-warning-text'}`}>
                      {cellWidth(trimmedLabel)}/{TEAM_LABEL_MAX_WIDTH}
                    </span>
                  </div>
                  {!labelOk && (
                    <p id={labelErrorId} role="alert" data-testid="approval-team-label-error" className="mt-1 text-xs text-status-warning-text">{t(`approval.dialog.team_label_invalid_${labelBad}`)}</p>
                  )}
                </>
              )}
              <label className={`${nameShown || labelShown ? 'mt-2' : 'mt-3'} flex items-center gap-2 text-xs text-text-secondary`}>
                {t('approval.dialog.max_members', { max: ceiling })}
                <input
                  type="number"
                  min={1}
                  max={ceiling}
                  step={1}
                  value={maxMembers}
                  disabled={locked}
                  onChange={(e) => setMaxMembers(e.target.value)}
                  aria-invalid={membersOk ? undefined : true}
                  aria-describedby={membersOk ? undefined : membersErrorId}
                  data-testid="approval-max-members"
                  className={`w-16 ${fieldClass}`}
                />
                {payload.max_members !== DEFAULT_MAX_MEMBERS && (
                  <span data-testid="approval-max-members-requested">{t('approval.dialog.max_members_requested', { n: payload.max_members })}</span>
                )}
              </label>
              {!membersOk && (
                <p id={membersErrorId} role="alert" data-testid="approval-max-members-error" className="mt-1 text-xs text-status-warning-text">{t('approval.dialog.max_members_range', { max: ceiling })}</p>
              )}
              <label className="mt-2 block text-xs text-text-secondary">
                {t('approval.dialog.roots')}
                <textarea
                  rows={3}
                  value={rootsText}
                  disabled={locked}
                  onChange={(e) => setRootsText(e.target.value)}
                  data-testid="approval-roots"
                  className={`mt-1 block w-full font-mono ${fieldClass}`}
                />
              </label>
              {!rootsOk && (
                <p data-testid="approval-roots-error" className="mt-1 text-xs text-status-warning-text">{t('approval.dialog.roots_required')}</p>
              )}
            </>
          )}
          {!connected && (
            <p data-testid="approval-disconnected" className="mt-2 text-xs text-status-warning-text">{t('approval.dialog.daemon_restarting')}</p>
          )}
          {queued && (
            <p data-testid="approval-queued" className="mt-1 text-xs text-text-muted">
              {t('approval.dialog.queued', { decision: t(queued.decision === 'approve' ? 'approval.dialog.approve' : 'approval.dialog.deny') })}
            </p>
          )}
          {openCount > 1 && (
            <p data-testid="approval-more" className="mt-2 text-xs text-text-muted">{t('approval.dialog.more_pending', { count: openCount - 1 })}</p>
          )}
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            type="button"
            data-testid="approval-deny"
            disabled={locked}
            aria-disabled={!connected || undefined}
            onClick={(e) => onButton(e, 'deny')}
            className={`${buttonBase} text-text-secondary hover:bg-surface-hover`}
          >
            {t('approval.dialog.deny')}
          </button>
          {canReveal && (
            <button
              type="button"
              data-testid="approval-approve-view"
              disabled={locked || !grantOk}
              aria-disabled={!connected || undefined}
              onClick={(e) => onButton(e, 'approve', true)}
              className={`${buttonBase} border border-border-default text-text-primary hover:bg-surface-hover`}
            >
              {t('approval.dialog.approve_and_view')}
            </button>
          )}
          <button
            ref={approveRef}
            type="button"
            data-testid="approval-approve"
            disabled={locked || !grantOk}
            aria-disabled={!connected || undefined}
            onClick={(e) => onButton(e, 'approve')}
            className={`${buttonBase} bg-accent text-white`}
          >
            {busy && <ArrowsClockwise size={12} aria-hidden="true" className="animate-spin" />}
            {t('approval.dialog.approve')}
          </button>
        </div>
      </div>
    </div>
  )
}
