# Plan — host sync identity (spec: `2026-09-23-host-sync-identity-spec.md`)

Three PRs. PR 1 is pure and unused; PR 2 switches the wire over (build + apply + ordinals in ONE PR — a build that
translates without an apply that does, or the reverse, breaks sync); PR 3 is the wizard and the paused state.
PR 1 and PR 3 can be written in parallel; PR 2 needs PR 1. One bump after PR 3.

| PR | owner | files (expected) | must not touch |
|---|---|---|---|
| 1 codec | purdex-38 | `lib/profile/host-identity.ts` (+test) | everything else |
| 2 wire | purdex-fb (subagent) | `sections.ts`, `collector.ts`, `applier.ts`, `apply-to-stores.ts`, `executor.ts` (settings gate), `projections.ts` (ordinals), tests, `types.ts` if needed | wizard, start.ts |
| 3 wizard + pause | purdex-3b | `wizard-run.ts`, `ProfileWizard.tsx` / `WizardChoiceSteps.tsx`, `start.ts` (`blocked: 'host-identity-mismatch'`), `CurrentBlock.tsx` (its sentence), `sync-view.ts` (dot), locales, tests | sections / collector / applier / apply-to-stores / executor |

## PR 1 — the codec (pure)

- `syncIdOf(daemonId): Promise<string>` — spec §3 exactly; golden vectors in the test (at least: `mini-lab:278cbm`,
  an ASCII id with spaces, a non-ASCII id, a 512-char id), computed once and pinned; both hash paths
  (`crypto.subtle` and the JS fallback) give the same bytes.
- `identityOf(hosts): Promise<Identity>` — `{ toWire: Map<local, wire>, toLocal: Map<wire, local>, conflict:
  string[] | null }`; spec §4 rules each with a test (valid claim → d1_; no claim → local id; invalid claim → local
  id; two hosts one daemon → conflict; two claims one sync id (forced by a stub) → conflict).
- Translators, each total and pure, with round-trip tests (`fromWire(toWire(x)) === x` for every pane kind, nested
  splits, host-settings record, preset columns incl. non-host columns untouched): `hostsToWire` / `hostsFromWire`
  (the matching of spec §6 lives here as `matchIncomingHosts(local, incoming) → {byRow: Map<wireId, localId|'new'>,
  removed: localId[]}`), `layoutToWire` / `layoutFromWire`, `hostSettingsToWire` / `…FromWire`,
  `presetColumnsToWire` / `…FromWire`.
- Mutation for every rule.

## PR 2 — the wire switch

- Collector: one `identityOf` per pass; `buildHostsSection` / `buildTabsSection` / settings build through it;
  identity conflict → no host-naming section built, problem `host-identity-conflict`.
- Apply: hosts via `matchIncomingHosts` (update in place / create new local id / cascade removed); `hostsRefusal` by
  identity; tabs and settings translated after hosts with the post-apply identity; the reported hash is the wire
  build.
- Executor: `settings` waits for `hosts` settled, like `tabs.*` (spec §6).
- Ordinals: hosts 2→3, tabs +1, settings +1; old-ordinal payloads accepted as spec §7 (legacy ids matched by local
  id, then by `daemonId`), and the first reconciliation pushes canonical.
- Tests: two-device simulation with independent ids (the scenario that shipped broken): A builds, B applies → B's
  hosts keep B's ids, B's panes name B's ids, no `host-removed`, no lock; B builds → equal hashes to A's. Legacy
  ordinal-2 payload from each device → canonical after one round. Old/new client coexistence → old locks
  `schema`, new does not ping-pong. Mutations for each translation point (dropping any one must fail the
  two-device test).

## PR 3 — wizard and pause

- `prepareRun`: pull requires the attach host verified with no mismatch (`master-unverified` / `master-mismatch`,
  with sentences); `listProfiles(…, {expectEndpoint})`.
- The pull warning lists the local hosts the pull will remove (fetch the SOT `hosts` section in `prepareRun` with
  `expectEndpoint`; use PR 1's `matchIncomingHosts`; if PR 1 is not merged yet, stub behind the same signature).
- `start.ts`: `blocked: 'host-identity-mismatch'` while any host of the master has a runtime mismatch (subscribe to
  the host store; no payload built or applied meanwhile); Current block sentence + switcher dot (`problem`).

## Review and acceptance

Plan + spec: one codex round. PR 1: R1 + attack. PR 2 and PR 3: R1 + attack + critic. Real machine: spec §10 with
independent host ids, run by purdex-fb after PR 3.
