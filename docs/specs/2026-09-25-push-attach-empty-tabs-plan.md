# Push attach keeps an empty `tabs.<id>` (#1450) — spec + plan

Status: rev 2 — codex plan review `task-muggb0zh-1kf1qg` folded in (R1 narrowed, guards T1b, R3 honest copy). Spec and
plan in one file: one bug, one PR.

## 1. The bug

Wizard: stop sync → pick local workbench L2 as the master → **push**. The old master's tabs were in the workspace
with the fixed id `unsorted` (`features/workspace/store.ts:22`); L2 also has an `unsorted` workspace, holding only a
Hosts tab (a device-local pane kind, never synced, #1380). After the push:

- `workspaces` → rev 2, `tabs.kypqck` created — correct;
- `tabs.unsorted` stays at rev 1 with the OLD master's two tmux tabs, and those tabs are **pulled into L2's screen**.

The wizard promises the push REPLACES the SOT with this device's world.

## 2. Root cause (verified against the code)

1. Every attach starts with no base: `start.ts` clears the section store, so `tabs.unsorted` has `base.hash === null`.
2. The collector reports `tabs.unsorted` = `{order: [], tabs: {}}` (the Hosts tab is filtered by `isSyncableTab`) — a
   real payload, not ABSENT.
3. `executor.ts` `receive` → `judgePlaceholder` (≈ line 1023): a `tabs.*` report whose payload is empty, with no base,
   while the SOT has content, is judged **`not-arrived`** and dropped (only `pump`). The rule (header, ≈ lines 67–81)
   exists for the PULL case: `workspaces` from elsewhere makes an empty workspace appear and its tabs are 500 ms away —
   asking the user to choose between a blank and the real tabs would be wrong.
   **The rule never looks at the direction.** A held report (index not landed) goes through the same judgement in
   `settleHeldPlaceholders`.
4. The report never reaches the reducer → `currentHash` stays null → not dirty, SOT moved → decision table row "pull"
   (`sync-state.ts` ≈ 402) → `pump`'s pull branch (`mayPull` only asks that `workspaces` is synced and the workspace is
   local; both true after the push of `workspaces`) → the SOT's old tabs are applied into L2.

The shared `unsorted` id only makes the collision likely; any workspace id present both locally (empty after
projection) and on the SOT hits it on a push attach — including stop sync → empty a workspace → attach again with push.
Ordinary sync (a base exists) is unaffected: emptying a workspace pushes `{order: [], tabs: {}}` (already tested in
`tabs-local-only.integration.test.ts`).

## 3. The fix (spec)

**R1 (rev 2, after codex plan review #1/#2).** A push period can stay open while `workspaces`, already synced, is
pulled again because another client moved it (row "clean + moved" → pull; `direction()` does not stop the pull branch
of `pump`). A workspace that lands that way is exactly the case the placeholder rule protects: its real tabs are on
their way. So the exemption is narrow:

> During the first reconciliation with direction **push**, `judgePlaceholder` returns `'edit'` for an empty
> `tabs.<id>` report **unless workspace `<id>` was added to this device by a `workspaces` pull applied during this
> period**. Such a workspace keeps today's placeholder judgement (`not-arrived` / held).

The executor records the set `pulledInThisPeriod` (workspace ids present after a `workspaces` pull apply and absent
before it, computed around the apply with the existing `localWorkspaceIds()`); the set is per executor (every attach
is a fresh period) and is not persisted. Direction `pull` and `null` keep today's behaviour exactly.

Consequence for the #1450 case (no new mechanism): the empty report reaches the reducer → dirty + SOT moved →
`lock-conflict` → `answerFor` answers push = keep-local synchronously → pushed with the SOT's rev as base → SOT
`tabs.<id>` becomes `{order: [], tabs: {}}`, rev +1, and nothing is pulled into this device.

**R2.** The executor header comment for the placeholder rule states the push exemption and its limit (a workspace
pulled in during the period is still a placeholder), and why.

**R3 (small, same wizard; rev 2 after codex #6).** `settings.profile.wizard.local.move` shows the old master as
`master.name ?? t('nav.home')` ("Home"), but the demoted record is named `master.name ?? offeredProfileName(...)`
(device name, e.g. "Chrome · macOS"), computed at run time — it can change between the screen and the run, so the text
must not promise an exact name it cannot guarantee:
- master named → text unchanged (the name is kept as is);
- master unnamed → a second key `settings.profile.wizard.local.move_unnamed` (en + zh-TW): the master (shown as
  "{{master}}") stays on this device as a local workbench **named after this device (right now: "{{demoted}}")**,
  where `demoted` = `offeredProfileName(<the same also-list the run would pass for the current draft>)`.

Out of scope: making `unsorted` per-world (treats the symptom only, needs id migration).

## 4. Plan (one PR, TDD, target ≤ 8 files, ≤ 250 lines)

**T1 — regression tests first (red).** `spa/src/lib/profile/executor.direction.integration.test.ts`,
`describe('a second client attaches')`:
- (a) A pushes `ws('unsorted', ['ta1'])`; B has `ws('unsorted', [])`; B attaches with **push**. Expect: SOT
  `tabs.unsorted` order `[]`, rev +1, last writer B; B's tab store has no `ta1`; nothing left locked; the period ends.
- (b) the same, B's only tab in `unsorted` is a Hosts tab (helpers in `tabs-local-only.integration.test.ts`); B keeps
  its Hosts tab, SOT `tabs.unsorted` is empty.
- (c) same as (a) but the empty report arrives **before the index lands** (held path) — use whatever ordering control
  the existing tests have; if the harness cannot order it, say so in the report instead of faking it.

(a)–(c) red on main.

**T1b — guards (green before and after; they fail a too-wide fix).**
- (d) **push period still open**, `workspaces` already synced; client A adds workspace `w2` with tabs — the SOT gets
  `workspaces` first and `tabs.w2` later (two writes). B pulls `workspaces`, the collector reports empty `tabs.w2`,
  then `tabs.w2` lands. Expect: B ends with A's `w2` tabs, B wrote no `tabs.w2`, no `locked:conflict` ever observed.
  Keep the period open by any existing means (e.g. another section still in flight); if the harness cannot keep it
  open, stop and report.
- (e) pull-direction attach with the (a) setup: B ends with `ta1`, and a recorded status history (`onStatus` or the
  section-state stream) **never** contains `locked:conflict` for `tabs.unsorted` — assert the history, not only the
  final state (codex #5).
- (f) a reconnect in the middle of the (a) push attach still ends as (a).

**T2 — fix.** `executor.ts`: `pulledInThisPeriod` recorded around the `workspaces` pull apply; `judgePlaceholder`
returns `'edit'` when `direction() === 'push'` and the report's workspace is not in the set. Must cover the held path
(`settleHeldPlaceholders` uses the same judgement). Header comment (R2). T1 green, T1b still green.

**T3 — mutations (deliverable, report each red test).** m1 remove the exemption → (a)(b)(c) red; m2 exempt regardless
of `pulledInThisPeriod` → (d) red; m3 exempt on `direction() !== null` → (e) red.

**T4 — wizard copy (R3).** `WizardChoiceSteps.tsx` `LocalStep`; en + zh-TW; tests in the existing wizard step test
file: master unnamed → `move_unnamed` with the name from `offeredProfileName` (call the helper in the test, no
literal); master named → `move` with that name. Mutation: unnamed uses the `move` key → red.

**Verification.** `cd spa && npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`,
`pnpm run build`.

**Acceptance on a real client (after review).** mlab daemon, SOT profile `p_5566490da878` (left there for this):
reproduce §1 on a worktree dev server (not :5174), two clients each with its own host id; the daemon section index
shows `tabs.unsorted` rev +1 and empty and L2 shows no old tmux tab; promote back and check the demoted L2 does not
receive them either. Wizard text for an unnamed master shows the device-derived name.

## 5. Risks

- A workspace that arrives by a pull during a push period keeps the placeholder rule (T1b d); an id that existed
  locally at attach AND is re-added by a pull in the same period is treated as pulled (safe side: placeholder).
- The conflict → keep-local → push path is already exercised by the PUSH tests (`'PUSH — B has one workspace of its
  own'`); R1 only routes one more report into it.
