# Approval pushes with buttons, and the late push — plan (P-1, P-2)

Spec: `docs/specs/2026-10-15-approval-push-actions-spec.md` (facts §3).

## 1. Design choices (for the reviewer)

- **No new daemon route**: the notification answers through the existing `decide` endpoint, which device tokens may already call.
- **Category only on the three yes / no kinds**; `hook_ask` keeps a plain push because its decisions need answers or text.
- **The late push reuses `holdSet`** and re-reads the card when it fires, so a close between scheduling and firing needs no explicit cancel; an in-memory pushed-set makes "pushed once" hold across the immediate and the late path.
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
- **T-1.2 Late push** (`internal/module/push/module.go`): split `onApproval("opened")` into `pushApproval(a, why)`; at the computer → `m.holds.after(lateAfter, func(){ m.latePush(a.ID) })` with `lateAfter = 60 s` (a seam for tests, like `holdFor`); `holdSet` full → push at once. `latePush`: module running; the card read again through the module's approval reader and still open; not in `pushed`; device snapshot not empty → `pushApproval`. The re-check before the enqueue (`AtComputer`) stays on the immediate path only.
- **T-1.3 Pushed-set and logs**: `pushed map[string]struct{}` under the module's lock, set after a successful enqueue, deleted on `closed` and on `Stop`; log lines per spec §4.1 (approval id first 8 chars, kind, outcome).
- Tests: category present for the three kinds and absent for `hook_ask` (M: on every kind); payload shape and size limit with the category; at the computer → nothing at once, one push after 60 s when still open (fake clock / seam) (M: push at once); closed before 60 s → no push; decided on another device in the window → no push; away → immediate push and no late push (M: both); `holdSet` full → immediate; `Stop` before firing → nothing; pushed-set blocks a second push of the same id; log line per outcome. Update `TestAway_AtTheComputerNoApprovalIsPushedEvenWithoutATmuxOrigin` (`away_gate_test.go:152`) to the new behaviour.

## 4. P-2 — iOS (purdex-ios)

- **T-2.1 Category**: register `PDX_APPROVAL` (`approve` 同意 `.authenticationRequired`; `deny` 拒絕 `.destructive`) in `PurdexAppDelegate.didFinishLaunching`, before the notification center's delegate can receive a response.
- **T-2.2 Background decide** (`PushCenter`): `didReceive response` with `approve` / `deny` → `ApprovalAction.perform(payload, decision)`: host record by `SyncID` from `hosts.v1`, token from the Keychain, `DaemonClient(...).decideApproval(id, decision, grant: lead && approve ? {max_members: 3} : nil, client: <the App's usual client>)`, inside `beginBackgroundTask`; `completionHandler` after it ends. A small injectable resolver so tests need no real Keychain.
- **T-2.3 Outcome**: per spec §4.2 — remove the delivered notification (same id) on closed / decided elsewhere / refused / not found, and post a local notice with the reason; keep it and post 「沒送出：…」 on network or other errors. Default tap unchanged.
- Tests: category and actions registered with their options; payload → resolver → decide call with the right body for each kind (lead approve carries `max_members: 3` only) (M: grant on relay kinds); each outcome's notification removal and local notice (M: remove on network failure); host not found → local notice, no request; default tap still opens the card.
- Release: TestFlight / OTA as usual; the OTA swap after release.
