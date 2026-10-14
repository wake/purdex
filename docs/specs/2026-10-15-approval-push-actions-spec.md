# Approval pushes with buttons, and a late push when the card waits — spec

> 2026-10-15, 88 (interface line). The user's decisions are §2; the rest is 88's design on them.
> Facts read from origin/main and purdex-ios origin/main `23032c6b8` on 2026-10-15.

## 1. Problem

1. An approval push (lead request, relay request) only opens the App; deciding takes opening the card. The user wants to answer from the notification.
2. While any Mac reports the user at the computer (`AtComputer()`), approval cards are never pushed, not even when the card then waits unanswered. On 10-14/15 the user sat at air-2019 looking at the iPhone, and relay requests were only seen by chance when opening the App. Measured 10-15 01:12: with the Macs locked, a lead card was pushed the same second and shown on the iPhone — the chain works; the gate is what hid them.

## 2. User decisions (2026-10-15, do not reopen)

1. Approval pushes carry **both buttons, 同意 and 拒絕** (「做完整按鈕」).
2. **At the computer → 丙**: not pushed at once; **if the card is still open after 1 minute, push it then**, whatever the presence says. Away from the computer → pushed at once, as today.

## 3. Facts this rests on

- **Decide API**: `POST /api/team/approvals/{id}/decide` (`internal/module/team/module.go:577`, `handler_decide.go:11`), body `{decision: "approve" | "deny", grant?, hook?, client: {kind, label}}` (`internal/team/wire.go:155`); no epoch; device tokens are allowed (`cmd/pdx/device_scope.go:37-39`). 200 → the closed approval; 409 `already_decided` with the approval; 409 `already_lead` / `member_cannot_lead` leave the card open; 404 `not_found`.
- **Kinds**: `self_relay` and `member_relay` take approve / deny with nothing else. `lead` approve may carry `grant {max_members, roots, team_name, team_label}`; without a grant the daemon takes the request's own values (`unattended.go:55-62`); the iOS card pre-fills `max_members = 3` (`ApprovalViews.swift:66-71`, U25), roots / names from the request. `hook_ask` approve needs the answers to every question and deny needs a message (`ask_handler.go:475-`) — neither fits a button. `adopt` and terminal-only `hook_ask` are not pushed.
- **Push payload** (`internal/push/content.go`): `aps {alert, sound, thread-id = host id, interruption-level = time-sensitive, mutable-content = 1}`, no category; `purdex {host_id, kind, approval_id}`; collapse id = approval id (`content.go:185`). iOS finds the host by `SyncID.from(daemonID: host_id)` against `hosts.v1` in `UserDefaults` (`PushModels.swift:32`, `Fleet.swift:79,227-229`); the token is in the Keychain, `AfterFirstUnlockThisDeviceOnly` (`Keychain.swift:16`).
- **The gate** (`internal/module/push/module.go` `onApproval`, ~371-405): on `opened`, `AtComputer()` (checked again before the enqueue), `ShowsName`, or no device → return, with no log line. `holdSet` (`agent_trigger.go:189-231`) is the module's cancellable timer set (cap 256, stopped in `Stop`). Every close (approved, denied, timeout, cancelled, abandoned) reaches `onApproval("closed", …)`; the last self-relay card that times out stays open with `unanswered_at` set (`pause_unanswered.go`). Cards wait 540 s by default (`team.DefaultWaitS`).
- **iOS**: no `UNNotificationCategory`; `didReceive response` only opens (`PushCenter.swift:112-119`); `willPresent` → `[]` in the foreground; card kinds skip the in-App notice (`:57`). The NSE only adjusts the badge and removes read notifications. `DaemonClient.decideApproval` → `DecideOutcome` (`TeamApproval.swift:201-215`).
- **Mac**: the SPA raises a macOS notification on a lead / relay / adopt card (`approval-ws.ts:115` → `approval-notify.ts:38` → `electron/main.ts:172`), without buttons.

## 4. Design

### 4.1 Daemon

- **Category**: approval pushes of kind `lead`, `self_relay`, `member_relay` carry `aps.category = "PDX_APPROVAL"`; `hook_ask` pushes carry none (tap to open, as today). An App that does not know the category shows the notification without buttons.
- **Late push**: on `opened`, at the computer → schedule a check in 60 s (`holdSet`). When it fires: the module is still running, the card is still open (read it again), it has not been pushed already, there is a device → push it with the same content as an immediate push. Away → pushed at once (unchanged). `holdSet` full → push at once. A daemon restart inside the 60 s loses the late push (presence is empty after a restart; the window is accepted and written down).
- **Pushed once**: an in-memory set of pushed approval ids guards both paths; cleared on `closed`.
- **Logs**: one line per approval decision of the push module — `[push] approval <id8> kind=<kind> pushed` / `held 60s (at_computer)` / `late push` / `not pushed: <reason>` (`shows_name`, `no_devices`, `kind`, `closed`, `already_pushed`).

### 4.2 iOS

- **Register** at launch the category `PDX_APPROVAL` with two actions:
  - `approve` 「同意」 — `.authenticationRequired` (the phone must be unlocked), not `.foreground`.
  - `deny` 「拒絕」 — `.destructive`, no unlock needed (a refusal changes nothing), not `.foreground`.
- **Handle** `approve` / `deny` in `didReceive response` without the App's UI: parse the payload (`approval_id`, `host_id`, `kind`), find the host record and token from `UserDefaults` + Keychain (independent of `Fleet`, which may not exist after a background wake), call `decideApproval` with `client {kind: "app", label: <the App's usual label>}`; a `lead` approve sends `grant {max_members: 3}` and nothing else (roots and names stay the request's, as the card would pre-fill). Wrapped in a background task; `completionHandler` after the request ends.
- **Outcome**: closed → remove the notification; decided elsewhere (409 with the approval) → remove it and post a short local notice 「已由〈誰〉處理」; refused (409 `already_lead` / `member_cannot_lead`) or not found → remove it and post the reason; network failure or any other error → keep the notification and post 「沒送出：〈原因〉，請打開 App 再試」.
- The default tap (no button) keeps today's behaviour: open the App at the card.

## 5. Out of scope

Buttons on `hook_ask` (answers or a message are needed); buttons on the Mac's notification; persisting the late push across a daemon restart; changing who counts as at the computer.

## 6. Phases

| phase | side | content |
|---|---|---|
| P-1 | daemon | category on three kinds; the 60 s late push with its guards; the push module's approval log lines |
| P-2 | iOS (purdex-ios) | category registration; background approve / deny; outcome notices |
