# Workbook journal prototype (reference copy)

The prototype the user reviewed on 2026-10-13 for the journal redesign of the session workbook (spec v3: `docs/specs/2026-10-14-workbook-journal-spec.md`). It is a reference, not app code: nothing here is built, linted or type-checked, and the real implementation is written fresh from the spec.

- `prototype/` — the prototype shell (`Proto.tsx`), its own Vite entry, and `fixture.json` (real events from the A line on 2026-10-13).
- `journal/` — `JournalPanel.tsx` and `journal-types.ts` (the event model drawn in the prototype: six kinds, a `pending` flag, five filters).
- `shots/` — the five states the user saw: both closed, journal only, both open, the pill, a narrow window with both open.

Source: the untracked worktree `.claude/worktrees/agent-a2902cc2ea7ee43e4` (base d611b312b, alpha.727), copied as is.

To rebuild it, copy `prototype/` back to `spa/prototypes/workbook-journal/` and `journal/` to `spa/src/components/journal/` on a base near alpha.727, then from `spa/` run `npx vite build -c prototypes/workbook-journal/vite.config.ts` (its `outDir` points at the main checkout's `docs/pages/workbook-journal`).
