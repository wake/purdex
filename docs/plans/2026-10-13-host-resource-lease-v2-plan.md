# Host resource lease v2 (plan)

Spec: `docs/specs/2026-10-13-host-resource-lease-v2-spec.md` (§ numbers below are the spec's). Issue #2759. Coordinator: purdex-1f.
Code facts: `~/.local/share/purdex-lead/lease-v2-inventory.md` ("INV §n").

## Conventions (every phase)

- One branch and one PR per phase: `feat/lease-v2-p1a`, `-p1b`, `-p2a`, `-p2b`, `-p3a`, `-p3b`. Each starts from the latest `origin/main`, after the previous phase has merged. Work in a worktree.
- TDD: write the test, see it fail for the right reason, implement, see it pass. One commit per task.
- **Mutation proof is a deliverable.** For each rule test named "(M)" below, revert or flip the rule line, show the test turns red, and restore it. Put the list in the PR body.
- Tests:
  - `go test -race -count=1 ./internal/resources/... ./internal/module/resources/...` for P1–P3. Add the other touched packages: `./internal/module/hostconfig/...`, `./cmd/pdx/...` (whenever `cmd/pdx` or the mod changes; it includes the plugin package), `./internal/module/team/...` and `./internal/module/push/...` in P3b.
  - Mod tests: `claude plugin test cmd/pdx/plugin/purdex`.
  - `make lint`.
  - Do not run the full `go test ./...`; the lead runs it before release.
- `resources.db` is live on two hosts: every schema change is an `ALTER TABLE … ADD COLUMN` migration in the style of `migrateDecisionColumns` (`store.go:432-492`), with a test that opens an old-schema DB.
- Drafts and measurement output go in the worktree's untracked `.scratch/` (never `/private/tmp`).
- PR → codex R1 + R2 (attacker → critic), `--model gpt-5.6-sol`. Re-review only the increment. Then `pdx report ready` and **wait for the lead's go-ahead** before merging (merge commit, no `--delete-branch`), then `pdx report merged --pr --sha`.
- The spec and this plan enter the repo in P1a's PR as `docs/specs/2026-10-13-host-resource-lease-v2-spec.md` and `docs/plans/2026-10-13-host-resource-lease-v2-plan.md`. Copy them from `~/.local/share/purdex-lead/lease-v2-{spec,plan}.md`, and change the spec's "Code facts" line to say the inventory lives outside the repo.

## P1a — pure library (no behaviour change)

**T1 Settings fields** — `internal/resources/settings.go`, `internal/module/hostconfig/resources.go`.
- Add the fields of spec "Settings": `mem_warn_pct`, `mem_danger_pct`, `gap_s`, `trend_window_s`, `trend_mem_dppm`, `trend_cpu_dppm`, `park_s`, `alert_danger_s`, `alert_stuck_s`, `alert_lease_mem_pct`, `alert_push`.
- Each gets its default and range, `Validate` (reject out of range) and `Effective` (clamp), with accessors (`GapDuration()`, `TrendMemPPM() float64` = dppm/10, …).
- `DefaultDeadlineS` stays 300 here (it becomes 540 in P2a, T12a); P1a changes no behaviour.
- `mem_danger_pct` must be > `mem_warn_pct`: Validate rejects it; Effective clamps danger to warn+1.
- The hostconfig normalizer accepts the new keys and rejects unknown ones as before.
- Tests:
  - every default;
  - every range edge, valid and invalid;
  - warn ≥ danger rejected and clamped;
  - a stored v1 JSON with none of the new keys reads back with the defaults;
  - normalizer round trip.

**T2 Zones** — new `internal/resources/hostzone.go`. Do not name it `zone*.go`: `internal/module/resources/zone_test.go` is an unrelated TZ `TestMain`.
- `type Zone int` (`ZoneUnknown`, `ZoneNormal`, `ZoneWarning`, `ZoneDanger`) with `String()` "unknown|normal|warning|danger".
- `type ZoneLatch struct{ z Zone }` and `func (l *ZoneLatch) Update(h HostUse, available bool, s Settings) Zone`, implementing the spec D-1′ table:
  - enter on any condition;
  - step down one level only when every leave condition of the current level holds (danger can drop to warning, or straight to normal if the warning conditions are also clear);
  - unknown when `!available || h.NCPU <= 0`.
- Tests (table, (M) on each threshold):
  - each enter condition alone;
  - load ≥ ncpu gives warning, never danger;
  - mem exactly at 80 and 90;
  - hysteresis: 81 → 79 stays warning, 77 → normal;
  - danger → mem 88.5 stays danger; → 87 with pressure 2 gives warning;
  - unknown and back.

**T3 Ring and trend** — new `internal/resources/trend.go`.
- `type TrendSample struct{ At time.Time; Mem, CPU float64; Pressure int }`.
- `type Ring` keeps samples newer than 10 min (`Add`, `Since(t) []TrendSample`, `All()`); it is not safe for concurrent use (the sampler goroutine owns it; readers get copies).
- `type Trend int` (`TrendUnknown`, `TrendFalling`, `TrendFlat`, `TrendRising`) with `String()`.
- `func TrendOf(samples []TrendSample, now time.Time, s Settings) Trend`, per spec D-2′:
  - least-squares slope in points/minute over the samples within `trend_window_s` of `now`;
  - the pressure comparison is first vs last sample in the window;
  - fewer than 6 samples, or a span < window/2, gives unknown.
- Tests ((M) on each branch):
  - flat noise ±0.3 is flat;
  - +2/min mem is rising, −2/min falling;
  - +8/min cpu is rising;
  - mem falling while cpu rising is rising;
  - pressure 1 → 2 is rising even with flat numbers; 2 → 1 is falling;
  - 5 samples is unknown; samples older than the window are ignored;
  - the ring drops samples older than 10 min.

**T4 AdmitV2** — new `internal/resources/admit_v2.go`; do not touch `admit.go` yet.
- Types:
  - `type V2Waiter struct{ ID string; CreatedAt time.Time; Parked bool; Cut bool }` (Cut = the caller's baseline or disk-guard cut);
  - `type V2Reason string` with values `gap`, `danger`, `warning_flat`, `rising`, `behind`, `cut`, `parked`.
- `func AdmitV2(zone Zone, trend Trend, running bool, lastGrant time.Time, waiters []V2Waiter, now time.Time, s Settings) (grant string, gapLeft time.Duration, reasons map[string]V2Reason)`:
  - `running` = any row is held; `lastGrant` = the newest `granted_at` of **any** row, held or ended (zero when none);
  - waiters are in `created_at` order;
  - parked ones get reason `parked` and are skipped;
  - the first non-parked one is the head;
  - a cut head stops everything (head `cut`, the rest `behind`);
  - if `now < lastGrant + gap_s`, the head is `gap`;
  - else apply the spec table; observe gives the head `danger` / `warning_flat` / `rising`;
  - everyone after the head is `behind`;
  - at most one grant;
  - zone unknown grants the head (fail open), whatever the trend.
- `func V2Decision(...)` returns the record fields for the grant: zone, trend, running, gapLeft.
- Tests:
  - one per table cell (13 cells incl. unknown, (M) each);
  - the gap boundary (29.9 s / 30 s);
  - the gap counts from a grant whose row has already ended (M);
  - a parked head is skipped and the next one granted;
  - a cut head stops the walk;
  - trend unknown with a running lease observes in normal;
  - no grant ever means no gap.

Done when: P1a merged, nothing calls the new code yet except tests, and the two docs are in the repo.

## P1b — wire it in (sampler, lease-mode pass, reasons, minutes)

**T5 Sampler: ring, zone, trend** — `internal/module/resources/module.go`, `sampler_loop.go`, `internal/resources/wire.go`.
- The Module owns a `Ring` and a `ZoneLatch`. Every good tick adds a sample and updates the latch, in modes measure, advise and lease.
- The published snapshot's host gains `Zone`, `Trend` (strings) and `ZoneSince` (unix ms of the last zone change; A1 uses it).
- Mode off, failed or unavailable ticks give zone unknown, without adding samples.
- Readers get copies under a small mutex: the pass and the handlers run on other goroutines, while today only the sampler goroutine touches this state.
- `GET /api/resources?samples=1` adds `samples: [...]` (the ring, oldest first).
- Tests:
  - a fake sampler drives readings, and the snapshot shows the zone/trend sequence;
  - measure mode publishes zones;
  - off or unavailable gives unknown;
  - `?samples=1` returns the ring, and it is absent without the flag.

**T6 Pass uses AdmitV2 (lease mode) and the reasons state** — `admission_pass.go`, `store.go`, `lease_handler.go`, new `reasons.go`.
- `passOnce` reads `Active()`/`Waiting()` as today, plus `lastGrant`. It is kept in memory: set at every grant, initialised at boot by `SELECT MAX(granted_at) FROM resource_leases`. The existing baseline and disk-guard cuts become `Cut` on that waiter.
- **lease mode:** grant what `AdmitV2` returns. Remove the deadline/overrun branch. Until P2a, a waiting row past `deadline_at` simply keeps waiting: the client gives up at its own cap and the row is abandoned 30 s later.
- Delete `Admit` (R9), its tests and `plan`'s advise branch. `Charge` and `would_wait_r2` stay as records only: `GrantDecided` keeps writing `dec_sum_charge`/`would_wait_r2` from a helper.
- Migration (ALTER): `dec_zone TEXT DEFAULT ''`, `dec_trend TEXT DEFAULT ''`, `dec_running INTEGER DEFAULT 0`, `dec_gap_ms INTEGER DEFAULT 0`, `parks INTEGER DEFAULT 0`, `wait_reasons TEXT DEFAULT ''`. `GrantDecided` writes the dec fields and `wait_reasons`.
- **Reasons state** (`reasons.go`), exactly per spec D-7 "How `wait_reasons` is counted":
  - per waiting id it keeps `{reason, since, acc map[reason]ms}`;
  - each pass first adds `now − since` to the previous reason, then stores the new one;
  - on grant and on every end of a waiting row (`End` cancelled, `CloseIfExpired` abandoned, and any CAS that ends it), the open interval is flushed first and the JSON goes into the same UPDATE;
  - it has its own mutex; lock order is `stateMu` → reasons mutex; GET and events read copies only.
- Paths:
  - `immediate`: granted by the pass started for its own POST;
  - `waited`: granted by a later pass;
  - `unknown_zone`: granted because the zone was unknown;
  - `overrun` is no longer produced.
- Tests ((M) where marked):
  - lease mode end to end: two requests and a fake host → the second waits for the gap, then for the trend; falling → granted (M);
  - danger with nothing held → waits (M);
  - `wait_reasons`: a row that was `danger` for 20 s, then `rising` for 10 s, then granted, records exactly those (M: charge the new reason instead and the test fails);
  - cancel and abandon flush the last interval;
  - concurrent GET + event + pass under `-race` (no data race, copies only);
  - old-schema DB migrates;
  - no `overrun` path is ever written (M: put the old branch back and the test fails).

## P1c — advise shadow, report, status (deploy in advise; G2 starts)

**T6b Advise shadow queue** — `admission_pass.go`, new `shadow.go`, `store.go`.
- Per spec D-3′ "Mode advise":
  - every request joins the shadow at creation;
  - each pass runs `AdmitV2` over the shadow (real zone/trend, real held rows as running, the shadow's own `lastGrant`);
  - a shadow grant writes `v2_wait_ms`;
  - a row that ends first writes the elapsed time with `v2_wait_open = 1`;
  - `would_wait` = not shadow-granted in the pass of its own POST; the first blocking reason goes into `wait_reasons` (advise rows record the shadow's reasons).
- Real grants in advise stay immediate, as today.
- Migration: `v2_wait_ms INTEGER DEFAULT -1`, `v2_wait_open INTEGER DEFAULT 0`.
- Tests:
  - a burst of three requests in advise: all granted at once; the shadow grants them 30 s apart (gap) → `v2_wait_ms` ≈ 0 / 30 s / 60 s;
  - an ended-before-shadow-grant row gets `v2_wait_open = 1`;
  - restart drops the shadow (rows pending at boot keep −1).

**T7 host_minutes columns** — `minute.go`, `store.go`.
- ALTER `sampled_s`, `normal_s`, `warning_s`, `danger_s`, `pressure_max`, `zone_max`, `waiting_max`.
- `noteMinute` folds them; seconds are counted from the tick interval, like `full_ticks`. `sampled_s` counts every good tick, so zone shares are `x_s / sampled_s`.
- Tests:
  - a minute with mixed zones gives the right seconds, and they sum to `sampled_s`;
  - failed ticks add nothing;
  - a merge on conflict keeps the maxima and the sums;
  - migration (old rows read `sampled_s = 0`).

**T8 Report additions** — `internal/resources/report.go`, `report_handler.go`, `wire_report.go`, `cmd/pdx/lease_report.go`.
- Add, per spec D-7:
  - zone shares from `sampled_s` (legacy rows with `sampled_s = 0` left out, and the coverage says how many minutes counted);
  - wait p50/p90/max overall and by dominant reason (the largest entry of `wait_reasons`);
  - `parks` total;
  - the 5 longest waits, with reasons and dec_zone/dec_trend;
  - advise: shadow waits (`v2_wait_ms`) p50/p90/max, how many were open (lower bounds), and the 5 longest with their reasons. This is G2's input.
- Text output in the existing style.
- Tests: `BuildReport` fixtures for each figure; the CLI prints them.

**T9 `pdx lease ls` and the event** — `cmd/pdx/lease_cmd.go`, `events.go`, `handler.go`.
- The snapshot carries zone, trend, `gap_until`, and each waiter's position, current reason and its `since` (a copy of the reasons state).
- `pdx lease ls` prints a header line `主機：<zone 中文> · 用量<方向中文> · 間隔剩 Ns`, and the queue with position and reason in Chinese (`間隔中`, `危險區`, `警戒且持平`, `用量上升`, `排在後面`, `等待基準`, `保留中`).
- Tests: fake snapshot → expected lines.

**T10 G1 measurement** (spec §6)
- After T5 is in a dev build on mlab, poll `GET /api/resources?samples=1` once a minute for ≥ 30 min of ordinary work, and recompute `TrendOf` for each sample time with the default thresholds (a small Go test helper or `go run` under `.scratch/`).
- Report the direction shares, at rest and with leases. If flat < 60 % at rest, propose thresholds.
- Numbers in the PR body. The lead decides any default change.

Deploy (after P1c): the lead bumps and deploys with mode **advise** on both hosts (V10). G2 starts at the deploy time.

## P2a — park and re-attach (daemon)

Order matters: T13 first (the re-attach lookup needs the session).

**T13 Session for scope process** — `lease_handler.go`, the module's process seam.
- At the top of `handleLeaseCreate`, before replay, re-attach and insert: when `scope=process` and `session_id` is empty, walk `PPID` from `holder_pid` until a pid that the peers registry knows as an agent with the same start time (≤ 16 hops); set `session_id` to that session.
- The PPID/start lookups go through a small interface (`ppid(pid)`, `start(pid)`), implemented by `*agent.ProcessSnapshot` in production and by a fake in tests. The current seam returns the concrete type and its process map is private (`module.go:44`, `process_snapshot.go:54`).
- pid → session: find or add the reverse of `sessionRoot` on the registry seam. If you change anything under `internal/module/peers`, add `./internal/module/peers/...` to the test run and name the change in the PR.
- Tests (fake view): the chain pdx → zsh → claude resolves; no agent within 16 hops gives empty; a pid with a different start is not matched.

**T11 Parking inside the pass** — `store.go`, `admission_pass.go`, `lease_handler.go`, `wire_lease.go`, `handler.go`, `events.go`, `cmd/pdx/lease_cmd.go`.
- ALTER `parked_until INTEGER DEFAULT 0` (the column list of spec D-7).
- In lease mode, `passOnce` (under `stateMu`, before `AdmitV2`) parks every waiting row with `deadline_at <= now` and `parked_until = 0`. One CAS each, as written in spec D-4′ (the predicate includes `deadline_at=?`, so a row re-attached in between is not parked). It counts `parks`, flushes the reasons interval and gives the row reason `parked`; then `wake()`.
- The sweeper's per-second pass covers rows nobody else touches.
- The long poll of a parked row answers at once with `state:"queued"`, `position`, `parked_until`, and does **not** renew `lease_until`.
- `CloseIfExpired` abandons it when `lease_until (= parked_until)` passes.
- `WaiterView`, `addLeases`, the event and `pdx lease ls` show `parked_until` (`保留中，到 HH:MM`).
- Tests ((M)):
  - a pass started by a POST parks a row past its deadline before deciding, so it is never granted (M);
  - parked once, not twice;
  - a poll of a parked row does not move `lease_until` (M);
  - the row behind a parked head is granted;
  - the park expires to `abandoned`;
  - advise mode never parks.

**T11b Polls name their client** — `lease_handler.go`, `cmd/pdx/lease_acquire.go`.
- `GET …/leases/{id}?client_id=<uuid>`. When the row's `client_id` differs, answer `state:"replaced"` and renew nothing.
- Without the parameter (an older client), behave as today.
- Tests: a matching poll renews; a different client gets `replaced` and does not renew (M).

**T12 Re-attach parked rows** — `lease_handler.go` (`handleLeaseCreate`), `store.go`.
- After the replay check (replay of its own `client_id` comes first), look for the oldest parked row (`state='waiting' AND parked_until>0`) with the same `session_id`, `kind` and `weight`.
- Adopt it with the CAS of spec D-4′ (on the old `client_id`). On a lost CAS try the next one; with none left, insert as today.
- Answer 200 with the adopted row. `created_at` is unchanged, so the position is kept.
- Tests ((M)):
  - a parked row is adopted: same id, same position (M);
  - a waiting row that is **not** parked is never adopted (two parallel calls stay two rows) (M);
  - a different kind, a different weight or a different session is not adopted;
  - an ended row is never adopted;
  - two parked rows → the oldest;
  - a replay of its own `client_id` still replays;
  - after adoption, the old client's poll gets `replaced`;
  - two concurrent adopters of one row: exactly one wins, the other gets the next row or a new one.

**T12a Hold default** — `internal/resources/settings.go`: `DefaultDeadlineS = 540`, comment "hold per call"; test the default.

## P2b — clients answer "queued"

**T14 `pdx lease acquire`** — `cmd/pdx/lease_acquire.go`, `exitcodes.go`.
- Polls send `client_id` (T11b).
- `acquireLine` adds `state`, `position` and `parked_until`.
- On `state:"queued"` or `"replaced"`: print the line, send **no** DELETE, exit `ExitQueued = 15`. Neither may fall into the fail-open path (which would run the command).
- ctx cancel still cancels (DELETE) and exits 12.
- Tests in `lease_acquire_test.go` with `fakeLeaseDaemon`: queued → exit 15, no DELETE, position and parked_until printed; replaced → exit 15, no DELETE (M: route it to fail-open and the test fails); cancel path unchanged.

**T15 `pdx lease run`** — `cmd/pdx/lease_run.go`.
- On queued or replaced: stderr gets the spec D-4′ text with N, M, P filled in (P from `parked_until`); exit 15; the command does not run; no release.
- Remove the overrun notice. Keep the waited notice.
- Tests in `lease_run_test.go`.

**T16 The mod** — `cmd/pdx/plugin/purdex/hooks/lease.js`, `lease.test.ts`.
- `readAcquire` understands `state` and `position`.
- On queued or replaced: `return { deny: <spec D-4′ text, filled in; P from parked_until> }`, without calling `next(e)` and **without** the release in `finally` (track a flag).
- Exit 15 from `$.process.run` is a normal answer here, not a failure: check how `$.process.run` reports a non-zero exit and read stdout anyway.
- Remove the overrun note.
- Tests in `lease.test.ts`: queued → deny text with position, the tool never ran, no release call; granted → as today; daemon down → runs (fail open).

**T17 Skill text** — `cmd/pdx/plugin/purdex/skills/pdx-lease/SKILL.md`: queued is normal; re-run the same command later; never `&`, `run_in_background` or a wrapper to bypass; `pdx lease ls` shows why it waits.

**T18 M-P1 probe** (spec §6)
- Write the method and the result in the PR body. Measure in a throwaway tmux session with `claude -p` or an interactive session and a throwaway plugin dir, as v1 M-R2 did: a foreground Bash that the mod classifies heavy, and a Bash timeout shorter than the command.
- Record whether `next(e)` resolves at the background move and when the release fires.
- Do not change behaviour based on it in this PR.

After P2b merges and G2 has run, the lead decides the switch to mode `lease` on both hosts, does it with `PUT /api/hostconfig/resources`, and tells the user.

## P3a — anomaly rules

**T19 Alerts store** — `store.go`.
- Table `resource_alerts` exactly as spec D-7: `subject` is a real column (the lease id, or `host`); partial unique index `(rule, subject) WHERE closed_at = 0`; `sends_json` holds per-recipient state.
- Functions: `OpenOrTouch(rule, subject, …)`, `RecordSend(id, recipient, ok, err, at)`, `Close`, `Open()` list; 14-day prune inside the existing hourly prune.
- Tests: opening twice keeps one row; close then reopen makes a new row; per-recipient send state; prune.

**T20 Rules engine (pure)** — new `internal/resources/anomaly.go`.
- `func Evaluate(in AnomalyInput, s Settings) []AlertCond`. `AnomalyInput` holds:
  - zone and zone-since;
  - trend;
  - held rows `{ID, Kind, Scope, SessionID, GrantedAt, RSSBytes, HolderPPID}`;
  - host memory bytes;
  - per-kind hold limits;
  - the head waiter `{ID, CreatedAt, Reason, ReasonSince}`, taken from the reasons state (a `cut` reason with its `since` is how "cut for ≥ 10 min" is known);
  - whether the sampler is available.
- It returns A1–A5 conditions per spec D-6, A5 with its diagnosis a/b/c.
- Tests:
  - each rule fires at its edge and not one second before ((M) each);
  - A2's limit: fallback 30 min under 10 rows, floor 15 min, 3 × p90;
  - A4 only for scope process;
  - A5 diagnosis a/b/c cases, including "a wait ≥ 30 min is c under any diagnosis" and "cut ≥ 10 min is c".

**T21 Kind hold limits** — `store.go`: p90 of `ended_at − granted_at` per kind over 14 days, over every row that was granted and has ended (any end reason); cached for 1 h. Tests with seeded rows, including `holder_gone` and `expired` rows in the sample.

**T22 Check step** — `sweeper.go`, `anomaly_step.go`.
- Every 5 s (not every 1 s tick) in the sweeper, outside `stateMu`:
  - build the input: zone/trend from the snapshot, held rows plus `leaseUse` RSS, the holder PPID from the sweep view, the head's reason from the reasons state;
  - run `Evaluate`;
  - open, touch or close in the store;
  - decide what is due per recipient: new, or last success ≥ 15 min ago, or a failed attempt whose backoff passed (5 s doubling to 1 min).
- A5 a/b are records only: they produce nothing due and never touch send state. A5 turning c is due at once.
- Sending is P3b; here a sender interface (fake in tests) receives the due items, and `RecordSend` stores each result.
- Tests with the sweep fixtures:
  - a hung lease → A2 opens once, re-sends after 15 min, closes when released;
  - a failed send is retried with backoff, while the other recipient's success stands;
  - A5 a → c sends at once;
  - A5 a never sends (M).

**T23 Status surfaces** — `handler.go`, `events.go`, `wire.go`, `cmd/pdx/lease_cmd.go`, `report*`.
- The snapshot (GET and `resources.changed`) carries `alerts: [...]` (open alerts, App contract shape).
- `pdx lease ls` lists them: the rule in Chinese, the subject, since when.
- The report shows alerts by rule and diagnosis.
- Tests: GET and event include the open alerts; ls prints them; the report counts them.

## P3b — delivery

**T24 Team seam** — `internal/team` (interface + key), `internal/module/team` (register).
- `team.LeaderLookup.LeadOf(sessionID) (leadSessionID string, ok bool)`, registered under a new key.
- resources looks it up **at send time**, every time (cheap registry Get), like the pause outbox does. No Init lookup and no new dependency edge, so module order cannot make it silently missing.
- Tests: present → used; absent → that recipient stays unsent and is retried.

**T25 Session notices** — resources gets the peers `Sender` and the `OriginResolver` (`ResolveOriginBySession`, `InboxOf`) from the registry, at send time.
- Holder that is a team member: from the lead's inbox to the member's address; copy from the member's inbox to the lead's address (spec D-6 Delivery).
- Holder that is a solo session or a lead: a user alert naming the session (T26). Never attempt a self-send (`peers/send.go:515` refuses it).
- Text (Chinese, one paragraph): the rule, the figures, that nothing is ended or killed, and `pdx lease ls` to look. The lead's copy has a prefix line with the member's address.
- `ResultDeliveryUncertain` counts as sent; any other failure is retried (T22).
- Tests with a fake sender: member holder → both sends; solo holder → user path, no peer send (M); a failed lead send does not block the holder's success.
- Open a follow-up issue: "daemon sender identity for notices to solo sessions and leads".

**T26 User alerts** — `internal/resources` interface `AlertNotifier`, registered by the push module like `team.PauseNotifyKey` (`push/module.go:232`); new push `Kind` `resources`.
- Looked up at send time. If it is absent, or `alert_push = 0`, there is no push and nothing counts as a push success.
- No presence suppression.
- Always `core.Events.BroadcastEvent` type `resources.alert` with the App contract value; closing emits it with `closed_at`.
- Tests: push only with the flag on and the notifier present; notifier appearing late is used on the next due send; the event is always emitted; the close event.

### App contract (for 88)

- WS event `resources.alert`. Value: `{id, rule: "danger_long|held_long|mem_spike|holder_adopted|queue_stuck", diagnosis: "a|b|c|", opened_at, closed_at (0 while open), host: {zone, mem, pressure}, lease: {id, kind, session_id, age_s, rss_bytes} | null, text}`. One event per send and one on close.
- Push kind `resources`: title `{主機}：資源警報`; body = `text`. Tapping it opens the host's resources view (88 decides where); collapse id = alert id.
- 88 turns `alert_push` on in host settings once iOS ships the kind.

## Order and owners

P1a → P1b → P1c (deploy advise, start G2) → P2a → P2b (G2 review, switch to lease) → P3a → P3b. One Sonnet member implements in order; the lead reviews each PR, releases it, and bumps and deploys after P1c, P2b and P3b (the others merge without a deploy, or ride the next bump). 88 gets the App contract when P3b starts.
