# Launcher & New Tab UI fixes — design

Date: 2026-09-17
Status: draft
Follow-up to: `2026-09-16-host-projects-commands-launcher-design.md` (Host Launcher A/B1/B2/B3)

Five defects reported after the Host Launcher shipped (alpha.357–360). Four are
SPA-only; one is a daemon bug that makes every launched session start in the
wrong directory.

---

## 1. Launched sessions ignore the project directory

### Observed

Launching `cld-yolo` from a project card whose path is `~/Workspace/wake/malb`
produces a pane whose prompt reads `wake [/Users/wake] ❯ cld-yolo` — the command
runs in `$HOME`, not the project.

### Root cause

`POST /api/sessions` passes the request's `cwd` straight to
`tmux new-session -d -s <name> -c <cwd>` (`internal/tmux/executor.go:239`).
tmux does **not** expand `~` and does **not** fail on an unusable `-c`: it
silently starts the session in the user's home directory.

Measured on this machine (tmux 3.6a):

```
$ tmux new-session -d -s t -c '~/Workspace/wake/purdex' ; echo $?
0
$ tmux list-sessions -F '#{session_name} #{pane_current_path}'
t /Users/wake              # not the requested path

$ tmux new-session -d -s t2 -c /nonexistent-dir-xyz ; echo $?
0                          # created anyway, again in $HOME
```

Host project paths are stored exactly as the user typed them, and `~/…` is an
explicitly supported form — `hostconfig/validate.go:124` accepts `/…`, `~`, and
`~/…`, and `hostconfig/checkpath.go` expands `~` with the daemon user's home
before stating it. The session-create path has no such expansion, so every
tilde project silently lands in `$HOME`.

This is not launcher-specific: tab rebuild, snapshot restore and every other
`createSession` caller share the defect.

### Decision

Fix it once, in the daemon, where the home directory is actually known.

`handleCreate` resolves `req.Cwd` before it reaches tmux:

1. Trim. Empty stays the existing `/` default.
2. A leading `~` or `~/` expands with `os.UserHomeDir()`. If home cannot be
   resolved, answer `400` — expanding to the wrong place is worse than saying
   so. (Same rule as `checkpath.go`, which is the one place in the daemon that
   already gets this right.)
3. `filepath.Clean` the result; a still-relative path is `400`.
4. `os.Stat` it. Missing, or not a directory → `400`. **This is a deliberate
   behaviour change**: today tmux would swallow it and put the session in
   `$HOME`. A session silently rooted in the wrong directory is the bug being
   fixed, and a rebuild that fails loudly on a deleted project directory is
   more useful than one that appears to succeed. Callers that pass a stale cwd
   (tab rebuild, snapshot restore) will now surface an error instead.
5. The resolved absolute path is what goes to tmux **and** what is recorded in
   `SessionMeta.Cwd` / the `SessionInfo` response, so the SPA's cwd readings
   agree with the pane's actual directory.

The stored project path is untouched — `~/…` stays `~/…` in host config, and
resolution stays a per-host, daemon-side concern.

### Out of scope

Expanding `~user` (only `~` and `~/` are supported, matching `checkpath.go`),
and any `$VAR` expansion.

### Deployment note

This is a daemon change: mlab needs `make build` + restart, and air-2026 needs
its App-installed daemon updated, before the fix is visible there.

---

## 2. New Tab scrolls to the bottom of the session list on open

### Observed

Opening a new tab leaves the session column scrolled to its bottom. Expected:
untouched, i.e. at the top.

### Root cause

`BrowserNewTabSection` focuses its URL input on mount:

```ts
useEffect(() => { inputRef.current?.focus() }, [])
```

`focus()` scrolls every scrollable ancestor until the target is visible. The
New Tab columns are `overflow-y-auto` (`NewTabPage.tsx`), so when the Browser
block sits below the session block in the same column, mounting the page drags
the column down past the session list.

The block only renders in the Electron app (`disabled: !caps.canBrowserPane`),
which is why the web SPA does not show the behaviour.

### Decision

Remove the mount-time focus entirely (user's call). The New Tab page is a
multi-section start screen; no single section owns the caret. Nothing else in
the component depends on the effect — the input is still focusable by click and
by keyboard, and the URL-history dropdown's own logic is untouched.

---

## 3. Host page expands the last host instead of the first

### Observed

Opening the Hosts page expands and selects a host other than the first one in
the list.

### Root cause

`HostSidebar` expands exactly `selectedHostId`. `HostPage.pickHostIdFallback`
resolves that, for a bare `/hosts` route, as:

1. `lastSelection.hostId` — module-scoped, survives closing and reopening the
   tab for the lifetime of the app
2. `activeHostId` — persisted across restarts, written by
   `handleNavigateToHost` and the notification dispatcher
3. `hostOrder[0]`

So the page reopens on whichever host was last visited or last navigated to,
never on the first.

### Decision

For a route that does not name a host, the Hosts page always selects
`hostOrder[0]`. No memory of the previously selected host, from either
`lastSelection` or `activeHostId`.

- `pickHostIdFallback` collapses to `hostOrder[0] ?? null`; `activeHostId` and
  `lastSel` stop being inputs to the host choice. The exported signature is kept
  so the existing equivalence tests keep compiling, with the arguments now
  documented as ignored — or the parameters are dropped and every call site
  updated; the plan picks one.
- The bare-`/hosts` branch of `resolveSelection` no longer restores
  `lastSel.hostId`.
- **Sub-page memory stays.** `lastSelection.subPage` still carries the user's
  working sub-page across host switches and tab reopens; only the *host* part
  stops being remembered. A remembered sub-page that is not selectable for
  `hostOrder[0]` falls back through `pickSelectableSubPage` as today.
- An explicit `/hosts/<id>/<sub>` route still wins over the fallback — clicking
  a host in the sidebar keeps working, and a deep link still resolves to the
  host it names.

---

## 4. New Tab session header: right-align the "+" and give it a fill

### Observed

The per-host `+` sits immediately after the host name, mid-row
(`SessionSection.tsx`, class `ml-1`), and its accent tint is too faint to read
as a button.

### Decision

- `+` moves to the right edge of the header row (`ml-auto`).
- It gets a filled accent background instead of the 15%-alpha tint, matching
  the selected-row treatment in `HostSidebar` (`bg-accent text-white`), with a
  hover state and the existing disabled styling preserved.
- The "reconnecting" text, which currently claims `ml-auto`, moves to sit
  directly after the host name so the `+` keeps the right edge on its own.
- Only the New Tab block (`components/SessionSection.tsx`) changes. The Host
  page's own Sessions section is a different surface and is left alone.

---

## 5. Launcher cards: one row per project, details on hover

### Observed

Each project card stacks three rows — name, path, command icons — so a single
card is tall and a two-column grid wastes a lot of vertical space.

### Target

```
[{project-name} {cmd1}{cmd2}{cmd3}]  [{project-name} {cmd1}{cmd2}{cmd3}]
```

One row per project: name on the left, command icons on the right, two cards
per grid row at the narrowest width and more when the column is wider (the
existing `grid-cols-2 @md:grid-cols-3 @3xl:grid-cols-4` container query already
does this and is unchanged).

Path, slug and command names are no longer printed; they appear on hover.

### Decision

- The card becomes a single flex row: the project button (folder icon +
  truncated name, `min-w-0 flex-1`) and a non-shrinking icon strip.
- The always-visible path line is removed.
- Hover/focus detail uses the existing `HoverTooltip` component (800 ms delay,
  anchored to its parent element, rendered in a portal) rather than native
  `title`:
  - project button → `{name} · {slug} · {path}`
  - command button → the command name
- `title` attributes are dropped where a `HoverTooltip` replaces them, so the
  two do not both appear. `aria-label` on the command buttons stays — it is the
  only accessible name an icon-only button has.
- Keyboard model, launch semantics, `data-launch-item` ordering and every
  `data-testid` are unchanged: this is a layout change, not a behaviour change.
  Tests that assert on the removed path line are updated to assert the tooltip
  instead.

---

## Phasing

One PR. The five fixes touch four files plus tests and share no logic; splitting
them would cost more review than it saves.

| # | Area | Files |
|---|---|---|
| 1 | daemon | `internal/module/session/handler.go` (+ new resolve helper & tests) |
| 2 | spa | `spa/src/components/BrowserNewTabSection.tsx` |
| 3 | spa | `spa/src/components/HostPage.tsx` |
| 4 | spa | `spa/src/components/SessionSection.tsx` |
| 5 | spa | `spa/src/components/session-launcher/SessionLauncher.tsx` |

## Verification

- Go: `go test ./internal/module/session/...`
- SPA: `cd spa && npx vitest run`, `pnpm run lint`, `pnpm run build`
- Manual, after the daemon is rebuilt and restarted on mlab:
  1. Launch a command from a project whose path starts with `~/` → the pane's
     prompt shows the project directory.
  2. Launch from a project whose directory has been deleted → the launcher
     shows an error instead of opening a pane in `$HOME`.
  3. Open a new tab → the session column is at the top and nothing is focused.
  4. Open the Hosts page after visiting a non-first host → the first host is
     selected and expanded.
  5. New Tab session header → `+` sits at the right edge with a filled accent
     background.
  6. Launcher → two cards per row at the narrowest column; hovering a project
     shows name · slug · path, hovering an icon shows the command name.
