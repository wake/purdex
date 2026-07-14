# New-Tab Sessions: tab-style indicator + per-host create & attach

Date: 2026-07-14
Status: Draft

## 1. Problem

The New-Tab start screen's **Sessions** list (`SessionSection.tsx`) renders each
session with a fixed `<TerminalWindow>` icon, name, and code. It has two gaps
relative to the rest of the app:

1. **No agent status.** A tab for the same session shows a rich indicator (agent
   icon such as the Claude Code glyph, a running/error status dot, subagent
   dots, unread pip) resolved by `useTabDisplay`. The Sessions list shows none of
   it, so the user can't tell a session's agent state from the start screen.
2. **No way to create a session.** The Host page can create sessions per host,
   but from the New-Tab screen the user can only pick an *existing* session.
   There is no "new session" affordance, and no way to spin one up and land it
   directly in the pane they're looking at.

## 2. Goals

- **G1** — Each Sessions-list row shows the **same** icon/status prefix a tab
  shows for that session: agent icon (falling back to the terminal icon), agent
  status, subagent dots, unread — honoring the user's `tabIndicatorStyle`.
- **G2** — Per host, the user can **create a new session** (name / cwd / mode)
  from the New-Tab Sessions list, and on success it **attaches directly into the
  current pane** (the full-page new tab, or a split new-tab pane).

## 3. Non-Goals

- No change to the Host page's session table or its create dialog.
- No "active session" highlighting (matching a list row to an open pane).
- No new create-session API; reuse `createSession()` from `host-api`.
- No delete/rename in the New-Tab list (those stay on the Host page).

## 4. Background (current code)

- `SessionSection.tsx` — New-Tab provider. Groups sessions by host; a host header
  row appears **only when `hostOrder.length > 1`**. Each session is a `<button>`
  calling `onSelect({ kind:'tmux-session', hostId, sessionCode, mode:'terminal',
  cachedName, tmuxInstance:'' })`. `onSelect` comes from `NewTabPaneWrapper`,
  which does `setPaneContent(currentTabId, pane.id, content)` + `setActiveTab` —
  i.e. it replaces **this** pane (works for a full-page new tab and a split
  new-tab pane alike).
- `useTabDisplay(tab)` (`hooks/useTabDisplay.ts`) — resolves a tab's display.
  The **agent layer** it computes: `ck = compositeKey(hostId, sessionCode)`,
  reads `useAgentStore` `statuses[ck]` / `agentTypes[ck]` / `subagents[ck]` /
  `unread[ck]`, `getAgentIcon(agentType, { ccVariant, codexVariant })`, and
  `tabIndicatorStyle` from `useUISettingsStore`. `IconComponent = agentIcon ??
  paneIcon`, where `paneIcon` is content-kind specific.
- `TabIcon` (`components/TabIcon.tsx`) — pure presentational; given
  `{ IconComponent, agentStatus, tabIndicatorStyle, isActive, iconSize,
  subagentRefs, isUnread }` it renders the exact prefix used in the tab bar.
- `createSession(hostId, name, cwd, mode)` (`lib/host-api.ts`) — `POST
  /api/sessions`, returns the new `Session` (has `.code`, `.name`).
- Host page's `NewSessionDialog` (in `components/hosts/SessionsSection.tsx`) is
  the reference create form (name / cwd default `~` / mode `terminal|stream`).

## 5. Design

### 5.1 Shared agent-indicator hook (single source of truth)

New `hooks/useSessionAgentIndicator.ts`:

```ts
interface SessionAgentIndicator {
  agentIcon: TabIconComponent | undefined   // undefined when no/terminated agent
  agentStatus: AgentStatus | undefined
  subagentRefs: SubagentRef[]
  isUnread: boolean
  tabIndicatorStyle: TabIndicatorStyle
}

function useSessionAgentIndicator(
  hostId: string,
  sessionCode: string | undefined,
  opts?: { isTerminated?: boolean },
): SessionAgentIndicator
```

Behavior (extracted verbatim from `useTabDisplay`'s agent layer):
- `ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined`.
- Reads `statuses[ck]`, `agentTypes[ck]`, `subagents[ck] ?? []`, `unread[ck]`
  (all guarded to empty/undefined when `ck` is undefined), plus
  `tabIndicatorStyle`, `ccIconVariant`, `codexIconVariant`.
- `agentIcon = !isTerminated && agentType ? getAgentIcon(agentType, { ccVariant,
  codexVariant }) : undefined` (default `isTerminated=false`).
- Empty-reference constants reused so identity is stable (no render churn).

`useTabDisplay` is refactored to **consume** this hook for the agent layer,
keeping its own `paneIcon` fallback (`IconComponent = agentIcon ?? paneIcon`) and
all label/title logic. This is a behavior-preserving extraction; the existing 14
`useTabDisplay.test.ts` cases must stay green.

### 5.2 SessionSection: tab-style prefix (G1)

- Extract each session row into a `SessionRow` component (hooks can't run inside
  `.map()`), which calls `useSessionAgentIndicator(hostId, session.code)` and
  renders `<TabIcon>` in place of `<TerminalWindow>`:
  - `IconComponent = agentIcon ?? TerminalWindow` (terminal is the idle/no-agent
    fallback, matching today's look).
  - `isActive={false}` (list rows are not "active" tabs).
  - `iconSize` chosen to sit in TabIcon's `w-4 h-4` box (14, matching the tab
    bar); final value confirmed during implementation.
- The row's button, keyboard nav (`data-session-btn`, Arrow/j/k), `onSelect`
  payload, disabled-when-offline behavior are unchanged.

### 5.3 SessionSection: per-host create & attach (G2)

- **Host header always rendered** (single- and multi-host), carrying: expand
  caret (multi-host only), host status dot (existing logic), host name, and a
  `+` new-session button on the right. `+` is disabled when the host is offline.
  - Single-host case previously rendered no header; it now shows a compact header
    so the `+` has a home. Expand/collapse stays multi-host-only.
- **Inline create form** (`NewTabSessionForm`, local to SessionSection): clicking
  `+` toggles a small form under that host with `name`, `cwd` (default `~`),
  `mode` (`terminal|stream`) — same fields/labels as the Host page dialog. On
  submit it calls `createSession(hostId, name.trim(), cwd, mode)`.
  - **On success** → `onSelect({ kind:'tmux-session', hostId, sessionCode:
    created.code, mode, cachedName: created.name, tmuxInstance:'' })`, then close
    the form. This attaches the new session into the current pane (via the
    wrapper's `setPaneContent` + `setActiveTab`). The `created.code` from the
    response lets us attach immediately, before the WS session-list sync arrives.
  - **On failure** → show the error text inline; do not attach; keep the form
    open. A create in-flight guard (`creating`) disables the submit button.
  - Empty `name` is a no-op (submit disabled), matching the Host dialog.

### 5.4 Reuse vs. duplication

The Host page's `NewSessionDialog` is not extracted or modified this round
(keeps blast radius off the Host page's tested behavior). `NewTabSessionForm`
reuses `createSession()` and the same fields; the two forms are near-duplicates.
If code review flags the duplication, extracting a shared presentational
`NewSessionForm` is a follow-up.

## 6. Data flow

```
SessionRow ─ useSessionAgentIndicator(hostId, code) ─▶ useAgentStore / useUISettingsStore ─▶ <TabIcon>
  "+"  ─▶ NewTabSessionForm ─ createSession() ─▶ Session ─ onSelect(tmux-session) ─▶ wrapper.setPaneContent(current pane)
```

## 7. Testing (TDD)

**`useSessionAgentIndicator.test`**
- No session code → all-empty (undefined status/icon, empty refs, unread false).
- `agentType` present + not terminated → `agentIcon` from `getAgentIcon`; status
  / subagents / unread / style passed through.
- `isTerminated: true` → `agentIcon` undefined even with an agentType.

**`SessionSection.test` (extend existing)**
- Row renders `<TabIcon>` prefix: no-agent → terminal icon; running agent →
  status indicator present (assert via TabIcon output / testid).
- Host header always present (incl. single host) with a `+` button.
- `+` disabled when host offline.
- Submitting the form calls `createSession` and, on success, `onSelect` with the
  correct `tmux-session` content (`created.code` / `created.name` / chosen mode).
- Create failure surfaces the error and does not call `onSelect`.

**`useTabDisplay.test`** — unchanged, must stay green (equivalence guard).

## 8. Phases

- **Phase 1 (G1)** — `useSessionAgentIndicator` hook + `useTabDisplay` refactor +
  `SessionRow` with `TabIcon`. Self-contained, visual-only.
- **Phase 2 (G2)** — always-on host header + `+` + `NewTabSessionForm` + attach.

Both land in one worktree. PR split (one PR / two, or two commits in one PR)
decided at plan time by review size.

## 9. Risks

- **useTabDisplay refactor** feeds the whole tab bar. Mitigated by pure
  extraction + the 14 existing tests. If the extraction proves awkward, fall back
  to a standalone hook with the small agent-layer reads duplicated.
- **Attach-before-sync**: attaching with `created.code` before the WS list sync
  is intentional; `SessionPaneContent` connects by code, and the store fills in
  shortly after. No dependency on the list containing the new session first.
- **Offline host** during create: `createSession` will fail → inline error; `+`
  is already disabled when the host is known-offline.
