# Relay, unattended mode and team rules v2 (plan)

Spec: `docs/specs/2026-10-13-relay-team-v2-spec.md` (§ numbers below are the spec's). Coordinator: purdex-1f.
Current behaviour: `review-relay-rules.md` ("RR §n") and `review-relay-ui.md` ("RU §n", G-n) at 10116256b. Code read for this plan: main d611b312b / origin/main 246cb8000 (the relay and team code is unchanged since 10116256b; only `internal/resources` moved). `H` = `cmd/pdx/plugin/purdex/hooks/register.js`, `T` = `internal/module/team/`, `W` = `internal/team/`.
Plan review: `relay-team-v2-codex-plan-review.md` (codex `task-mv6iksi3-q67nf0`, 24 findings), folded in as rev 2; rev 3 applies 1f's decisions of 2026-10-13 (§0). "C#n" below = codex finding n.

## 0. 已決（1f, 2026-10-13）

The spec's §3–§4 now carry the decisions marked "spec"; the rest are plan rules. Evidence for each is in the backup of the earlier §0 (`backups/relay-team-v2-plan.pre-decisions.md`) and in the codex review (C#n).

- **S-1** Person token = a misuse guard, not a security boundary (an agent can type into its own pane and holds the App's bearer); spec §4.1 says so; App/agent permission separation is **#2774** (1f opens it).
- **S-2** A card is final at `used ≥ 90` (`team.RelayLastAskPct`), whatever `PDX_RELAY_THRESHOLD` says; the mod asks nothing more once `lastAskPct ≥ 90`.
- **S-3** `session_pauses` CHECK rebuilt for `relay_unanswered`; switch-off and reconcile resumes take only `quota` / `lead` rows.
- **S-4** (spec §4.5) In unattended mode the daemon accepts a member's ask against the lead's pool; pool 0 → the member pauses (`quota`); attended, the lead decides.
- **S-5** `spawn_resources` card life 480 s (spec §4.6).
- **S-6** (spec §4.2) A lead paused for `relay_unanswered` stops alone; members keep working, get the paused auto-reply from it, and their own asks time out on the ladder (they may pause themselves at 90 %).
- **S-7** Person-granted lead: `max_members` 3, roots [cwd]; the request's `--max-members` / `--root` ignored.
- **S-8** Remote spawns and a remote member's final ask stay as today; `--deny` of a remote member → 409 `relay_unsupported`; follow-up issue.
- **S-9** `half` is stored as `cancelled` / reason `half`; `relay_ops.mode` = '' / `half` / `handoff`.
- **S-10** Freshness: the mod reports a half stale at the next main turn; the daemon also drops it at a later claim, a compaction or the session's end.
- **S-11** (spec §4.2) After an explicit denial at 90 % the idle ask still comes as usual.
- **S-12** A lead's `--deny` closes the ask `withdrawn` / `denied` (no CHECK rebuild).
- **S-13** (spec §4.2) The final card has no deadline and no lease expiry; it ends by an answer, the session's end or a compaction; a reloaded mod re-adopts it from hello.
- **S-14** Terminal: status 「已寫好接力檔」 + one toast.
- **S-15** (spec §3) #2765 merged as d3a6e69f: the spawn window stays 20 s; `resources.HostReader` exists on main, so **RT-5a is unblocked** (it waits only for lease v2 P1b's zone).
- **S-16** (spec §4.1) Token lifetimes: 60 s for `relay` / `adopt`, 10 min for `lead` / `spawn`; one use.
- **S-17** (spec §4.1) A member typing `/team` gets 「要先被釋出才能成為 lead」; nothing else happens.
- **S-18** (spec §4.2) A compaction ends an unanswered card, ends the pause, and restarts the ladder.
- **S-19** (spec §4.1) `/team` and `/team spawn` during a relay wait for idle; `adopt` / `release` / `kill` / show act at once.
- **S-20** (spec §4.2) Final = `self_relay`, ≥ 90 %, not idle, not an older mod's manual card; reask cards are final.
- **S-21** (spec §4.8) Fresh handoff for every session: `GET /api/relay/fresh` + host event `relay.fresh`; the roster for team rows.

待決: none.

## Conventions (every phase)

- One branch and one PR per phase: `feat/relay-v2-<phase>` (`rt1a`, `rt1b`, `rt1c`, `rt2a1`, `rt2a2`, `rt2b`, `rt2c`, `rt3a1`, `rt3a2`, `rt3b`, `rt3c`, `rt4`, `rt5a`, `rt5b`, `rt6`). Each starts from the latest `origin/main`, after the previous phase has merged. Work in a worktree.
- **Size budget** (spec §5: ≤ 800 lines / 20 files per PR). Each phase below states its expected production files. Before asking for review, `git diff --stat origin/main...HEAD`; a phase over budget splits at the task boundary marked "split point" and the second half becomes its own PR (C#24).
- TDD: write the test, see it fail for the right reason, implement, see it pass. One commit per task.
- **Mutation proof is a deliverable.** For each rule test named "(M)", revert or flip the rule line, show the test turns red, and restore it. Put the list in the PR body.
- Tests:
  - `go test -race -count=1 ./internal/team/... ./internal/module/team/...` for every phase. Add `./cmd/pdx/...` whenever `cmd/pdx` or the mod changes (it includes the plugin package; memory `feedback_mod_change_run_cmd_pdx_all`), `./internal/module/peers/... ./internal/peers/...` in RT-2a2, `./internal/push/...` in RT-3a2 and RT-5b, `./internal/core/...` when a capability is added, `./internal/resources/... ./internal/module/resources/...` in RT-5a.
  - Mod tests: `claude plugin test cmd/pdx/plugin/purdex`.
  - `make lint`.
  - Do not run the full `go test ./...`; the lead runs it before release. Run only the packages above (the host is often loaded).
- `team.db` is live on two hosts: every schema change is a migration with a test that opens an old-schema DB with rows in it. Column adds are `ALTER TABLE … ADD COLUMN` in the style of `migratePauseRefTitle` (`T/pause_store.go:62`); a CHECK change is a rebuild (create new, copy, drop, rename, in one transaction, guarded by reading `sqlite_master.sql`).
- **Mod protocol.** `VERSION` (H:59) is bumped by the phases that change the mod: RT-1c → 6, RT-2c → 7, RT-3b → 8, RT-4 → 9. A daemon gate on the version is needed where an older mod would do the wrong thing with the new answer (RT-3a1 half and the switch-on idle split, RT-4 notices); those tests are (M).
- Deploying a phase that bumps `VERSION` needs `pdx setup`: the lead's gate `relays_active = 0` before the restart and before `pdx setup` (memory `feedback_deploy_gate_before_pdx_setup`).
- Drafts and measurement output go in the worktree's untracked `.scratch/` (never `/private/tmp`).
- PR → codex R1 + R2 (attacker → critic), `--model gpt-5.6-sol`. Re-review only the increment. Then `pdx report ready` and **wait for the lead's go-ahead** before merging (merge commit, no `--delete-branch`), then `pdx report merged --pr --sha`.
- The spec and this plan enter the repo in RT-1a's PR as `docs/specs/2026-10-13-relay-team-v2-spec.md` and `docs/plans/2026-10-13-relay-team-v2-plan.md`. Copy them from `~/.local/share/purdex-lead/relay-team-v2-{spec,plan}.md`, change the spec's "Current behaviour" line to say the reviews live outside the repo, and apply the spec fixes the lead accepted from §0.
- New user-facing texts are Chinese and exactly as written here; a test pins each one.

## RT-1a — person tokens and the direct `/relay` (daemon, CLI)

Expected files: `W/wire_person.go`, `W/wire_relay.go`, `T/person.go`, `T/module.go`, `T/relay_begin.go`, `T/store_unattended.go`, `T/pause_resume_store.go`, `cmd/pdx/relay.go` + tests (~9 production files).

**T1 Person tokens** — `W/wire_person.go` (new), `T/person.go` (new), `T/module.go` (route), `cmd/pdx/relay.go`.
- Wire: `PersonRoute = "/api/relay/person"`; `PersonRequest{SessionID, Command}` with `Command` one of `relay|adopt|lead|spawn`; answer `PersonResponse{Token, ExpiresAt}`; TTL `relay`, `adopt` 60 s and `lead`, `spawn` 10 min (S-16); `ErrPersonTokenInvalid = "person_token_invalid"` (409, exit 13); `team.PersonClient()` = `Client{Kind: "person", Label: "使用者（終端機指令）"}`.
- Module: an in-memory table `{token → session, command, expires, used}` under its own mutex; at most 4 live per session (the oldest dropped); a restart drops them (the person types again). The session must be a live CC session of this host (`ResolveOriginBySession`; 404 otherwise). The token is 16 random bytes, hex; never logged.
- `consumePerson(session, command, token) bool`: constant-time compare, unexpired, unused, same session and command; marks it used; one winner under concurrency.
- CLI: `pdx relay person --session <sid> --command <c>` prints the JSON; exit 0 / 13 / 20.
- Tests: consume once → true, twice → false (M); wrong session, wrong command, expired at each TTL edge, unknown → false; concurrent consumers: one true (`-race`); the fifth mint drops the oldest; the daemon log never contains a token.

**T2 Direct `/relay`** — `W/wire_relay.go`, `T/relay_begin.go`, `T/store_unattended.go`, `T/pause_resume_store.go`.
- `RelayBeginRequest.PersonToken string json:"person_token,omitempty"`; `SelfRelayPayload.Person bool` (the row's, set by the daemon).
- In `handleRelayBegin`, after the replay check: `Manual && PersonToken != ""` → `consumePerson(session, "relay", token)`; false → 409 `person_token_invalid`, nothing opened. True → **person begin**:
  - the switch refusals (`off` / `paused`, relay_begin.go:108-114) do not apply; a member passes (`manualMemberAllowed` still refuses one being killed); the unattended branch (:231) is not taken;
  - **an open self-relay request of the session** (awaiting approval: a threshold, idle, reask or unanswered card) is approved by the person — the row closes approved with `Close{DecidedBy: PersonClient(), Auto: false}`, its op is claimed, the answer is 201 with that op (C#3). An op already past `awaiting_approval` answers 409 `relay_open` as today (a relay is running);
  - otherwise the op and its row are written approved at once through `CreateSelfRelayApproved` (`T/store_unattended.go:28`) with the same close: no quota read or spent (`closeSelfRelayApprovedIn` spends only when `c.Auto`, `T/team_store.go:300-305`), the member check passes (manual payload, close not `Auto`, `team_store.go:294-297`); announced closed only, as `beginApproved` does.
- A session holding pause rows: the person relay releases them as a release by a person **without** a reask (`ResumeScope{Release: sid, NoReask: true}`): the relay is the answer.
- `Manual` **without** a token behaves as today (an older mod: a card).
- Replay with the same `request_id` returns the op even though the token is spent (`sameBeginPayload` compares `Person`, not the token).
- Tests:
  - person begin → op claimed, row approved by `person`, no `opened` broadcast (M: route it to the card path and the test fails);
  - with an open threshold card → that card approved by `person`, no second op (M);
  - unattended on, rule on, `self_left = 0` → approved, nothing spent, no pause (M);
  - host switch off and `/relay off` → approved (M);
  - a member → approved; the lead's manual notice as today (`RelayManualNoticeFmt`); a member being killed → 409 `relay_unsupported`;
  - a paused session → approved, its pause rows gone, no reask outbox row (M);
  - bad token → 409, nothing written; manual without a token → a card; replay after the token is spent → the same op.

**T3 CLI flag** — `pdx relay begin … --manual --person <token>`. Test: exit 13 with `person_token_invalid` as the last stderr word.

## RT-1b — unattended refusals; person-originated lead, adopt and spawn (daemon, CLI)

Expected files: `W/wire_unattended.go`, `W/wire.go`, `W/wire_team.go`, `T/unattended.go`, `T/unattended_handler.go`, `T/handler_create.go`, `T/adopt_handler.go`, `T/spawn_handler.go`, `cmd/pdx/lead.go`, `cmd/pdx/adopt_cmd.go`, `cmd/pdx/spawn_cmd.go`, `internal/core/info_handler.go` + tests (~12). Split point: after T6 (T7–T8 become RT-1b2).

**T4 Lead and adopt are no longer auto-approvable** — `W/wire_unattended.go:36-38`, `T/unattended.go`, `T/unattended_handler.go`.
- `AutoApprovable` = `self_relay`, `member_relay` (M: put `lead` back and the switch-on test fails).
- `ErrUnattendedRefused = "unattended_refused"`; `UnattendedRefusedText = "無人值守模式不開放申請，請等待模式解除後，依情況重新申請"`.
- `sweepUnattended` (unattended.go:362) at switch-on (unattended_handler.go:112), at boot and on every tick while on (`reconcileUnattended`, :396): every open `lead` and `adopt` row closes `cancelled` / `close_reason = unattended_refused` through `closeAs`.
- `UnattendedView.Refused int json:"refused,omitempty"` (PUT only).
- Tests: switch-on with an open lead and an open adopt row → both refused, `refused: 2`, no team, no member (M); a lead row committed after the switch-on → refused by the next tick; self_relay rows at switch-on as today.

**T5 Lead request** — `T/handler_create.go`, `W/wire.go` (`CreateApprovalRequest.PersonToken`), `cmd/pdx/lead.go`.
- The id-replay check runs first. For a new id with a token: `consumePerson(origin, "lead", token)`; false → 409 `person_token_invalid`. True → **person lead**, whatever the switch:
  - an open lead row of the same origin (the agent's earlier request, which `OpenByOrigin` refuses today, :208-214) is **approved by the person** with the person grant and answered (C#3);
  - otherwise the row is created approved in one transaction (as `createApprovedLead`, unattended.go:173) with `Close{DecidedBy: PersonClient()}` and the grant `{MaxMembers: 3, Roots: [origin.Cwd] canonical, TeamName/TeamLabel: the request's}` (S-7).
  - A member still gets 409 `member_cannot_lead` (S-17).
- No token and the switch on → 409 `unattended_refused`, nothing written (M). No token, switch off → a card, as today.
- CLI: `pdx lead request … --person <token>`. A row closed `cancelled` / `unattended_refused` (switch-on while it waited) → the text, exit 13 (today exit 12, lead.go:338-340); a 409 `unattended_refused` → the text, exit 13.
- Tests: person lead with the switch on → team at once, grant 3 / [cwd] (M); person lead with the agent's card open → that card approved, one team (M); no token, switch on → 409 (M); replay with a spent token → the stored row; CLI exits.

**T6 Adopt** — `T/adopt_handler.go` (`handleCreateAdopt`, :278), `cmd/pdx/adopt_cmd.go`.
- Token valid → approved at create through `createApprovedAdopt` (:346) with the person decider, whatever the switch; an open adopt row for the same target (`OpenAdoptForTargetOn`, :252) from this lead is approved by the person instead (C#3); from another lead → 409 `request_open` as today. No token and the switch on → 409 `unattended_refused`; no token, off → a card.
- CLI: `pdx adopt <ref> --person <token>`; `cancelled` + `close_reason` → exit 13 with the text (adopt_cmd.go:231-235 already maps it).
- Tests: the cases above (M on the switch-on refusal and on the open-row approval).

**T7 Spawn carries the token** (split point) — `W/wire_team.go` (`SpawnRequest.PersonToken`), `T/spawn_handler.go`, `cmd/pdx/spawn_cmd.go`.
- Only a **new** spawn id consumes it (`acceptSpawn`'s replay path, :146-152, runs first: the CLI re-posts the same body while the op runs). Invalid → 409 `person_token_invalid`. The result (`person bool`) is kept on the op row for RT-5 (`ALTER TABLE spawn_ops ADD COLUMN person INTEGER NOT NULL DEFAULT 0`) and changes nothing else here.
- `spawnHash` leaves the token out.
- Tests: new id + bad token → 409, no op (M); a re-post of an accepted id with the spent token joins it; migration of `spawn_ops`.

**T8 Capability** `team.person.v1` (`internal/core/info_handler.go:49-66`).

## RT-1c — `/team`, `/lead` alias and direct `/relay` in the mod; skill text

Expected files: `hooks/register.js`, `hooks/member.js`, `hooks/relay.test.ts`, `hooks/member.test.ts`, `skills/pdx-team/SKILL.md` (5).

**T9 Pure helpers** — `member.js`: `parseTeamArgs(args)` → `{kind: 'show'|'lead'|'spawn'|'adopt'|'release'|'kill', note?, task?, model?, effort?, ref?}` or `{error}`; `--model` / `--effort` only for spawn, values from the CLI's lists. Tests: a table of parses.

**T10 Getting a token** — H: `personToken($, sid, cmd)` runs `pdx relay person --session sid --command cmd` (≤ 8 s) and returns the token or `undefined` (a daemon without the route: exit 21).

**T11 `/relay`** — H `relayNow` (H:914-959).
- State `awaiting` (a card is open: threshold, idle, reask, or unanswered) → a token, then `begin --manual --person`: the daemon approves the open card (T2) and the running wait loop answers `approved`; the reply is 「已核准這張接力申請，開始接力」. States `beginning`, `approved`, `clearing`, `seeding` → 「接力進行中」 as today.
- State `idle` → a token, then `begin --manual --person`: the row is approved at begin, so the existing path needs no new state: `awaiting` → the wait loop's first `pdx relay wait` answers `approved` at once (what an unattended approval at begin does today, `T/unattended.go:198-258`) → `settle` → write → clear → seed.
- No token (no answer, an old daemon) → 「無法確認是使用者下的指令（daemon 沒有回應），沒有接力；請再打一次 /relay」.
- Usage: `/relay：立刻接力（不需核准、不扣額度）；/relay off｜on｜status`.
- Tests: `/relay` in a member, with `/relay off`, with the host switch off, unattended with quota 0 → each goes straight to the write prompt (M: `begin --manual` without `--person` opens a card); `/relay` with an idle card open → approved, the full relay (not withdrawn); no token → the message, state unchanged.

**T12 `/team` and `/lead`** — H (`register`, H:1448-1451; `command.run`, H:1664).
- Register `team` (`argumentHint: '[名稱或說明] | spawn <任務> [--model m] [--effort e] | adopt <ref> | release <ref> | kill <ref>'`); keep `lead` with 「（將移除，請改用 /team）」.
- Role is read now (`pdx team --json`, as `leadCommand` does), not from the hello cache.
- `show` in a lead → `pdx team` (text). `show` / `lead` in a session with no role → become lead: a token for `lead`, then the prompt below (the `/lead` path, H:851-912, with `leadNote`). In a lead, `/team <note>` → 「已經是 lead」 plus the roster. In a member → the S-17 text, nothing else.
- Lead prompt: 「（nonce）使用者剛用 /team 要求你現在成為 lead。請依 pdx-team skill，立刻在前景（Bash timeout: 600000）執行 pdx lead request --person <token> --reason "<原因>" --name "<team 名稱>" --label "<短名>"；這是使用者親手下的指令，會立刻生效，不要先判斷工作夠不夠大。reason、name、label 依目前的工作自己決定（下面的補充若有指定就照它）。如果回 person_token_invalid，告訴使用者再打一次 /team。」 + the quoted note.
- `spawn` (lead only) → a token for `spawn`, then: 「使用者用 /team spawn 要求開一個 member：「<task>」。請依 pdx-team skill 立刻執行 pdx spawn --person <token> …，把上面的任務寫成 brief；model／effort 用 <given>／（沒指定就依任務自己選）。」
- `adopt` → a token, then `pdx adopt <ref> --person <token>` run by the mod; `release` / `kill` → `pdx release|kill <ref>` run by the mod.
- **Relay in flight** (S-19): `adopt`, `release`, `kill` and `show` act at once in any state. `/team` (become lead) and `/team spawn` while the mod is not idle are queued (one at a time, newest wins) and run at the next return to idle (`toIdle`, H:572), with the token minted then; the reply is 「接力進行中，完成後立刻執行」.
- Every daemon call ≤ 8 s.
- Tests: each kind's argv / prompt text; spawn carries model / effort only when given; non-lead `/team spawn` → 「只有 lead 可以 /team spawn」 with no token request; a member's `/team` → the S-17 text (M); `/team` during a relay → queued, then run at idle (M); `/team kill` during a relay → runs at once; `/lead x` = `/team x` plus the deprecation line.

**T13 Skill text** — `skills/pdx-team/SKILL.md`:
- `/team …` and `/relay` are the person's commands and act at once. `--person <token>` only when the mod's prompt gives one; never invent, store or reuse it. **Never type into your own terminal** (`tmux send-keys`, the send-keys route) to run a slash command, and never call the App's routes to approve or to switch modes (S-1: the daemon cannot stop you yet, #2774; the rule is yours to keep).
- In unattended mode your own `pdx lead request` and `pdx adopt` are refused (exit 13 `unattended_refused`); wait until it is off and ask again only if still needed.
- A person's `/relay` in a member relays it at once.
- Test: the Go embed test passes; a small test pins the new rules' key phrases.

`VERSION` → '6'.

## RT-2a1 — the final self-relay card and pause cause `relay_unanswered` (daemon)

Expected files: `W/wire_pause.go`, `W/wire_relay.go`, `W/wire.go`, `T/pause_store.go`, `T/store.go`, `T/team_store.go`, `T/sweeper.go`, `T/pause_unanswered.go`, `T/pause_unanswered_store.go`, `T/relay_handler.go`, `T/migrate.go` + tests (~11).

**T14 Schema and constants**
- `migratePauseCauses`: rebuild `session_pauses` with `CHECK (cause IN ('quota','lead','relay_unanswered'))` when `sqlite_master.sql` lacks it; one transaction; rows copied.
- `ALTER TABLE approval_requests ADD COLUMN unanswered_at INTEGER NOT NULL DEFAULT 0`; `Approval.UnansweredAt int64 json:"unanswered_at,omitempty"`.
- `PauseCauseUnanswered = "relay_unanswered"`; `RelayLastAskPct = 90`; `UnansweredDeadlineMs` = a far sentinel (`253402300799000`, 9999-12-31); texts:
  - `PauseNoticeUnanswered` 「[pdx team] 接力申請沒有回應，先暫停作業：手邊這一步收尾後不要開始新的工作，等使用者確認。」
  - resume: `…Approved` 「[pdx team] 接力已核准，恢復作業。」, `…Denied` 「[pdx team] 使用者決定不接力，恢復作業；之後不會再申請。」, `…Ended` 「[pdx team] 接力申請已結束，恢復作業。」
  - push title `{name} 已暫停：接力申請沒有回應，等你核准`.
  - bounce details (used in RT-2a2): 「我目前暫停作業，等待使用者確認中」 / 「我目前暫停作業，等待 lead 確認中」.
- Tests: an old-schema DB with `quota` and `lead` rows keeps them and accepts a `relay_unanswered` row (M: skip the rebuild and the insert fails); the column add on an old approvals table.

**T15 Final card → pause** — `T/sweeper.go` (the deadline branch, :96, and the lease branch, :98), new `T/pause_unanswered.go`, `T/pause_unanswered_store.go`.
- **Final** (S-2, S-20): kind `self_relay`, payload `used_percentage ≥ 90`, not `reason idle`, not `manual`.
- At its deadline, a final row with `unanswered_at = 0` is not closed. One transaction with a CAS on `(id, state open, unanswered_at 0, deadline_at)`: `unanswered_at = now`, `deadline_at = UnansweredDeadlineMs`; the `relay_unanswered` row (role `solo` or `lead`; members untouched, S-6: they keep working, get the paused auto-reply when they write to it, and their own asks run the ladder against it); the notice and the push in `pause_outbox` (the push follows `internal/module/push/pause_push.go`'s rules). Then wake the row's long-poll waiters (the store's row notify that a close uses) and `rosterChanged()`. **No new approval event op** (C#23): the App learns it from the pause (roster, `GET /api/team/paused`) and from the row when it reads it.
- An unanswered row is **never abandoned for its lease** (S-13): the sweeper's lease branch skips rows with `unanswered_at > 0`; the liveness branch (`checkLive && sessionGone`) still abandons it when the session is gone.
- Every other row past its deadline closes `timeout` as today.
- Tests:
  - final row at its deadline → open, marked, sentinel deadline, pause row, notice, push (M: let it time out and the test fails);
  - an 89 % row, an idle row at 95 % and a manual row at 95 % → `timeout` as today (M on the manual exclusion); a reask row at 95 % → final; a lead marked unanswered → no pause row for its members, a member's message to it gets the unanswered auto-reply, and that member's own 90 % ask, unanswered by the paused lead, pauses the member by RT-2b T23 (S-6);
  - an unanswered row with a lease 10 min old stays open (M); its session gone → abandoned at the liveness tick;
  - a `GET /api/relay/wait/{id}?wait=25` in flight returns within one tick, `state: open`, `unanswered_at` set;
  - the mark happens once.

**T16 Leaving `relay_unanswered`** — `T/store.go` (`CloseIfOpen`, `CloseIfExpired` :543, `closeRowIn` :577), `T/team_store.go` (`closeSelfRelayApprovedIn` :286, the approve transaction :247), `T/pause_unanswered_store.go`.
- Every close of a row with `unanswered_at > 0` deletes the session's `relay_unanswered` row **in the same transaction** and enqueues the resume notice for its outcome (approved / denied / any other: abandoned, cancelled) and the bounce list (`bounceListIn`, `pause_resume_store.go:190`); no reask control. The generic closes (`CloseIfOpen`, `CloseIfExpired`, which today run one statement on the DB, C#13) run in a transaction for such rows; every caller keeps its signature: decide (approve / deny), DELETE, the sweeper's liveness abandon, the mod's `cancelled{compacted}` report (`closeRequestOfReportedOp`), the switch-on sweep and the held-card pause (`pauseHeldCard`).
- Tests, one per path: approve, deny, DELETE, compaction report, liveness abandon, switch-on auto-approve, held-card pause → row gone, the right text, the bounce list (M on approve and on the compaction path); a session that also holds `quota` stays paused and is told nothing yet.

**T17 Hello re-adopts an unanswered card** — `W/wire_relay.go` (`RelayHelloResponse.Waiting *RelayWaiting{RequestID, Op, UnansweredAt}`), `T/relay_handler.go` (hello, :120-135).
- When the session's open op is `awaiting_approval` and its row is unanswered, hello carries it (C#9). Older mods ignore the field.
- Test: hello of a session with an unanswered card carries it; with an ordinary open card carries nothing.

## RT-2a2 — resume scoping, release and the bounce text (daemon, peers)

Expected files: `T/pause_resume_store.go`, `T/pause_release.go`, `T/pause_store.go`, `internal/module/peers/pause_gate.go`, `peers/send.go`, `peers/deliver_local.go`, `internal/peers/wire.go`, `internal/core/info_handler.go` + tests (~8).

**T18 Resume scoping and release** — `T/pause_resume_store.go` (`ResumeScope.where`, :23-34), `T/pause_release.go`.
- The switch-off and reconcile scopes (`All`, `OnSinceNot`) add `AND cause IN ('quota','lead')` (S-3).
- A person's release (`POST /api/team/paused/{sid}/release`) of a session holding `relay_unanswered`: its final card closes **denied** by the request's client (T16 removes that row), then the other causes are released **with `NoReask`** (C#10: the person chose not to relay; a reask would ask again).
- Tests: unattended off + reconcile → a `relay_unanswered` row survives (M); release → card denied, row gone, the denial notice; release with `quota` + `relay_unanswered` → both gone, **no** reask token or control (M); release of a `quota`-only session unchanged (reask as today).

**T19 Bounce text by cause** — `peers/pause_gate.go`, `send.go:615-617`, `deliver_local.go:84-89`, `T/pause_store.go` (`PauseVerdict`, :248).
- `PauseGate.PauseVerdict(...) (paused bool, detail string, err error)`. The team side answers the unanswered detail (solo / lead, or the member one) when the session holds `relay_unanswered`, else `SessionPausedDetail`. Peers puts `detail` into the 409 on both paths.
- Tests: peers with a fake gate (detail passed through, local and remote); team with each cause and with two causes (unanswered wins) (M).

**T20 Capability** `relay.unanswered.v1`.

## RT-2b — the member's ladder: `--deny`, the final ask, unattended auto-accept (daemon, CLI)

Expected files: `W/wire_relay_member.go`, `T/relay_ask.go`, `T/relay_ask_store.go`, `T/relay_ask_deny.go`, `T/relay_member.go`, `T/relay_member_store.go`, `T/commands_relay_store.go`, `T/compacted.go`, `T/module.go`, `cmd/pdx/relay_member_cmd.go`, `skills/pdx-team/SKILL.md` + tests (~11).

**T21 `pdx relay _<ref> --deny`** — `W/wire_relay_member.go`, `T/relay_ask_deny.go` (new), `T/module.go` (`POST /api/team/relays/deny`), `cmd/pdx/relay_member_cmd.go`.
- Body `{origin_inbox, target}`; the caller leads the member's team (`callerTeam`, `matchMember`). Its open ask → `withdrawn` / `denied` (S-12), under `askMu` and `createMu` like the accept. An unanswered ask (T23) also loses the member's `relay_unanswered` row in the same transaction, with 「[pdx team] lead 決定不接力，恢復作業。」. No open ask → 409 `no_open_ask`. Remote member → 409 `relay_unsupported` (S-8).
- CLI: `--deny`, no `--wait`; exit 0 / 13; 「已拒絕 <ref> 的接力申請」.
- Tests: deny an open ask (M); not your member → 409; no ask → 409; deny of an unanswered ask resumes; a later `pdx relay _ref` works.

**T22 Unattended accepts a member's ask** (S-4, U-7.2) — `T/relay_ask.go` (`handleRelayAsk`), `T/relay_member.go` (factor the create core of `handleRelayCreate` into `createMemberRelay(t, mr, originInbox)`), `T/relay_ask_remote.go` / `T/commands_relay_store.go` (the mirror of a remote member's ask).
- While the switch is on, a new ask (local, or a remote member's mirror on the lead host) is accepted by the daemon through `createMemberRelay` with the lead's inbox as origin, under `createMu` as the lead's `pdx relay` is: the pool gate (`memberRelayGate`) spends one unit, or pauses the member at 0 (local) / holds a `member_relay` card (remote), exactly as today. The ask is marked accepted in the op's transaction (as `relay_member_store.go:97` / `commands_relay_store.go:84`). The lead is told 「[pdx team] 無人值守：member %s「%s」已用 %d%%，已自動接力（member 額度剩 %d）」, or the pause notice it already gets.
- At switch-on, every open ask is accepted the same way (the sweep).
- Off: unchanged (the lead decides, U-6.1).
- Tests: unattended + pool 2 → op requested, pool 1, ask accepted, lead notice (M: leave it to the lead and the test fails); pool 0 → member paused for quota, no op (M); switch-on with an open ask → accepted; off → only the lead's notice, as today.

**T23 Final member ask** — `T/relay_ask.go` (`settleAsks`), `T/relay_ask_store.go`, `T/relay_member_store.go`, `T/commands_relay_store.go`, `T/compacted.go`.
- `ALTER TABLE relay_asks ADD COLUMN unanswered_at INTEGER NOT NULL DEFAULT 0`.
- `ExpireRelayAsks` skips asks with `used_pct ≥ 90`. `MarkFinalAsksUnanswered(now)`: such an open ask past `expires_at` with `unanswered_at = 0` → `unanswered_at = now`, `expires_at` = the sentinel; the member's `relay_unanswered` row (role member) and its notice 「[pdx team] 接力申請 lead 沒有回應，先暫停作業，等 lead 確認。」, in one transaction; then the lead's notice 「[pdx team] member %s [%s]「%s」接力申請 5 分鐘沒有回應，已暫停作業。請執行 pdx relay _%s（接力）或 pdx relay _%s --deny（不接力，恢復作業）。」 through `noticeToLead`.
- The lead's accept of an unanswered ask (the accept's `expires_at > now` holds with the sentinel) removes the member's row with 「[pdx team] lead 已安排接力，恢復作業。」 in the op's transaction. A withdrawal (compaction, `compacted.go:58-61`; member left) removes it with the "ended" text.
- Ask notice text (`RelayAskNoticeFmt`, `RelayAskRemoteNoticeFmt`): 「…已用 %d%%，申請接力。\n%d 分鐘內請回覆：同意 pdx relay _%s；不同意 pdx relay _%s --deny」; a final ask adds 「這是最後一次申請；沒有回覆的話這個 member 會暫停作業。」
- Tests: a 90 % ask past 5 min → unanswered, member paused, both notices (M); 85 % → expired as today; accept, deny and compaction of an unanswered ask resume (M on accept); the texts.

**T24 Skill text**: approve with `pdx relay _<ref>`, **decline with `pdx relay _<ref> --deny`**; never let it lapse; a member at its last ask pauses until you answer; in unattended mode the daemon accepts asks itself and tells you.

## RT-2c — the ladder in the mod (mod, CLI)

Expected files: `hooks/register.js`, `hooks/relay.test.ts`, `cmd/pdx/relay.go` + tests (3).

**T25 `lastAskPct` only on an answer** — H `maybeBegin` (H:685), `begin` (H:724-733), `reaskBegin` (H:793).
- `begin` sets `s.lastAskPct = u.percent` when it opened a request (201) or the daemon refused it (exit 13, any code); exit 20 / 21 / 1 set nothing. `maybeBegin` and `reaskBegin` stop setting it before the call.
- No threshold ask once `s.lastAskPct ≥ 90`, in `maybeBegin` and `maybeAsk`.
- Tests: daemon down at 72 % → the next turn end at 73 % asks again (M); refused at 72 % → nothing until 82 %; denial at 91 % → no ask at 99 % (M); the member's guard.

**T26 Idle ask freed from +10** — H `idleFire` (H:1193), `settle` (H:1041): drop the `lastAskPct + REASK_POINTS` gate from `idleFire`; an idle ask's denial or timeout no longer sets `lastAskPct`. Tests: threshold timeout at 70 %, then idle 40 min at 71 % → an idle ask (M); an idle denial at 75 % leaves the 80 % threshold ask in place.

**T27 Unanswered in the mod** — `cmd/pdx/relay.go` (`runRelayWait`, :355-440), H `waitLoop` (H:1014), `prompt.submit` (H:1595), hello (H:219-253).
- CLI: `pdx relay wait <id> --return-unanswered` returns at once (exit 0, the approval JSON) when a poll shows `state: open` and `unanswered_at > 0`.
- Mod: the loop's calls carry the flag until it sees such an answer; then `p.unanswered = true`, held prompts are let go **unchanged** (a `p.release` deferred the hold races against), status 「接力申請沒有回應，已暫停作業；在 App 核准後接力」, and the loop goes on without the flag until the card closes. `prompt.submit` does not hold while `open.unanswered`.
- Hello's `waiting` (T17): a mod that holds no request re-adopts it — `s.pending` with its request id and op, `unanswered: true`, state `awaiting`, the loop started (C#9).
- An approval goes through `settle` → `startWrite` (a running turn defers it, H:1064). A compaction ends it (S-18): the existing path (H:1650-1657) reports `cancelled{compacted}` and resets `lastAskPct`.
- Tests: CLI early return with the flag only (M); a held prompt goes on at the mark (M); later prompts are not held; approval later → the write prompt after the running turn; denial → idle, no more asks; hello re-adoption after a reload (M); unanswered → a typed turn → an auto compaction → the card cancelled, `lastAskPct` reset (S-18).

`VERSION` → '7'.

## RT-3a1 — half ops, reuse and the switch-on idle split (daemon)

Expected files: `W/wire_relay.go`, `T/relay_store.go`, `T/relay_begin.go`, `T/unattended.go`, `T/relay_report.go`, `T/relay_store_report.go`, `T/relay_fresh.go`, `T/relay_reconcile.go`, `T/module.go`, `cmd/pdx/relay.go` + tests (~10).

**T28 Mode and reasons** — `W/wire_relay.go`, `T/relay_store.go`.
- `ALTER TABLE relay_ops ADD COLUMN mode TEXT NOT NULL DEFAULT ''`; `RelayOp.Mode` (`''` full, `half`, `handoff`); `RelayReasonHalf = "half"` (S-9); `MinHalfModVersion = 8`; `SelfRelayIdleDeadlineS = 300`.
- Table `relay_fresh_half (session_id TEXT PRIMARY KEY, op_id TEXT NOT NULL, path TEXT NOT NULL, written_at INTEGER NOT NULL)` (new: CREATE IF NOT EXISTS).
- Tests: migration of an old `relay_ops`; the table on an old DB.

**T29 Begin: half, idle in unattended, the idle card's life, the switch-on split** — `T/relay_begin.go`, `T/unattended.go`.
- `RelayBeginRequest.Half bool`, valid only with reason `idle` (400 otherwise). A half begin: the switch refusals apply (`off` / `paused` → 409); a member → 409 `member_relay_is_leads`; else the op (mode `half`) and its row are written approved at once by `Client{Kind: "daemon", Label: "閒置寫接力檔"}`, `Auto: false` (no quota); 201 claimed.
- An idle begin while the switch is on: mod protocol ≥ 8 → the half path (§4.5, U-7.1); below 8 → today's auto-approve (M: an old mod must never get a half).
- An idle card (switch off) gets `DeadlineAt = now + 300 s`; the hash uses the same figure.
- Switch-on sweep and the tick, for an open idle `self_relay` row: mod ≥ 8 → closed `timeout` / `close_reason = unattended` (its mod then runs the half); mod < 8 → today's auto-approve (C#14).
- Tests: half begin → claimed, mode half, nothing spent (M); half without idle → 400; unattended idle with mod 8 → half (M), with mod 7 → today's approval; idle card deadline 300 s; switch-on with an open idle card from mod 8 → `timeout` / `unattended`, from mod 7 → approved (M).

**T30 Written closes a half or a handoff** — `T/relay_report.go` (`written`, :135), `T/relay_store_report.go`, `T/relay_reconcile.go`.
- `written` on an op with mode `half` **or** `handoff` (C#15): in the same transaction the op goes `cancelled` / `half`; for `half`, `relay_fresh_half` is upserted; for `handoff`, the member's kill is claimed in the same transaction (RT-3c T38). The relay lock is lowered as at any terminal state.
- `outcomeNoticeAsync` (`relay_report.go:156`, the text built at `relay_reconcile.go:137`) sends **nothing** for `cancelled` / `half` (C#20): a self half has no lead to tell; a handoff's lead gets T38's notice only.
- Tests: written on a half op → cancelled/half, fresh row (M); on a handoff op → cancelled/half and the kill claimed (M: leave it `written` and the test fails); on a full op → `written` as today; no "接力已取消" notice for either (M).

**T31 Reuse and staleness** — `T/relay_begin.go`, `T/relay_fresh.go` (new), `T/relay_handler.go` (hello), `cmd/pdx/relay.go`.
- `RelayBeginRequest.ReuseOp string`: valid when that op is this session's, `cancelled` / `half`, named by `relay_fresh_half`, and its file exists and is non-empty → the new op's `handoff_path` is that file; else 409 `reuse_invalid` (the mod then begins without it). Every begin path (card, unattended auto, person, half never).
- `POST /api/relay/fresh {session_id, op_id, stale: true}` deletes the row (the mod's report, S-10). The row is also deleted when any relay op of the session reaches `claimed` **without** reusing it, when a compaction is reported for the session, and when the liveness tick finds the session gone.
- Hello carries `fresh_half {op_id, path, written_at}` when the row exists (C#18), so a reloaded mod can reuse it.
- CLI: `pdx relay begin … --half`, `--reuse <op>`; `pdx relay fresh --session <sid> --op <id> --stale`.
- Tests: reuse valid → new op with the old path, the fresh row kept until that op claims (M); another session's op, a non-half op, a missing file, no fresh row → 409; each staleness path deletes the row; hello carries it.

## RT-3a2 — fresh-handoff surfaces, retention, push (daemon)

Expected files: `W/wire_relay.go`, `W/wire_roster.go`, `T/relay_fresh.go`, `T/roster_publish.go`, `T/retention.go`, `internal/push/content.go`, `internal/core/info_handler.go` + tests (~7).

**T32 Surfaces** (S-21)
- `GET /api/relay/fresh` → `{sessions: [{session_id, ref, op_id, path, written_at}]}`; host event `relay.fresh` (`{op: "snapshot"|"changed", sessions: [...]}`) on subscribe and on every change; `RosterSession.FreshHandoff *{op_id, path, written_at}` for team rows.
- Tests: GET and the event follow the table; the roster carries it for a lead.

**T33 Retention** — `T/retention.go` (`retentionVictims`, :101-138; `removeHandoff`, :183).
- A `cancelled` op named by `relay_fresh_half` is never a victim while the row exists (C#19); after the row goes it follows the 3-day rule from then (its `updated_at` is touched when the row is deleted).
- A reuse op points at the half op's file: its row is marked pruned without a removal and without the "not <relay dir>/<op id>.md" log line (that line stays for any other mismatch).
- Tests: a fresh half older than 3 days keeps its file (M); after the row goes, 3 days later it is removed; a reuse op removes nothing, logs nothing.

**T34 Push and capability** — `internal/push/content.go:119-122`: the idle body 「不核准的話，5 分鐘後會先寫好接力檔（不清空）」. Capability `relay.half.v1`.

## RT-3b — the first half in the mod (mod)

Expected files: `hooks/register.js`, `hooks/relay.test.ts` (2).

**T35 Running a half** — H.
- An idle card that settles `timeout` (H:1038-1043) → from a timer, `begin --self --half --reason idle --idle-minutes N`. A begin answer whose op has `mode: half` (this call, or an unattended idle begin) is approved at begin, so it takes the existing `awaiting` → wait (answers at once) → `settle` → `startWrite` path; `s.pending.half = true` (from `body.op.mode`) is the new mark; an idle half never holds a prompt (D6).
- The write turn's end (`onWriteTurnDone`, H:1357) for a half: a good file → report `written`, **no clear**, `toIdle`, `s.half = {op, path, at}`, status 「已寫好接力檔」, one toast 「快取即將過期，已先寫好接力檔（<path>）。要接力請打 /relay 或 /relay now，會直接接上後半：清空並由新對話接手，不用重寫接力檔；不需要就直接關掉這個 session，或照常繼續。」 (S-14). Then, in the same turn.complete, the threshold check runs once (U-4.1: at ≥ 70 % it asks at once and reuses the half).
- Tests: idle timeout → half begin → write prompt → written, no `/clear` (M); unattended idle answered half → the same; the texts; at 75 % the threshold ask follows the half with `--reuse`.

**T36 Fresh and stale** — H.
- `s.half` is dropped (and `pdx relay fresh --stale` sent from a timer) at the first main-conversation `turn.start` that is not the mod's own write or seed turn, at a compaction and at `/clear`. Hello's `fresh_half` restores `s.half` in a mod that has none (a reload).
- Every begin (threshold, `/relay`, reask) passes `--reuse <op>` while `s.half` is set and `checkHandoff(s.half.path)` passes. When the answer's `op.handoff_path === s.half.path`, the mod skips the write turn: reports `writing` then `written` and goes to `startClear`. A 409 `reuse_invalid` → `s.half` dropped, the begin sent again without it.
- Tests: reuse with no turn between → no write prompt, straight to `/clear` (M); a typed prompt between → a fresh write; the stale report once; a broken file → a fresh write; a reload, then `/relay` → reuse from hello's `fresh_half` (M).

**T37 Gate M-2** (spec §6): a real session with `PDX_RELAY_IDLE_MINUTES=1` writes a half after the 5-min card (and once unattended), then `/relay` reuses it with no write turn; a typed prompt in between makes the next `/relay` write again. Method and result in the PR body.

`VERSION` → '8'.

## RT-3c — `pdx kill --handoff` (daemon, CLI, mod)

Expected files: `W/wire_team.go`, `T/team_handler.go`, `T/member_relay_store.go`, `T/relay_report.go`, `T/kill_recover.go`, `cmd/pdx/kill_cmd.go`, `hooks/register.js`, `hooks/relay.test.ts`, `skills/pdx-team/SKILL.md` + tests (~9).

**T38 Daemon** — `W/wire_team.go` (`KillRequest.Handoff`), `T/team_handler.go` (`handleKill`, :189), `T/member_relay_store.go`, `T/kill_recover.go`.
- `handoff: true` for a local active member whose mod protocol ≥ 8: a member op with mode `handoff` (`requested`) and the control message (`sendMemberControl`, `T/relay_member.go:196`).
- On `written` (T30): in the **same transaction** as `written → cancelled/half`, the member row is claimed for the kill exactly as `pdx kill` claims it (state `killing`) (C#16); after the commit the kill runs as `pdx kill`'s does. A crash between the commit and the kill is settled by `settleStuckKillingMembers` (`T/kill_recover.go:53`) as for any stuck kill - it never signals on the lead's behalf: a process that is gone → `gone`, one still there → `active` again - and the lead is told 「member …的交接檔已寫好（<path>），但關閉沒完成，請再下一次 pdx kill」 (decided with the lead, 2026-10-14); a second `pdx kill --handoff` reuses the file (the handoff keeps `relay_fresh_half`, and the claim answer says `reuse`). The lead hears once: 「[pdx team] member %s「%s」已交接：%s，已關閉。內容請由你決定要不要寫進記憶與文件。」 (and no outcome notice, T30).
- A member that does not claim or write (the timeouts of `T/relay_timeouts.go`) is **not** killed; the lead hears the failure as for a relay.
- Remote member or mod < 8 → 409 `relay_unsupported`.
- Answer: the op (201); the CLI waits for it like `pdx relay _<ref> --wait` (its loop reused) and exits 0 when the member is closed, 14 when it did not respond, 13 refused.
- Tests: handoff → op, control, written → kill claimed in the same transaction, then killed, one notice (M); a crash after the commit (test seam) → the member active again, the lead told, the next `--handoff` on the same file (M); unresponsive → not killed; remote → 409.

**T39 Mod** — H `claim` (H:1106): a claimed op with mode `handoff` writes the handoff and reports `written`, then goes idle: **no** `s.half`, no status or toast, no threshold check, no `/clear` (C#17). Tests: the claim writes and does not clear; none of the half's follow-ups run (M).

**T40 Skill text**: `pdx kill <ref> --handoff` (U-5.2) and when to use each of the three answers.

## RT-4 — member idle intercept (gate M-1 first)

Expected files: `hooks/register.js`, `hooks/relay.test.ts`, `W/wire_relay_member.go`, `T/compacted.go`, `T/notice_idle.go`, `T/notice_usage.go`, `skills/pdx-team/SKILL.md` + tests (7).

**T41 Gate M-1** (spec §6; may run any time before, e.g. beside RT-1): a throwaway session with a small `CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS` and a throwaway plugin dir whose `session.compact` hook logs `e.trigger`, `turnRunning` and usage and answers `{skip}`. Record: the hook sees the idle compaction; it differs from a context-full one (no turn running; percent); `{skip}` prevents it; whether and when Claude Code retries. Results in the PR body or an issue comment. If it fails, T42 is the fallback (§6): a daemon member idle timer at 50 min that notifies the lead, and compaction then happens.

**T42 Intercept** — H (`session.compact`, H:1640), `T/compacted.go`, `W/wire_relay_member.go`, `T/notice_idle.go`, `T/notice_usage.go`.
- Mod: in a member, an `auto` compaction with no main turn running (and the M-1 signal) → `{skip: 'member 閒置：已通知 lead 決定'}`; from a timer, `pdx relay compacted --session sid --trigger idle --intercepted`.
- Daemon: `RelayCompactedRequest.Intercepted bool`. For an active local member: no withdrawal of an open ask (it was not compacted), one notice per idle period (`claimIdleTold`): 「[pdx team] member %s「%s」閒置，快取快過期，context %d%%。請決定：接力（pdx relay _%s）、交接後關閉（pdx kill _%s --handoff）、直接關閉（pdx kill _%s）。」
- `noticeIdle` and `noticeUsage` skip members whose mod protocol ≥ 9 (`MinIdleInterceptModVersion = 9`) (M: an old member still gets them).
- Tests: intercept → one notice per idle period (M); a context-full compaction in a member runs and is reported as today; a member with mod 8 gets both old notices; mod side: skip only in a member with no turn running.

**T42b Solo and lead intercept** (spec §4.3a, U-4.3, user 10-16). Two PRs: **RT-4a** = T42 rebased on main with the
routing change below (member path) and the protocol bump; **RT-4b** = the solo/lead path, the card and its answers.
Expected files add `W/wire_relay.go`, `W/wire.go`, `T/relay_begin.go`, `T/relay_decide.go`, `T/unattended.go`,
`cmd/pdx/relay.go`.
- **The daemon decides, the mod only skips and reports** (replaces T42's mod-side branch on the cached role). On an idle
  compaction (T42's recognition) the mod answers `{skip}` and posts `pdx relay compacted --intercepted --period <p>`;
  `p` is the idle period's identity: the end time of the last main turn (a new turn starts a new period; it survives a
  mod reload because it comes from the transcript's last turn, not from mod memory). The daemon, under the session's
  relay lock, reads the **authoritative** role and answers one of: `member` (T42's notice to the lead), `silent`,
  `ask` (attended: it has created the `idle_intercept` ask), `half` (unattended: it has started the first half), `pass`
  (the person answered `compact` for this period: the mod lets the next compaction through). So a release or an adopt
  between two intercepts is routed by the daemon's role, never the mod's cache.
- **One decision per period, kept by the daemon**: a row per session (`relay_intercepts`: session, period, outcome,
  at). `silent` when, in this period: an idle ask is open, answered or its first half is beginning or done; a fresh
  `half` handoff exists; or an intercept outcome is already recorded (a card answered `deny`, timed out, or `compact`
  spent). Creating the ask or starting the half and recording the period happen in one transaction with the
  session's one-open-op rule, so the 40-min idle timer, a `halfLater` begin and the intercept cannot both act.
- **An open idle card is never cancelled by an intercept** (today a non-member in `awaiting` goes `toIdle` +
  `cancelled{compacted}` and then compacts): the intercept is `silent`, the card stays open and its timeout still runs
  the first half (§4.3).
- **The ask**: `reason: idle_intercept` (new wire constant; CLI and `relay_begin` validation accept it). Attended:
  timeout → **nothing** (unlike the idle ask, whose timeout runs the first half); `deny` → nothing; `approve` → the
  relay as `/relay` (a fresh handoff reused); `compact` (new `Decision` value; the op closes `cancelled` with reason
  `compact`; the CLI's JSON and exit code say `compact`, exit 0) → the period's outcome becomes `pass` and the mod's
  settle branch remembers it, so the next idle compaction in this period is let through once (and, if the mod API can
  start a compaction, it does so at once — record the finding in the PR). Unattended at creation: the first half
  (no card, no quota); no first half if a fresh handoff exists (→ `silent`).
- **Switching unattended on with an `idle_intercept` card open**: the sweep treats it as an idle card: close it and run
  the first half (no quota, no clear) unless a fresh handoff exists; never the general `autoApprove`.
- **Versions**: mod protocol `VERSION` → **'10'** for this path ('9' = member intercept only). The daemon's hello
  answer carries `idle_intercept: 1` (members) or `2` (all sessions); the mod skips an idle compaction only when the
  daemon's level covers the session (a daemon that does not say → no skip: the compaction happens as before). A
  daemon seeing a mod < 10 for a non-member never answers `ask`/`half` (that mod cannot settle `compact`) — it answers
  `silent` only if the mod skipped, and logs. App: capability `team.idle_intercept.v1` for the third button (B line).
- **Gate**: M-1 passed on Claude Code 2.1.299 (#2870). RT-4b re-runs M-1's two checks (skip honoured; no immediate
  retry) on the Claude Code version installed at the time and records it in the PR; if either fails, RT-4b is
  blocked and the lead is told (no fallback for solo sessions: their compaction then happens as before).
- Tests (mod): any session's idle compaction is skipped only when the daemon's level covers it (M); a context-full one
  in a turn runs; the period id comes from the last turn and survives a reload. Daemon: routing by the authoritative
  role, incl. a release and an adopt between two intercepts; one decision per period across a mod reload and a
  retry 54 min later (M); idle card open → `silent` and the card's timeout still runs the half; races under `-race`:
  idle-timer begin in flight × intercept, `halfLater` begin in flight × intercept, a fresh half written while the
  intercept is decided; attended `idle_intercept` timeout → nothing, `deny` → nothing, `compact` → `pass` once (M);
  unattended at creation → half, with a fresh handoff → `silent`; switching unattended on with the card open → half,
  no quota, not `autoApprove`; mixed versions: new mod × daemon without the level → no skip; mod 9 × new daemon
  for a solo session → no `ask`.

**T43 Skill text**: the intercept notice and the three answers; §4.3a's card and answers.

`VERSION` → '9' in RT-4a, '10' in RT-4b (T42b).

## RT-5a — spawn resource check: seam, check, card (after lease v2 P1b; #2765 merged as d3a6e69f; daemon)

Expected files: `internal/resources/*` (the seam), `internal/module/resources/*` (register), `W/wire.go`, `W/wire_team.go`, `T/spawn_handler.go`, `T/spawn_store.go`, `T/unattended.go`, `T/handler_decide.go` + tests (~9).

**T44 Zone seam** — reuse `resources.HostReader` (`HostReaderKey`, `LatestHost()`; on main since #2765, merged as d3a6e69f, `internal/resources/wire.go:73-75`); do not add a second key. Extend what it answers with the zone, trend and `zone_since` (lease v2 P1b publishes them), the held heavy leases and the queue (copies; no lock beyond the snapshot pointer). Absent, not ok, or no zone = unknown. From host-metrics HM-3 the resources module reads its host figures from the hostmetrics reader, and this seam keeps answering from the resources snapshot (host-metrics spec D-H1), so RT-5a does not change. Tests: the published zone and leases; absent → unknown.

**T45 Check and card** — `W/wire.go` (`KindSpawnResources`, `SpawnResourcesPayload`, `SpawnResourcesDeadlineS = 480`, S-5), `W/wire_team.go` (`SpawnAwaitingResources`), `T/spawn_handler.go` (`acceptSpawn`), `T/spawn_store.go`, `T/unattended.go`.
- For a **new** local spawn id, after the grant checks and before `startSpawn`: zone `normal` / `unknown` → as today. `warning` / `danger`:
  - switch on and not a person spawn (T7's `person`) → 409 `unattended_refused`, nothing written;
  - else the op is written in state `awaiting_resources` (holding a seat, so `team_full` stays exact) and a `spawn_resources` row (id = the spawn id) in the same transaction; the runner does not start.
- The row: approve → `running`, `startSpawn`; deny → `failed` / `spawn_denied`; timeout → `failed` / `spawn_timeout`; abandon (the CLI stopped polling) → `failed` / `spawn_abandoned`; the seat is freed on every failure.
- Not auto-approvable; a switch-on sweep refuses an open one of an agent's spawn (`unattended_refused`) and leaves a person's.
- Tests: normal → runs (M); warning → card, op waiting, seat held (M); approve → runs; deny / timeout → failed, seat freed; unattended agent spawn → 409 (M); unattended person spawn → card; unknown → runs.

## RT-5b — CLI and push for the spawn card

Expected files: `cmd/pdx/spawn_cmd.go`, `internal/push/content.go`, `internal/module/hostconfig/*`, `internal/core/info_handler.go` + tests (~5).

**T46 CLI** — the re-post loop treats `awaiting_resources` as not settled and prints once 「主機資源緊（<zone 中文>：記憶體 N%、壓力 P），等使用者放行…」; `spawn_denied` → exit 10, `spawn_timeout` → 11, `unattended_refused` → 13 with the text.

**T47 Push** — title 「{host}：{lead} 要開 member，主機資源{警戒|危險}」, body 「記憶體 N%、壓力 P、load L。放行或不放行？」, sent only when host config `relay.push_spawn_cards` is on (default off; 88 turns it on once iOS knows the kind). Capability `team.spawn_resources.v1`.

## RT-6 — unattended preview (daemon)

Expected files: `W/wire_unattended.go`, `T/unattended_preview.go`, `T/unattended.go`, `T/module.go`, `internal/core/info_handler.go` + tests (5).

**T48 `GET /api/team/unattended/preview`** — read-only; every list computed by the **same helper** the switch-on sweep uses, so the preview cannot drift (the sweep's rules: T4, T22, T29). Shape in the App contract. Tests: one fixture host with each case (rule on/off; quota 0 and 2; threshold / idle from mod 7 and 8 / reask / member_relay / old manual-member cards; an open member ask; a lead and an adopt card; a spawn card; a paused session) → the expected lists (M: change the sweep's rule for one case and the preview test fails). Capability `team.unattended_preview.v1`.

## Order and owners

RT-1a → RT-1b → RT-1c (deploy) → RT-2a1 → RT-2a2 → RT-2b → RT-2c (deploy) → RT-3a1 → RT-3a2 → RT-3b → RT-3c (deploy) → RT-4 → RT-5a (after lease v2 P1b; #2765 is merged, d3a6e69f, so the seam is there) → RT-5b → RT-6.
- Gate M-1 (T41) can run any time before RT-4, by anyone with a throwaway session.
- RT-6 needs RT-3a1 (the idle split) and RT-2b (asks); it may run beside RT-3b.
- One Sonnet member implements in order. The lead reviews each PR, releases it, and bumps and deploys at the three marks.
- 88 gets the App contract below now; each item names its capability.
- **Follow-up issues**: App/agent permission separation is **#2774** (1f opens it); S-8 remote cases (opened with RT-1a).

## App contract (for 88)

All new things are behind capabilities; an App that does not see one keeps today's behaviour.

1. **「這個 session 不再詢問」 removed** (U-8.2), Mac (`ApprovalDialogHost.tsx:396-403`) and iOS. `/relay off` is the only switch. Pure UI.
2. **Approvals decided by a person's command** (`team.person.v1`): `decided_by.kind = "person"`, label 「使用者（終端機指令）」. Rows created approved are announced `closed` only; an open card a person's command approved closes like any approval (the dialog closes; say 「已由終端機指令核准」). Lead and adopt rows refused by unattended mode close `cancelled` / `close_reason: "unattended_refused"`; PUT `/api/team/unattended` answers `refused: N` beside `swept` (RU G-5).
3. **Self-relay card changes**:
   - idle cards (`payload.reason = "idle"`) live 300 s (`relay.half.v1`); the push body is the new text;
   - a **final** card past its deadline stays open (`relay.unanswered.v1`): no new event op; the row read later has `unanswered_at` (unix ms) and `deadline_at` = 9999-12-31 — show no countdown and 「已暫停作業，等你核准」; the session's pause (below) is the live signal.
4. **Pause cause `relay_unanswered`** (`relay.unanswered.v1`) in `GET /api/team/paused` and `RosterPause.causes`. Row text: solo / lead 「接力申請沒有回應，已暫停」; member 「等待 lead 確認接力」. **解除** = 拒絕 the card (the session goes on, no more asks); confirm text 「解除後不接力，這個 session 繼續作業，之後不再申請接力」.
5. **Fresh handoff** (`relay.half.v1`): `GET /api/relay/fresh`, host event `relay.fresh` (`{op: "snapshot"|"changed", sessions: [{session_id, ref, op_id, path, written_at}]}`), and `RosterSession.fresh_handoff` for team rows → the session row shows 「已寫好接力檔」 (tooltip: path and time). Relay ops may end `cancelled` / `reason: "half"`: show 「已寫好接力檔（未清空）」, not a failure.
6. **Card kind `spawn_resources`** (`team.spawn_resources.v1`):
   ```json
   {"spawn_id":"…","team_id":"…","lead_ref":"_abc123","lead_title":"…","cwd":"/…","title":"…","model":"sonnet","effort":"low",
    "task_subject":"…","person":true,
    "host":{"zone":"warning","trend":"rising","mem":83.1,"pressure":2,"load1":12.3,"ncpu":10,"sampled_at":0},
    "heavy":[{"id":"…","kind":"test-full","session_id":"…","ref":"_x","title":"…","age_s":120,"rss_bytes":0}],
    "queue":[{"id":"…","kind":"build","position":1,"reason":"rising"}]}
   ```
   放行／不放行; deadline 480 s (S-5). Push only once 88 turns on `relay.push_spawn_cards`. While open the spawn holds a seat (`in_use` counts it), op state `awaiting_resources`: show it like a starting member with 「等待放行」.
7. **`GET /api/team/unattended/preview`** (`team.unattended_preview.v1`), for the confirm dialog before switching on (U-8.1, Mac and iOS):
   ```json
   {
     "on": false,
     "quota_rule": true,
     "zero_quota":    [{"session_id":"…","ref":"_x","title":"…","role":"solo|lead","self_left":0}],
     "member_pools":  [{"lead_session_id":"…","ref":"_x","title":"…","pool_left":0,"members":2}],
     "relay_cards":   [{"approval_id":"…","kind":"self_relay|member_relay","session_id":"…","ref":"_x","title":"…",
                         "used_pct":72,"idle":false,"reask":false,"unanswered":false,
                         "becomes":"auto_approve|pause|half|stays_open"}],
     "member_asks":   [{"ask_id":"…","session_id":"…","ref":"_x","title":"…","used_pct":75,"becomes":"auto_approve|pause|stays_open"}],
     "refused_cards": [{"approval_id":"…","kind":"lead|adopt|spawn_resources","session_id":"…","ref":"_x","title":"…"}],
     "paused":        [/* PausedSession, as GET /api/team/paused */]
   }
   ```
   `zero_quota` and `member_pools` are empty when `quota_rule` is false (the quota is inert, RR §10.6). `becomes`: `half` for an open idle card of a mod ≥ 8; `stays_open` for a reask card, an old manual-member card, a member_relay card whose pool is 0, or a remote member's ask whose pool is 0. One dialog, then PUT as today.
8. **Team cap and folders move to the team panel** (U-0): routes unchanged (`PUT /api/team/max-members`, `PUT /api/team/roots`, `POST /api/team/host-roots`). The unattended panel keeps the relay quota and the member pool.
9. **iOS `member_relay` card** (G-1): the existing `MemberRelayPayload` (`W/wire_relay_member.go:73-84`).
10. **Badge** (U-8.3) is 88's, outside this plan.

### Capability flags (`internal/core/info_handler.go:49-66`)

| flag | phase | meaning |
|---|---|---|
| `team.person.v1` | RT-1b | person decider, direct `/relay`, `unattended_refused`, PUT `refused` |
| `relay.unanswered.v1` | RT-2a2 | `unanswered_at`, pause cause `relay_unanswered`, release = deny |
| `relay.half.v1` | RT-3a2 | idle card 300 s, op reason `half`, `/api/relay/fresh`, `relay.fresh`, `fresh_handoff` |
| `team.spawn_resources.v1` | RT-5b | card kind `spawn_resources` |
| `team.unattended_preview.v1` | RT-6 | the preview route |

## Risks

- **Person commands are not agent-proof** (S-1): an agent that ignores the skill can type `/relay` into its own pane or call the App's routes with the shared bearer. This is today's exposure for cards too; #2774 is the fix.
- **Tokens through a prompt** (`/team`, `/team spawn`): the model sees them; one use, one command, 10 min. A slow model gets `person_token_invalid` and asks the person to type again.
- **Two causes at once** (`quota` and `relay_unanswered`): each resume group takes only its own cause; the session resumes when its last row goes; a person's release with a final card never re-asks (T18).
