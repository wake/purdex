# Launcher & New Tab UI fixes — plan

Spec: `docs/superpowers/specs/2026-09-17-launcher-ui-fixes-design.md`
Branch: `worktree-launcher-ui-fixes`
One PR, five independent tasks. TDD: every task writes its failing test first.

Parallel-safety: tasks touch disjoint files, so they may run concurrently —
each commit MUST use `git commit --only <files>` (shared index).

---

## T1 — daemon: resolve `cwd` before handing it to tmux

**Files**
- new `internal/module/session/cwd.go`
- new `internal/module/session/cwd_test.go`
- `internal/module/session/handler.go`
- `internal/module/session/handler_test.go` (or the existing create tests)

**Tests first** (`cwd_test.go`, table-driven, `home func() (string, error)` injected
the same way `hostconfig.checkPath` takes it):

| input | home | expect |
|---|---|---|
| `""` | ok | `/` |
| `"   "` | ok | `/` |
| `"~"` | `/Users/wake` | `/Users/wake` |
| `"~/Workspace/x"` | `/Users/wake` | `/Users/wake/Workspace/x` |
| `"~/Workspace/../Workspace/x"` | `/Users/wake` | cleaned `/Users/wake/Workspace/x` |
| `"~foo/bar"` | ok | error (not a supported form; stays relative → rejected) |
| `"relative/x"` | ok | error |
| `"~/x"` | home fails | error |
| path containing NUL | ok | error |
| existing dir | — | that dir |
| existing **file** | — | error |
| missing path | — | error |

Use `t.TempDir()` for the stat cases. Signature:

```go
func resolveCwd(raw string, home func() (string, error)) (string, error)
```

`/` is the empty default and is stat'd like anything else (it exists, so it
passes) — no special case.

**Then** in `handleCreate`: replace the `if req.Cwd == "" { req.Cwd = "/" }`
block with

```go
cwd, err := resolveCwd(req.Cwd, os.UserHomeDir)
if err != nil {
    http.Error(w, "invalid cwd: "+err.Error(), http.StatusBadRequest)
    return
}
req.Cwd = cwd
```

placed where the current default is — **before** `createMu.Lock()`, with the
other input validation. Everything downstream (`tmux.NewSession`,
`SetMeta`, the `SessionInfo` response) then carries the resolved path with no
further edits.

Handler test: a create with `cwd: "~/sub"` reaches the fake executor with the
expanded absolute path, and a create with a missing directory answers 400 and
creates nothing (`FakeExecutor.HasSession` stays false).

**Verify**: `go test ./internal/module/session/...`
**Commit**: `fix(daemon): expand ~ and validate cwd before creating a tmux session`

---

## T2 — spa: New Tab must not steal focus on mount

**Files**
- `spa/src/components/BrowserNewTabSection.tsx`
- `spa/src/components/__tests__/` — new `BrowserNewTabSection.test.tsx` if none
  exists (check first; the component currently has no test file)

**Test first**: render `<BrowserNewTabSection onSelect={vi.fn()} />` and assert
`document.activeElement` is `document.body` — i.e. the URL input is NOT focused
after mount.

**Then**: delete the `useEffect(() => { inputRef.current?.focus() }, [])`.
`inputRef` is still used by the outside-click handler (`dropdownRef` /
`inputRef` containment check) — keep the ref, drop only the effect. Remove
`useEffect` from the import only if nothing else uses it (it does: two other
effects remain).

**Verify**: `cd spa && npx vitest run src/components/__tests__/BrowserNewTabSection.test.tsx`
**Commit**: `fix(spa): stop the New Tab browser field stealing focus on mount`

---

## T3 — spa: Hosts page always opens on the first host

**Files**
- `spa/src/components/HostPage.tsx`
- `spa/src/components/HostPage.test.tsx`, `HostPage.route-sync.test.tsx`
- whichever suite covers `pickHostIdFallback` (grep `pickHostIdFallback` under
  `spa/src`; the spec calls it "host-selection-utils tests")

**Tests first**
1. `pickHostIdFallback(['a','b','c'], 'c', { hostId: 'b' })` → `'a'`.
   Empty `hostOrder` → `null`.
2. Bare `/hosts` after the user selected host `b`: HostPage renders host `a`
   and redirects to `/hosts/<a>/<sub>`.
3. `/hosts/<b>/overview` still selects `b` (explicit route wins).
4. Sub-page memory survives: select `b`/`commands`, return to `/hosts` → host
   is `a`, sub-page is still `commands` (assuming `commands` is selectable for
   `a`).

**Then**

- `pickHostIdFallback(hostOrder, activeHostId, lastSel)` → `hostOrder[0] ?? null`.
  Keep the three-parameter signature (call sites and tests stay valid); mark the
  last two `_activeHostId` / `_lastSel` and rewrite the doc comment to say the
  Hosts page deliberately does not remember a host. Do **not** delete the
  parameters — `preResolveHostId` and `getFallbackSelection` both pass them and
  the churn buys nothing.
- In `resolveSelection`, the `parsed.kind === 'hosts'` + `if (lastSel)` branch:
  drop the `hostOrder.includes(lastSel.hostId) ? lastSel.hostId : …` choice and
  use `fallbackSelection.hostId`, keeping `lastSel.subPage` as the requested
  sub-page. The non-host-route `if (lastSel)` branch at the bottom does the same.
- Leave `lastSelection` itself, its write-back effect and the `isActive` gates
  alone — sub-page memory still needs them.
- `HostSidebar` needs no change: it expands `selectedHostId`, which is now the
  first host.

**Verify**: `cd spa && npx vitest run src/components/HostPage`
**Commit**: `fix(spa): open the Hosts page on the first host, not the last visited`

---

## T4 — spa: right-align the New Tab "+" and fill it

**Files**
- `spa/src/components/SessionSection.tsx`
- `spa/src/components/SessionSection.test.tsx`

**Test first**: in the header row, assert the reconnecting label precedes the
`new-session-<hostId>` button in DOM order and that the button carries
`ml-auto` and the filled accent class. (Class assertions are brittle but this
is a pure-styling change — assert the two that the request is actually about:
`ml-auto` and `bg-accent`.)

**Then**, in the header `<div>`:
- host-header button (unchanged)
- `{isOffline && <span className="text-xs text-text-muted">{t('session.reconnecting')}</span>}`
  moves up, directly after it, and loses `ml-auto`
- `+` button moves last and becomes:
  `ml-auto p-1 rounded bg-accent text-white hover:bg-accent/80 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed`
  (the accent border and the `/15` tint go away; `title` stays)

Nothing else in the file changes.

**Verify**: `cd spa && npx vitest run src/components/SessionSection.test.tsx`
**Commit**: `feat(spa): right-align and fill the New Tab new-session button`

---

## T5 — spa: one-row launcher cards with hover detail

**Files**
- `spa/src/components/session-launcher/SessionLauncher.tsx`
- `spa/src/components/session-launcher/SessionLauncher.test.tsx`

**Tests first**
1. The project path is no longer rendered as visible card text.
2. The project button renders a `HoverTooltip` whose content is
   `{name} · {slug} · {path}` (assert on the tooltip's text; `HoverTooltip`
   portals into `document.body` and renders its children immediately with
   `opacity-0`, so `screen.getByText` finds it without simulating hover —
   confirm against `SortableTab`'s existing tests before relying on it, and
   fall back to a `data-testid` on the tooltip if not).
3. Each command button renders a tooltip with the command name and no longer
   carries a `title`.
4. Existing launch/keyboard tests still pass untouched.

**Then**, replacing the card body only:

```tsx
<div key={project.id} data-testid={`launcher-project-${project.id}`}
  className="min-w-0 flex items-center gap-1.5 p-1.5 rounded border border-border-subtle bg-surface-primary">
  <button type="button" data-launch-item data-testid={`launcher-project-name-${project.id}`}
    disabled={locked} {...activate({ name: trimmed, project })}
    className={`relative flex items-center gap-1.5 text-left text-sm font-bold text-text-primary min-w-0 flex-1 ${itemCls}`}>
    <FolderSimple size={14} className="shrink-0 text-text-secondary" />
    <span className="truncate">{project.name}</span>
    <HoverTooltip placement="top">{`${project.name} · ${project.slug} · ${project.path}`}</HoverTooltip>
  </button>
  {config.commands.length > 0 && (
    <div className="flex items-center gap-0.5 shrink-0">
      {config.commands.map((command) => (
        <button key={command.id} type="button" data-launch-item
          data-testid={`launcher-command-${project.id}-${command.id}`}
          aria-label={`${project.name} · ${command.name}`} disabled={locked}
          {...activate({ name: trimmed, project, command })}
          className={`relative p-1 text-text-secondary hover:text-text-primary hover:bg-surface-hover ${itemCls}`}>
          <CommandIconView icon={command.icon} size={16} />
          <HoverTooltip placement="top">{command.name}</HoverTooltip>
        </button>
      ))}
    </div>
  )}
</div>
```

Notes the implementer must respect:
- `HoverTooltip` anchors to **its parent element** via a hidden marker span, so
  it must be the button's own child — not a sibling wrapper.
- The grid classes (`grid-cols-2 @md:grid-cols-3 @3xl:grid-cols-4`) stay.
- Both `title` attributes go (the path line's and the command's); `aria-label`
  stays on the command buttons.
- `data-launch-item` order is unchanged, so the arrow-key model is unaffected.
- Long project names truncate; the icon strip never shrinks.

**Verify**: `cd spa && npx vitest run src/components/session-launcher`
**Commit**: `feat(spa): compact launcher cards to one row with hover detail`

---

## Wrap-up (main session, after all five land)

1. `cd spa && npx vitest run && pnpm run lint && pnpm run build`
2. `go build ./... && go test ./internal/...`
3. PR against `main`, body listing the five fixes and the daemon-deploy caveat.
4. Codex review round (`--base origin/main --model gpt-5.5`).
5. Merge → separate bump PR (`VERSION` + `package.json` + `spa/package.json` +
   `CHANGELOG.md`) → rebuild and restart the mlab daemon.

---

## Amendments after codex plan review (`task-mu51bwkl-9d6ll3`)

These override the task bodies above wherever they disagree.

### A1 — T1 tests must not hard-code this machine's paths

The table in T1 lists expectations like `/Users/wake/Workspace/x`. With the
`os.Stat` gate in the same task, those cases only pass if the directory happens
to exist on the test machine. Rewrite them so every case that is expected to
SUCCEED is built from `t.TempDir()`:

- `home := t.TempDir()`, then `os.MkdirAll(filepath.Join(home, "Workspace", "x"))`
- feed `func() (string, error) { return home, nil }` as the `home` argument
- expectations are `filepath.Join(home, …)`, never a literal

The failure cases (`~foo/bar`, `relative/x`, home error, NUL, missing path,
existing file) stay as they are — they never reach a successful stat.

The handler test goes through `os.UserHomeDir`, so it must set the home
directory for the process: `t.Setenv("HOME", tmp)` with `tmp` containing `sub`.
Assert the fake executor received `filepath.Join(tmp, "sub")`.

### A2 — T3 must test a remembered sub-page that is disabled on the first host

The spec promises the remembered sub-page clamps through `pickSelectableSubPage`
when it is not selectable for `hostOrder[0]`. Without a test for it, an
implementer can carry `lastSel.subPage` straight through, pass every planned
test, and still select a disabled sub-page.

Add: `lastSelection = { hostId: 'b', subPage: <X> }` where `<X>`'s contribution
is disabled for host `a`'s ctx; navigate to bare `/hosts`; assert the resolved
selection is host `a` with the first SELECTABLE sub-page, and that the canonical
redirect names that sub-page — not `<X>`.

### A3 — T5 tooltips: anchor off the button, and test visibility not presence

Two changes to the T5 markup and tests.

**Markup.** `HoverTooltip` anchors to its parent element, and a `disabled`
button is an unreliable target for `mouseenter` / `focusin`. The tooltip must
therefore hang off a wrapper that is never disabled, not off the button itself:

```tsx
<span className="relative flex min-w-0 flex-1">
  <button … className={`… min-w-0 flex-1 ${itemCls}`}>…</button>
  <HoverTooltip placement="top" data-testid={`launcher-project-tip-${project.id}`}>
    {`${project.name} · ${project.slug} · ${project.path}`}
  </HoverTooltip>
</span>
```

and the same shape (`<span className="relative inline-flex">`) around each
command button, with
`data-testid={`launcher-command-tip-${project.id}-${command.id}`}`.

The wrapper carries the sizing classes the button used to need from its parent
(`min-w-0 flex-1` for the project, nothing for the icons). `data-launch-item`
stays on the buttons, so `items()` and the arrow-key model are untouched.

**Tests.** `HoverTooltip` always portals its children into `document.body` with
`opacity-0`, so asserting on text presence proves nothing. Use the per-tooltip
`data-testid` plus fake timers:

```ts
vi.useFakeTimers()
fireEvent.mouseEnter(screen.getByTestId(`launcher-project-name-${id}`).parentElement!)
act(() => { vi.advanceTimersByTime(800) })
expect(screen.getByTestId(`launcher-project-tip-${id}`).className).toContain('opacity-100')
```

(`HOVER_TOOLTIP_DELAY_MS` is 800.) Assert the same for one command tooltip, and
assert the content string. Restore real timers in the test's cleanup.
