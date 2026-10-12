# Unattended + no relay quota → pause the session (and its team); relay card shows a brief and the title (spec + plan, rev 4)

Owner: A line (1f) for UP-1…UP-4 (daemon / mod), interface line (88) for UP-5 (App). Builds on
`docs/specs/2026-10-08-unattended-mode-spec.md`, the relay quota spec, `docs/specs/2026-10-11-idle-relay-spec-plan.md`,
`docs/specs/2026-10-09-session-workbook-spec.md`.

rev 4 (codex review of rev 3, `task-mv4b6ryp-2n3cz5`: rev-1 findings resolved; 3 critical + 13 important new, all
mechanics): a one-time **release token** carries the re-ask past the pause rule; pause rows carry the unattended
**on-epoch** so reconciliation also clears pauses left from an earlier on-period; one store primitive **spends or
pauses** in a single write transaction; the held-card sweep cancels row + op + writes the pause atomically; exact
member predicate; FIFO outbox with head-of-line blocking; atomic check-and-bounce; push wired through the outbox;
phases split.

rev 3 (user 2026-10-12: rev 2 tied the pause to the relay card — 「用 relay 卡去鎖後續的行為和 peer 訊息，不順」): **the
pause is the session's own state**. Entering it, the relay ask is **not opened** (no card hangs around); leaving it,
the ask is **sent again** as a fresh card. Rev 1's codex findings (`task-mv4akii1-k8zrx6`) are answered inside this
model: pause rows keyed by (session, cause) — two causes coexist; entering = one `team.db` transaction with no approval
row to race; leaving = the switch-off handler plus a boot / tick reconciliation (a pause row while unattended is off is
stale by definition); flow notices from a persisted outbox, the only messages that pass a pause, marked in process.

## 1. User decisions (2026-10-12, do not reopen)
- **When**: unattended on (per host), a relay is wanted (70 % ask, idle ask, member relay), no quota left.
- **Enter**: the relay ask is withdrawn (not left as a card); the session is paused and told
  「接力額度用完，先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來」 (the turn in progress finishes); a 「被擋下」
  light in the App; one push.
- **Members through the lead**: a paused lead ⇒ its members paused by the daemon, told 「lead 已暫停…等 lead 指示」;
  a member whose own relay finds no quota ⇒ that member paused, and only its lead is told.
- **While paused**: messages to it are **bounced** — the sender is told at once 「對方暫停中（接力額度用完，等使用者回來），
  訊息沒有送達」 — and the session gets the list (who, when, first line) when it resumes. The user's own input always
  goes through.
- **Back** = the user turns unattended off (「管理者已回位」): lead / solo sessions resume, are told, and their relay ask is
  **sent again** (a fresh card for the user); then their members resume (「lead 已恢復，等 lead 指示」); members paused for
  their own quota resume and their lead gets the list (the lead decides whether to relay them). Every resumed session
  gets its bounce list.
- **Release one**: a 「解除」 button beside the light releases that session alone (a lead together with its members),
  same notices and re-ask, unattended stays on.
- **Relay card**: the session's title beside the ids, and a brief from the session's **workbook** (Haiku writes it
  after every turn): the 3 newest entries as 「申請前做了什麼」, open todos as 「接下來打算做什麼」. No extra model call.
- Remote members (another host) are told but their host does not refuse messages in this spec (§6; user OK 2026-10-12).

## 2. What exists (facts, main e5448abf4)
- Unattended auto-approves `lead`, `self_relay`, `member_relay`, `adopt` (`internal/team/wire_unattended.go:33`).
  `PUT /api/team/unattended` (`internal/module/team/unattended_handler.go:81`, under `createMu`) runs
  `sweepUnattended("switch on")` on a change to on and nothing on a change to off (`:77`, `:109`); the switch is a
  host-config CAS (`internal/module/hostconfig/unattended.go:112`); boot sweeps only when on (`team/boot.go:69`).
- Quota: `beginApproved` / `autoApprove` → `ErrQuotaExhausted` → `holdForQuota` (in-memory, `quota.go:291`); the self
  begin path opens op + approval (`relay_handler.go:379-409`); member `relay_member.go:151,177,227-280`; forwarded
  `relay_forwarded.go:76`. A held row is a person's card, timed out at 600 s (`wire_relay.go:74`, `sweeper.go:90`).
- Delivery: `POST /api/peers/send` (`peers/send.go:261`) → local `deliverLocal` (`send.go:600`) / remote → receiver
  `deliverToLocalTarget` (`deliver_local.go:45`); daemon team notices: `m.sender.Send` (`team/module.go:420`) →
  `moduleSender` (`peers/sender.go:56`) → `handleSend` in process. Owed notices retry ~10 min then drop
  (`notify.go:14,79,123`); best-effort notices drop on failure (`notice.go:72`); remote notices treat 409 as permanent
  (`peers/team_notice.go:134`). `DeliverRequest` / `SendRequest` carry no message class (`internal/peers/wire.go:274,313`).
- Mod: begin → `awaiting`, U7 prompt hold ≤ 11 min (`register.js:1529-1565`); a 409 refusal with a known code leaves
  the mod idle (e.g. `self_relay_paused`); control messages parse only `[pdx-relay:control] op=<uuid>`
  (`member.js:16`, `register.js:1348`); mod protocol is reported in hello (`mod_hello`).
- Workbook routes `GET /api/workbook/conversations/{provider}/{session_id}`, `/entries`, `/todos`
  (`workbook/api.go:153-155`). Card: `Approval.Origin.Title` reaches the App (`internal/team/wire.go:63-73`);
  `MemberRelayPayload.member_title`; SPA `ApprovalDialogHost.tsx:70,75`.

## 3. Design

### 3.1 State (UP-1a)
- `session_pauses(session_id, cause CHECK (cause IN ('quota','lead')), role ('solo'|'lead'|'member'), team_id,
  lead_session_id, on_since INTEGER NOT NULL, since, ref TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (session_id, cause))` — `ref` / `title` are a snapshot taken when the pause is written (UP-2), used by
  the route when the live registry does not know the session (offline, registry error). `on_since` is the unattended
  switch's `Since` at the time of the pause (the host-config value changes on every off → on). A session is **paused
  iff it has any row**. Writing a cause that already exists is a no-op (idempotent per (session, cause)).
- `pause_outbox(seq INTEGER PRIMARY KEY AUTOINCREMENT, session_id, kind, text, reask_id, created_at, attempts)` —
  flow notices, reask controls and push requests, written in the transaction that causes them; one loop sends them in
  **`seq` order with head-of-line blocking** (a failed head is retried before anything after it; a target session that
  no longer exists drops its row). Sends are at least once.
- `pause_bounces(session_id, from, text_hash, at, first_line, PRIMARY KEY (session_id, from, text_hash))`.
- `pause_release_tokens(token PK, session_id, created_at)` — §3.4.
- "Live local member of a lead" = `team_members` of the lead's team with `state = 'active'`, `host_id` = this host,
  and `teams.ended_at = 0` (the persisted predicate of `ActiveMembersOfLiveTeams`, `team_store_members.go:71`;
  `killing` / `releasing` / `gone` / remote excluded).
- `GET /api/team/paused` → `{sessions: [{session_id, ref, title, role, causes, since, lead_ref, bounced}]}`; roster
  rows gain `paused: {causes, since} | null`. Capability `team.pause.v1` is announced only by **UP-3b** (when entering,
  refusing, leaving and release all exist). **Entering a pause is behind a compile-time switch** (`pauseEntryEnabled`,
  false until UP-3b flips it together with the capability): while false, `SpendOrPause` never takes the pause branch and
  begin behaves as today (held card). So main stays deployable after every phase (1f, UP-1a review #3, UP-2a review #4).

### 3.2 Refusing deliveries (UP-1b)
- Both delivery points (before `deliverLocal`; in `deliverToLocalTarget` after `findTarget`) call **one team-store
  primitive** `PauseVerdict(session, from, text)` (reached through a registry interface, so peers does not import
  team): in one transaction, if the session has a pause row, upsert the bounce row and return `paused`; else return
  `ok`. `paused` → 409 `{error: "session_paused", detail: "對方暫停中（接力額度用完，等使用者回來），訊息沒有送達"}`.
  Resume deletes pause rows and bounce rows in one transaction, so a delivery is either bounced-and-listed or
  delivered, never bounced-and-lost.
- **Only exemption**: `peers.Sender.SendFlow(ctx, req)`, an in-process Go call used only by the pause outbox loop;
  nothing over HTTP can set it. Everything else (daemon team notices, the lead's own messages) is refused while paused;
  notice drivers keep their own retry rules and what they lose is in the bounce list. The lead manages a paused member
  with commands (`pdx relay`, `pdx kill`), not messages.
- The user's input goes through the mod (`prompt/next`), never `/peers/send`. Native `SendMessage` is out of scope.

### 3.3 Entering (UP-2a self / idle, UP-2b member / held cards)
- **One store primitive `SpendOrPause`** used by the self begin path and the member relay gate: one write transaction
  that (1) returns the existing pause if this (session, `quota`) row exists; (2) tries the conditional quota spend;
  (3a) spend ok → creates the op (+ approval) as today; (3b) spend found no quota **and** unattended is on (read in the
  same call, passed in) → inserts the pause rows (session `quota`; for a lead, each live local member `lead`) with
  `on_since`, the outbox rows (notices, push), and creates **no** op. Quota PUT cannot slip between the check and the
  pause (same transaction). Unattended off → today's held card.
  - Self / idle answer: 409 `{error: "paused_no_quota"}`; member relay answer to the lead's `pdx relay _<ref>`: 409
    `{error: "member_paused_no_quota", detail: "<title>（<ref>）接力額度用完，已暫停"}`. Both codes join the CLI's
    exit-13 list (`cmd/pdx/relay.go:51,196`): stderr ends with the code, stdout has no op, `--wait` does not poll.
  - Texts: self 「[pdx team] 接力額度用完（無人值守），先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來。」; member of
    a paused lead 「[pdx team] lead 已暫停（接力額度用完）：手邊這一步收尾後暫停，等 lead 指示。」; member's own quota
    「[pdx team] lead 安排：接力額度用完，先暫停，等 lead 指示。」.
- **Switch on with open held cards** (`sweepUnattended`): for each held row whose auto-approve finds no quota, one
  transaction: CAS the approval (`state = 'open'`, deadline not passed) **and** its linked op (`awaiting_approval`)
  → both `cancelled` (reason `paused_no_quota`), insert the pause rows and outbox rows. A concurrent decide or an
  expired row loses the CAS and nothing else happens.
- **Push**: an outbox row of kind `push` (lead / solo only: 「<title> 已暫停：接力額度用完，等你接手」(+「，含 N 個 member」)),
  sent by the loop through an in-process push interface with collapse id `pause:<session>:<since>`; at least once.
- **Remote members** (another host): a forwarded member relay with no quota keeps today's held card (a pause row on the
  lead host could not stop a member whose deliveries are judged on its own host — UP-2b review). A paused lead's remote
  members are not paused; they are **told** through the remote notice pump: new notice kinds `lead_paused` /
  `lead_resumed` with the same texts as the local ones, written with the lead's pause / resume group, best effort
  (UP-3c).
- `adopt` and other approvals are unaffected.

### 3.4 Leaving (UP-3a switch off / reconcile, UP-3b release one)
- **Resume group** for a set of pause rows, in one transaction: delete the rows; then for each session **whose last
  cause is now gone** enqueue (in this order) its resume notice, its bounce list (and delete its bounces), and — for a
  lead / solo `quota` cause — a release token + `reask` control; a member that still has another cause gets nothing.
  Lead rows are enqueued before their members' rows (outbox FIFO keeps the order).
  - lead / solo: 「[pdx team] 管理者已回位。接力申請已重新送出，等使用者核准。」 (release one: 「[pdx team] 已解除暫停。接力申請
    已重新送出，等使用者核准。」); member (lead cause): 「[pdx team] lead 已恢復，等 lead 指示。」; member (own quota):
    「[pdx team] 管理者已回位，等 lead 指示。」 and one notice to its lead listing them 「[pdx team] 這些 member 之前因額度用完
    暫停：…，要不要接力由你決定。」; bounce list 「[pdx team] 暫停期間有 N 則訊息被退回：<time> <from>：<first line> …」.
- **Switch off** (`handleUnattendedPut`, on → off, after the CAS, under `createMu`): resume group for all rows.
- **Reconciliation** (boot and every 30 s): resume group for every row with unattended off **or** `on_since` ≠ the
  current `Since` (a pause from an earlier on-period: covers a crash between the off CAS and the team transaction even
  if the switch was turned on again).
- **Release one** (`POST /api/team/paused/{session_id}/release`, body `{client: {kind: "app", label}}` — 400 when
  missing / blank / another kind; an audit claim like `PUT /api/team/roots`, not a security boundary): resume group for
  that session; a lead takes its `lead`-cause members with it; unattended stays on.
- **Release token**: `pause_release_tokens` row (token = the `reask_id`, a UUID) written in the resume group; the
  control is `[pdx-relay:control] reask id=<uuid>`; the mod calls `pdx relay begin --reask <uuid>`; `SpendOrPause`
  consumes the token in its transaction and, with a valid token and no quota, opens a **normal held card for a person**
  (never a pause, never auto-approved) — so a released session's re-ask reaches the user even with unattended on. An
  unknown or used token → the ordinary rules. Tokens older than 24 h are deleted by reconciliation.
- Old mods (hello without the new protocol) get no `reask`; their session gets 「[pdx team] 管理者已回位。請在這個 session
  打 /relay 重新申請接力。」 instead (and no token is written).

### 3.5 Mod (UP-4)
- 409 `paused_no_quota` → idle, no hold, and a **paused latch** set: no self ask on later turns (the +10 % gate is not
  enough) until a `reask` control or `session.start` clears it.
- `[pdx-relay:control] reask id=<uuid>` → consumed (never shown to the model); a bounded set of seen ids drops
  duplicates; `pdx relay begin --reask <uuid>` once; latch cleared.
- Hello reports the new protocol so the daemon may send `reask`.

### 3.6 App (UP-5a card, UP-5b light — interface line)
- UP-5a: the relay card shows the title (`approval.origin.title` / `member_title`) beside the ids, and a brief fetched
  at render time for the subject session: the 3 newest workbook entries (newest first) as 「申請前做了什麼」 and its open
  todos as 「接下來打算做什麼」; loading shows nothing extra, an error or no workbook shows no brief. 88 writes the task
  list from this contract.
- UP-5b: the 「被擋下」 light per session (roster `paused`, `GET /api/team/paused`) with a 「解除」 button (§3.4 release).

## 4. Compatibility
- Additive: tables, roster field, capability, error codes. An old App ignores them.
- Old mod: 409 `paused_no_quota` → idle (its +10 % gate may ask again later and get the same 409; harmless); no
  `reask` (protocol gate) — the 「打 /relay」 notice instead. Mod change ⇒ `pdx setup` on both hosts at release.

## 5. Plan (each phase one PR, ≤ 800 lines; codex R1 + R2 each)
- **UP-1a tables (incl. the `ref` / `title` snapshot), predicate, paused query, route, roster (no capability yet).** Tests: migration on an existing DB; two causes
  on one session; idempotent cause write; member predicate (active local live-team yes; remote, killing, releasing,
  gone, ended team no); route / roster shapes.
- **UP-1b delivery refusal.** Tests: both arms → 409 + bounce, collapse on (from, hash); `SendFlow` passes, an admin
  `/peers/send` and a remote `/deliver` with any text do not; daemon team notices refused; delivery vs resume in both
  orders (bounced-and-listed or delivered, never an orphan bounce). Mutation: exempt by text prefix ⇒ red.
- **UP-2a `SpendOrPause` + self / idle + cascade + outbox loop + push wiring.** Tests: spend ok ⇒ today's op; no quota +
  unattended on ⇒ 409, no op, rows, outbox; unattended off ⇒ held card; already paused ⇒ 409, no new rows; begin vs
  quota PUT on the last unit (both orders) ⇒ consistent; cascade members by the predicate; outbox FIFO with a failing
  head blocking the next; crash after send before delete ⇒ repeat, never lost; push reaches the push module (integration).
  Mutation: skip cascade ⇒ red; check quota outside the transaction ⇒ the PUT race test red.
- **UP-2b member relay / forwarded + held-card sweep + CLI codes.** Tests: member gate ⇒ member row, lead's 409 with
  title/ref, CLI exit 13 and no poll; remote (forwarded) member: no quota keeps the held card, no `session_pauses` / `pause_outbox` rows (notices are UP-3c); sweep: open held row ⇒ row + op cancelled + pause in one
  transaction; a concurrent decide (both orders) and an expired row ⇒ no pause; dual cause (member quota then lead
  pause, and the reverse).
- **UP-3a switch off + reconcile + resume group.** Tests: resume group order (lead before members), only-last-cause
  resume notice, bounce lists sent and cleared, member-quota list to the lead; off CAS then crash then on ⇒ the old
  rows resumed by `on_since`; reconcile while on with current `on_since` ⇒ nothing; old-mod fallback notice.
- **UP-3b release one + tokens + capability `team.pause.v1` + flip `pauseEntryEnabled`.** Tests: claim validation; solo / lead-with-members / member-quota release; the re-ask
  with the token under unattended on + no quota ⇒ a person's held card (not a pause); token used once; duplicate
  `reask` delivery ⇒ one begin; token expiry.
- **UP-3c remote member notices.** Tests: a lead pause with a remote member queues one `lead_paused` remote notice with
  the local text; the resume group queues `lead_resumed`; the member host renders both kinds; an old member host (kind
  unknown) drops it without breaking the pump.
- **UP-4 mod.** Tests: 409 ⇒ latch, no ask on later turns; `reask` ⇒ consumed, dedup by id, one begin with `--reask`,
  latch cleared; `session.start` clears the latch; hello protocol; old daemon unchanged. `go test ./cmd/pdx/...` (incl.
  plugin) + plugin vitest.
- **UP-5a / UP-5b (88).**

## 6. Out of scope
- Refusing messages to **remote** members on their host (they get the notices only; user OK 2026-10-12); native
  `SendMessage`; pausing when unattended is off; interrupting a running turn; a dedicated Haiku call for the brief.
