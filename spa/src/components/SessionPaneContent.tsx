import { useEffect, useMemo } from 'react'
import TerminalView from './TerminalView'
import { TerminatedPane } from './TerminatedPane'
import { MissingHostPane } from './MissingHostPane'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { fetchWsTicket } from '../lib/host-api'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { selectSessionView, sessionBinding, useSessionViewStore } from '../stores/useSessionViewStore'
import { useAttachStall } from '../hooks/useAttachStall'
import { useConversationOfPane } from '../hooks/useConversationOfPane'
import { useSendQueueDriver } from '../hooks/useSendQueueDriver'
import { useConversationViewGate } from '../hooks/useConversationViewGate'
import { ChatPane } from './deck/ChatPane'
import { AskStrip } from './dock/AskStrip'
import { QuestionDock } from './dock/QuestionDock'
import { openAsks } from '../lib/conversations/asks'
import { DeckPane } from './deck/DeckPane'
import { SessionInput } from './deck/SessionInput'
import { SessionStatusRow } from './deck/SessionStatusRow'
import type { DeckFooterContext } from './deck/footer-context'
import { retireStaleSessions } from '../lib/conversations/pane-release'
import { findPane } from '../lib/pane-tree'
import { probeSessionCwd } from '../lib/rebuild/cwd-probe'
import { probeSessionProvenance } from '../lib/rebuild/provenance-probe'
import type { PaneRendererProps } from '../lib/module-registry'

export function SessionPaneContent({ pane, isActive, isFocusTarget = false }: PaneRendererProps) {
  const content = pane.content
  const sessionCode = content.kind === 'tmux-session' ? content.sessionCode : ''
  const hostId = content.kind === 'tmux-session' ? content.hostId : ''
  const tmuxInstance = content.kind === 'tmux-session' ? content.tmuxInstance : ''
  const terminated = content.kind === 'tmux-session' ? content.terminated : undefined

  const wsBase = useHostStore((s) => s.getWsBase(hostId))
  // A reference this device cannot resolve (host ownership spec §3.2) is kept
  // verbatim and rendered as missing. It must be checked before anything that
  // reaches the network: `getWsBase` falls back to the active host for an
  // unknown id, so attaching would open a terminal on the WRONG host.
  // `Object.hasOwn`, not a lookup: `toString` / `__proto__` & co. are not hosts.
  const hostKnown = useHostStore((s) => hostId !== '' && Object.hasOwn(s.hosts, hostId))

  // Second of the two cwd-probe triggers (spec §4.4): a pane opened after the
  // session list has settled gets no further `sessions` broadcast, so it would
  // otherwise never learn its own directory. Once per binding — the probe
  // itself deduplicates against the sessions-branch sweep.
  //
  // Gated on the same attach gate as the terminal WS (spec §4.6.2). The other
  // trigger fires from inside the reconciliation that opens the gate, so only
  // this one can run against a connection that has not yet proved which
  // generation owns the pane's code. Subscribed rather than merely read, so
  // the probe fires when the gate opens under an already-mounted pane.
  const attachGateOpen = useHostStore((s) => (hostId ? s.runtime[hostId]?.attachReady === true : true))
  useEffect(() => {
    if (terminated) return
    if (!hostKnown) return
    if (!attachGateOpen) return
    probeSessionCwd(hostId, sessionCode, tmuxInstance)
    // The third provenance trigger (spec §5.4), under the same gate and the
    // same binding: a pane opened after the list settled gets no sweep, and a
    // session whose agent is silent sends no hook, so without this one nothing
    // would ever ask on its behalf. The probe itself decides whether this
    // binding still wants an answer.
    probeSessionProvenance(hostId, sessionCode, tmuxInstance)
  }, [hostId, sessionCode, tmuxInstance, terminated, hostKnown, attachGateOpen])

  // #1474: a reachable host whose session list never arrives keeps the gate
  // shut, and the terminal would say "connecting..." forever. Called before the
  // early returns below (rules of hooks); the message only shows while the
  // terminal is not yet attached, which is the only time its overlay is up.
  const t = useI18nStore((s) => s.t)
  const attachStalled = useAttachStall(hostKnown ? hostId : '')

  // Look up tabId from store (pane renderers don't receive tabId as a prop)
  const tabId = useTabStore((s) => {
    for (const id of Object.keys(s.tabs)) {
      if (findPane(s.tabs[id].layout, pane.id)) return id
    }
    return ''
  })

  // Link-source workspace for terminal link openers (PR-5): use the workspace
  // that owns this tab, not active workspace. `undefined` for standalone tabs.
  const workspaceId = useWorkspaceStore((s) =>
    tabId ? s.findWorkspaceByTab(tabId)?.id : undefined,
  ) ?? undefined

  // Which view this device shows for the pane (U3 plan D1). Read before the early returns (rules of hooks).
  const view = useSessionViewStore(selectSessionView(tabId, pane.id, sessionBinding(hostId, sessionCode)))

  // The pane holds its conversation's stream in every view (plan D4: the terminal view needs the approvals too), as long
  // as it is a live Claude Code session on a host that serves conversations. Before the early returns (rules of hooks).
  const gate = useConversationViewGate(pane.content)
  const conversation = useConversationOfPane(pane.content, gate.ok)
  // The pane now reads another conversation (/clear, relay, rebuild): what it kept for the old one will not be read again
  // (#2457). Importing the module also installs the release of a pane that leaves the tab world.
  const readSession = conversation.state === 'ready' ? conversation.sessionId : ''
  useEffect(() => { retireStaleSessions(pane.id, hostId, readSession) }, [pane.id, hostId, readSession])
  // The send queue is driven from here, which stays mounted under every view (the terminal's too): a busy message is resent on idle
  // and an echo is matched whatever the reader is looking at.
  const readyDoc = conversation.state === 'ready' ? conversation.entry?.doc : undefined
  const readyItems = useMemo(() => (readyDoc ? readyDoc.turns.flatMap((turn) => turn.items) : []), [readyDoc])
  useSendQueueDriver(pane.id, hostId, readSession, readyDoc?.header?.status === 'idle', readyItems)
  const switchToTerminal = () => useSessionViewStore.getState().setView(tabId, pane.id, sessionBinding(hostId, sessionCode), 'terminal')

  // The footer of the deck AND the chat, top to bottom: [dock cards (U3-4 stacks them here)] → input → status row.
  const footer = (ctx: DeckFooterContext) => (
    <div data-testid="session-footer">
      <QuestionDock ctx={ctx} />
      <SessionInput {...ctx} />
      <SessionStatusRow sessionCode={sessionCode} ctx={ctx} />
    </div>
  )

  if (content.kind === 'tmux-session' && content.terminated) {
    return <TerminatedPane content={content} tabId={tabId} paneId={pane.id} />
  }

  if (content.kind !== 'tmux-session') return null

  if (!hostKnown) return <MissingHostPane hostId={hostId} />

  // The swap (D2): the terminal stays mounted whatever the view, so its WebSocket, scrollback and size survive a trip to
  // the deck or the chat. Hidden with `visibility` (not `display:none`: the fit observer skips zero-size boxes) and
  // `inert` (no focus, no clicks, not read out). `visible` goes false with it, so the view stops asking to be refit or
  // focused; coming back flips it to true and the terminal refits and takes focus as on any activation.
  const showTerminal = view === 'terminal'
  return (
    <div className="relative h-full w-full">
      <div
        data-testid="session-terminal-layer"
        className="absolute inset-0"
        style={showTerminal ? undefined : { visibility: 'hidden' }}
        inert={!showTerminal}
      >
        <AskStrip open={showTerminal && readyDoc !== undefined && openAsks(readyDoc.approvals).length > 0} />
        <TerminalView
          key={pane.id}
          wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
          visible={isActive && showTerminal}
          isFocusTarget={isFocusTarget && showTerminal}
          hostId={hostId}
          sessionCode={sessionCode}
          workspaceId={workspaceId}
          getTicket={() => fetchWsTicket(hostId)}
          connectingMessage={attachStalled ? t('session.attach_stalled') : undefined}
        />
      </div>
      {view === 'deck' && (
        <DeckPane
          paneId={pane.id}
          conversation={conversation}
          isActive={isActive}
          isFocusTarget={isFocusTarget}
          onSwitchToTerminal={switchToTerminal}
          footer={footer}
        />
      )}
      {view === 'chat' && (
        <ChatPane
          paneId={pane.id}
          conversation={conversation}
          title={content.cachedName}
          isActive={isActive}
          isFocusTarget={isFocusTarget}
          onSwitchToTerminal={switchToTerminal}
          footer={footer}
        />
      )}
    </div>
  )
}
