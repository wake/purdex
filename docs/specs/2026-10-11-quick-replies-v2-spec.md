# Quick replies v2 — title + content, per-item and whole-row switches — spec + plan (rev 1)

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
- Item shape: `{id, title, content}`. Rules: `id` as today; `title` trimmed, non-empty, ≤ 100 bytes, valid UTF-8, no NUL;
  `content` trimmed, non-empty, ≤ 1000 bytes (today's `quickReplyMaxBytes`), valid UTF-8, no NUL; ≤ 20 items (today's cap).
- **Stored rows from before v2** are `{id, text}` (mlab already has some). The GET's lenient reader upgrades such a row to
  `{id, title: text, content: text}` (title cut to its byte limit on a rune boundary); nothing is rewritten until the next PUT.
- **Old clients** (an iOS build or SPA from before v2) keep working:
  - the GET answers each item with `text` = `content` as well (an old client reads `text`);
  - a PUT item that carries `text` and no `content` is a legacy item: `content = text`; its `title` is the stored title of the
    item with the same `id` when one exists, else `text` (title-limited). An item with both `content` and `text` → `content` wins.
- Revisions, the If-Match / conflict contract and the error answers are unchanged.
- The defaults shown for a host that never wrote a list move to the clients (unchanged rule), now with titles.

## 3. Mac App (SPA)
- Types / API: `QuickReply {id, title, content}` (read `text` as a fallback for old daemons: title = content = text). A PUT
  sends each item with `text` = `content` as well, so a daemon from before v2 still accepts it (it keeps only the text).
- **Prefs store** `purdex-quick-reply-prefs` (persisted, synced across windows, **projected by Profile Sync** next to the other
  profile settings): `{ rowEnabled: boolean (default true), disabled: Record<hostId, string[]> }` — off-lists, so a new item is
  on by default; an id that no longer exists in the host's list is ignored (and dropped on the next edit of that host's list).
- **The row** (`components/deck/QuickReplies.tsx`): hidden when `rowEnabled` is false; shows only items not in
  `disabled[hostId]`; the button shows the title (full content in its tooltip / accessible description); a tap sends the content
  (destructive check on the content, unchanged).
- **Settings**: the per-host quick-reply editor gains a title field and a content field per item and an on/off switch per item
  (writes the prefs store, not the daemon); a "show the quick-reply row" switch at the top of the editor (app-wide, prefs store).
  Defaults for a never-written host: 「繼續」/`continue`, 「跑測試」/`run the tests`, 「解釋」/`explain that`.

## 4. iOS App
- Read `{id, title, content}` (fallback `text`); the dock shows titles, sends content; the editor edits title + content.
- Per-item and row switches stored in the App (UserDefaults or the App's settings store), keyed by host id + item id.

## 5. Phases
- **QR-1 daemon**: §2 + tests (new shape round trip; legacy row read upgrade; legacy PUT keeps an existing title by id, else
  title = text; both fields → content wins; GET carries `text`; limits per field; 20-item cap; If-Match unchanged). go test
  -race on hostconfig.
- **QR-2 SPA**: §3 + tests (prefs store + Profile projection; row hidden / item hidden; title shown, content sent; editor fields
  and switches; old-daemon read fallback; every PUT item carries `text`). **Merge after QR-1 is deployed on mlab and air26**
  (an old daemon would accept the PUT but drop the titles).
- **QR-3 iOS**: §4, after QR-1 is deployed. OTA as usual.
