# Spec — Global scrollbar styling + Tmux Agent Monitor removal

Date: 2026-09-16
Branch: `worktree-scrollbar-and-monitor-removal`
Baseline: `origin/main` @ `27be6b9b` (v1.0.0-alpha.360)

Two independent changes, shipped as two phases in one PR. Phase 1 is a
CSS-only visual fix; Phase 2 is a dead-surface removal. They touch disjoint
files, so either can be reverted without the other.

---

## Phase 1 — Global thin scrollbar

### Problem

Only the activity bar has a styled scrollbar. `spa/src/index.css:34-52`
defines `.activity-bar-workspace-scroll` (8px, accent thumb at 38%, rounded,
transparent track) and it is applied in exactly two places —
`ActivityBarWide.tsx:344` and `ActivityBarNarrow.tsx:182`.

Every other scroll container in the SPA — 41 files, including
`NewTabPage.tsx:145` (the column in the reported screenshot), `SettingsPage`,
`HostPage`, `FileTreeView`, `SessionPanel`, `ConversationMessages`, the whole
`editor/` tree — renders the platform's native scrollbar. `.xterm-viewport`
is also unstyled (no xterm scrollbar theming exists anywhere in the repo).
On macOS against the dark themes that bar reads as a light grey slab.

### Decision

One global base rule, neutral (not accent) thumb.

Accent was rejected for the global default: on a large list surface such as
new-tab it competes with the content. The activity bar keeps its accent
variant as a deliberate exception.

### Implementation

All scrollbar rules live in **`spa/src/styles/scrollbars.css`**, imported from
`index.css` after the theme import. `index.css` is already a 250-line global
junk drawer (Tailwind import, cursor defaults, breathe animation, Tiptap
typography); adding a fourth scrollbar rule to it was the point at which the
policy earned its own file. The two pre-existing overrides move there too, so
all scrollbar behaviour is readable in one place. Rationale stays in this spec,
not in the CSS — the CSS keeps only what cannot be read off the syntax.

```css
:root {
  /* Inherited property — one declaration at the root reaches every scroll
     container, including any shadow tree, since inheritance crosses shadow
     boundaries while a `*` selector does not. */
  --scroll-thumb: color-mix(in srgb, var(--text-secondary) 55%, transparent);
  scrollbar-color: var(--scroll-thumb) transparent;
}

/* `scrollbar-width` is NOT an inherited property (CSS Scrollbars 1:
   `scrollbar-color` Inherited: yes / `scrollbar-width` Inherited: no), and the
   root value is only required to apply to the viewport. Thin bars on the 40+
   inner scroll containers therefore need a selector that actually matches
   them — hence the universal selector, not `:root`. */
* {
  scrollbar-width: thin;
}

/* The terminal viewport is excluded: xterm's fit addon reserves a hardcoded
   14px for the scrollbar, so narrowing the real one costs up to a column. */
.xterm-viewport {
  scrollbar-width: auto;
  scrollbar-color: auto;
}
```

Notes on each decision:

1. **Token source is `--text-secondary`, not `--text-muted`.** Both are in the
   canonical theme token list (`spa/src/lib/theme-tokens.ts:6`), so either
   works for runtime-registered custom themes without touching `themes.css` —
   but `--text-muted` is measurably too faint. Measured WCAG contrast of the
   composited thumb against each builtin theme's four surfaces
   (`surface-primary` / `-secondary` / `-elevated` / `-hover`):

   | Formula | worst case | dark/primary | light/hover | dracula/elevated |
   |---|---|---|---|---|
   | `text-muted` 45% (first draft) | **1.35 : 1** | 1.71 | 1.48 | 1.35 |
   | `text-muted` 65% | 1.55 : 1 | 2.36 | 1.78 | 1.55 |
   | `text-muted` 100% | 1.94 : 1 | 4.05 | 2.57 | 1.94 |
   | **`text-secondary` 55% (chosen)** | **2.46 : 1** | 3.04 | 2.46 | 2.63 |
   | `text-secondary` 65% | 3.00 : 1 | 3.80 | 3.00 | 3.07 |

   `--text-muted` cannot be rescued by raising alpha: on Dracula it is within
   1.94:1 of `surface-elevated` even at full opacity, because that theme's
   muted hue sits right next to its elevated surface.

   55% of `--text-secondary` was chosen over 65% because the reported problem
   is a scrollbar that is *too* prominent. 2.46:1 worst case is in the same
   band as VS Code's default slider (`rgba(121,121,121,.4)` on `#1e1e1e` =
   2.42:1). A scrollbar thumb is a browser-provided control rather than one of
   our own UI components, so WCAG 1.4.11's 3:1 is not a hard gate here; 65% is
   the drop-in alternative if we later decide it should be.

2. **The terminal viewport keeps the platform scrollbar.**
   `@xterm/addon-fit@0.11.0` computes columns as
   `width − padding − (options.scrollback === 0 ? 0 : options.overviewRuler?.width || 14)`
   — a **hardcoded 14px**, never a measurement of the real scrollbar.
   `useTerminal.ts:56` sets neither `scrollback: 0` nor `overviewRuler`, so 14
   it is, and `.xterm-viewport` is `overflow-y: scroll` in xterm's own CSS.
   Making that scrollbar thinner therefore leaves fit reserving more width than
   the scrollbar occupies: up to ~4px, i.e. at most one column of unused space.
   The error is in the safe direction — content is never hidden behind the
   scrollbar — and an equivalent imprecision already exists on macOS overlay
   scrollbars, where the real width is 0 and fit still reserves 14.

   It is nonetheless free to avoid, so the terminal viewport is excluded and
   its rendering stays byte-identical to `main`. This is also what closes the
   verification gap: "unchanged from `main`" needs no live daemon to
   demonstrate, whereas "thin works correctly inside xterm's sizing model"
   would. Deliberately styling the terminal scrollbar — which means picking an
   `overviewRuler.width` that matches whatever width is chosen — is tracked
   separately as its own decision, in #1075.

3. **No global `*::-webkit-scrollbar` block.** Chromium has supported the
   standard properties since 121, and a computed `scrollbar-color` /
   `scrollbar-width` other than `auto` overrides the `::-webkit-scrollbar-*`
   pseudo-elements on that element. Under our render targets (Electron's
   bundled Chromium, Chrome 145) the standard properties are already the live
   path — including for `.activity-bar-workspace-scroll`, whose webkit block
   is inert there today. Adding a global webkit block would write a second
   rule that can never win.

### Existing overrides — must keep working

Both are class rules (specificity 0,1,0) on the scroll container itself, so
they beat both the universal selector (specificity 0,0,0) and an inherited
root value — an element's own declaration always wins over what it inherits:

| Rule | Used by | Expected after Phase 1 |
|---|---|---|
| `.scrollbar-hide` (`scrollbar-width: none`) | `TabBar.tsx:133` tab overflow strip | still no visible scrollbar |
| `.activity-bar-workspace-scroll` (accent thumb) | ActivityBarWide / ActivityBarNarrow | still accent, unchanged |

The dead `::-webkit-scrollbar` blocks inside those two class rules are left
alone in this phase — they are inert under Chromium but still the only
styling on older WebKit, and removing them is unrelated churn.

### Verification

There is no JS behaviour to unit-test, and asserting the text of a CSS rule in
Vitest would test the file rather than the rendering — jsdom computes no
scrollbar styles. Verification is therefore browser-side, and it must cover
the inheritance question specifically, since that is what the first draft of
this spec got wrong:

- **Playwright computed-style assertions** (`playwright cli -s=scrollbar-and-monitor-removal`)
  against the dev server, reading `getComputedStyle(el)` on:
  - the new-tab scroll column → `scrollbarWidth === 'thin'` and
    `scrollbarColor` resolved to the neutral thumb (proves the universal
    selector reaches an inner container, not just the viewport);
  - the activity bar list → `scrollbarColor` still the accent value;
  - the tab overflow strip → `scrollbarWidth === 'none'`;
  - `.xterm-viewport` → `scrollbarWidth === 'auto'`, i.e. **excluded**. This one
    is provable on a synthetic element carrying the class, because the claim is
    purely about the cascade: no rule in this repo narrows that class. A real
    terminal is not required, since the exclusion means the terminal renders
    exactly as it does on `main`.
- Screenshot of the new-tab pane for the visual check that prompted this.
- `TabBar.test.tsx:177` (already asserts the `.scrollbar-hide` container
  exists) must still pass — guards the override contract.
- `pnpm run lint` + `pnpm run build` clean.

**Environment limits, recorded from the actual implementation run:**

- `playwright cli … eval` is refused outright in a worktree-isolated session
  (the isolation guard rejects any command that runs a string through eval).
  `open` / `snapshot` / `click` work. The working substitute for
  `getComputedStyle` readings is a standalone Playwright node script.
- Assertions (a) (b) (c) need only a locally created workspace and tab, and
  were confirmed: the new-tab column at `NewTabPage.tsx:145` computes
  `scrollbar-width: thin` with `scrollbar-color:
  color(srgb 0.611765 0.639216 0.686275 / 0.55) rgba(0,0,0,0)` — i.e. dark
  theme `--text-secondary` `#9ca3af` at 55% over a transparent track, exactly
  what the contrast table above is computed against. The activity bar still
  reports the accent thumb at 42%; the tab strip still reports `none`.
- Assertion (d) was originally **not** obtainable: reaching a real
  `.xterm-viewport` needs an authenticated daemon and a live session, which the
  vite dev proxy (pointing at `localhost:7860`) does not provide. The PR review
  turned this from a missing measurement into a design change — the terminal
  viewport is now excluded, so (d) asserts `auto` and is satisfiable on a
  synthetic element.
- Headless macOS Chromium uses overlay scrollbars that take no layout width
  and do not paint at rest, so a still screenshot cannot show the thumb. The
  computed values are the real evidence; the "does it look right" judgement
  needs the app or a human.

### Out of scope

- Per-surface scrollbar variants beyond the existing accent one.
- Restyling the xterm scrollbar specifically (it inherits the global rule like
  everything else; anything more belongs to terminal theming).
- Removing the now-inert webkit pseudo-element blocks.

---

## Phase 2 — Remove the Tmux Agent Monitor surface

### Finding: three layers, only the top two are reachable

| Layer | Code | Consumers |
|---|---|---|
| ① Settings UI | `TmuxAgentMonitorSection.tsx` + `tmux-agent-monitor/{ChainList,StepTree,StepInspector}.tsx` | none but the settings sidebar |
| ② Read API | `GET /api/agent/monitor/{chains,chains/{id},projection}` (`monitor.go`, routes at `module.go:266-268`) + `host-api.ts` fetch fns | ① only |
| ③ Write pipeline | `hookTraceSink` (`trace.go`) → `store.TraceStore` (`trace_write.go` / `trace_read.go`), written from `handler.go:199` and 4 sites in `probe_intent_dispatcher.go` | **not** Lights |

The Lights / agent status dots do not read traces — they run off agent status
and frames. Removing ① and ② cannot affect them.

### Decision

Remove ① and ②. Keep ③ in full, including `trace_read.go`.

Rationale for keeping ③: the writes are cheap and already bounded by
`traceLimits()`, the trace tables are the only record that can reconstruct a
hook chain after the fact, and the store read path is *not* dead — a dozen
existing Go tests of the write pipeline (`handler_test.go:1698,2343,2487`,
`trace_test.go:67,127,144`, `probe_intent_dispatcher_observability_test.go:168,177,233,239`)
assert through `m.traces.ListChains` / `GetChainRecord`.

### Known forward reference

`docs/specs/2026-04-23-lights-rebuild-spec.md:324` (§Phase 5, Dev Inspector)
plans to consume these same three endpoints plus a new
`/api/agent/monitor/coverage`, and `:377` names
`internal/module/agent/monitor.go` as the Monitor API. Phase 5 was never
implemented — there is no Inspector component anywhere in `spa/src`. Since
that plan already requires changing the endpoint shape, a rebuild from the
retained store layer is cheaper than carrying ① and ② as dead code until then.
Annotating those two lines is an explicit edit item below.

### Evidence that ③ is unreachable from the UI

- Non-test readers of `m.traces.ListChains` / `GetChainRecord`: only
  `monitor.go`.
- Writers, all retained: `handler.go:199`, `trace.go:95`,
  `probe_intent_dispatcher.go:367` (+3 more sites in the same file).
- Lights / agent status run off the hook WS broadcast and statusline, not
  traces: `spa/src/stores/useAgentStore.ts:195`, daemon broadcast at
  `handler.go:586` and `handler.go:1248`.

### Deletions

**SPA — files**

- `spa/src/components/settings/TmuxAgentMonitorSection.tsx` (261)
- `spa/src/components/settings/TmuxAgentMonitorSection.test.tsx` (460)
- `spa/src/components/settings/tmux-agent-monitor/ChainList.tsx` (47)
- `spa/src/components/settings/tmux-agent-monitor/StepTree.tsx` (75)
- `spa/src/components/settings/tmux-agent-monitor/StepInspector.tsx` (67)

**SPA — edits**

- `lib/register-modules/index.tsx` — drop the import and the
  `if (import.meta.env.DEV || caps.devUpdateEnabled)` registration block at
  `:383-390`. Note `dev-environment` has its own separate gate directly above;
  only the monitor block goes.
- `lib/settings-order.ts` — drop `TMUX_AGENT_MONITOR: 21`; update the
  "Tail built-in" row of the doc table to list Dev Environment only.
- `lib/host-api.ts` — drop `fetchAgentMonitorChains` / `fetchAgentMonitorChain`
  / `fetchAgentMonitorProjection` and the four now-unreferenced interfaces
  (`AgentMonitorChainSummary`, `AgentMonitorStep`, `AgentMonitorStepNode`,
  `AgentMonitorProjectionSummary`).
- `locales/en.json`, `locales/zh-TW.json` — drop the 30 `settings.monitor.*`
  keys and `settings.section.tmux_agent_monitor` from each (62 lines total).
  Must be symmetric: `locales/locale-completeness.test.ts:9` asserts the two
  key sets are identical, so a one-sided deletion fails the suite.
  Note `settings.monitor.*` belongs to **this** section, not to the
  performance-monitor module (whose keys live under other prefixes) — verified
  by the grep in Verification below.

**SPA — test edits**

- `lib/register-modules.test.ts` — delete the
  `registers tmux agent monitor section in dev mode` case (`:151-157`); drop
  `tmux-agent-monitor` from the explanatory comment at `:408`.
- `lib/__tests__/settings-order-pr2.test.ts` — drop `'tmux-agent-monitor'`
  from `OPTIONAL_GATED` (`:52`) and from the header comment (`:29`). The
  always-on strict-equality list is unaffected.
- `lib/host-api.test.ts` — delete the **whole** `agent monitor api` describe
  block, `:296-357` (it continues past the happy-path cases into two
  error-path cases; stopping at `:338` would leave them referencing deleted
  imports), and the three imports at `:7`.
  **Trap:** the very next block at `:359` is `describe('monitor api')` — that
  is the *performance monitor* module (`MonitorSnapshot`), unrelated to this
  removal. It stays.

**Daemon — files**

- `internal/module/agent/monitor.go` (287)
- `internal/module/agent/monitor_test.go`
- `internal/module/agent/monitor_dedup_test.go`

**Daemon — edits**

- `internal/module/agent/module.go` — drop the three `mux.HandleFunc` lines at
  `:266-268`. Keep the `traces` field, `tracesInitFn`, the best-effort init and
  its `m.traces == nil` degradation comment at `:94` (reword: monitor endpoints
  no longer exist; the degradation now only means "no trace recording").
  `m.traceSink` and `Close()` are untouched.
- `internal/module/agent/module_test.go:53` — same stale wording ("monitor
  endpoints degrade") in the degraded-mode test comment; reword to trace
  recording / observability. The test itself is unchanged and must stay green.

**Docs — edits**

- `docs/specs/2026-04-23-lights-rebuild-spec.md:324` and `:377` — annotate that
  the three Monitor endpoints and `internal/module/agent/monitor.go` were
  removed on 2026-09-16 and that Phase 5 must re-add its own read layer over
  the retained `store.TraceStore`. This is a **required edit of this phase**,
  not a follow-up: leaving it unannotated is how a future implementer discovers
  the endpoints are gone the hard way.

### Coverage note

`monitor_dedup_test.go` is an HTTP golden-bytes test for dedup rehydration.
Deleting it does not lose the behaviour: `internal/store/trace_dedup_test.go`
already covers it at store level —
`TestGetChainRecord_RehydratesDedupedPayloads`, `_LegacyRowsReadUnchanged`,
`_ReadSaveRoundTrip`, `_InterleavedSaveKeepsStepsAndPayloadOneVersion`. The
only thing lost is byte-fidelity of a DTO that no longer exists.

Both of its helpers (`dedupChainRecord`, the inner `step` closure) are local
to the file. `newTestModule` lives in `handler_test.go` and stays.

### Verification

- `cd spa && npx vitest run` — full suite green. Expect zero failures from the
  removal; any failure means a consumer was missed.
- `cd spa && pnpm run lint` && `pnpm run build` — no unused-import or
  unresolved-key fallout.
- `go build ./... && go test ./internal/module/agent/... ./internal/store/...`
  — green, and the retained write-pipeline tests still exercise
  `m.traces.ListChains` / `GetChainRecord`.
- `rg -n 'AgentMonitor|agent/monitor|tmux-agent-monitor|settings\.monitor\.|monitorChainSummaryFromStore' spa/src internal cmd electron`
  returns nothing. (Pre-change this hits exactly the 5 SPA files being
  deleted, `host-api.ts`, `host-api.test.ts`, the two order/registration
  files and their tests, the locales, and the Go monitor files — nothing else.)
- Manual: open Settings with `PDX_DEV_MODE=1`; sidebar ends at
  Dev Environment, no console error, no missing-i18n-key warning.

### Out of scope

- Any change to the trace write pipeline, `TraceStore`, or its schema.
- Dropping the existing trace tables or their data.
- Building the Phase 5 Dev Inspector.
- Retention/limit tuning for trace rows.

---

## Commits

1. `style(spa): thin neutral scrollbars app-wide`
2. `refactor: remove the Tmux Agent Monitor settings surface and read API`

`VERSION` / `CHANGELOG.md` are **not** touched here — bump is a separate PR
after merge, per the project flow.

---

## Review history

Codex cross-review of the first draft (job `task-mu3m3qzh-g82npz`, gpt-5.5,
read-only). Findings and their resolution:

| Sev | Finding | Resolution |
|---|---|---|
| high | `scrollbar-width` is not an inherited property, so a `:root`-only rule would style the viewport and nothing else | rewritten: `scrollbar-color` on `:root` (inherited, crosses shadow boundaries), `scrollbar-width` on `*` |
| medium | `text-muted` @ 45% has too little contrast (worst 1.35:1) | switched to `text-secondary` @ 55% (worst 2.46:1); measured table added, with the reason `text-muted` cannot be rescued by alpha |
| medium | `host-api.test.ts` delete range `:296-338` cuts the describe block in half | corrected to `:296-357`, plus the `describe('monitor api')` trap at `:359` |
| medium | Phase 1 verification could not catch the inheritance error it contained | replaced screenshot-only check with Playwright `getComputedStyle` assertions on four specific containers |
| low | stale "monitor endpoints degrade" comment also in `module_test.go:53` | added to the edit list |
| low | Phase 5 annotation was prose, easy to skip | promoted to a required edit item with line numbers |
| low | webkit-precedence rationale imprecise | reworded |
| low | two phases in one PR acceptable, given Phase 1 stays CSS-only | kept; Phase 1 did stay CSS-only |

PR review, 4 reports (round 1 standard: **no actionable findings**; round 2
attack / defend / file-health, jobs dispatched in parallel):

| Sev | Finding | Resolution |
|---|---|---|
| medium (attack) | global `thin` narrows the real scrollbar while `@xterm/addon-fit` reserves a hardcoded 14px | confirmed in `node_modules/@xterm/addon-fit/lib/addon-fit.js`; impact is ≤1 column in the safe direction, but avoided outright — `.xterm-viewport` is excluded |
| medium (defend) | the spec's own `.xterm-viewport` acceptance gate was left unverified | closed by the exclusion above: the assertion is now about the cascade, provable without a live daemon |
| medium (health) | scrollbar policy accreting in the `index.css` junk drawer, with comments drifting into a spec summary | moved to `spa/src/styles/scrollbars.css`, comments trimmed, rationale kept here |
| medium (health) | other specs still describe the deleted API as live | `2026-05-02-settings-architecture-fix-spec.md` (order 21) and `2026-09-07-trace-payload-dedup-spec.md` (`handleMonitorChain` as sole caller) annotated as superseded |
| low (health) | trace read path is now a tested island with no runtime consumer | #1073 — issue, not a code change; the retention is a deliberate decision recorded above |
| medium (attack) | old renderer + new daemon → 404 on the removed routes | accepted: dev-only surface, alpha, and removing the API was the explicit decision. #1074 for the skew policy; no deprecation shim |
