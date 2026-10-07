# Lead: adopt, release and team display (spec)

Date: 2026-10-08. Coordinator: purdex-d3. Status: user decisions final (§1); plan and implementation pending.
Extends: `docs/specs/2026-10-06-lead-team-relay-spec.md` (U8, U9, U11, U13, §7) and `docs/specs/2026-10-08-unattended-mode-spec.md` (U23). When the relay spec's U-table is next edited, fold §1 in as **U24** and link here. It moves "adopting an existing session" out of that spec's §11 ("Later") and replaces U11's "另外獨立處理" for the member display.

## 1. User decisions (2026-10-08, do not reopen)

| # | Decision |
|---|---|
| U24 | lead 再補幾個功能：<br>- 一個正在跑的 session，經使用者要求可以**把自己升級成 lead**（既有 `pdx lead request`，照舊一鍵核准；U23 開著時自動通過）。<br>- lead 可以把**指定的、已經在跑的 session 納進來當 member**（adopt）。**要使用者按一下核准**，和「成為 lead」同一套；無人值守模式（U23）開著時自動通過。<br>- lead 可以**釋出**或**關閉** member：釋出＝離開 team、session 繼續跑、恢復成一般 session（可以自己接力）；關閉＝session 結束。使用者直接要求時 lead 照做；**lead 自己判斷某個 member 不需要時，一定先用 ask 問使用者「釋出／關閉／保留」**，使用者選了才做。<br>- lead／member 的介面顯示：**左側選單縮排**（member 縮在 lead 底下）＋**tab 區群組化**（參考 Chrome 分頁群組）。群組標籤＝lead 名稱、點一下可收合，**顏色先用自動調色盤**（和主機色分開）；太花俏再收斂，可能改用 lead 的主機色。<br>- 排在 lead/team 線的 P9 之後。 |

## 2. Derived rules

**D-U24-1 · Upgrade to lead is the existing path.** No new route. The skill says: when the user asks the session to become a lead, run `pdx lead request` (exit codes and refusals unchanged: `already_lead`, `member_cannot_lead`, `request_open`). The only change is the skill text.

**D-U24-2 · Adopt is a new approval kind, `adopt`.** `pdx adopt <ref>` (lead only) opens an `approval_requests` row of kind `adopt` with `{lead_session_id, target_ref, target_session_id, title}`, decided like `lead` (one click on any App, U5b; same CAS, `approval.request` event, audit row). U23's unattended switch covers it: on → approved at once by the daemon with decider `unattended` (U23 D-U23-1 applies unchanged; D-U23-3 includes open `adopt` rows).
- **Refused before a request opens (409, exit 13):** `not_lead`; `team_full` (adopted members count against `max_members`, §7); `adopt_self` (the target is the lead); `adopt_target_is_lead` (a lead cannot become a member, mirroring `member_cannot_lead`); `adopt_already_member` (the target is a member of any team, this one included); `adopt_target_not_found` (the ref does not resolve to a live session on this host).
- **v1 is same-host only:** the target must be on the lead's host. A ref on another host → `remote_unsupported` (cross-host adoption stays in §11 of the relay spec; spawn/kill/relay across hosts are unaffected).
- **On approve:** in one transaction the daemon re-checks every refusal above (state may have changed while waiting) and inserts the `team_members` row (origin `adopted`, no spawn op, no worktree, no `--model`; the member keeps its own model/effort and `pdx team` shows the actual values as for spawned members). If a re-check fails, the request closes as `cancelled` with that code.
- **The adopted session is told.** After the row commits the daemon sends the target a peer message: it is now a member of `<lead address>`, its self relay is off and its relay is the lead's (U13), and how to reach the lead. The skill's member section covers the rest.
- **Deny / expiry:** nothing changes for the target; the lead gets the decision like a lead request.

**D-U24-3 · Release.** `pdx release <ref>` (only the member's own lead; else `not_your_member`) ends the membership without touching the session: `team_members.state=released`, `ended_at` set. The session becomes `none` again: `relayRole` returns `none`, self relay follows the host switch and its own pause (U13). The daemon tells the released session by peer message. No approval (the lead acts on the user's request or after D-U24-4's ask). An open member relay op on that session blocks release with `relay_open` (finish or cancel the relay first).

**D-U24-4 · The lead asks before closing on its own judgement.** When the lead itself decides a member is no longer needed, it must call AskUserQuestion with exactly three options — 釋出 / 關閉 / 保留 — naming the member, and act on the answer. This rides U19's 分流, so the question reaches every client. It is enforced by the skill (the daemon cannot tell who asked for a `release`/`kill`); a direct user request to release or close skips the question. `pdx kill` itself is unchanged (§7).

**D-U24-5 · Sidebar indentation.** Wherever the left sidebar lists sessions/workers, a member row is placed directly under its lead's row and indented one level; the lead row gets a collapse control for its members. Members whose lead is not in the list (another host not shown, or the lead ended) are listed normally with a small "member of <lead name>" hint. Team data comes from the team module (`GET /api/team`), cached per host like other host data.

**D-U24-6 · Tab groups (Chrome-style).**
- A group exists for every team that has at least one open tab (the lead's or a member's). Its chip sits before the group's first tab and shows the lead's name (title, else address name); the group's tabs carry a coloured underline in the group colour.
- **Colour:** assigned from a fixed palette of 8, per team id, stable across restarts (hash of team id), deliberately different from the host-colour palette. A later setting may switch to the lead's host colour (U24 "可能改用主機色").
- **Membership is derived, not manual:** tabs showing the lead or a member join the group automatically and are kept contiguous (the lead's tab first, then members in join order). Dragging a grouped tab reorders it within the group; dropping it outside snaps it back. Tabs that are not part of a team are unaffected.
- **Collapse:** clicking the chip collapses the group to the chip alone (Chrome behaviour); the active tab is never hidden — activating a collapsed group's tab expands it.
- Existing tab indicators (host badge, status dot, awaiting-approval hand, unread dot) stay on each tab unchanged.
- **Setting:** Settings → 介面 gets 「分頁群組顯示 team」 (default on) so the user can compare with it off; it only changes the tab bar, not the sidebar.
- Released/ended members leave the group at once; a team with no open tabs has no group.

## 3. Not in scope
- Cross-host adoption (§11 of the relay spec).
- Manual tab groups unrelated to teams.
- Changing `pdx kill` semantics.

## 4. Phases (each ≤ 800 lines / ≤ 20 files), after P9 and U23
- **PL-1 daemon + CLI:** `adopt` kind, `pdx adopt`, approve transaction with re-checks, peer notice, `pdx release` + `released` state, `relayRole` honouring `released`, unattended coverage, skill text for D-U24-1/2/3/4. Tests: every refusal code before and at approve; unattended auto-approve; adopted member's hello says `self_relay=off`; released session's hello follows the host switch; `relay_open` blocks release.
- **PL-2 SPA:** `adopt` approval card (reuse the lead dialog host), sidebar indentation (D-U24-5), team data store.
- **PL-3 SPA:** tab groups (D-U24-6) with the setting. Tab-hosted state follows the CLAUDE.md checklist.

Owner: the lead/team/relay line (purdex-f0). Review: plan with this spec through codex once; PRs R1 + attacker + critic.
