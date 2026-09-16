# Spec — Peer information in the tab panel and the status bar

Status: draft v1
Date: 2026-09-16
Branch: `worktree-peer-info-panel`
Scope: `spa/src/components/StatusBar.tsx`,
`spa/src/components/RenamePopover.tsx`, one new store, one new host-api call

## 1. What was asked

Show `{host} {cwd} {agent} {peer id} {connected status}` in two places — the
panel that opens when a tab is double-clicked, and the status bar — with

- **visual separation** between segments (today the status bar's segments sit
  next to each other with only whitespace, and read as one run-on line), and
- **each segment individually click-to-copy**, with immediate feedback:
  clicking the host copies the host, clicking the peer id copies the **full
  address**, and so on.

## 2. What the data actually costs

The handoff recorded that `GET /api/peers` already carries `session_code` —
the same key the SPA uses for a pane — so peer rows join to tabs directly, and
that `rg "api/peers" spa/src electron` has zero hits. Both are true and both
were re-verified.

What it did not record is the price, and the price decides the design:

```
pdx version --json     10 ms      (CLI startup floor)
pdx peers --json    2 100 ms      cold
pdx peers --json    2 069 ms      immediately after
```

That is not a slow first call. `internal/module/peers/module.go` sets
`budget: 2 * time.Second` and resolves the owning agent of **every** tmux
session under that budget; with 23 sessions on this host it spends the budget
every time and returns `partial: true` for whatever it could not finish.
`/api/peers` is a fleet inventory endpoint, not a per-session lookup.

A status bar that called it on tab switch would put a two-second, tmux-heavy
daemon job behind every click, on every connected client.

### 2.1 Which segments actually need it

Once the cost is on the table, the obvious question is how much of the ask
depends on it. Almost none of it:

| Segment | Source | Cost |
|---|---|---|
| host | `useHostStore` — already rendered by `StatusBar` | free, live |
| cwd | `useSessionStore`, `Session.cwd` — already fetched for every session | free, live |
| agent | `useAgentStore` (`agentTypes`, `models`) — already rendered | free, live (WS-driven) |
| connected status | `useHostStore.runtime[hostId].status` — already rendered | free, live |
| **peer id** | **`GET /api/peers`** | **~2 s** |

Four of the five are already in the component's hands. Exactly one field —
the peer address — needs the expensive call, and that reframes the fetch
policy from a compromise into the obvious shape: render four segments
immediately from live stores, and treat the fifth as something fetched
sparingly and cached.

## 3. The peer store

```ts
// spa/src/stores/usePeerStore.ts
interface PeerState {
  byHost: Record<string, {            // hostId →
    rows: Record<string, PeerRow>     //   sessionCode → row
    fetchedAt: number
    partial: boolean                  // the envelope's own flag
    error: string | null
    loading: boolean
  }>
  refresh(hostId: string): Promise<void>   // deduped while in flight
}
```

`PeerRow` keeps only what is displayed: `address`, `label`, `labelSource`,
`deliverable`, `reason`, and `agent` (`type`, `peerName`, `status`).

**Joining.** Rows are indexed by `session_code`. Entry rows carry an empty
`session_code` (verified: of 24 rows, 23 are session rows with unique codes,
the single entry row's code is `""`), so indexing by it keeps exactly the
session rows — one per tmux session, which is precisely what a tab points at.
Rows with an empty code are dropped rather than overwriting each other under
the `""` key.

### 3.1 When a fetch happens

| Trigger | Why |
|---|---|
| the tab panel opens | it is where the address is read and copied, so it must be current; 2 s is acceptable behind a spinner in a panel the user deliberately opened |
| a host's first activation in this app session | populates the status bar once, in the background, without blocking anything |
| the user clicks a stale or empty peer-id segment | explicit |

**No polling, and no fetch on tab switch.** Switching tabs between sessions of
one host reuses that host's cached rows; switching to a different host costs
that host one background fetch, once.

### 3.2 Staleness, and why it is visible

A cached peer id can go wrong in a way that matters: default labels are
*place* addresses (alpha.363), so renaming a tmux session, or a second agent
appearing in one, changes an address without anything in the SPA hearing about
it. A user who copies a stale address and sends to it reaches the wrong agent
— the exact failure class the label work spent three review rounds removing.

So the age is part of the display, not an implementation detail:

- under 60 s: rendered normally;
- over 60 s: rendered dimmed, with the age in its tooltip, and clicking it
  refreshes instead of copying;
- while loading: the previous value stays, marked busy — never a flash of
  empty.

The panel sidesteps the question by refreshing on open: what a user reads
there is at most a few hundred milliseconds old.

## 4. The status bar

Today: `host  sessionName  status` plus optional pane title, upload status and
the view-mode control, separated by nothing but `gap-3`.

After: the same row gains `cwd`, `agent` and `peer id`, and every segment is
delimited. The separator is a thin vertical rule (`border-l` on a spacer
span), not a `|` character — a glyph would be selected and copied along with
the text the user is trying to grab.

Priority when the window is narrow, because six segments do not fit a 400 px
window: `host` and `status` never shrink; `cwd` truncates from the left (its
tail is the informative end); `agent` and `peer id` truncate from the right;
below ~600 px `cwd` is dropped entirely — it is the one segment the panel
always shows in full.

### 4.1 Copy behaviour

Each segment is a `<button>`, not a `<span>` with a click handler: it is
keyboard-reachable and announces itself, and the status bar is currently one
of the few places in the app with no keyboard path at all.

| Segment | Copies |
|---|---|
| host | the host's display name |
| cwd | the absolute path |
| agent | the agent's peer name (`ai-chat-story-3a`), not its type |
| peer id | the **full address** (`mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a`) |
| status | nothing — it is not a value, so it is not a button |

Feedback follows the existing convention in `LocalDaemonSection.tsx`: a
transient confirmation for ~1.5 s, and a distinct failure message when
`copyText` rejects (it can — the Electron window over plain http has no
`navigator.clipboard`, which is why `copy-text.ts` exists).

The displayed peer id is the **label**; the copied value is the full address.
A label alone is not addressable without its host, and a user copying "the
peer id" means to paste something that works in `pdx msg send`.

## 5. The tab panel

`RenamePopover`'s `PaneDetailBlock` already renders one block per pane with
`DetailRow` label/value rows (name, working directory, resume command). It
gains a peer section below those, using the same `DetailRow` rhythm:

- **Address** — the full address, click-to-copy, with `label_source` shown as
  a `*`-equivalent marker when it is `default` (matching `pdx peers`, where
  the marker is the only thing distinguishing a default from a user label of
  the same shape).
- **Agent** — type, peer name and live status (`idle` / `busy`).
- **Deliverable** — when `deliverable` is false, the row shows `reason`
  (`no_agent`, `not_cc`, `inbox_dead`, `proxy`, `ambiguous`) rather than a
  bare "no": the reason is the actionable half.

The panel shows the peer block only for a **live tmux-session pane**. A
terminated pane has no peer, and the block is omitted rather than rendered
empty — the panel already distinguishes live from terminated for its other
rows.

## 6. States that are not the happy path

| State | Panel | Status bar |
|---|---|---|
| host disconnected | no fetch; last known rows marked stale | peer id dimmed, status segment already says disconnected |
| fetch failed | the error, with a retry control | peer id shows `—`, tooltip carries the error |
| envelope `partial: true` | a note that the inventory was incomplete | no marker — the row that *is* present is still that row |
| no row for this session code | "no peer" | `—` |
| row present, `label: ""` (no cc agent) | the agent row only | `—` for peer id |
| pane is not a tmux session (editor, browser) | no peer block | unchanged (the status bar already early-returns) |

`partial: true` deserves its own line: it means some *other* session's owner
lookup did not finish, not that this row is suspect. Marking this row
uncertain because of an unrelated timeout would be a lie in the safe
direction, which is still a lie.

## 7. Out of scope

- Any daemon change. No new endpoint, no change to `/api/peers`, no change to
  the 2 s budget. (A per-session peer endpoint would make this cheap and is
  the obvious follow-up — §9.)
- Cross-host rows. The fan-out rows in a host's envelope describe *other*
  hosts; this feature only ever reads the row matching the tab's own host.
- Editing anything. Every peer field here is read-only; naming is
  `pdx msg name`, unchanged.

## 8. Acceptance

1. `usePeerStore`: indexes by session code; drops empty-code rows; dedupes
   concurrent `refresh` calls for one host; keeps per-host state independent;
   records `fetchedAt`, `partial` and `error`.
2. **No fetch on tab switch**: a test switches the active tab ten times
   between sessions of one host and asserts exactly **one** `/api/peers` call
   — the one from that host's first activation. This is the test that protects
   the daemon from the design the cost measurement rejected.
3. The panel refreshes on open, shows a spinner over the previous value, and
   renders address / agent / deliverable-reason.
4. Staleness: under 60 s renders normally; over 60 s renders dimmed and a
   click refreshes rather than copies; a fetch in flight keeps the old value.
5. Copy: each segment copies the value in §4.1's table — in particular the
   peer id copies the **full address**, not the displayed label — with
   confirmation, and a distinct message when `copyText` rejects.
6. Every state in §6's table renders without throwing, including a host with
   no rows at all.
7. Narrow layout: at 400 px the row does not overflow horizontally, `host` and
   `status` are still readable, and `cwd` is absent.
8. Keyboard: each copyable segment is focusable and activates on Enter/Space.

## 9. Follow-up, not done here

A `GET /api/peers/session/{code}` that resolves one session's owner would cost
milliseconds instead of two seconds, and would let the status bar show a
never-stale address. It is the right fix; it is a daemon change with its own
spec, and this feature works without it. An issue is filed when this ships.
