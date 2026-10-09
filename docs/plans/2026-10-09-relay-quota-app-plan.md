# Relay quota — App side (RQ-A) — plan

Spec: `docs/specs/2026-10-09-relay-quota-spec-plan.md` (#2062; rules R1–R5 confirmed by the user, do not reopen).
Owner: interface line. The coordinator turns the host switch `relay_quota` on only after this is merged and the main
checkout is fast-forwarded (spec §3.6), so this PR is what makes the rule safe to switch on.

Daemon facts (alpha.634, `internal/team/wire_quota.go`, `wire_unattended.go`):
- `GET /api/team/unattended` → `UnattendedView.quotas: SessionQuota[]` (`session_id`, `root_session_id`, `title?`,
  `address`, `is_lead`, `self_left`, `member_pool_left`) for every live session of the host; **absent** when the
  quotas could not be read (or there are none). `held: Approval[]` arrives with RQ-1b; until then it is absent.
- `PUT /api/team/relay-quota` `{session_id, self_left?, member_pool_left?, client}` → `RelayQuotaView`
  (`session_id`, `root_session_id`, the numbers, `pending_lineage?`, `updated_at`, `updated_by?`); values 0–99;
  `client.kind` must be `app`.
- Host event `team.relay_quota` `{op:"changed", root_session_id, self_left, member_pool_left}`.
- Capability `team.relay_quota.v1`.

## Rulings for the App (lead, 2026-10-09; the user may adjust after the screenshot)

- **Where:** the existing unattended panel (`UnattendedPanel.tsx`, the ▾ beside the moon button). It is reachable
  while unattended is off, so quotas are set before leaving.
- **Layout, top to bottom:** (1) 「接力額度」 — one row per session that can hold a quota, grouped by host when more
  than one host is shown; (2) 「額度用完，等你核准」 — the `held` rows, only when the daemon sends `held` and it is not
  empty; (3) the existing 「無人值守期間自動通過的申請」 list, unchanged.
- **Which sessions:** every `quotas` row whose session is **not a member** of a team on that host (R4: a member has no
  quota of its own; membership from `useTeamRosterStore`). Leads first, then the rest; each group by title, then
  address.
- **A row:** the session's title (else the name part of its address) on the left; on the right a stepper
  「自動接力 − N +」; a lead row has a second stepper 「member − N +」 (its member pool). Steppers clamp at 0 and 99
  (the button at the limit is disabled).
- **One line of explanation** under the section title: 「無人值守時，額度 ≥ 1 才會自動接力，每次扣 1；0 就等你核准。整條接力鏈共用。」
- **Writing:** a click sets the new absolute value at once (optimistic) and PUTs it with `client` from
  `client-label.ts`; clicks on one stepper are coalesced (300 ms, the last value wins, one PUT in flight per stepper;
  a value changed while a PUT is in flight is sent after it). On failure: the row goes back to the last value the
  daemon confirmed, and a toast says 「<host>：<session> 的額度沒有存成功（<code>）」. On `pending_lineage: true`: the
  row goes back too, toast 「<session> 正在接力，接力完成後再設定一次」 (spec §3.1: a value written then is orphaned).
- **Live:** `team.relay_quota` events update every shown row whose `root_session_id` matches (the numbers belong to
  the chain); an event for a root no row shows is ignored. A confirmed PUT answer updates the row the same way.
- **Hosts without `team.relay_quota.v1`:** no quota section for them (their other sections as today). Hosts whose
  view came without `quotas`: one muted line 「<host>：讀不到額度」 instead of rows.
- **Held rows:** `<host>：<session> · 接力申請 · <time>` with the same row style as the approved list; clicking
  opens the approval dialog the App already shows for open requests (no new decide path).
- **Team panel (TI-4)**: the lead row shows 「自動接力 N · member N」, member rows show nothing. Not in this PR:
  folded into TI-4 (the team panel does not exist yet); this PR only exposes the numbers already in the roster.

## Tasks (one PR; TDD; one commit per task)

1. **Types + parsing.** `spa/src/lib/team/types.ts`: `RelayQuota`, `SessionQuota`, `RelayQuotaView`,
   `RelayQuotaEvent`; `UnattendedView.quotas?` and `held?`; roster / team member `relay_quota?` (optional: older
   daemons). Parsers reject malformed rows (numbers outside 0–99, missing ids) without dropping the whole view. Tests:
   shapes from `wire_quota_test.go`, absent fields, malformed rows.
2. **Capability.** Probe `team.relay_quota.v1` alongside `relay.unattended.v1` (`unattended-support.ts`), stored per
   host in `useUnattendedStore` (`quotaSupport: 'unknown' | 'yes' | 'no'`). Tests: listed / not listed / probe failure.
3. **Quota state + writer.** `spa/src/lib/team/relay-quota.ts`: per host, rows keyed by session id with the last
   confirmed numbers and the shown (optimistic) numbers; `setQuota(hostId, sessionId, field, value)` with the
   coalescing, one-in-flight rule, rollback and toasts above; `applyQuotaEvent(hostId, ev)` by root. Tests (fake
   timers + fake fetch): coalescing (5 clicks → 1 PUT of the last value); a click during an in-flight PUT is sent after
   it; failure rolls back to the confirmed value; `pending_lineage` rolls back with its toast; an event updates every
   row of the same root and ignores unknown roots; clamp 0 / 99; the PUT body carries only the changed field and the
   app client.
4. **Event wiring.** `useMultiHostEventWs.ts`: a `team.relay_quota` branch bound to its host like `team.unattended`
   (dropped after the host is removed). Tests: frame applied; frame for a removed host dropped.
5. **Panel.** `UnattendedPanel.tsx`: the quota section and the held section per the rulings (rows from the view
   fetched at open; membership from the roster store; steppers; per-host failure line); the existing list untouched.
   Keep `UnattendedPanel.tsx` under ~300 lines: the section components live in `UnattendedQuotaSection.tsx` /
   `UnattendedHeldSection.tsx`. i18n zh-TW / en. Tests: members excluded; leads first; lead row has two steppers;
   limits disable buttons; no section for a host without the capability; 「讀不到額度」 when `quotas` is absent; held
   section only when `held` is non-empty; clicking a held row opens its dialog; the approved list unchanged.
6. **Screenshot gate.** The real App against mlab (or the worktree dev server with a real daemon): the panel with a
   lead (two steppers), a plain session, and the approved list; send the screenshots to the lead; merge after the lead
   approves them. (The held section cannot be shown until RQ-1b is deployed; a test covers it.)

Not in scope: the team panel (TI-4), iOS (a separate decision whether the phone can set quotas), the host switch.

Tab-hosted state checklist: not applicable (the panel is a floating panel, not tab-hosted).
