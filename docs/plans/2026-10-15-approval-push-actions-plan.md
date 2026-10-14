# Approval pushes with buttons, and the late push — plan (P-1, P-2)

Spec: `docs/specs/2026-10-15-approval-push-actions-spec.md` (facts §3).

## 1. Design choices (for the reviewer)

- **No new daemon route**: the notification answers through the existing `decide` endpoint, which device tokens may already call.
- **Category only on the three yes / no kinds**; `hook_ask` keeps a plain push because its decisions need answers or text.
- **The late push reuses `holdSet`**; correctness comes from one per-card state under the module's own lock (reserve before enqueue, a tombstone on close), a final open check in the sender, and a `Stop` that waits for in-flight pushes — re-reading the card alone would leave a close-after-read window (codex plan review `task-mv8fhwcf-bjgiij`, 11 findings, all taken).
- **Restart loses a pending late push** (presence is empty after a restart, so a re-schedule could not tell a pushed card from a held one); accepted, written in the spec.
- **iOS action handling stands alone** (UserDefaults host list + Keychain token → `DaemonClient`), because a background wake may have no `Fleet`.

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| P-1 | daemon: category, late push, guards, log lines | 1f (push module) | ~350 |
| P-2 | iOS: category, background decide, outcome notices | — | ~400 |

P-2 can start at once (an unknown category is ignored by an old App, and a new App with an old daemon simply never sees the category). Every PR: TDD, one commit per task, mutation table; daemon `go test -race` on the touched packages, never the full `go test ./...`; iOS tests on air26 only. codex R1 → attacker → critic.

## 3. P-1 — daemon

- **T-1.1 Category** (`internal/push/content.go`): `Content.Category`; `ApprovalContent` sets `"PDX_APPROVAL"` for `lead`, `self_relay`, `member_relay`; `payload()` writes `aps.category` when set; the size-limit accounting includes it.
- **T-1.2 Approval push state** (`internal/module/push/approval_push.go`, new): one struct with **its own mutex** (never `m.mu`, which `snapshot()` takes): `running bool`, `inflight sync.WaitGroup`, `states map[id]{state: held | pushing | pushed | closed, at}`.
  - `opened`: under the lock, not running → return. At the computer (first `AtComputer()`), and `holdSet` has room → `held`, schedule `late(id)` after `lateAfter` (60 s; a test seam like `holdFor`). Otherwise → the push path; when `holdSet` is full the push path is told to **skip the presence re-check**.
  - **Push path** (immediate or late): under the lock — running, state is not `pushing` / `pushed` / `closed` → set `pushing`, `inflight.Add(1)`; unlock; device snapshot, the presence re-check on the immediate path only (unless told to skip), enqueue with **`RequireOpen(id)`** on the job; under the lock → `pushed`, or back to the earlier state when nothing was enqueued (unless `closed` meanwhile); `inflight.Done()`.
  - `late(id)`: under the lock, running and state `held`; then read the card through the module's approval reader. Open (including the unanswered last self-relay card, which stays open with `unanswered_at` set) → push path. Not open → log `closed`. Reader missing or read error → push path anyway (fail open: a stale card's notification is cleaned by the existing close badge push, a missed relay request is not), logged as `read_failed`.
  - `closed`: under the lock → `closed` (a tombstone, so a late callback or a push path that has not enqueued yet stops).
  - Entries older than 30 min are pruned on each event, so ids whose close event was dropped (the subscriber queue drops past 256) cannot grow without bound.
  - **Lifecycle**: `Start` resets the map and sets `running`; `Stop` clears `running` under the lock first, then `holds.stopAll()`, then `inflight.Wait()`, and only then swaps the sender out.
- **T-1.3 Sender final check**: approval jobs carry `RequireOpen(id)`; the sender checks the latest open set right before sending, and drops the job when the card is no longer open (both paths; today the sender sends a queued approval job even after the card closed).
- **T-1.4 Logs**: per spec §4.1, with the reasons `pushed`, `held 60s (at_computer)`, `late push`, `not pushed: closed | already_pushed | shows_name | no_devices | kind | dropped_at_send`, `read_failed`.
- Tests: category present for the three kinds and absent for `hook_ask`, while `hook_ask` is still pushed (M: on every kind); `Build` with the category at the size boundary keeps the carried reads consistent with the payload (`reads_payload_test.go` pattern); at the computer → nothing at once, one push after 60 s when still open (M: push at once); the unanswered last self-relay card is late-pushed; closed before 60 s → no push; **close racing the late callback** (deterministic: close between the open read and the enqueue) → no send (M: drop the tombstone); **immediate and late concurrently** → one enqueue (M: set `pushed` after the enqueue instead of reserving); away → immediate push and no late push; `holdSet` full with `AtComputer()` held true → pushed at once (M: re-check presence); reader missing / read error → pushed with `read_failed`; **Stop while a callback is past the running check** → `Stop` waits and nothing is sent after it returns, and a later `Start` begins empty; queued job whose card closes before sending → dropped (M: no `RequireOpen`); log line per outcome. Update `TestAway_AtTheComputerNoApprovalIsPushedEvenWithoutATmuxOrigin` (`away_gate_test.go:152`) and the closed / kind assertions in `trigger_test.go:154-176,209-214` (visible approval push vs background badge push vs `hook_ask`).

## 4. P-2 — iOS (purdex-ios)

- **T-2.1 Category**: register `PDX_APPROVAL` (`approve` 同意 `.authenticationRequired`; `deny` 拒絕 `.destructive`) in `PurdexAppDelegate.didFinishLaunching`, before the notification center's delegate can receive a response.
- **T-2.2 Background decide** (`PushCenter`): `didReceive response` with `approve` / `deny` → `ApprovalAction.perform(payload, decision)`: host record by `SyncID` from `hosts.v1`, token from the Keychain, `DaemonClient(...).decideApproval(id, decision, grant: lead && approve ? {max_members: 3} : nil, client: <the App's usual client>)`, inside `beginBackgroundTask`; `completionHandler` after it ends. A small injectable resolver so tests need no real Keychain.
- **T-2.3 Outcome**: per spec §4.2 — remove the delivered notification (same id) on closed / decided elsewhere / refused / not found, and post a local notice with the reason; keep it and post 「沒送出：…」 on network or other errors. Default tap unchanged.
- Tests: category and actions registered with their options; payload → resolver → decide call with the right body for each kind (lead approve carries `max_members: 3` only) (M: grant on relay kinds); each outcome's notification removal and local notice (M: remove on network failure); host not found → local notice, no request; default tap still opens the card.
- Release: TestFlight / OTA as usual; the OTA swap after release.
