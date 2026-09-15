# Host Projects / Commands / Snapshots + Session Launcher — Design

Date: 2026-09-16
Status: draft (pending codex review)
Branch: `worktree-host-launcher`

## 1. Goal

Move "things you launch on a host" under the host, stored on that host's daemon:

- **Projects** — `name` / `slug` / `path` per host, with path existence check.
- **Commands** — per host, two kinds:
  - **Normal** commands: `name` + `command` + `icon` (agent icon or any Phosphor icon).
  - **Resume** templates: per agent type `{exact, fallback}`, replacing the global SPA `useResumeTemplateStore`.
- **Snapshots** — the existing global Snapshot page, moved under the host and filtered to that host.

And replace the "new session" form (New Tab block `+`, Host › Sessions `New Session`) with a
**launcher**: session-name input + a grid of every project, each followed by the icon set of all
normal commands. Clicking an icon = new tmux session with `cwd = project.path`, then run the command.

Non-goals: stream mode entry (will return later as "Nexen mode"); workspace/host placement slots
(removed entirely, not migrated); cross-host sharing of projects/commands.

Alpha: no data migration of old `purdex-quick-commands` / `purdex-resume-templates` localStorage.

## 2. Phases (one PR each)

| Phase | Scope | Deploy |
|---|---|---|
| B1 | daemon `hostconfig` module + API + tests | daemon rebuild + restart |
| B2 | SPA: `useHostConfigStore` cache, Host › Projects / Commands / Snapshots pages, resume lookup rewired to host config, removal of quick-command system + global Commands / Snapshot pages | SPA (HMR) |
| B3 | SPA: launcher component, used by New Tab host block `+` and Host › Sessions `New Session` | SPA (HMR) |

B2 depends on B1 being deployed on every host the user tests with; the SPA must degrade when the
endpoint is 404 (old daemon): show "daemon too old" state and fall back to default resume templates.

## 3. B1 — daemon `hostconfig` module

### 3.1 Package / wiring
- `internal/module/hostconfig/{module,store,handler,validate}.go` (+ `_test.go` each), same shape as
  `internal/module/devicestate`. Registered in `cmd/pdx/main.go` next to `devicestatemod`.
- DB: `DataDir/host_config.db`, WAL, single table:

```sql
CREATE TABLE IF NOT EXISTS host_config (
  key        TEXT PRIMARY KEY,          -- 'projects' | 'commands' | 'resume_templates'
  value      TEXT    NOT NULL,          -- JSON
  revision   INTEGER NOT NULL,          -- starts at 1 on first write
  updated_at INTEGER NOT NULL           -- ms
);
```

A missing row means "empty list / no overrides" at revision 0.

### 3.2 Data shapes (JSON, camelCase)

```ts
Project  { id: string; name: string; slug: string; path: string }
Command  { id: string; name: string; command: string; icon: CommandIcon }
CommandIcon = { kind: 'agent'; value: 'cc-bot' | 'cc-star' | 'openai' | 'codex' | 'opencode' }
            | { kind: 'phosphor'; value: string }   // Phosphor component name, e.g. "Terminal"
ResumeTemplates = Record<agentType, { exact: string; fallback: string }>  // sparse overrides
```

List order in the array is the display order.

### 3.3 Validation (400 with plain-text reason on failure)
- `id`: `^[A-Za-z0-9_-]{1,64}$`, unique within its list.
- Project `name`: trimmed, 1–64 runes. `slug`: `^[a-z0-9][a-z0-9-]{0,31}$`, unique within host.
  `path`: trimmed, 1–1024 bytes, must start with `/` or be `~` or start with `~/`; no NUL.
- Command `name`: trimmed 1–64 runes. `command`: 1–4096 bytes, no NUL.
  `icon.kind` ∈ {agent, phosphor}; agent value in the fixed set; phosphor value `^[A-Z][A-Za-z0-9]{0,63}$`
  (daemon does not know the Phosphor catalog; SPA renders a fallback icon for unknown names).
- Max 200 items per list. Resume: agentType `^[a-z0-9][a-z0-9_-]{0,31}$`, max 32 entries,
  `exact`/`fallback` 0–4096 bytes, no NUL.
- Request body cap 1 MB (read cap+1 → 413).

### 3.4 Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/hostconfig` | — | `{ projects: {items, revision}, commands: {items, revision}, resumeTemplates: {items, revision} }` |
| PUT | `/api/hostconfig/projects` | `{ items: Project[], baseRevision: number }` | `{ items, revision }` |
| PUT | `/api/hostconfig/commands` | `{ items: Command[], baseRevision }` | `{ items, revision }` |
| PUT | `/api/hostconfig/resume-templates` | `{ items: ResumeTemplates, baseRevision }` | `{ items, revision }` |
| POST | `/api/hostconfig/check-path` | `{ path }` | `{ status: 'dir' \| 'not_dir' \| 'missing' \| 'error', resolved: string, reason?: string }` |

- PUT is whole-collection replace with optimistic concurrency: if `baseRevision` ≠ stored revision →
  **409** with the current `{items, revision}` as JSON body. Compare-and-set runs in one transaction.
- `check-path`: expand `~` / `~/` using the daemon process user's home (`os.UserHomeDir`), then
  `filepath.Clean`, `os.Stat`. Not absolute after expansion → 400. `ENOENT` → `missing`;
  other stat errors (e.g. permission) → `error` + reason. Stat is followed-symlink (`os.Stat`).
- Auth: same middleware as all `/api/*` routes (nothing module-specific).

### 3.5 Tests
Store: empty get, first write revision 1, CAS conflict, round-trip JSON. Validation table tests.
Handler: 200/400/409/413 paths, check-path dir/not_dir/missing/`~` expansion/relative→400.

## 4. B2 — SPA host config + settings pages + removals

### 4.1 `useHostConfigStore` (new, `spa/src/stores/useHostConfigStore.ts`)
- **Not persisted, not synced** (daemon is SOT). State per host:
  `byHost[hostId] = { status: 'idle'|'loading'|'ready'|'unsupported'|'error', projects, commands, resumeTemplates, revisions, error? }`.
- `load(hostId)`: GET; 404 → `unsupported`. Called when a host transitions to connected (hook into the
  existing host connection state subscription) and when a host settings page mounts.
- `saveProjects / saveCommands / saveResumeTemplates(hostId, items)`: PUT with current revision;
  on 409 replace local with server copy and throw a typed `HostConfigConflictError` so the UI shows
  "changed elsewhere, reloaded". On success store the returned items/revision.
- `ensureLoaded(hostId): Promise<void>` — for non-React callers (rebuild engine), loads if not ready,
  never throws (errors leave status `error`).
- API wrappers live in `spa/src/lib/host-config-api.ts` (uses `hostFetch`).

### 4.2 Resume template lookup rewire
- Keep `DEFAULT_RESUME_TEMPLATES`, `ResumeTemplatePair`, lookup semantics (sparse override, own-property
  lookup) — move them to `spa/src/lib/resume-templates.ts`.
- `ResumeTemplateLookup` stays `(agentType) => pair | undefined`, but lookups are now **built per host**:
  `resumeLookupFor(hostId)` (live, reads store) and `useResumeTemplateLookup(hostId)` (subscribed).
  If host config isn't `ready`, lookup answers from defaults.
- Rebuild engine / batch: they already pin `hostId` per operation; resolve with
  `await useHostConfigStore.getState().ensureLoaded(hostId)` at operation start, then
  `resumeLookupFor(hostId)`. `batch.ts` default param changes accordingly (lookup factory per host).
- `RenamePopover`, `RebuildActionSet`, Snapshot page pass the relevant `hostId`.
- Delete `useResumeTemplateStore.ts`, its sync registration and `STORAGE_KEYS.RESUME_TEMPLATES`.

### 4.3 Host sub-pages
Added to `setHostBuiltinSections` after `nex` (order 7/8/9): `projects`, `commands`, `snapshots`.
All receive `{ hostId }`. Host offline / `unsupported` → inline notice, editing disabled.

**Projects** (`components/hosts/ProjectsSection.tsx`)
- Table-ish list: name, slug, path, path status icon, reorder (up/down buttons), edit, delete; "Add project".
- Edit dialog: name, slug (auto-suggested from name: lowercase, non `[a-z0-9]` → `-`, collapse, trim, ≤32;
  user-editable; client validation mirrors §3.3), path.
- Path status: debounced 400 ms `check-path` while editing, and on list render for each row
  (✅ dir / ⚠️ not a directory / ❌ missing / ❓ error or unverifiable). Never blocks saving.

**Commands** (`components/hosts/CommandsSection.tsx`) — two tabs:
- *Normal*: list with icon preview, name, command (mono), reorder, edit, delete; "Add command".
  Edit dialog: name, command, icon picker, command-word check reusing `resolveShellCommand(hostId, firstWord)`
  (same verdict UI as today's resume check; never blocks).
- *Resume*: the existing `ResumeTemplateSettings` UI, re-targeted to this host (no host selector;
  check runs against this host), saving through `saveResumeTemplates`.

**Icon picker** (`components/hosts/CommandIconPicker.tsx`)
- Top row: 5 agent icons (`cc-bot`, `cc-star`, `openai`, `codex`, `opencode`) rendered via
  `lib/agent-icons.tsx` explicitly by variant (independent of the global cc/codex variant setting).
- Below: search input + virtualized/paginated grid of all Phosphor icons. Catalog (names + tags) from
  `@phosphor-icons/core` metadata; components resolved through a lazily imported chunk
  (`import('@phosphor-icons/react')`) so the full set is not in the main bundle.
- `CommandIconView` renders a stored `CommandIcon`; unknown phosphor name or chunk still loading →
  `Terminal` icon fallback.

**Snapshots** (`components/hosts/SnapshotsSection.tsx`)
- Reuses the body of `SnapshotSettingsSection` with a `hostId` filter prop: rebuild-records table,
  tmux sessions table and unattached disclosures show only rows whose `hostId` matches; the tabs block
  shows tabs containing at least one pane on this host. The host column is dropped.
  `ResumeTemplateSettings` is no longer rendered here (moved to Commands › Resume).
- Global `snapshot` settings section is unregistered and its file becomes the host-filtered component
  (rename; no duplicate).

### 4.4 Removals
Delete (with their tests): `useQuickCommandStore`, `lib/quick-command-bindings.ts`,
`lib/quick-command-slots.ts`, `lib/slot-executor.ts`, `components/CommandSlot.tsx`,
`components/QuickCommandMenu.tsx`, `hooks/useCommands.ts`, `components/settings/QuickCommandsSettingsSection.tsx`,
`lib/sync/contributors/quick-commands.ts`, `STORAGE_KEYS.QUICK_COMMANDS`, workspace quick-action popover /
quick-commands context menu and their entry points in `WorkspaceContextMenu` / `WorkspaceRow`,
the command slot in Host › Sessions header, the `QuickCommandMenu` usage in `PaneLayoutRenderer`,
and the global "Commands" settings section registration. Module-contributed commands consumed via
`useCommands` — if any module still contributes commands, that contribution API is removed too
(verify during planning; if something real depends on it, stop and surface).
Stale i18n keys removed from all locales.

## 5. B3 — Launcher

### 5.1 Component
`components/session-launcher/SessionLauncher.tsx`, props
`{ hostId, onLaunched(session), onCancel }`. Used by:
- New Tab host block `+` (replaces inline form; `onLaunched` opens the session in the tab as today).
- Host › Sessions `New Session` dialog (replaces `NewSessionDialog`; `onLaunched` closes dialog, same as today).

Layout:
1. Session name input (autofocus). Placeholder shows the name that would be used.
2. Grid of project cards: container-query based — `@container` on the launcher, `grid-cols-2` by default,
   `@md:grid-cols-3`, `@3xl:grid-cols-4` (Tailwind 4 container variants) → never fewer than 2 columns,
   more as width allows. Each card: project name (bold) + path (muted, truncated,
   title tooltip) + row of command icon buttons (tooltip = command name).
3. Empty state when host has no projects: hint + link to Host › Projects. `unsupported` → hint.

### 5.2 Behaviour
- **Enter in name input with nothing selected** → create session `name || <default name>`, `cwd '~'`,
  mode `terminal` (identical to current default submit). Empty name keeps current behaviour
  (current forms require a name — keep: Enter with empty name and no selection does nothing, shows validation).
- **Click a command icon** → launch `{project, command}`. **Click project name** → launch `{project}` (cwd only).
- Keyboard: `ArrowDown` from input moves focus into the grid; arrows move between items
  (project name and each icon are items, row-major); `Enter`/`Space` launches the focused item;
  `Escape` → `onCancel`. Selecting == launching (no separate "selected" state).
- **Session name for a project launch**: typed name if non-empty; else `{slug}-{N}` where
  N = 1 + count of live sessions on this host whose name is exactly `slug` or matches `^slug-\d+$`;
  if that name exists, increment N until free. On 409 from create (race), increment and retry up to 5 times.
- **Launch sequence** (shared helper `lib/session-launch.ts`):
  1. `createSession(hostId, name, project.path, 'terminal')`.
  2. If command: `sendKeys(hostId, session.code, command.command, session.tmuxInstance)` — same call and
     tmux-instance guard the rebuild engine uses.
  3. `onLaunched(session)`.
  Failure in 1 → inline error, stay open. Failure in 2 → session is kept, launcher reports
  "session created but command failed to send", and still calls `onLaunched` so the user lands in it.
- Busy state disables inputs to prevent double launch.

### 5.3 Tests
Name generation (pure function) table tests; launch helper with mocked API (success, 409 retry,
send failure); component: Enter default path, click icon, click project name, keyboard navigation,
empty/unsupported states, ≥2-column class present.

## 6. Risks / open points
- Multi-device staleness: host config is fetched on connect and on page mount; edits from another
  device aren't pushed live. The 409 CAS prevents lost updates. Acceptable for now.
- Phosphor full catalog chunk size — lazy chunk only loads when the picker opens or a stored phosphor
  icon is rendered; acceptable.
- Removing quick-command system touches workspace menus; planning must enumerate exact entry points.
