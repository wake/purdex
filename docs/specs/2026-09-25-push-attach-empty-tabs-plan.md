# Push attach keeps an empty `tabs.<id>` (#1450) — spec + plan

Status: draft for codex plan review (spec and plan in one file: one bug, one PR).

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

**R1.** During the first reconciliation with direction **push**, `judgePlaceholder` returns `'edit'` for every report:
this device's world wins by definition, so an empty `tabs.<id>` is this device's content, not a placeholder.
Direction `pull` and `null` (no period / period over) keep today's behaviour exactly.

Consequence (no new mechanism): the empty report reaches the reducer → dirty + SOT moved → `lock-conflict` → the
existing `answerFor` answers push = keep-local synchronously → pushed with the SOT's rev as base → SOT `tabs.<id>`
becomes `{order: [], tabs: {}}`, rev +1, and nothing is pulled into this device.

**R2.** The executor header comment for the placeholder rule states that it applies to pull / ordinary sync only, and
why push is exempt.

**R3 (small, same wizard).** `settings.profile.wizard.local.move` shows the old master's name as
`master.name ?? t('nav.home')` ("Home"), but `promoteToMaster` names the demoted local workbench
`master.name ?? offeredProfileName(...)` (the device name, e.g. "Chrome · macOS"). The consequence text must name what
will actually be created: pass a separate `demoted` value computed by the same rule the run uses
(`master.name ?? offeredProfileName(<the same `also` list the run passes>)`), and change the string (en + zh-TW) to
mention both: the master "{{master}}" stays on this device as a local workbench named "{{demoted}}". When
`master.name` is set the two are equal; the string must still read naturally (or pick the one-name variant when equal —
executor's call, tested either way).

Out of scope: making `unsorted` per-world (C in the investigation — treats the symptom only, needs id migration).

## 4. Plan (one PR, TDD, target ≤ 8 files, ≤ 200 lines)

**T1 — regression test first (red).** `spa/src/lib/profile/executor.direction.integration.test.ts`,
`describe('a second client attaches')`: A pushes `ws('unsorted', ['ta1'])`; B has `ws('unsorted', [])` (and a
variant where B's only tab is a Hosts tab — helpers exist in `tabs-local-only.integration.test.ts`); B attaches with
**push**. Expect: SOT `tabs.unsorted` order `[]`, rev 2, last writer B; B's tab store has no `ta1`; nothing left
locked; the period ends. Both variants red on main.

Also a guard test: the same setup with direction **pull** still ends with B holding A's `ta1` and no lock ever shown
(the placeholder rule still protects pull) — green before and after.

**T2 — fix.** `executor.ts` `judgePlaceholder`: `if (direction() === 'push') return 'edit'` before the other checks
(after the kind/empty check is fine too — must cover the held path). Update the header comment (R2). T1 green.

**T3 — mutation (deliverable).** (m1) drop the new line → T1 red; (m2) make it `direction() !== null` → the pull guard
test red. Report both.

**T4 — wizard copy (R3).** `WizardChoiceSteps.tsx` `LocalStep` gets / computes `demoted`; en + zh-TW
`settings.profile.wizard.local.move`; test in the existing wizard step test file: master unnamed → the text contains
the device-derived name that `promoteToMaster` would use (assert by calling the same helper, not a literal), master
named → its name. Mutation: pass `masterName` as `demoted` → red.

**Verification.** `cd spa && npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`,
`pnpm run build`.

**Acceptance on a real client (after review).** mlab daemon, SOT profile `p_5566490da878` (left there for this): reproduce
the §1 steps on a worktree dev server (not :5174), two clients each with its own host id; check with the daemon
section index that `tabs.unsorted` is rev +1 and empty and that L2's screen shows no old tmux tab; then promote back
and check the demoted L2 does not receive them either. Wizard consequence text shows the name that the demoted record
actually gets.

## 5. Risks

- During a push attach a `workspaces` change from another client cannot land (push answers every lock keep-local), so
  no genuine "not yet arrived" placeholder can exist to be lost.
- The conflict → keep-local → push path is already exercised by the PUSH tests (`'PUSH — B has one workspace of its
  own'`); R1 only routes one more report into it.
