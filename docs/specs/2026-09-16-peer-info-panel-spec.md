# Spec — Peer information in the tab panel and the status bar

Status: draft v2 (codex review `task-mu48f1fj-b9ccr0`: 2 Blockers, 6 Majors,
1 Minor, 5 omissions — all accepted; §10 holds the disposition)
Date: 2026-09-16
Branch: `worktree-peer-info-panel`
Scope: `spa/src/components/StatusBar.tsx`,
`spa/src/components/RenamePopover.tsx`, two new stores, one new host-api call

## 1. What was asked

Show `{host} {cwd} {agent} {peer id} {connected status}` in two places — the
panel that opens when a tab is double-clicked, and the status bar — with

- **visual separation** between segments (today the status bar's segments sit
  next to each other with only whitespace, and read as one run-on line), and
- **each segment individually click-to-copy**, with immediate feedback.

## 2. What the data actually costs

The handoff recorded that `GET /api/peers` carries `session_code` — the same
key the SPA uses for a pane — so peer rows join to tabs directly, and that
`rg "api/peers" spa/src electron` has zero hits. Both are true and both were
re-verified.

What it did not record is the price, and the price decides the design:

```
pdx version --json     10 ms      (CLI startup floor)
pdx peers --json    2 100 ms      cold
pdx peers --json    2 069 ms      immediately after
```

That is not a slow first call. `internal/module/peers/module.go` sets
`budget: 2 * time.Second` and resolves the owning agent of **every** tmux
session under that budget; with 23 sessions on this host it spends the budget
every time. `/api/peers` is a fleet inventory endpoint, not a per-session
lookup. A status bar that called it on tab switch would put a two-second,
tmux-heavy daemon job behind every click, on every connected client.

### 2.1 Where each segment really comes from

v1 claimed four of the five segments were free and live. Two of those claims
were wrong, and both were wrong in the same direction — assuming a value the
component already had was the value the feature needs:

| Segment | Source | Cost | Live? |
|---|---|---|---|
| host | `useHostStore` — already rendered | free | yes |
| connected status | `useHostStore.runtime[hostId].status` — already rendered | free | yes |
| cwd | **`GET /api/sessions/{code}/cwd`** (not `Session.cwd`) | one tmux call | on demand |
| agent | **`GET /api/peers`** → `agent.peer_name` | ~2 s | cached |
| peer id | **`GET /api/peers`** → `address` | ~2 s | cached |

**cwd.** `Session.cwd` is `#{session_path}` — the directory the session was
*created* in (`internal/tmux/executor.go`). It does not follow a `cd`. The
real working directory is `#{pane_current_path}`, served by
`/api/sessions/{code}/cwd`, which the snapshot capture already uses for
exactly this reason (alpha.321 switched to it after `session_path` recorded
the wrong directory). Displaying `session_path` under the label "cwd" would be
wrong whenever it matters most — after the user has navigated somewhere — and
this segment is copyable, so a wrong value gets pasted into a command.

**agent.** The status bar's existing agent badge is the **model name**
(`Claude Opus 4`, from `useAgentStore.models`; `StatusBar.test.tsx` pins it).
The peers API's `agent.peer_name` is the agent's identity (`ai-chat-story-3a`)
— the readable half of a peer address, and what a person means by "which
agent". They are different values from different sources, and the existing
badge is left alone: this feature adds the peer name, it does not repurpose
the model badge.

So: two segments are free, one is a cheap on-demand call, and two come from
the expensive one. The fetch policy below follows from that split.

## 3. The stores

### 3.1 `usePeerStore`

```ts
interface PeerState {
  byHost: Record<string, {            // hostId →
    rows: Record<string, PeerRow>     //   sessionCode → row
    fetchedAt: number
    envelope: { partial: boolean; labelsUnavailable: boolean; unknownRegistryFiles: string[] }
    error: string | null
    loading: boolean
  }>
  refresh(hostId: string): Promise<void>   // deduped while in flight
  forgetHost(hostId: string): void
}
```

`PeerRow` keeps only what is displayed: `address`, `label`, `labelSource`,
`deliverable`, `reason`, `agent` (`type`, `peerName`, `status`).

**Indexing.** A row is indexed when `row_kind === 'session'` **and**
`session_code !== ''`. Both conditions, stated positively — v1 relied on
"entry rows happen to have an empty code", which is true today
(`EntryRecord` never sets `SessionCode`) but is a property of one constructor
rather than a rule anyone promised to keep. `inbox_dead` and `ambiguous` rows
are session rows and *do* keep their code; they are indexed, and §6 says what
they render.

**Invalidation.** `forgetHost` runs when a host is removed, its endpoint
changes, or its token changes — a cached address belongs to a daemon identity,
and keeping it across a re-point would show one machine's peers under
another's name.

### 3.2 `useSessionCwdStore`

Per `(hostId, sessionCode)`: `cwd`, `fetchedAt`, `loading`, `error`, with the
same dedupe. Populated by `fetchSessionCwd` (already in `host-api.ts`).
Refreshed when the panel opens and when a tab is activated — one tmux call,
unlike the peers inventory.

### 3.3 When a peers fetch happens

The trigger is stated as a predicate rather than an event, because v1's "first
activation of a host" had no single meaning — the app opens a WS to **every**
configured host (`App.tsx`), so "activated" could have meant all of them:

> A peers fetch for host H happens when something on screen needs H's peer
> data — the status bar rendering the active tab whose pane is on H, or a
> panel block for a pane on H — **and** `runtime[H].status === 'connected'`
> **and** H has no entry in `byHost` yet.

Plus two explicit triggers: the panel refreshes on open, and the refresh
control (§4.2) refreshes on click. **No polling, and no fetch on tab switch**
— switching between sessions of one host reuses that host's rows.

Hosts that are `disconnected`, `reconnecting` or `auth-error` are never
fetched: the call would fail slowly and the UI already says the host is not
reachable. `connected` with `tmuxState === 'unavailable'` **is** fetched — the
daemon answers, it just has no tmux sessions, and "no rows" is a real answer.

### 3.4 Staleness

A cached peer id can go wrong in a way that matters: default labels are
*place* addresses (alpha.363), so renaming a tmux session, or a second agent
appearing in one, changes an address without anything in the SPA hearing about
it. A user who copies a stale address and sends to it reaches the wrong agent
— the exact failure class the label work spent three review rounds removing.

So age is part of the display: under 60 s renders normally; over 60 s renders
dimmed with the age in its tooltip; while loading, the previous value stays,
marked busy — never a flash of empty.

**Clicking never changes meaning with age.** v1 made a stale segment refresh
instead of copying, which is a mis-click waiting to happen: the same element,
the same gesture, a different outcome depending on a timer the user cannot
see. The segment always copies. Refreshing is its own control (§4.2).

## 4. The status bar

### 4.1 Segments and separators

The row gains `cwd`, `agent` (peer name) and `peer id`. Every segment is
delimited by a thin vertical rule — `border-l` on a spacer, not a `|`
character, because a glyph would be selected and copied along with the text
the user is trying to grab.

### 4.2 Copy, and the refresh control

Each copyable segment is a `<button>`, not a `<span>` with a handler: it is
keyboard-reachable and announces itself, and the status bar currently has no
keyboard path at all.

| Segment | Copies |
|---|---|
| host | the host's display name |
| cwd | the absolute path |
| agent | the peer name (`ai-chat-story-3a`) |
| peer id | the **full address** (`mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a`) |
| status | nothing — not a value, so not a button |

The displayed peer id is the **label**; the copied value is the full address.
A label alone is not addressable without its host, and someone copying "the
peer id" means to paste something that works in `pdx msg send`.

A single small refresh control sits after the peer id segment. It refreshes
both stores for the active pane's host, is disabled while loading or when the
host is not connected, and is the only way a click causes a fetch.

**Feedback placement.** With five copyable segments, a confirmation next to
each would reflow the row on every copy. Instead one confirmation appears in a
fixed slot at the end of the segment group — `copied: cwd`, `copy failed` —
for ~1.5 s, so nothing moves. (`copyText` genuinely can reject: the Electron
window over plain http has no `navigator.clipboard`, which is why
`copy-text.ts` exists.)

### 4.3 Narrow windows

The row already carries host, session name, status, the model badge, upload
status, pane title, split buttons and the view-mode control; three more
segments do not fit 400 px. The priority, highest first:

1. **status** and the **view-mode control** — never dropped, never shrink.
2. **host** — never dropped; truncates to 8ch at the narrowest.
3. **peer id** — truncates from the right.
4. **session name** — truncates from the right.
5. **agent** (peer name) — dropped below 700 px.
6. **cwd** — truncates from the left (its tail is the informative end);
   dropped below 600 px. It is the one segment the panel always shows in full.
7. **pane title** — dropped below 700 px (it is already `max-w-[40ch]`).
8. **upload status** — never dropped: it is transient and it is the only
   feedback for an operation in flight. It takes its space from the segments
   above by pushing them to truncate.
9. **split buttons** — dropped below 500 px.

The 400 px acceptance test asserts no horizontal overflow with all of it
present, including an active upload.

## 5. The tab panel

`RenamePopover`'s `PaneDetailBlock` renders one block per terminal pane with
`DetailRow` rows (name, working directory, resume command). Each block gains a
peer section, in the same rhythm:

- **Address** — full address, click-to-copy, with the default-label marker
  when `labelSource === 'default'` (matching `pdx peers`, where the marker is
  the only thing distinguishing a default from a user label of the same shape).
- **Agent** — type, peer name and live status (`idle` / `busy`).
- **Deliverable** — when false, the row shows `reason` (`no_agent`, `not_cc`,
  `inbox_dead`, `proxy`, `ambiguous`) rather than a bare "no": the reason is
  the actionable half.

**Per-block, not per-panel.** A tab can hold several panes across several
hosts, so loading and error state live in each block. One pane's host being
unreachable must not blank another's block.

The peer section renders only for a **live** tmux-session pane; a terminated
pane has no peer and the section is omitted, as the panel already does for its
other live-only rows.

## 6. States that are not the happy path

| State | Panel | Status bar |
|---|---|---|
| host not `connected` | no fetch; "host not connected", last known rows shown dimmed if any | peer id and agent dimmed; status segment already says so |
| fetch failed | the error, with the refresh control | `—`, tooltip carries the error |
| envelope `partial` **and** a row exists | show the row, plus a note naming the cause (`unresolved owners` / `unreadable registry files` / `labels unavailable`) | render the row; no marker |
| envelope `partial` **and** no row | "could not be determined" — **not** "no peer" | `—`, tooltip says undetermined |
| `labels_unavailable` | note that labels could not be read, so addresses may be hash defaults | as above |
| no row, envelope complete | "no peer" | `—` |
| row exists, `label: ""` (no cc agent) | agent row only, no address | `—` for peer id |
| row is `inbox_dead` / `ambiguous` | address shown with the reason | address shown, dimmed |
| pane terminated | no peer section | primary pane terminated → existing behaviour, no peer id |
| session gone from the daemon | no peer section (the pane is marked terminated by the WS) | `—` |
| session not yet in `useSessionStore` (pane not reconciled) | block renders with a spinner, no error | `—`, no error |
| primary pane is not a tmux session (editor, browser) | other blocks unaffected | unchanged — the status bar already early-returns |
| stream-mode pane | **no block at all** — the panel does not render one today (`collectRenameTargets` returns `mode === 'terminal'` panes only), and this feature does not change which panes the panel collects | peer info **is** shown: the status bar reads the primary pane directly and does not go through that collector |
| tab has panes on two hosts | each block fetches its own host | the status bar follows the **primary** pane only |

The stream-pane row is a **deliberate narrowing of v1**, forced by a conflict
the plan review found: v1 said the panel shows a peer section for stream
panes, but the panel renders no block for them at all — `collectRenameTargets`
filters to terminal panes, and its comment says stream panes are out of scope.
Honouring v1 would have meant changing which panes the rename panel collects,
which would give stream panes name/cwd/resume rows as a side effect of a
read-only peer feature. The status bar covers the case instead, because it
reads the primary pane directly.

`partial` deserves its own lines because v1 got its meaning wrong. It is set
by any of three causes — an owner lookup that did not finish, unreadable
registry files, or a failed label snapshot (`module.go`) — so it cannot be
described as "some other session's owner". And when there is no row *and* the
envelope is partial, "no peer" is an unsupported conclusion: the row may
simply not have been resolved.

## 7. Out of scope

- Any daemon change. No new endpoint, no change to `/api/peers`, no change to
  the 2 s budget. (§9 is the follow-up.)
- Cross-host rows in an envelope. Only the row matching the pane's own host is
  ever read.
- Editing anything. Naming remains `pdx msg name`.
- The existing model badge, upload status, split and view-mode controls, other
  than the truncation rules in §4.3.

## 8. Acceptance

1. `usePeerStore`: indexes only `row_kind === 'session' && session_code !== ''`;
   dedupes concurrent `refresh(host)`; keeps hosts independent; records
   `fetchedAt`, envelope flags and `error`; `forgetHost` clears on host
   removal, endpoint change and token change.
2. **No fetch on tab switch** — split in two, because v1's single test could
   not have been written against the component alone:
   - store level: ten `refresh('h1')` calls in flight collapse to one request;
   - UI level: switching the active tab ten times between sessions of one
     already-fetched host calls `refresh` **zero** times; and the first render
     that needs an unfetched connected host calls it **once**.
3. Hosts that are `disconnected` / `reconnecting` / `auth-error` are never
   fetched; `connected` + `tmuxState: 'unavailable'` is.
4. `useSessionCwdStore` shows `pane_current_path`, and a test asserts the
   displayed value differs from `Session.cwd` when the session has `cd`-ed —
   the whole reason this segment does not use the free field.
5. Copy: each segment copies §4.2's value — the peer id copies the **full
   address**, not the label — with confirmation in the fixed slot, and a
   distinct message when `copyText` rejects. Clicking a stale segment copies;
   it does not refresh.
6. Every row of §6's table renders without throwing, including partial-with-no-row
   ("could not be determined", not "no peer") and a two-host tab.
7. 400 px: no horizontal overflow with an active upload present; `status` and
   the view control readable; `cwd`, `agent` and pane title absent per §4.3.
8. Keyboard: every copyable segment and the refresh control are focusable and
   activate on Enter/Space.
9. i18n: every new string exists in **both** `en.json` and `zh-TW.json`; a test
   asserts the two files have identical key sets for the new namespace.

## 9. Follow-up, not done here

`GET /api/peers/session/{code}` — resolving one session's owner would cost
milliseconds instead of two seconds and would let the status bar show a
never-stale address. It is the right fix, it is a daemon change with its own
spec, and this feature works without it. An issue is filed when this ships.

## 10. Codex spec review disposition (`task-mu48f1fj-b9ccr0`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| B1 | Blocker | "agent is free": the existing badge is the **model name**, not the peer name the feature needs | **Accepted** — §2.1: the peer name comes from `/api/peers`; the model badge is untouched |
| B2 | Blocker | `partial` means any of three causes, and "no row + partial" is not "no peer" | **Accepted** — §6 gains four rows and the causes are named |
| M1 | Major | `Session.cwd` is `session_path`, not the pane's cwd — it does not follow a `cd` | **Accepted** — §2.1, §3.2: a second cheap store on `/api/sessions/{code}/cwd`, with §8.4 pinning the difference |
| M2 | Major | "first activation of a host" has no single meaning; the app opens a WS to every host | **Accepted** — §3.3 restates the trigger as a predicate, and gates on `connected` |
| M3 | Major | Click meaning changing with staleness invites mis-clicks | **Accepted** — §3.4, §4.2: clicking always copies; refresh is its own control |
| M4 | Major | The join should be positive (`row_kind === 'session'`), not "entry rows happen to be empty" | **Accepted** — §3.1 |
| M5 | Major | Narrow-layout rules ignored view controls, upload status and pane title | **Accepted** — §4.3 is a full priority list; §8.7 tests with an upload present |
| M6 | Major | State table missing multi-pane, stream panes, unconnected hosts, unreconciled sessions | **Accepted** — §6 |
| m1 | Minor | Acceptance 2 was not executable as written | **Accepted** — §8.2 split into store-level and UI-level |
| — | Omission | Envelope fields (`partial`, `labels_unavailable`, `unknown_registry_files`) had no UI mapping | **Accepted** — §3.1, §6 |
| — | Omission | Disconnected-host fetch conditions undefined | **Accepted** — §3.3 |
| — | Omission | i18n must land in both locale files | **Accepted** — §8.9 |
| — | Omission | Cache invalidation on host remove / endpoint / token change | **Accepted** — §3.1 `forgetHost` |
| — | Omission | Copy feedback placement with several segments | **Accepted** — §4.2 fixed slot |
