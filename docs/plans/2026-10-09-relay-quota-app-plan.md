# Relay quota — App side (RQ-A) — plan

Spec: `docs/specs/2026-10-09-relay-quota-spec-plan.md` (#2062; rules R1–R5 confirmed by the user, do not reopen).
Owner: interface line. The coordinator turns the host switch `relay_quota` on only after this is merged and the main
checkout is fast-forwarded (spec §3.6), so this PR is what makes the rule safe to switch on.

Daemon facts (alpha.634, `internal/team/wire_quota.go`, `wire_unattended.go`, `internal/module/team/quota_handler.go`):
- `GET /api/team/unattended` → `UnattendedView.quotas: SessionQuota[]` (`session_id`, `root_session_id`, `title?`,
  `address`, `is_lead`, `self_left`, `member_pool_left`) for every live session of the host.
- `PUT /api/team/relay-quota` `{session_id, self_left?, member_pool_left?, client}` → `RelayQuotaView`
  (`session_id`, `root_session_id`, both numbers, `pending_lineage?`, `updated_at`, `updated_by?`); values 0–99;
  `client.kind` must be `app`. The daemon broadcasts the event **before** it writes the HTTP answer.
- Host event `team.relay_quota` `{op:"changed", root_session_id, self_left, member_pool_left}`.
- Capability `team.relay_quota.v1`.

**Daemon prerequisites (asked of purdex-1f / η, 2026-10-09; RQ-A starts when they are on main):**
- **D1** `quotas` tells failure from empty: no `omitempty`; a read failure is `null`, a successful read with no
  session is `[]`.
- **D2** an order for numbers: every carrier of a pair — the `team.relay_quota` event, each `SessionQuota`, the PUT
  answer, and the roster / team member `relay_quota` — carries the root row's `rev` (+1 on every write; a root with
  no row is 0).
- **D3** `held: Approval[]` (RQ-1b) is its own field, not paged with `approved`: failure `null`, none `[]`. Until
  RQ-1b it is absent.

All three were accepted by purdex-1f on 2026-10-09 (η's RQ-1a2 for D1/D2, RQ-1b for D3).

## Rulings for the App (lead, 2026-10-09; the user may adjust after the screenshot)

- **Where:** the existing unattended panel (`UnattendedPanel.tsx`, the ▾ beside the moon button). It is reachable
  while unattended is off, so quotas are set before leaving.
- **Layout, top to bottom:** (1) 「接力額度」 — one row per session that can hold a quota, grouped by host when more
  than one host is shown; (2) 「額度用完，等你核准」 — the `held` rows, only when the daemon sends `held` and it is not
  empty; (3) the existing 「無人值守期間自動通過的申請」 list, unchanged.
- **Which sessions:** every `quotas` row whose `session_id` is **not a member** in any loaded team roster (all
  hosts: a member of a lead on another host is still a member). While the roster of the row's **own** host has not
  arrived yet, that host shows one muted line 「讀取 team 狀態中…」 instead of rows (unknown is not "not a member").
  A member whose lead's host roster is unreachable may show a stepper: harmless, the daemon never reads a member's
  own quota (spec §3.3), and the row disappears when that roster arrives. Leads first, then the rest; each group by
  title, then address.
- **A row:** the session's title (else the name part of its address) on the left; on the right a stepper
  「自動接力 − N +」; a lead row has a second stepper 「member − N +」 (its member pool). Steppers clamp at 0 and 99
  (the button at the limit is disabled).
- **One line of explanation** under the section title: 「無人值守時，額度 ≥ 1 才會自動接力，每次扣 1；0 就等你核准。整條接力鏈共用。」
- **The numbers belong to the chain**, so the App keys them by `(host, root_session_id)`: every row of the same root
  shows the same numbers, and a click on any of them writes the same thing.
- **Confirmed numbers:** per `(host, root)` the last numbers the daemon confirmed, with their `rev` (D2). A GET
  row, an event or a PUT answer replaces them **only when its `rev` is not smaller** than the one held. Events
  that arrive while the host's GET is in flight are kept and applied after it, by the same rule.
- **Writing, per `(host, root, field)`:** a click sets the field's *desired* value at once; the stepper shows the
  desired value while that field has a write pending or in flight, the confirmed value otherwise — so an answer or
  an event about the other field (both carry the pair) never changes what this stepper shows. Clicks coalesce
  (300 ms, the last value wins); at most one PUT in flight per `(host, root, field)`; when it settles and the desired
  value differs from what was sent, the new value is sent. The PUT body carries only that field and the app client
  (`client-label.ts`).
- **Failure:** the field's desired value is dropped (the stepper falls back to the confirmed value) and a toast says
  「<host>：<session> 的額度沒有存成功（<code>）」.
- **`pending_lineage: true`:** the value went to a provisional root that will be orphaned (spec §3.1). Drop the desired
  value, toast 「<session> 正在接力，接力完成後再設定一次」, and re-GET that host's view 5 s later (the rows then carry
  the real root).
- **Hosts without `team.relay_quota.v1`:** no quota section for them (their other sections as today). `quotas: null`
  (D1) or a malformed row: one muted line 「<host>：讀不到額度」 instead of rows (the whole array is rejected, as the
  existing unattended parser rejects a malformed page — no silent partial list). `quotas: []`: 「沒有可設定的 session」.
- **Held rows (display only in RQ-A):** `<host>：<session> · 接力申請 · <time>` in the approved list's row style. Open
  requests already raise the approval dialog by themselves; opening a *specific* one from the panel needs a
  "show this request" action the approval store does not have — a follow-up, not this PR.
- **Team panel (TI-4)** shows 「自動接力 N · member N」 on the lead row; that parsing and display belong to TI-4.

## Tasks (one PR; TDD; one commit per task)

1. **Types + parsing.** `spa/src/lib/team/types.ts`: `RelayQuota`, `SessionQuota`, `RelayQuotaView`, `RelayQuotaEvent` (each
   with `rev`); `UnattendedView.quotas?: SessionQuota[] | null` and `held?`. The quotas
   array is accepted whole or rejected whole (`quotasFailed`); `held` rows go through the existing approval parser.
   Tests: wire shapes (from the daemon's `wire_quota_test.go` once D1/D2 land), `null` vs `[]` vs absent (an older
   daemon: no section), a malformed row rejects the array, values outside 0–99 rejected.
2. **Capability.** Probe `team.relay_quota.v1` alongside `relay.unattended.v1` (`unattended-support.ts`), stored per
   host in `useUnattendedStore` (`quotaSupport: 'unknown' | 'yes' | 'no'`). Tests: listed / not listed / probe failure.
3. **Quota state.** `spa/src/lib/team/relay-quota.ts` (a small zustand store, not persisted): per `(host, root)` the
   confirmed pair + `rev`; per `(host, root, field)` desired value, in-flight value, coalescing timer. Pure
   reducers for: apply GET rows (with the buffered events), apply event, apply PUT answer — all by the
   not-smaller `rev` rule. Tests: event → answer and answer → event in both orders end at the newest numbers; another window's
   newer event while ours is in flight is kept; an older GET after an event does not roll back; events during a GET
   are applied after it; an answer for one field leaves the other field's desired value on screen; two rows of one
   root show the same numbers.
4. **Writer.** `setQuota(hostId, sessionId, root, field, value)`: desired value, 300 ms coalescing, one in flight
   per `(host, root, field)`, resend when the desired value moved, failure and `pending_lineage` handling with their
   toasts and the delayed re-GET. Tests (fake timers + fake fetch): 5 clicks → 1 PUT of the last value; a click during
   the in-flight PUT is sent after it; clicks on two rows of the same root share one queue; failure falls back to the
   confirmed value; `pending_lineage` falls back, toasts and re-GETs after 5 s; clamp 0 / 99; the body carries only the
   changed field and the app client.
5. **Event wiring.** `useMultiHostEventWs.ts`: a `team.relay_quota` branch bound to its host like `team.unattended`
   (dropped after the host is removed). Tests: frame applied; frame for a removed host dropped.
6. **Panel.** `UnattendedQuotaSection.tsx` and `UnattendedHeldSection.tsx`, mounted in `UnattendedPanel.tsx` (which
   stays as it is otherwise, under ~300 lines). i18n zh-TW / en. Tests: members excluded (same host and another
   host's roster); 「讀取 team 狀態中…」 while the own host's roster is absent; leads first; the lead row has two
   steppers; limits disable buttons; no section without the capability; 「讀不到額度」 for `null` / malformed;
   「沒有可設定的 session」 for `[]`; held section only when `held` is non-empty; the approved list unchanged.
7. **Screenshot gate.** The real App against mlab (or the worktree dev server with a real daemon): the panel with a
   lead (two steppers), a plain session, and the approved list; screenshots to the lead; merge after the lead approves
   them. (The held section cannot be shown before RQ-1b is deployed; tests cover it.)

Not in scope: the team panel (TI-4), opening a specific held request (follow-up), iOS (a separate decision whether the
phone can set quotas), the host switch.

Tab-hosted state checklist: not applicable (the panel is a floating panel, not tab-hosted).

## Plan review fold-in (codex `task-mv0l9jyt-9k22dj`, 2026-10-09)

| # | Finding | Fold-in |
|---|---------|---------|
| 1 | critical: `quotas` absent means failure or empty | D1 (daemon: `null` vs `[]`); three displays |
| 2 | critical: answer / event / newer intent ordering | D2 `rev`; not-smaller rule; desired vs confirmed (Task 3) |
| 3 | critical: a pair answer clobbers the other stepper | Per-field desired value; answers only touch confirmed |
| 4 | critical: one queue per stepper, but the quota is per chain | Keyed by `(host, root, field)` |
| 5 | important: `pending_lineage` after the event | Drop desired, toast, re-GET in 5 s |
| 6 | important: GET vs event race | Events during a GET are buffered, then the not-smaller `rev` rule |
| 7 | important: roster not loaded fails open | 「讀取 team 狀態中…」 until the own host's roster arrives |
| 8 | important: cross-host members | Membership from every loaded roster by `session_id` |
| 9 | important: no hook to open a specific request | Held rows display-only; follow-up |
| 10 | important: `held` contract not on main | D3; parsed by the existing approval parser; tests from the spec shape |
| 11 | important: dropping bad rows hides failure | Reject the whole array → 「讀不到額度」 |
| 12 | minor: roster / member quota types | Left to TI-4 |
