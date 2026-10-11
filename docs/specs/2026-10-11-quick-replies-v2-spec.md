# Quick replies v2 — title + content, per-item and whole-row switches — spec + plan (rev 3)

Follows #2685 (the deck's quick-reply row, terminal and worker, Mac). Amends the quick-reply list format of
`internal/module/hostconfig` (R3) and the iOS dock.

## 1. User decisions (2026-10-11, do not reopen)
- A quick reply sends its text only; attachments in the draft stay where they are (the draft is never touched).
- Each item has a **title** (shown on the button) and a **content** (the text that is sent).
- Each item can be switched off on its own; the whole row can be switched off.
- **The list is per host**, on that host's daemon, like quick commands and project directories: a Mac and an iPhone connected to
  the same host see the same list; mlab and air26 may differ.
- **The switches are per app**, not on the daemon: the Mac App keeps them in its Profile (synced to every Mac by Profile Sync);
  the iOS App keeps them in the App. Mac and iOS switch independently.

## 2. Daemon (hostconfig `quick_replies`)

### 2.1 Wire contract (complete — the iOS App is built from this section alone)
- **Read**: `GET /api/hostconfig` → `{ …, "quickReplies": { "items": [Item…], "revision": n, "invalid"?: true, "dropped"?: {…} }, … }`.
- **Write**: `PUT /api/hostconfig/quick-replies`, body `{ "items": [Item…], "baseRevision": n }` (the revision last read; there is
  no `If-Match` header). Answers: **200** `{ "items", "revision" }` (the stored copy, normalised); **409**
  `{ "items", "revision", … }` (the server's current copy — a stale `baseRevision`, whatever the payload); **400** a plain-text
  reason (validation). Unchanged from today.
- **Item, as the daemon answers it** (GET, PUT 200 **and** PUT 409 — every answer goes through `collectionOf`):
  `{ "id", "title", "content", "text" }`, with `text` = `content` — so a client from before v2, which reads `text` in all three
  answers (the SPA's `take()` parses the PUT 200 and the 409 the same way as a load, `useHostConfigStore.ts:227`), keeps working.
- **Item, as a client sends it**: v2 shape `{ "id", "title", "content" }` (a v2 client adds `"text": content` too, so a daemon from
  before v2 accepts it — it keeps only the text); legacy shape `{ "id", "text" }`.
- Never-written host: `items: []`; the clients show their defaults (§3, §4).

### 2.2 Rules
- `id` as today. `title`: string, trimmed, non-empty, ≤ 100 bytes, no NUL. `content`: string, trimmed, non-empty, ≤ 1000 bytes
  (today's `quickReplyMaxBytes`), no NUL. ≤ 20 items (today's cap).
- **UTF-8**: as today's `text` — `encoding/json` replaces invalid bytes with U+FFFD while decoding, and the value is accepted as
  replaced (`validate.go:216`); the `ValidString` check stays as a guard. No raw-token check is added. Tests pin it.
- **Which shape an item is** is decided by the **presence of the `content` key**, read from a raw wire struct
  (`json.RawMessage` per field, not a typed string): `content` absent → legacy item; `content` present → v2 item, whose `content`
  must be a string meeting the rules — `null`, a non-string or an empty string is a 400, even when `text` is present (and when both
  are present, `text` is ignored). A v2 item needs `title` the same way (absent / `null` / non-string / empty → 400).
- **Legacy item** (`content` absent): needs `text` meeting the content rules; `content = text`; `title` = the title of the stored
  item with the same `id` when one exists, else `text` cut to the title limit on a rune boundary. Any `title` it carries is ignored.
- **"The stored title" is read inside the store's CAS**, from the row the revision check just matched: `Store.Put`'s builder
  becomes `build func(cur Entry) (json.RawMessage, error)` (today `build func()` cannot see `cur`, `store.go:108`); its two callers
  (`handler.go` putHandler, `unattended.go`) adapt, and `putHandler`'s normalizer gains the current value. The stored items are
  read with the GET's lenient reader, so a pre-v2 stored row lends its `text` as the title.
- **Stored rows from before v2** are `{id, text}` (mlab already has some). The lenient reader upgrades such a row to
  `{id, title: text (title-limited), content: text}`; nothing is rewritten until the next PUT.
- Revisions, the CAS / 409 contract and the error answers are unchanged.

### 2.3 Compatibility (the four quadrants)
| | old client | v2 client |
|---|---|---|
| **old daemon** | today | reads `text` when `content` is absent (title = content = text); its PUT carries `text`, so it is accepted and stored as text only — **titles are lost**, hence QR-2a / QR-3 merge only after QR-1 is on mlab and air26 |
| **v2 daemon** | reads `text` in GET / 200 / 409; sends legacy items, which keep the stored titles by id | full |

## 3. Mac App (SPA)
- Types / API: `QuickReply {id, title, content}`; the parser reads `title`/`content`, falling back to `text` (title = content =
  text) when `content` is absent (an old daemon) — in the load, the PUT 200 and the 409 alike. A PUT sends each item as
  `{id, title, content, text: content}`.
- **The row** (`components/deck/QuickReplies.tsx`): the button shows the title (full content in its tooltip / accessible
  description); a tap sends the content (destructive check on the content, unchanged). Hidden / filtered by the prefs below.
- **Settings** (`components/hosts/QuickReplySettings.tsx`): a title field and a content field per item. Defaults for a
  never-written host: 「繼續」/`continue`, 「跑測試」/`run the tests`, 「解釋」/`explain that`.
- **Prefs store** `purdex-quick-reply-prefs` (persisted, synced across windows, **projected by Profile Sync**):
  `{ rowEnabled: boolean (default true), disabled: Record<wireHostId, string[]> }` — off-lists, so a new item is on by default; an
  id that no longer exists in the host's list is ignored.
  - **Keys are WIRE host ids** (`wireIdOfHost`: `d1_…` per daemon, else the local id), exactly like `purdex-host-looks.looks`: a
    local host id is random per Mac, so a local-id key would not name the same host on another Mac. The builder and the applier
    pass the keys through verbatim (no local↔wire mapping); a key naming a host this Mac lacks round-trips untouched. Readers
    resolve a host to its key the way `lookKeyOf` does.
  - **Re-key pass**: the store joins `rekeyWireKeyedStores` (`lib/host-look.ts`) — when a host gains its daemon id, its local-id
    entry moves to the `d1_…` key (an entry already there wins) — **and** the pass's store lists in `lib/host-reresolve.ts`:
    `REREAD` (re-read before rewriting) and `STORES` (the pass waits for all of them to hydrate, and a late hydration of any of
    them requests the pass again).
  - **Profile Sync wiring** (every fixed registry, or the store is silently not synced): `SettingsStorageKey` union
    (`lib/profile/types.ts`), the collector's `SETTINGS_STORES` (`collector.ts`), the applier's `SETTINGS_STORES`
    (`apply-to-stores.ts`), the projection guard test's store map (`projections.test.ts`), `STORAGE_KEYS`, `syncManager.register`;
    projection `settingsPaths('purdex-quick-reply-prefs', ['rowEnabled', 'disabled'])`; `SECTION_SCHEMA_ORDINAL.settings` 9 → 10
    (a projection change: the fingerprint moves by itself, no marker — the ordinal-9 worker-settings precedent); an ordinal-9
    payload lacks the store and leaves it alone on apply. The store's `merge` sanitises (unknown fields dropped, non-string ids
    dropped, duplicate ids collapsed).
  - **Pruning**: only after a **successful** PUT (200) for host H, `disabled[wire(H)]` becomes its intersection with the ids in the
    200's items. A 409, a 400 or a network failure prunes nothing (the prefs survive a failed edit).
  - The row is hidden when `rowEnabled` is false and shows only items not in `disabled[wire(hostId)]`. The editor gains an on/off
    switch per item and a "show the quick-reply row" switch at its top (app-wide); both write the prefs store, not the daemon.

## 4. iOS App
- Wire: §2.1 exactly — GET `quickReplies`; PUT `/api/hostconfig/quick-replies` with body `{items, baseRevision}`; items sent as
  `{id, title, content, text: content}`; read `title`/`content`, falling back to `text` when `content` is absent, in the GET, the
  PUT 200 and the 409. A 409 replaces the local list with the answer's items and revision, and says the list changed elsewhere
  (the App's existing conflict wording for host settings).
- The dock shows titles, sends content; the editor edits title + content. Never-written host → the same three defaults as §3.
- **Prefs, in the App** (not synced): `{ rowEnabled: Bool (app-wide, default true), disabled: [hostKey: [itemId]] }`, `hostKey` =
  the key the App stores that host under. Same off-list semantics; the same pruning rule (only after a 200, with its ids).
- QR-3 merges after QR-1 is on mlab and air26, so "old daemon × v2 iOS" only loses titles (§2.3) and is not otherwise handled.

## 5. Phases (one phase = one PR, ≤ 800 lines or ≤ 20 files)
- **QR-1 daemon**: §2. Tests: v2 round trip; GET, PUT 200 and PUT 409 items carry `text` = `content`; legacy row read upgrade
  (title cut on a rune boundary); legacy PUT keeps a stored title by id (including a pre-v2 stored row), else title = text; the
  title comes from the row the CAS matched (a concurrent PUT through the `afterRead` seam: the loser gets 409, never a title from a
  stale read); `content` absent → legacy; `content` `null` / non-string / empty → 400 even with `text`; both present → content
  wins; `title` absent / `null` / empty in a v2 item → 400; limits per field; raw invalid UTF-8 bytes stored as U+FFFD; 20-item
  cap; `baseRevision` / 409 unchanged; the unattended writer unchanged. go test -race on hostconfig.
- **QR-2a SPA — model and row**: §3 types / API / row / editor fields / defaults (no prefs). Tests: parser fallback to `text` in
  load, 200 and 409; every PUT item carries `text`; the row shows titles and sends content; the editor edits both fields.
  **Merge after QR-1 is deployed on mlab and air26.**
- **QR-2b SPA — switches**: §3 prefs store, Profile Sync wiring, re-key pass, row / item hiding, the two switches, pruning.
  Tests: sanitising merge; wire-id keys; Profile build and apply with ordinal 10, a key for a host this Mac lacks round-trips;
  cross-window rehydrate; the re-key pass moves a local-id entry and keeps an existing wire entry, **also when the prefs store
  hydrates last**; row hidden / item hidden; pruning after a 200 for edit and delete, nothing pruned on 409 / 400 / network
  failure. After QR-2a. If it still exceeds the limit, the switch UI splits off as QR-2c.
- **QR-3 iOS**: §4, after QR-1 is deployed. OTA as usual.
