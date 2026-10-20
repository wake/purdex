# Name pool v2 + team interface v2 — spec

> B line, 2026-10-19, written by 88 (interface lead). It builds on the name pool v1 spec (`2026-10-14-name-pool-spec.md`),
> team name (`2026-10-09-team-name-spec.md`) and journal v5 §3.9 (`2026-10-17-journal-v5-spec-plan.md`). The user's
> discussion record, question rounds 1–4 and the identity probe table are on the B-line page
> https://pages.mlab.host/wake/purdex-b-line/#d-names-team. The avatar layout mock-ups are at `avatar-layouts.html` and the
> 32 sample characters at `personas.html` on the same site. Code facts are from main `7af59cc75`.
>
> 1f reviewed §5, §6.3–6.4 and §8 on 2026-10-19. Its eight points are folded in:
> - the two-DB order of a lead rename (§5.3);
> - the event table's DB and transaction (§8);
> - the time of a timeout (§8);
> - alias retention (§6.3);
> - the relay skip and the rename notice (§6.4);
> - ambiguous `/team` names (§5.3);
> - reservations being local (§5.2);
> - migrations and backups (plan §3).
>
> Items marked **[待確認]** are user-visible defaults that 88 chose while the user was away. They are listed again in
> §12.

## 1. Problem

Pool names (v1) made addresses readable (`purdex-3f-adam`), but four problems remain:

1. **They carry no face.** A name is only a string, and every place in the App still shows an id-like string:
   - the deck shows `來自 mlab/purdex-1f-vq`;
   - the status bar shows `purdex-1f-vq [1f9a2b]`;
   - approval dialogs show a title or ref.
2. **They are random.** A lead's members get new random names every time, so a team never looks like the same team twice.
3. **The team panel has no room for faces.** Its settings also live in a different panel: the cap and the folders sit in
   the unattended panel, and JV-5b was going to put them behind a gear.
4. **The sidebar has only one grouping, by workspace.** It cannot show "who works on what".

## 2. User decisions (do not reopen)

From the B-line page, rounds 1–4 and the follow-ups, 2026-10-18 22:2x to 2026-10-19 04:xx.

| # | Decision |
|---|---|
| U1 | **No pre-built teams.** The name pool stays global. The goal is stable lead/member line-ups (about 1+6, 1+3, 1+3, 1+1). |
| U2 | **"Familiar faces first"** assignment (the proposal on the page, rules 1–3). |
| U3 | **Becoming a lead lets you choose the name:** a card like today's ask card, or the name given in the command. |
| U4 | **A rename keeps the old name as an alias.** Existing messaging must not break, and the address format does not change. |
| U5 | **Old conversations get a name automatically** (no card). |
| U6 | **Member cap ceiling 8 → 9.** The per-team cap stays (default 3). |
| U7 | **Characters ("personas") are not bundled** (the source is public on GitHub). A **persona manager page** in the App lets each user build their own. Fields follow `personas.json`, plus `lead`, which marks a default lead identity. Images are cached when the settings are saved. |
| U8 | **Avatar sources may be mixed:** Lorelei, Notionists, Open Peeps, Micah, Shapes, Clay (DiceBear), PlaceCats, PlaceDog, Picsum. Avatars must stay visible on the dark theme. |
| U9 | **The team panel gets avatars of about 44 px that span two rows.** The lead row also gets two rows. |
| U10 | **The sidebar uses the "a" layout:** the avatar replaces the agent icon and the light stays. **Changed 2026-10-20 (user, 759 acceptance):** only in the task-group view; the workspace view keeps the rows as before 758 (§9.6). |
| U11 | **The sidebar gets a second view, "task groups"** (任務編組－員工模式). Its group header is the task group name, which for a team is today's team name (no fixed team name). Names can be set by command or in the App. **Single sessions are put into a task group by hand.** Their groups sit **at the same level as team groups**, much like a workspace. |
| U12 | **The icon left of the home row's name becomes its own toggle button.** It switches between the two sidebar views. Its icon follows the view: the profile's own icon, or a **team icon** (a new profile setting). |
| U13 | **Team settings (member cap, relay quotas, folders) sit directly on the team panel.** There is no gear; this replaces JV-5b 2/3 and 3/3. The original → new wording was confirmed. |
| U14 | **The deck shows avatar + name instead of a peer id** wherever it names a sender. This changes the display only; addresses and routing stay as they are. |
| U15 | **The deck shows avatar + name before every non-continuous stretch of a session's own speech.** The turn header becomes `[avatar] Name · model · time`. Speech that resumes after a command or tool rows gets its own avatar + name. Continuous speech keeps the header only. |
| U16 | **The unattended panel shrinks.** It keeps the **latest 30 decisions** in a scrollable list: unattended auto-approvals and the user's approvals and refusals. **Unattended on/off events** go into the same list in a prominent style. |
| U17 | **Persona style in the journal is postponed** to the workbook topic. |
| U18 | **The daemon half is B-line work** (1f agreed). The parts that touch the team module are reviewed by 1f. |
| U19 | **Mac first; iOS follows later.** |

## 3. Decisions taken here (lead)

| # | Decision | Why |
|---|---|---|
| L1 | Personas are stored by **each daemon**, in hostconfig key `personas`. **One host is the source and the App copies it to the others** (§4.3); daemons do not sync among themselves. **User 2026-10-19**, interim until the sync review. | one pool across hosts needs one persona set (v1 rule "no duplicates across hosts") |
| L2 | A persona has a **code** (`^[a-z]{3,12}$`, unique, fixed once created) and a **display name** (free text, can be changed). The address uses the code. **[待確認]** | addresses must not change when a label is edited |
| L3 | An avatar is stored as a **128 px raster data URI** (WebP, PNG fallback), made in the App when the persona is saved. The recipe (style, seed, options, or photo source) is kept beside it so the image can be redrawn. **[待確認]** | U7 "cached on save"; no network fetch to show a face; no SVG (script surface) |
| L4 | **No personas → the built-in 30 names** of v1. They are unchanged, `kenji` included. Personas present → the pool is exactly the active personas. Pool used up → `<base>-<xx>`, as today. | a fresh install keeps working; the built-in list is only a fallback now, so the Kenji → Hiro rename is moot |
| L5 | **Deleting a persona retires it.** A retired persona is never picked, but its code is still recognised, still displays, and cannot be re-created by another persona. | live holders and old messages keep a face; "historic names" stay pool names |
| L6 | **Name history** lives in the lead's daemon's meta DB, in two tables (§6.1). The familiar set travels to the other hosts in the peers envelope. | the team module knows leads and members; the picker runs in peers |
| L7 | **A member's name is reserved when the team accepts its spawn** and stored on the spawn op. For a remote spawn it is sent in the spawn command. | the lead's daemon holds the pair history; the session registers later |
| L8 | **`/team <name>`:** the first word names the lead when it equals an active persona's code or display name (case-insensitive). Without a name, `/team` shows the name card. **[待確認]** | U3 |
| L9 | ~~**A manual task group only gathers sessions of one host.**~~ **Retired (TG-6):** the groups are the profile's `purdex-task-groups` now ([task groups spec](2026-10-20-task-groups-as-workspaces-spec.md) §4, §8); a group can hold sessions of any host. | ~~stored per host, so no cross-host sync is needed~~ |
| L10 | Task-group membership is keyed by **lineage root**, so a relay keeps it. A session in a live team shows under its team, and its manual membership is ignored until the team ends. | U11 "same level"; no double listing |
| L11 | The decision log covers the **team approvals**: `lead`, `adopt`, `self_relay`, `member_relay`, `spawn_resources`. Chat prompts (`hook_ask`, `hook_permission`) are excluded. **[待確認]** | the panel is about unattended team actions; chat prompts would flood 30 rows |
| L12 | **Relay quotas stay in both panels**, as decided on 10-17, until the user says otherwise. **[待確認]** (open question on the page) | no silent reversal of a decision |
| L13 | The **team rename already exists**: `PUT /api/team/appearance`, capability `team.edit.v1` (`internal/module/team/appearance_store.go:83`). D-N5's "no rename" is stale, and task-group naming of a team reuses this route. | fact (probe) |

## 4. Personas

### 4.1 Shape

```json
{
  "code": "hiro",                 // ^[a-z]{3,12}$, unique among active and retired, immutable
  "name": "Hiro",                 // display name, 1..24 runes, printable, trimmed
  "sex": "male",                  // "male" | "female" | "" (display only)
  "lead": false,                  // a default lead identity (U7)
  "retired": false,               // L5
  "nationality": {"code": "JP", "zh": "日本", "en": "Japan"},
  "template": {"name": "...", "name_zh": "...", "field_zh": "...", "field_en": "..."},
  "personality": {"zh": "...", "en": "..."},   // ≤ 50 runes / words each (U7 limits)
  "method": {"zh": "...", "en": "..."},
  "interests": {"zh": "...", "en": "..."},     // ≤ 10 runes / words each
  "avatar": {"source": "open-peeps", "seed": "Hiro", "options": {}},   // the recipe, opaque to the daemon, ≤ 2 KB
  "avatar_data": "data:image/webp;base64,...", // L3, ≤ 32 KB as a string; "" → the App draws initials
  "updated_at": 1760000000000
}
```

- The daemon checks:
  - the code rule;
  - uniqueness, including against retired codes;
  - the sizes;
  - that `avatar_data` is `data:image/(webp|png|jpeg);base64,`, decodes, has matching magic bytes, and is at most 128×128.
  The recipe and the text fields beyond their sizes are opaque.
- **At most 64 personas**, retired ones included. The personas route accepts a body of up to **4 MiB** (hostconfig's
  default is 1 MiB, `internal/module/hostconfig/handler.go:12`).
- `personas.json` from the B-line page imports with the fields mapped by name: `id` → `code`, `name` → `name`, and the
  rest by the same names.
  - **Import merges, it never replaces** (decided 10-19 for NA-1b, after codex found that a literal replace lets an old or
    partial file wipe personas).
  - A new code is created. For an existing code, each field the file has overwrites the stored one; a field the file
    lacks keeps its stored value, and nested objects are compared sub-field by sub-field.
  - `retired` is never changed by an import. A code missing from the file is left alone: an import never deletes or
    retires.
  - Avatar: for an existing code whose recipe in the file differs from the stored one, the image is redrawn from the
    file's recipe. An equal recipe keeps the stored image. A new code is always drawn.
  - The dry-run summary counts new, changed and skipped personas, with the reason for each skip.
  It is the user's own data and is **not committed to the repo** (U7).

### 4.2 Storage and routes

- The data is the hostconfig key `personas`. It reuses the store's revision and CAS (`store.go` `Put`, 409 with the
  current entry). The value is `{"items": [...], "stamp": {"at": ms, "host": "<host_id>"}}`. On a PUT from the App, the
  daemon sets the stamp to now and its own host id.
- `GET /api/hostconfig/personas` returns that value. **`GET /api/hostconfig` (all collections) leaves out `personas`**,
  so the frequent full read stays small.
- `PUT /api/hostconfig/personas` takes `{items, baseRevision}`, like every collection.
- The capability is `personas.v1`.

### 4.3 One source, copied by the App (L1, user 2026-10-19)

The user decided this on 10-19 as the interim model. It replaces the daemon-to-daemon sync of rev 2, which was dropped
before any of it merged. Purdex's sync and centralisation mechanisms as a whole are to be reviewed and consolidated
separately, and this section may change with that review.

- **Each daemon keeps its own copy** in hostconfig key `personas` (§4.2). A daemon names its sessions from its own copy,
  with or without the App running.
- **One host is the persona source**, by default the App's main host (mlab). It is a per-device App setting, shown on the
  persona page as 「正本主機」. The App edits only the source's copy.
- **Saving.** The App writes the source first (`PUT /api/hostconfig/personas`, CAS as usual). It then writes the same
  items to every other host it is connected to, each with that host's own `baseRevision`.
- **Catching up.** When the App connects or reconnects to a host, it compares that host's copy with the source's, by a
  hash of the canonical items. When they differ, it writes the source's items there.
  - A host that was offline during a save is therefore caught up the next time the App reaches it.
  - A host the App never reaches keeps its last copy, and keeps naming from it.
- **The App reads the source's copy** for the page, the avatars and `seatOf` (§9.2). When the source is unreachable, it
  reads the copy of any connected host, and the page is read-only with a line saying the source is offline.
- **Daemons do not talk to each other about personas.** There is no envelope field, no peer route, and no stamp
  comparison.
  - The stamp (§4.2) only records who wrote last and when.
  - The familiar sets of §6.1 still travel in the envelope, because they belong to naming, not to personas.
- **Codes are never lost.** A daemon refuses a PUT that drops a stored code (L5; the 400 with index −1, NP-1a). So a copy
  that has a code the source lacks blocks the mirror write. This can happen when an older App or another device saved
  there.
  - The App then adds that code's persona to the source unchanged, and mirrors again.
  - The persona page lists such adoptions in its next save notice.
- Without the capability `personas.v1` on a host, the App neither reads nor writes that host's copy.

### 4.4 The pool in the daemon

- `ipeers.PoolNameOf` stops being a fixed set (`internal/peers/namepool.go:31`). A small registry in `internal/peers`
  holds the **recognised** names and the **display names**. The peers module swaps it atomically whenever the persona
  collection changes.
  - Recognised = the built-in 30 ∪ every persona code, retired ones included (L5).
  - Pickable = the active personas, or the built-in 30 when there are no personas (L4).
- `journal.DisplayName` (`internal/journal/names.go:17`) returns the persona's display name when the suffix is a persona
  code. Otherwise it keeps today's rule: the capitalised built-in name, or the virtual name itself.
- The tests that pin 30 names (`internal/peers/namepool_test.go:10`, `namepool_snapshot_test.go:314-349`) pin the
  built-in fallback instead. The 3–5 letter and first-three-letter rules apply to the built-in list only; persona codes
  follow L2. Nothing routes on prefixes (tier 1 is an exact match, `internal/peers/address.go:321-327`).

## 5. Team-module changes (1f reviews)

### 5.1 Member cap ceiling 9 (U6)

- `team.MaxMaxMembers` becomes 9 (`internal/team/wire.go:109`). Every literal "8" follows:
  - the daemon:
    - `max_members_handler.go:30-31` (the message text);
    - `handler_create.go:44-50` (comment);
    - tests `unattended_test.go:199`, `person_requests_test.go:58`;
  - the CLI: `cmd/pdx/lead.go:27` (usage), `:126-127`, and `lead_test.go`;
  - the App:
    - `spa/src/lib/team/types.ts:20-22,363,428-434`;
    - `ApprovalDialogHost.tsx:199,445`;
    - `UnattendedQuotaSection.tsx:110-123`;
    - locales `en.json:2392` / `zh-TW.json:2392` ("1–8");
    - tests `ApprovalDialogHost.test.tsx:143`, `ApprovalDialogHost.memberLimit.test.tsx`.
- The App allows 9 only against a host that has the new capability **`team.max_members.v2`**. Against an older daemon
  it keeps 8, because that daemon would refuse 9 with a 400.
- `UnattendedLeadMaxMembers` (3) is unchanged.

### 5.2 Member name reservation (L7)

- `spawn_ops` gets the column `pool_name TEXT NOT NULL DEFAULT ''` (`internal/module/team/spawn_store.go:29`, `spawnCols`).
- On `accepted`, the team module asks the peers name service for a member name for this lead (§6.2 rule 2) and stores
  it. The spawn answer carries it, so `pdx spawn` can print `→ Freya`.
- A **remote spawn** sends `pool_name` in the `spawn` team command (`internal/team/wire_command.go:57`). The member host
  stores it on its own `spawn_ops` row. An older member host ignores the field and names the session by its own rules.
- **Held names include live reservations:** ops not yet `registered`, not failed, and younger than 10 minutes. Two
  concurrent spawns never get the same name.
- **Where reservations live (codex #2).**
  - They are kept **in memory by the name service**, under the same pool lock as picking (`assignPooled`) and settling
    (`settlePoolCollisions`). Reserving, releasing and counting held names are one atomic section.
  - At boot they are **rebuilt from the team DB**: the open ops with a `pool_name`. The team module registers a reader
    in core, and the peers module never opens `team.db`.
  - **Released** when the op is registered (the session took the name), fails, is voided or times out, or when its
    remote command expires. An entry older than 10 minutes also lapses by itself, so a missed release costs nothing.
  - `ReserveExact` (a remote spawn; the lead host chose the name) is **refused when the name is held locally**. The
    member host then names the session by rule 3, and the spawn itself goes on.
- **A reservation is local to the daemon that holds it.** Other hosts do not see it. A cross-host clash is settled by
  v1's "newcomer yields" rule, which applies the same way when a spawn outlives its 10 minutes (a slow remote spawn, a
  spawn queued behind memory pressure). That degradation is accepted.
- So the `→ Freya` that `pdx spawn` prints is the **reserved** name, not a guarantee. The text says 「預訂」.
- **Naming the session:** on the first naming pass of a session born from a spawn op, the reserved name wins over a
  random pick. The link is the op id in the pane tag `@pdx_spawn_op` (`spawn_tmux.go:23,52`). The plan picks how
  `resolveNames` gets it (today's `nameCandidate` has no pane data, `internal/module/peers/vnames.go:49-53`).
- If the reserved name became held by an older process in the meantime, v1's "newcomer yields" rule renames the member.

### 5.3 Lead name at approval (U3, L8)

- `Grant` gets `lead_name` (a code, optional). On approve, the daemon renames the lead session to `<base>-<code>`,
  records the alias (§6.3) and writes a lead-history row.
- **Order.** The names are in the meta DB (`internal/store`, `cmd/pdx/main.go:298`) and the team row is in the team
  DB (`internal/module/team/store.go:180`). These are two SQLite files, so there is no single transaction.
  1. **Rename first**, a CAS in the meta DB (the rename and its alias row in one meta transaction). This is the only
     step that can fail with `name_taken`. Then **the approval is refused for now**: it stays open, and the dialog
     reopens with the list refreshed.
  2. **Then the team row** (`CloseLeadApproved`).
  3. If the team row fails, the daemon **renames back** by CAS and deletes the alias it just added.
  4. The lead-history row is written **only after the team row succeeds**. Its failure is only logged.
  - **A crash between steps 1 and 2** leaves the lead renamed, with no team and the approval still open. This is
    harmless: on a retry, 「保留目前的名字」 is the new name.
  - **Losing the close (codex #1).** `CloseLeadApproved` reports a lost CAS as `won = false` with no error
    (`team_store.go:460-480`): another decide, a requester cancel, or the sweeper's timeout closed the approval first.
    A lost close is handled **like a failed one**: rename back, delete the alias, write no history, send no notice.
  - **Decides on one approval run one at a time.** An in-process lock keyed by approval id is held across steps 1–3.
    The sweeper's expiry close (`sweeper.go:197-207`) takes the same lock. Closes that do not take it (a cancel, an
    abandon) are covered by the lost-close rule above.
- The renamed lead gets a notice with its new address (§6.4).
- Absent `lead_name` means the session keeps its name. It still gets a lead-history row when its name is a persona code.
- `LeadPayload` gets `lead_names`: the candidate list at request time (§6.2 rule 1).
  - The payload is stored, so it goes stale. The dialog reads a **fresh list when it opens**, through
    `GET /api/team/approvals/{id}/lead-names` (codex #11).
  - The stored list is the fallback when that read fails.
- **The `/team` command** (`cmd/pdx/plugin/purdex/hooks/member.js:78` `parseTeamArgs`, `register.js:990` `teamCommand`):
  - The first word matches an active persona → it is `lead_name`, bound into the person token request, so the agent
    cannot drop it, and the rest is the note. The lead is created at once, as today.
  - Matching is case-insensitive. The word is compared with each active persona's **code** and with the **first word
    of its display name** (a display name with spaces can only match by its first word).
  - **More than one persona matches** (display names may repeat) → this counts as **no match**: the card appears, and
    the daemon never picks one.
  - No match → the request is created as a **normal lead approval with a card**, so the user picks the name. The
    person token is still used, so the decider is `person`.
  - **`/team` with no words (codex #5).**
    - A session that is not a lead → the card. Today this becomes a lead at once (`register.js:1038-1064`,
      `becomeLeadOrShow`).
    - A lead → the roster, unchanged (`register.js:1057`).
  - **Mixed versions (codex #10).** The mod checks the persona list through `pdx`. When that fails (an older `pdx`),
    or when the daemon lacks `team.lead_name.v1`, `/team` behaves exactly as today: no name, no card, the words are the
    note.
- Unattended mode refuses lead requests, as today (`UnattendedRefusable`, `wire_unattended.go:49`), so no lead is ever
  named without the user.
- The capability is `team.lead_name.v1`.

### 5.4 Adopt

An adopted session keeps its name. On approval the daemon records one pair (lead code → member code) when both are
persona codes (U2 rule "adopt keeps its own name and counts from then on").

## 6. Names (peers module)

### 6.1 History tables (meta DB, `internal/store`)

```sql
CREATE TABLE IF NOT EXISTS name_leads (code TEXT PRIMARY KEY, count INTEGER NOT NULL, last_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS name_pairs (lead_code TEXT NOT NULL, member_code TEXT NOT NULL, count INTEGER NOT NULL,
  last_at INTEGER NOT NULL, PRIMARY KEY (lead_code, member_code));
```

- Written only through the name service:
  - on lead approval, after the team row succeeds (§5.3; a failure is only logged);
  - **for a spawned member, when its op is registered** (codex #3). The pair is the lead's code and the op's
    `pool_name`; the member session may not be named yet at that moment. A local spawn records it on the lead host at
    registration. A remote spawn records it on the lead host when `FinishRemoteSpawn` reports the registration
    (`internal/module/team/spawn_remote.go:185-217`). An op without a `pool_name` records nothing.
  - on adopt approval (§5.4).
  - Each failure is only logged.
- A relay writes nothing: the successor inherits the name.
- **Familiar** = every `name_leads.code` ∪ every `name_pairs.member_code`. Each daemon publishes its familiar set in
  the peers envelope (`names.familiar`, added by NP-3), capped at 64 codes, most recent first, and unions the other hosts' sets.

### 6.2 Picking rules

Free = pickable (§4.4) − held (local live, remote snapshot, live reservations).

| Rule | Who | Order |
|---|---|---|
| 1 | a lead's name card | 1. keep the current name (preselected); 2. free former leads, most recent first; 3. free `lead: true` personas; 4. the other free names |
| 2 | a member of lead L | 1. free names paired with L, most pairs first, then most recent; 2. a free **newcomer** (not familiar, not `lead: true`), at random; 3. the free name least paired with any lead; 4. `lead: true` names last |
| 3 | any other session | 1. a free newcomer at random; 2. the free name least paired with any lead; 3. `lead: true` names last |

- Nothing free → `<base>-<xx>` (v1).
- The pick and the store stay under v1's single pool lock (`internal/module/peers/namepool.go:152`).

### 6.3 Rename and aliases (U4)

- A new meta-DB table, `peer_name_aliases (alias TEXT PRIMARY KEY, session_id TEXT NOT NULL, ref TEXT NOT NULL,
  created_at INTEGER NOT NULL)`.
- A rename is a CAS on the old name (v1's `RenamePool`, `internal/store/peer_name.go:121`). In the same transaction it
  inserts the old full virtual name as an alias.
- **Routing:**
  - After tier 1 misses (`address.go:321-333`), a `<host>/<alias>` resolves through the alias row's ref, by the
    existing ref path. Lineage therefore works, and a relayed successor is reached.
  - `ipeers.Resolve` (`address.go:182`) is a pure function over the rows, so the aliases reach it as data: a new
    `ResolveSnapshot.Aliases` map (full alias → ref) (codex #4). Its one production caller (`send.go:451`) fills the
    map from a meta-DB reader, cached and invalidated on each rename.
  - Tier 1 always wins: when a live session holds the name, the alias is never consulted.
  - **A known alias is authoritative.** Once the alias row is found, the answer of its ref is the answer, a miss included:
    it does not fall on to the bare tmux-name tier, where an unrelated session of the same text would take the message.
  - **Fail closed.** When the alias table cannot be read, the local resolution counts as partial (`not_ready`): a miss may
    only be a missing alias. A live name still resolves.
  - `ResolveSnapshot` also carries `AliasSessions` (alias → the session id it belonged to). In the combined form, a live row
    with the alias's ref passes only when it is that session (two live rows can share a ref); the lineage match asks no
    owner, because a relayed successor is another session.
  - When a name is assigned anew, its alias row is deleted.
- The bracket check (`name_mismatch`) accepts a name that is the session's current name or one of its aliases.
- **Retention.** An alias is deleted once its session's lineage has had no live successor for 30 days. The table also
  keeps at most 512 rows, and the oldest by `created_at` go first.

### 6.4 Old conversations (U5)

- When personas exist, a live conversation gets a persona name on the next naming pass, under the rules of §6.2, when
  **its suffix is not a persona code (active or retired)**. This covers `purdex-88-b8`, `purdex-1f-vq` and built-in
  names such as `kenji`. Which rule applies:
  - a live lead → rule 1 without a card: the first entry other than "keep", or keep when there is none;
  - a live member → rule 2 with its lead;
  - otherwise → rule 3.
- The old name becomes an alias (§6.3). The row's source becomes `pool`.
- **A session in a relay is skipped.** When `relays_active` includes it, it is not renamed until a later pass, after
  the relay ends.
- **The renamed session is told.** It gets a system notice, through the same channel as the `[pdx team]` notices, for
  example 「你的地址改成 mlab/purdex-1f-wei，舊地址仍可用」. Agents write their own address into briefs and reports, so
  without the notice they would keep using the old one. The same notice follows a lead rename at approval (§5.3).
- The first save of personas renames every live conversation in one pass. This is expected; the relay skip and the
  notices above apply to each one.
- **[待確認]** This renames long-lived conversations, including 1f's and 88's own addresses. Their old addresses keep
  working.
- The project `CLAUDE.md` sentence "已經有名字的舊對話維持原名" changes to this rule.

## 7. Task groups (U11, L9, L10)

- **Retired (TG-6, task groups spec §8):** ~~each host stores its groups in the hostconfig key `task_groups` (`[{id, name, color?, roots, created_at, updated_at}]`, ≤ 32 groups, ≤ 64 roots each, one group per root, name by `peers.ValidateTitle`), capability `task_groups.v1`, writes by the usual CAS PUT~~. The daemon no longer has the key, the route `PUT /api/hostconfig/task-groups` or the capability; a `task_groups` row an older daemon left in `host_config` is never read. The groups live in the profile (`purdex-task-groups`).
- **A team is a task group by itself.** Its header is the team name, and renaming it goes through
  `PUT /api/team/appearance` (L13). Manual groups hold solo sessions only (L10).
- **Sidebar task-group mode** (App):
  - The list holds team groups (a team row with its members, cross-host members included), then manual groups, each in
    creation order, then **「未分組」** at the bottom.
  - The App shows a group's live sessions only. A group with none collapses and offers 「清除」.
  - Sessions are added by drag, or from the context menu with 「加入任務編組」 / 「新建任務編組」. A group header can
    be renamed in place.
  - Clicking a session switches to its workspace and tab.
  - The toggle state is per device.
- **Where the App gets a session's lineage root** (decided in NA-6a, #3495):
  - The source is the `quotas` of `GET /api/team/unattended` (`team.relay_quota.v1`). The daemon gives one row per
    live session of the host, with `session_id`, `root_session_id` and `address` (`internal/team/wire_quota.go`). The
    App matches `session_id` against the agent session id that its pane records.
  - A host with `task_groups.v1` always has `team.relay_quota.v1`. If one does not, all of that host's sessions show
    under 「未分組」; the App does not guess.
  - The App reads it only while the task-group view is open: once when the view opens, once on a host-events
    reconnect, and once (debounced) when it sees a live session that the list does not have. It always goes through
    the existing shared reader.
  - **Display** may put a session whose root is not known yet under 「未分組」 (a relayed session, before the read).
  - **Writes** (加入任務編組, 新建任務編組, a drop) need the real root. Until it is known, the menu items are disabled
    with 「讀取中」 and a drop is refused. The App never stores a session's own id as its root: after the next relay
    that session would drop out of its group, which silently corrupts the data.

## 8. Unattended v2 (U16; 1f reviews)

- **Decision log:** the latest 30 closed approvals of the kinds in L11, newest first, whoever decided. The states
  are:
  - `approved`, by unattended or by the user;
  - `denied`, by the user, or by unattended refusing a lead or an adopt;
  - `timeout`, shown as 「逾時」.
  - `cancelled` (the requester withdrew) and `abandoned` (lease ran out, origin gone) are not decisions and are left
    out.
- **The time of an entry:**
  - for `timeout`, the approval's `deadline_at`;
  - for every other state, its `decided_at`.
  - A timeout is recorded by the team sweeper (`internal/module/team/sweeper.go:206`, `CloseIfExpired` with
    `StateTimeout`). It writes `decided_at` as the sweep time, which can be much later than the deadline after a daemon
    restart. That is why the deadline is the timeout's time.
- **On/off events:** a new table `unattended_events (id INTEGER PRIMARY KEY, at INTEGER NOT NULL, on INTEGER NOT NULL,
  by_json TEXT NOT NULL)`.
  - It lives in the **hostconfig DB** (`host_config.db`, `internal/module/hostconfig/store.go:44`), the same file that
    holds `KeyUnattended`.
  - It is written **in the same transaction** as `SetUnattended`'s CAS (`internal/module/hostconfig/unattended.go:112`),
    so the state and its record cannot differ. To make this possible, the store's `Put` gains an in-transaction
    callback.
    - `Put` deliberately runs `BEGIN IMMEDIATE` on a dedicated `*sql.Conn` (`store.go:97-151`), not a `*sql.Tx`, so
      that concurrent CAS writes answer 409 instead of `SQLITE_BUSY_SNAPSHOT` (codex #8). The callback therefore gets
      that same connection as an exec/query interface.
    - A failing callback rolls the state change back too.
  - It keeps the newest 100 rows.
  - It is created by migration; no backfill (history before this version is not known).
  - `GET /api/team/unattended` merges these rows with the team DB's decisions **when it reads**.
- **Wire:** `GET /api/team/unattended` gets `log: [{at, type: "decision"|"toggle", ...}]`, the 30 newest of both kinds
  merged. Capability `team.decision_log.v1`. The current `approved` page stays for older Apps.
- The App's panel shows the log in place of the auto-approved list. The cap column and the folders leave the panel for
  the team panel (U13). The relay quotas stay (L12).

## 9. App

### 9.1 Persona manager (settings page 「角色」)

- Registered like the other sections (`spa/src/lib/settings-section-registry.ts:66`, order in `settings-order.ts`).
- Lists the personas with avatar, display name, code, lead mark and source. Edits one in a side form. Retires instead
  of deleting (L5). Imports a `personas.json` file.
- **Avatar rendering at save:**
  - DiceBear styles are drawn in the page with `@dicebear/core` and the needed styles (lazy-loaded with the page,
    versions as on the B-line page), then rasterised to 128 px WebP on a canvas.
  - A photo source is fetched by URL and drawn the same way. When the fetch is blocked (CORS), the form asks for an
    uploaded file instead.
  - The plan measures which photo sources allow CORS before the page is built.
- A credits block lists the sources that require attribution (Micah, CC BY 4.0).

### 9.2 One identity resolver

- `seatOf(address | virtual name)` → `{code, name, avatar}`. It takes the suffix after the last `-` of the name after
  the last `/`, and looks it up in the persona store.
- **The App uses one persona collection: the source host's** (§4.3); when the source is unreachable, any connected
  host's copy, read-only. It never unions collections by code (codex #6).
- It is the only parser. The four inline slicers become callers:
  - `team-views.ts:120`;
  - `UnattendedQuotaSection.tsx:47`;
  - `StatusBar.tsx:67`;
  - `WorkerStatusBar.tsx:73`.
- `<Avatar>`: one component in sizes 16 / 18 / 24 / 44. 759 acceptance (user, 2026-10-20), so a face never borrows the
  host colour (bright, and green already means "working"):
  - an image sits on a round `#dfe4ea` backing (#3604), since the drawn avatars are transparent;
  - a persona with no image (or one that fails to load) shows its initial on `personaColor(code)`, the colour the
    persona page uses, so a persona looks the same everywhere (#3621);
  - no persona: a neutral circle (a surface token, the initial in the secondary text colour) (#3621).
  - The light on an avatar's corner is the session's status light, the same one that sits beside the agent icon.
- **Trust boundary** (NA-2, #3555 / #3563): the App resolves a persona only from an address that the daemon resolved:
  - a deck sender whose `from.kind` is `peer` and that is not marked `unverified`;
  - an approval's `Origin.address`, a quota row's `address`.
  - It never resolves a name taken from message text, a plugin notice, or a registry name (`Origin.name`, which a
    session sets itself). Otherwise a session named `x-kenji` could wear Kenji's face. A sender that is not resolved
    keeps today's label.

### 9.3 Where identity becomes avatar + name

This is the default classification from the page's probe table. **[待確認]**

| Place | Change |
|---|---|
| deck peer caption `來自 …` (`deck-format.ts:34`, `UserBlock.tsx:52`), chat peer line (`ChatPeerLine.tsx`) | avatar + name; the sender string is the address (`convmodel.From.Name`), so `seatOf` resolves it |
| queued `來自 …` (`PendingTail.tsx:23`) | **unchanged, plain text** (decided 10-19, below) |
| team panel, sidebar beads, tooltips, ghost lead, ended list, workbook title, folder "who uses it" (all through `labelOf`, `team-views.ts:118`) | the `[agent][host][avatar][name][title]` structure; tooltips and titles use the name |
| approval dialog titles, pushes, toasts (`approvalSessionLabel`, `approval-format.ts:9`) | the name, with the title after it; the dialog's address row keeps the full address |
| unattended panel rows and quota table | members and solo sessions by name; a lead row keeps the team name (JV-5a) |
| session notifications (`useAgentNotifications.ts:77`) | the name when the session has one, else today's tmux name |
| status bar peer id (`StatusBar.tsx:63`), rename popover address (`RenamePopover.tsx:119`) | avatar + name; the tooltip and the copy action keep the full address + ref |
| tab titles, session lists, host page, session picker | unchanged (10-17 decision) |
| journal card actor / owner | later: needs structured seats from the daemon (§10, NP-7) |

**Why the queued line stays plain text** (88, 2026-10-19; #3559 in Backlog):
- The queued entries come from Claude Code's `queue-operation` rows (`internal/convmodel/ccnorm/queue.go`). Those rows
  carry only the text and no origin, and `queueSource` only checks whether the text starts with a peer envelope.
- A verified peer origin appears only once the message enters the turn, on the user row (`ccnorm/user.go`, kind
  `peer`). While a message is queued, the daemon has nothing that proves its sender was confirmed by the transport.
- So the line stays plain text. Once the message is delivered, the deck caption shows the avatar + name as in the first
  row. Matching queued text against recent deliveries would be a new cross-module mechanism with its own impersonation
  risk, for a line that lasts only until the turn takes the message.
- #3559 is done when Claude Code carries an origin on queued rows.

### 9.4 Deck speech (U15)

- `DeckView.tsx:72,86` marks an `agent_text` item as a **speech start** when the item before it in the turn is not
  `agent_text`. `thinking` counts as a break; `system` does not.
- `AgentHeader` shows `[avatar] Name · model · time` on the first start of a turn; the avatar replaces ◆. Every later
  speech start shows a compact `[avatar] Name` line.
- A session with no persona keeps today's header and shows no extra lines.

### 9.5 Team panel v2 (U9, U13)

- Each row: a 44 px avatar spanning two rows (no light on it: 761 acceptance moved the light to the top-right corner of the line-1 agent icon, dimming with it in a transition; the one-line cell keeps its own light), then
  - line 1 = agent icon, host badge, **name**, title (muted), context ring, workbook button;
  - line 2 = task / last line.
  - The agent icon keeps the sidebar's spacing to the subagent dots before it (759 acceptance, #3621).
- The lead row gets line 2 from the lead's own last journal line (`TeamPanelRow.tsx:81-152` today).
- **The one-line cells** (the pinned header and the title-bar strip, `TeamCell`): a seat with a persona shows its 18 px
  avatar in the agent icon's place, the light on its top-right corner and the subagent dots where they were; a seat
  without one keeps the agent icon (user, 759 acceptance, #3604).
- **Team settings on the panel:** a compact block under the title holds the cap stepper (1–9 per §5.1), the relay
  quotas (own + member pool) and the folder chips with edit.
  - It reuses the JV-5b sections: `TeamSettingsPanel` content and `Steppers.tsx` from #3187 / #3188, which are closed
    unmerged, and the JV-5b 1/3 shared reader (#3186).
  - The exact layout is fixed in the design phase on a real screen (Opus, the 10-16 rule).
  - 761 acceptance (user, 2026-10-20, #3652):
    - The labels of the settings block and of the opened editor are `text-text-secondary`; the values keep their colour.
      The steppers take the label colour from their caller (`Steppers.tsx` `labelClass`), so the unattended panel keeps
      `text-text-muted`.
    - The folders line is read-only chips. Its button is a Phosphor `PencilSimple` (i18n label, `aria-expanded`) that
      opens the editor under the line; adding and removing happen in the editor only.
    - In the editor, a `border-border-subtle` divider separates 「成員可啟動的目錄」 from 「其他主機的資料夾」 (only when
      other hosts are listed).
    - The other hosts' folders that are **applied** are chips on the line too, after the team's own, each with a
      `HostBadge` (a neutral badge when the host has no id or no look) and the tooltip 「{{alias}}：路徑（整台 {{lead}}
      共用，所有 team）」. Pending and refused changes are not listed. These grants belong to the lead's host and are
      shared by all its teams; the local roots are each team's own.
    - Opening the editor re-reads the host roots (a read already in flight queues one more).

### 9.6 Sidebar (U10, U11, U12)

- **Workspace view** (user, 2026-10-20): the session rows stay as they were before 758: `[agent icon + light] [host badge]
  session name`, with no avatar and no persona name (#3626).
- **Task-group view**: a session row's agent icon becomes its avatar (the light on its corner), then the host badge, the
  persona name (bold) and the session title (muted). A session without a persona keeps the agent icon.
- `HomeRow.tsx:42`: the `ProfileIcon` leaves the home button and becomes its own toggle button. Clicking it switches
  between the workspace view and the task-group view.
  - Its icon is the profile icon (workspace view) or the team icon (task-group view).
  - The team icon is a new field of `ProfileAppearance` (`useLocalProfilesStore.ts:88`), per device like the rest,
    defaulting to Phosphor `UsersThree`.
  - 759 acceptance (#3597): the toggle has no outline in either view (the icon itself tells the view). The team icon
    has its own weight (`teamIconWeight`, valid without `teamIcon`, so it applies to the default `UsersThree`) and its
    own colour (`teamColor`); without `teamColor` it wears the profile colour when the profile has an icon, else none.
  - The home button keeps its label and its click (home, or the profile menu on a device with slave profiles).
- Task-group view as in §7.
  - 761 acceptance (user, 2026-10-20), task-group sidebar rows only: (1) a team seat no tab shows wears the same row as an
    open one (avatar, host badge, bold persona name, then the label), dimmed to say "not open", and a click still opens
    it; (2) the persona name is `text-secondary` when the row is not active (hover and active follow the row);
    (3) the text after the name is the tab's title (`displayTitle`), the seat's label only when there is no tab;
    (4) the group title's focus ring is `focus-visible`, not the browser default outline.

### 9.7 Lead name card and cap

- The approval dialog gains a name picker for lead requests that carry `lead_names` (§5.3), with avatars. It sends
  `grant.lead_name` only when the user picked a name other than "keep".
- `/team` without a name arrives as that same dialog.
- The cap stepper allows 9 against `team.max_members.v2`.

## 10. Phases

Each phase is one PR, ≤ 800 lines or ≤ 20 files.

| Phase | Content | Review | Needs |
|---|---|---|---|
| **NP-1** | personas key and routes, validation, 4 MiB route cap, pool registry (`PoolNameOf`, display names), picker uses personas, `journal.DisplayName`, capability `personas.v1` | codex | — |
| ~~NP-2~~ | **Dropped 10-19** (user): no daemon-to-daemon persona sync; the App copies from the source (NA-1d) | — | — |
| **NP-3** | history tables, rules 1–3, aliases and routing, bracket check, backfill of old conversations, familiar sets in the envelope (`names.familiar`), `CLAUDE.md` text | codex | NP-1 |
| **NP-4** | spawn reservation (local and remote), lead name at approval, `/team` parse and card path, adopt pair, cap 9, capabilities `team.lead_name.v1`, `team.max_members.v2` | codex + **1f** | NP-3 |
| **NP-5** | task groups key and capability | codex | — |
| **NP-6** | decision log, `unattended_events` migration, `log` on the unattended route, capability `team.decision_log.v1` | codex + **1f** | — |
| NP-7 | (later) structured seats for journal actors and owners | — | NP-3 |
| **NA-1** | persona store, `seatOf`, `<Avatar>`, persona manager page, import | codex | NP-1 |
| **NA-2** | identity display in the places of §9.3 | codex | NA-1 |
| **NA-3** | deck speech starts and header | codex | NA-1 |
| **NA-4** | team panel v2 with settings on the panel; close #3187 / #3188 | codex | NA-1 |
| **NA-5** | sidebar avatar, home-row toggle split, team icon setting | codex | NA-1 |
| **NA-6** | sidebar task-group view | codex | NA-5, NP-5 |
| **NA-7** | lead name card, cap 9 | codex | NP-4, NA-1 |
| **NA-8** | unattended panel v2 | codex | NP-6, NA-4 |

- The App phases key on capabilities, so each can merge before its daemon phase. Against an older daemon they show
  nothing new.
- **One release** carries all of them, and mlab and air26 are deployed together (v1 rule).
- Design work (NA-4's layout, NA-6's look, the persona page) is done by Opus. The rest is done by Sonnet.

## 11. Tests (each with a mutation)

- **NP-1**
  - A persona with a bad code, a duplicate code, a retired code reused, an SVG `avatar_data` or an oversize image is
    refused (M: skip the check → accepted).
  - With personas, only active codes are picked (M: pick retired); without personas, the built-in names are picked.
  - A retired code is still recognised by `PoolNameOf` (M: drop retired → a live holder loses its name on the next
    pass).
  - `GET /api/hostconfig` has no `personas` (M: include it).
- **NA-1d** (App copy)
  - A save writes the source, then every other connected host with `personas.v1` (M: write the source only).
  - A reconnect to a host whose copy differs from the source's is caught up (M: compare stamps instead of hashes).
  - A mirror refused for a dropped code adds that persona to the source and mirrors again (M: give up → the copies
    stay different).
  - Source offline → the page is read-only with the offline line.
- **NP-3**
  - Rule order for each rule with seeded history (M: swap the first two tiers).
  - A rename leaves a routable alias (M: no alias → `peer_not_found`).
  - A live holder of the alias name wins tier 1.
  - The bracket check accepts an alias.
  - Backfill renames `purdex-88-b8` but not a retired-code holder.
- **NP-4**
  - Two concurrent spawns never share a reserved name (M: no reservation in held).
  - A session born from the op takes the reserved name (M: ignore the tag → random).
  - An approval whose name was taken meanwhile is refused with `name_taken`, leaves no team row, and the approval
    stays open.
  - A team-row failure after the rename renames back and removes the alias (M: skip the undo → renamed without a team).
  - A close lost to a concurrent deny, cancel or timeout after the rename renames back, writes no history and sends no
    notice (M: treat `won = false` as success).
  - A daemon restart with an open op keeps its reservation (M: no rebuild → a second spawn gets the same name).
  - A registered local spawn and a remote `FinishRemoteSpawn` each record one pair (M: record at acceptance → a failed
    spawn counts).
  - `/team` with no words on a non-lead gives the card; on a lead it shows the roster.
  - A new mod with an older `pdx` or daemon keeps today's `/team`.
  - The lead-names route leaves out a name reserved after the request was made.
  - `ensureColumn` for `spawn_ops.pool_name` runs twice without error.
  - Two personas sharing a display name's first word → `/team <that word>` gives the card (M: take the first match).
  - Cap 9 accepted and 10 refused; the CLI message says 1–9.
- **NP-5**: a root in two groups is refused; a relayed session stays in its group.
- **NP-6**
  - The log mixes decisions and toggles by time and keeps 30 (M: filter by `since` → the list empties after a toggle).
  - The toggle row and the state change commit together (M: write outside the transaction).
  - A failing event insert leaves the state unchanged.
  - Two concurrent toggles still settle by CAS: one wins, the other retries (`unattended.go:120-151`).
- **NA-1..8**
  - `seatOf` on an address, a bare name, an alias and a legacy name.
  - A speech start after a tool row gets the line, and continuous text does not (M: mark every `agent_text`).
  - The team panel tab-switch regression (CLAUDE.md checklist) for the inline settings.
  - The home-row toggle keeps the profile menu on a device with slave profiles.
  - Each display place renders the name and keeps the full address where §9.3 says so.

## 12. Open items for the user

These are the defaults this spec uses. Each can be changed later without reworking the phases.

1. ~~Personas sync to every host automatically~~ → decided 10-19: one source host (default mlab), the App copies it to the others (§4.3).
2. Code and display name are separate; the code is fixed once created (L2).
3. `/team <name>` takes the first word as the name when it matches a persona; without a name, a card appears (L8).
4. ~~A manual task group holds sessions of one host only (L9).~~ Retired (TG-6): see the task groups spec.
5. Avatars are stored as 128 px images in the daemon (L3).
6. The decision log leaves out chat permission and ask prompts (L11).
7. Relay quotas stay in both the team panel and the unattended panel (L12).
8. Old conversations, including the leads', are renamed to persona names; their old addresses keep working (§6.4).
9. The places that switch to avatar + name (§9.3).
10. Whether the Mac should also keep unconfirmed messages across a reload (message stages plan L8; not part of this
    spec).
