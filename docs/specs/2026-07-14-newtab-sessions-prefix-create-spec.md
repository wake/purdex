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
- No new create-session API; reuse `createSession()` from `host-api`. We accept
  its coarse `"<status> <statusText>"` error (not the Host dialog's response-body
  text) rather than changing `createSession()` — that signature is shared by all
  callers and widening its behavior is out of scope.
- No change to the **existing session-row** click/disable semantics (rows keep
  today's `hostRuntime && status !== 'connected'` gate). Only the new create
  ("+") affordance adopts the Host-page offline semantics (§5.3).
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

**Type location (avoid a cycle).** `TabIconComponent` currently lives in
`useTabDisplay.ts`. Since `useTabDisplay` will import the new hook and the new
hook needs `TabIconComponent`, move the type into `useSessionAgentIndicator.ts`
(or a small shared types module) and have `useTabDisplay` import it from there —
preventing a `useTabDisplay ⇄ useSessionAgentIndicator` import cycle.

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

**Always render a host header per host in `hostOrder`** — including hosts with
**zero** sessions. Today the component early-returns a single global "No
sessions available" when *no* host has any session, and renders a host header
only when `hostOrder.length > 1`. Both must change so every host gets a create
anchor:
- Iterate `hostOrder`; for each existing host render a header (single- and
  multi-host). If a host has no sessions, render a subtle per-host empty line
  (e.g. `session.no_sessions`) under its header instead of hiding it.
- The global early-return only applies when there are **no hosts at all**.

**Header structure (no nested buttons).** The header is a `<div>` row, not a
single button:
- **Left**: in multi-host, a `<button>` wrapping caret + status dot + name that
  toggles expand/collapse (single-host: same content as a non-interactive span,
  no collapse). Status-dot logic unchanged.
- **Right**: a **separate** `+` `<button>` (its own interactive element — never
  nested in the collapse button) that toggles the inline create form. This
  avoids illegal nested interactive elements and a `+` click never toggles
  collapse.

**`+` / create offline gate — Host-page semantics.** The create button and the
create submit are disabled when the host is offline per the Host page's rule:
`!runtime || runtime.status !== 'connected' || runtime.tmuxState ===
'unavailable'` (stricter than the row gate; `tmuxState === 'unavailable'` must
block create). The existing session-row gate is unchanged (§3).

**Inline create form** (`NewTabSessionForm`, local to SessionSection): clicking
`+` toggles a small form under that host with `name`, `cwd` (default `~`),
`mode` (`terminal|stream`) — same fields/labels as the Host page dialog. On
submit it calls `createSession(hostId, name.trim(), cwd, mode)` with a `creating`
in-flight guard disabling submit.
- **On success**, before attaching, apply two guards:
  1. **Blank code** — if `!created.code`, treat as a failed create (show an
     error, keep the form open, do **not** attach). Matches the repo's
     "blank session code = failure" convention.
  2. **Host still live** — re-read `useHostStore.getState()` and confirm
     `hosts[hostId]` still exists and is connected (same offline rule as above).
     A host removed/disconnected while `createSession` was in flight must not
     attach into a pane pointing at a dead host; show an error instead.
  - If both guards pass → `onSelect({ kind:'tmux-session', hostId, sessionCode:
    created.code, mode, cachedName: created.name, tmuxInstance:'' })`, then close
    the form. This attaches into the current pane (wrapper's `setPaneContent` +
    `setActiveTab`). Using `created.code` from the response lets us attach
    immediately, before the WS session-list sync arrives.
- **On failure** (network/HTTP throw, blank code, or dead host) → show the error
  text inline; do not attach; keep the form open. Error text is the coarse
  `createSession()` message (§3).
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
  status indicator present. Assert the status indicator DOM under a non-`icon`
  `tabIndicatorStyle` (e.g. `dot` / `iconDot`) so the test exercises the real
  prefix rather than only the terminal fallback.
- Host header always present (incl. single host) with a `+` button.
- **Zero sessions**: a connected host with no sessions still renders its header +
  `+`, and creating works (no global "No sessions" swallow).
- Clicking `+` toggles the form and does **not** collapse/expand the host.
- `+` disabled when host offline per Host-page rule — cover `runtime`
  undefined and `tmuxState === 'unavailable'`, not only `status !== 'connected'`.
- Submitting the form calls `createSession` and, on success, `onSelect` with the
  correct `tmux-session` content (`created.code` / `created.name` / chosen mode).
- **Race**: `createSession` resolves after the host is removed/disconnected →
  no `onSelect`, error surfaced.
- **Blank code**: `createSession` resolves with empty `code` → no `onSelect`,
  error surfaced.
- Create failure (throw) surfaces the error and does not call `onSelect`.

**`useTabDisplay.test`** — unchanged, must stay green (equivalence guard).
Additionally confirm a cc/codex icon-variant change still updates the resolved
icon after the refactor (guards against the extraction dropping a variant input).

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
  is already disabled when the host is known-offline (Host-page rule).
- **compositeKey delimiter**: agent state is keyed by `compositeKey(hostId,
  sessionCode)` = `` `${hostId}:${sessionCode}` ``. Cross-host uniqueness holds as
  long as `hostId`/`sessionCode` contain no ambiguating colon (existing
  invariant already relied on by `useTabDisplay`); no design change needed.
