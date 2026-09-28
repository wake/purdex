# Worker pane theme & display fixes — spec

- Date: 2026-09-28
- Status: v1.0 (design agreed in chat, 2026-09-28)
- Scope: SPA (worker pane, sidebar tab row, settings) + a small daemon endpoint (non-image upload)
- Depends on (Nexen, delegated to `mlab/nexen-c3`, not blocking phases A–D):
  image attachments on send, and `session_title` on the execution summary
- Prior art: `docs/specs/2026-09-20-worker-pane-views-spec.md` (room / chat, R1–R4)

## 1. Goal

The worker pane (`{kind:'execution'}`) should read like the terminal it sits next to:
normal type size, terminal density, a full-width band for the user's own input, a
visible rail, a per-turn footer, a scroll position that survives switching away,
and a sidebar row that carries the same light semantics as a terminal agent tab.
File upload is added. All visual choices live in a **worker theme**; today's look
is the first theme, named **Purdex**.

## 2. User decisions (do not reopen)

| # | Decision |
|---|---|
| T1 | Theme choice is **one global setting**, under the execution module's settings. Not per pane, not two-level. |
| T2 | A theme controls **appearance only**. It never changes the room / chat structure, subscriptions or behaviour. |
| U1 | Upload: **images go native** (Nexen attachments, delegated); **everything else goes by path reference** (saved on the daemon host, path inserted in the text, the agent reads it). |
| F1 | Turn footer text is **fixed English**, not rotating verbs: `✻ Worked for 38m 2s · done 5:48 PM`. |
| F2 | Time follows the **system 12/24-hour setting**. No date is added across days; the full date/time is in the hover title. |
| F3 | A **failed** turn shows a footer (`✻ Failed after 12s · 5:48 PM`, error colour). A turn the **user interrupted shows none**. |
| L1 | Sidebar light uses the **exact same definition** as Purdex's own terminal agent light (§8.2), including unread and desktop notifications. |
| L2 | Sidebar icon is a setting with three options: provider logo mono (today's white), provider logo in original colour (tuned for a dark background), or a user-picked icon. |
| N1 | Tab title = `<primary> - <cwd basename>`; primary is, first present: `custom-title` → `agent-name` → `ai-title` (via Nexen `session_title`) → the pre-handoff terminal title → the brief's first line. `last-prompt` is never used. |

Note: R1's Q1 ("no per-turn duration") is superseded by F1–F3.

## 3. Facts this design rests on (measured 2026-09-28)

- Tables do not render because `RoomProse` runs `react-markdown` **without `remark-gfm`**; `remark-gfm` is not installed. `lib/nex/markdown-text.ts` mirrors RoomProse's pipeline for the search index and must change with it.
- The terminal (`hooks/useTerminal.ts`) uses `fontSize: 14`, `Menlo, Monaco, monospace`, xterm's default `lineHeight: 1`. There are no font tokens in the app theme system.
- Scroll: `useTranscriptScroll.follow()` scrolls to the bottom on every growth **unless** the search bar is open or a release is active — a reader who scrolled up is pulled down whenever anything arrives, including while the tab is hidden (tabs are kept mounted with `visibility:hidden` by the alive pool). A remount (alive-pool eviction, room⇄chat) jumps straight to the bottom. There is no scroll memory for the transcript (the editor has one, `useEditorStore.paneStates`).
- `result` carries `duration_ms`; durable events carry `created_at` (unix ms, server clock), but the reducer drops it for everything except tool timing. Turn boundaries are `turnStarts`; `TURN_ENDING_KINDS` ends a turn.
- The sidebar row (`InlineTab` → `useTabDisplay`) reads agent status only for `tmux-session` panes. For `execution`, `getPaneIcon` returns `'Robot'`, which is **missing from `ICON_MAP`** → empty icon slot; the title is always the locale string 執行.
- Terminal status is decided by the daemon from hook events (§8.2 table); the SPA stores it in `useAgentStore` (statuses / unread / subagents / agentTypes keyed by `compositeKey(hostId, sessionCode)`) and adds unread + notifications.
- `claude -p` (CLI 2.1.283) writes **no** `ai-title`; handoff workers (`--resume` of an interactive session) keep the interactive `ai-title` in the same jsonl. Title-ish records: `custom-title.customTitle`, `agent-name.agentName`, `ai-title.aiTitle`, `last-prompt.lastPrompt`.
- Nexen `send` accepts `{lease_id, text}` only (v0.13.2). The terminal upload endpoint (`POST /api/agent/upload`) saves under the upload dir then pastes into tmux — the save half is reusable, the paste half is not.

## 4. Worker theme architecture (phase A)

### 4.1 Shape

```ts
// spa/src/lib/worker-theme/types.ts
interface WorkerTheme {
  id: string                 // 'purdex'
  labelKey: string           // locale key
  vars: Record<WorkerThemeVar, string>   // CSS values; colours reference app theme vars
}
```

- `WorkerThemeVar` is a closed set of CSS custom properties, prefixed `--wt-`. Initial set:
  `font-size`, `line-height`, `block-gap` (space between blocks / paragraphs),
  `heading-weight`, `list-marker-color`, `list-indent`,
  `user-band-bg`, `user-band-fg`, `user-band-prefix-color`,
  `rail-color`, `footer-color`, `footer-error-color`,
  `table-border`, `table-header-bg`, `code-font`.
- Colour values are expressions over the app theme's variables (e.g. `var(--text-muted)`, `color-mix(...)`), so a worker theme works under every app theme (dark / light / nord / dracula / custom) without per-app-theme copies.
- A registry (`registerWorkerTheme` / `getWorkerTheme` / `listWorkerThemes`) with one entry, `purdex`. Unknown / missing id → `purdex`.
- The pane root (`ExecutionView`) sets `data-worker-theme={id}` and the theme's vars as inline style; room and chat components read only `var(--wt-*)` (via Tailwind arbitrary values), never hard-coded sizes for the items this spec touches.

### 4.2 Setting

- A `purdex`-scope settings contribution on the execution module: **Worker → Appearance**, holding `theme` (select; one option today) and `icon` (§8.3).
- Stored in a persisted store `useWorkerSettingsStore` (`theme`, `iconStyle`, `customIcon`). Whether it rides Profile Sync follows how the other module settings in that section behave; no new sync path is added.

## 5. Purdex theme — typography, lists, tables, user band, rail (phase A)

### 5.1 Type size and density (item 1)

- Body 14px — the terminal's `fontSize`. **h1–h6 are 14px**, weight `--wt-heading-weight` (600), with the same block gap as a paragraph; no size ladder.
- Line height matches the terminal row: measure the xterm cell height for Menlo 14 (expected ≈ 17px → ≈ 1.2) and set `--wt-line-height` to it. CJK may read tight at 1.2; the value is tuned once on the real device against a terminal screenshot and recorded in the theme, not guessed in code.
- Paragraph / block gap = one line (`1lh`-equivalent), replacing prose's large margins.
- The markdown body stays proportional (`prose` structure kept, sizes overridden); inline and block code use `--wt-code-font`.

### 5.2 Lists (item 1)

- Markers (`1.`, `-`/`•`) in `--wt-list-marker-color` (muted), not prose's default.
- Hanging indent: the marker starts at the pane's left text edge; wrapped lines align under the item text, not under the marker. Nested lists indent by `--wt-list-indent`.

### 5.3 Tables (item 2)

- Add `remark-gfm` (tables, strikethrough, task lists, autolinks) to RoomProse **and** `markdown-text.ts`; the existing "search text equals rendered textContent" test must cover a table.
- Table style: 1px `--wt-table-border` cell borders, header row bold on `--wt-table-header-bg`, cell padding a fraction of a line; a table wider than the pane scrolls horizontally inside its own wrapper — never the whole transcript.
- Chat mode renders prose through the same component, so it gains tables too.

### 5.4 User input band (item 3)

- Room: the user's line becomes a **full-width band** — background `--wt-user-band-bg` from the transcript's left edge to its right edge (bleeding into the gutter the rail uses), a `›` prefix in `--wt-user-band-prefix-color`, text in `--wt-user-band-fg`. The accent gutter mark is removed.
- The pending (not-yet-accepted) line is the same band at reduced opacity, with its `queued` tag kept.
- Chat bubbles are unchanged (T2: structure is not the theme's business); only their colours may read theme vars.

### 5.5 Rail (item 4)

- `op-rail` and `subagent-rail` use `--wt-rail-color` — noticeably brighter than `border-subtle` (target: the reference screenshot's grey line, readable on dark). Error / denied fills are unchanged.

## 6. Scroll position (phase B, item 5)

- **Follow only from the bottom, always.** `follow()` scrolls only if the reader is at the bottom (the existing `atBottom` flag), whether or not the search bar is open. Consequence: growth while the tab is hidden no longer moves a reader who had scrolled up; a reader at the bottom still follows. Release / hold semantics from R3/R4 remain (they become special cases of the same rule plus their existing position memory).
- **Remember per pane.** A non-persisted, module-level map `paneId → { scrollTop, atBottom }` updated on scroll. On (re)mount the transcript restores it: `atBottom` → jump to bottom (today's behaviour); otherwise restore `scrollTop` (clamped). Room ⇄ chat have different heights, so on a view switch only `atBottom` is honoured and a non-bottom position falls back to keeping the same first visible turn in view (turn index is shared by both views).
- Reload of the app is out of scope (the map is memory only).
- The search bar's own placement on mount (R1-1) keeps priority over restore.

## 7. Turn footer (phase B, item 6)

### 7.1 Data

- The reducer keeps timing next to the boundaries: `turnStarts` (message indexes) is unchanged, and a parallel, index-aligned `turnMeta[i] = { startAt, endAt, outcome, durationMs }` records the start event's `created_at` and, at the main turn's end, the end event's `created_at` and the outcome:
  - `outcome: 'ok'` — a top-level `result` with `is_error` false.
  - `outcome: 'failed'` — `result.is_error`, `execution.error`, `execution.turn_stalled`, `execution.turn_orphaned`, `execution.rejected`, `execution.terminal` with a failure reason.
  - `outcome: 'interrupted'` — `execution.interrupted` (and a `result` that follows an interrupt within the same turn), `execution.terminated` / `execution.archived` (user actions).
  - `execution.terminal` is classified by its payload reason; the plan lists each reason Nexen emits and its outcome, and an unknown reason is `failed` (a failure must never be hidden, F3).
  - Subagent frames never end a turn (existing rule).
- `durationMs` = `result.duration_ms` when present, else `endAt − startAt` when both are non-zero, else absent.
- Replay rebuilds the same data (both fields come from durable events).

### 7.2 Render

- Rendered as the last line of each completed turn (room and chat), in `--wt-footer-color`:
  - ok: `✻ Worked for {duration} · done {time}`
  - failed: `✻ Failed after {duration} · {time}`, in `--wt-footer-error-color`
  - interrupted: nothing (F3); the live turn: nothing.
  - No duration known → `✻ Done · {time}` / `✻ Failed · {time}`. No `at` → no footer.
- `{duration}`: `Ns` under a minute (integer seconds, `<1s` below one), `Mm Ss` under an hour, `Hh Mm` above.
- `{time}`: `Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })` — follows the system's 12/24-hour preference; the element's `title` is the full localised date and time.
- Fixed English regardless of UI locale (F1). The footer is not a search unit.

## 8. Sidebar row (phase C, item 7)

### 8.1 Projection

Worker state is projected into **the same `useAgentStore`** under a key that cannot collide with a tmux session code (e.g. `compositeKey(hostId, 'exec:' + executionId)`), so `tabIndicatorStyle`, `renderInlineTabIcon`, `TabStatusIndicator`, `SubagentDots`, unread and `useNotificationDispatcher` are reused unchanged. `agentTypes[key]` is set from the provider (`claude` → `cc`, `codex` → `codex`).

Source of truth, in order: the pane's `useExecutionStore` state when subscribed (live), else the host's execution list row (`state`, `activity`, `running_tasks`). Hosts with a worker tab keep a list subscription so a row can be derived for an evicted pane.

### 8.2 Status mapping (L1 — same definition as terminal)

| Terminal (daemon, hook events) | Worker equivalent |
|---|---|
| `running` ← UserPromptSubmit; PostToolUse lifts waiting | from `message_accepted` / `delegated` (incl. queued, starting) until the turn ends |
| `waiting` ← permission_prompt / elicitation_dialog / PermissionRequest | **not produced yet** — no permission channel (P4) and no `ask_human`; wired when those exist |
| `idle` ← Stop; user interrupt | turn ended `ok`, or `interrupted` |
| `error` ← StopFailure only (tool failures do not count) | turn ended `failed` (§7.1); tool errors do not count |
| error guard: only a new prompt / Stop / session start-end replaces error | only the next accepted send (a new turn) replaces error |
| clear ← SessionEnd, dead process | execution `terminated` (or archived); `rejected` before any turn → `error` |
| unread: set on waiting/error/idle when not the active tab; cleared by running, tab activation, notification click | identical (reused store logic); tab activation must map an active worker tab to its key |
| subagents: native (blue) on SubagentStart/Stop; proxy/delegating (orange) | running `subagent` tasks → native refs; delegating / proxy not detectable → not shown |

Notifications (L1): the dispatcher is reused as is; it needs a lookup from the worker key to its tab (focus on click, "has a tab" check) alongside the existing session-code lookup.

### 8.3 Icon (L2)

Setting `iconStyle`:
1. `mono` — provider logo, current white rendering; Claude honours the existing `ccIconVariant` (bot / star), codex `codexIconVariant`.
2. `color` — provider logo in original colour: `claudecode-color` / `claude-color` / `codex-color` from `@lobehub/icons-static-svg`. A logo that disappears on a dark surface (OpenAI's black mark has no colour variant) falls back to the lobe `codex-color`. "Tuned for dark" means: the colour files are used as is on dark app themes; if one is illegible on a given surface, the theme swaps in its light variant — verified visually per logo.
3. `custom` — a Phosphor icon chosen with the existing workspace icon picker.

The light is drawn on this icon exactly as on a terminal agent icon. Separately, `Robot` is added to `ICON_MAP` (or `getPaneIcon('execution')` changed) so no path yields an empty slot.

### 8.4 Title (N1)

`displayTitle = primary + ' - ' + basename(cwd)`; primary is the first present of:
1. `summary.session_title.text` (Nexen; carries `custom` → `agent_name` → `ai` precedence) — only when the capability says the field exists;
2. the pre-handoff terminal title, recorded on the pane content (`from`-side) at Hand-to-nex time;
3. `firstLine(brief)`.

Titles are sanitised for display (single line, truncated by CSS). No title → the locale's 執行 as today.

## 9. Upload (phase D non-image, phase E image)

### 9.1 Non-image — path reference (phase D)

- Daemon: `POST /api/nex/executions/{id}/uploads` (multipart `file`), saves with the existing dedup helper; returns `{ path, name, size }`. Same auth as other daemon APIs; filename is `filepath.Base`d; 50 MiB cap. No tmux involvement.
- **Location (measured 2026-09-28):** under the `standard` (acceptEdits) and `readonly` (dontAsk) profiles `claude -p` denies Read outside its cwd, and mlab's default profile is `standard`. Files are therefore saved **inside the execution's cwd** at `.purdex-uploads/<executionId>/`, with `.purdex-uploads/.gitignore` = `*` so the directory never shows in git.
- SPA: `WorkerInput` accepts drag-drop, paste (files) and a `+` button. Each upload shows a removable chip while uploading / done / failed. On send the chips become text: the message is the typed text followed by one line per file, `[file: <absolute path>]`. A failed or still-uploading chip blocks send with a visible reason. Removing a chip deletes nothing on disk.
- Images before phase E go through this same path (the agent can Read them); the chip shows a thumbnail.

### 9.2 Image — native (phase E, after Nexen ships)

- Feature-detect Nexen's attachment capability; when present, image files (png / jpeg / gif / webp within the advertised limits) are sent as attachments instead of path lines. Over-limit or unsupported provider → falls back to path reference with a notice, never silently dropped.
- Replayed user lines show attachment thumbnails fetched from Nexen's attachment endpoint.
- Exact wire follows Nexen's shipped contract; this section is revised when it lands.

## 10. Phases (one PR each)

| Phase | Content | Items |
|---|---|---|
| A | Worker theme registry + setting + Purdex vars; typography, lists, `remark-gfm` tables, user band, rail | 0, 1, 2, 3, 4 |
| B | Scroll follow-from-bottom + per-pane memory; reducer timing + turn footer | 5, 6 |
| C | Sidebar projection into `useAgentStore`, icon setting, title (with fallback until Nexen `session_title`), `ICON_MAP` fix, notification lookup | 7 |
| D | Daemon upload endpoint + input chips (all files by path) | 8 (non-image) |
| E | Pin Nexen with attachments + `session_title`: native images, title source 1 | 8 (image), 7 (title) |

A–D do not depend on Nexen. E waits for nexen-c3's two PRs. Each phase stays within ≤ 800 lines / ≤ 20 files, else it splits.

## 11. Testing

- Theme: registry fallback; pane root carries the vars; components use `--wt-*` (snapshot of computed class names on the touched elements).
- Markdown: a GFM table renders `<table>`; search text of a table equals rendered `textContent` (extends the existing markdown-text parity test).
- Scroll: reader scrolled up + growth (bar closed) → position unchanged; at bottom + growth → follows; unmount/remount restores `scrollTop`; remount at bottom → bottom; view switch honours `atBottom` only.
- Reducer: `turnMeta` for ok / failed / interrupted / subagent result (none); replay yields the same; duration fallback.
- Footer: formatting of durations; 12h and 24h locales via an injected formatter; failed colour; none for interrupted and live turns.
- Sidebar: status mapping table as a pure function test (every row of §8.2); error guard; unread via the existing store tests with an exec key; icon style resolution; title precedence including capability-absent fallback.
- Upload: daemon handler (dedup, traversal, size, unknown execution); input chip states; send text composition; blocked send while uploading.
- Mutation check per phase (feedback: tests that verify nothing): revert the core change and confirm the new tests fail.

## 12. Out of scope

- Other worker themes (the registry makes them cheap later).
- Reload-persistent scroll position.
- `waiting` status and the orange delegating dot (need P4 / `ask_human` / detection).
- Rotating verbs, localised footer text.
