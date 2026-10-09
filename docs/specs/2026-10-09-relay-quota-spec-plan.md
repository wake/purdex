# Per-session relay quota under unattended mode (#2062) — spec + plan

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_3d0nql`). Line: lead/member (A); App surface: interface line (88).
Supersedes nothing; changes U23 (`docs/specs/2026-10-08-unattended-mode-spec.md`) for `self_relay` only.

## 1. User decisions (2026-10-09, confirmed as five rules — do not reopen)

| # | Rule (user's words, confirmed) |
|---|---|
| R1 | 每個 session 一個「自動接力額度」，預設 0，**只能在 App 的無人值守面板設定**，agent 沒有任何指令能改。 |
| R1 邊界 | **沒有 pdx 指令能改；持 host token 的程式在技術上仍能呼叫路由，與無人值守開關相同，靠 client 宣稱＋稽核（Addr）＋skill 禁止。** 本 spec 不宣稱已技術強制（codex plan review #1，1f 裁定 (a)）。 |
| R2 | **只在無人值守開著時生效**：額度 ≥1 就自動核准並扣 1；額度 0 就掛著等人核准。使用者在場（無人值守關）時照舊人工核准、**不扣額度**。 |
| R3 | 額度**跟整條接力鏈共用**：給 A 3 次，A→A′ 剩 2，A″ 剩 1，用完為止。 |
| R4 | member 自己沒有額度；lead 另有一池「member 額度」（預設 0、App 設定）；無人值守時 lead 幫任何 member 接力都從池子扣 1，池子 0 就等人；lead 自己的接力用自己的額度，兩池不互扣，都跟 lead 的接力鏈走。 |
| R5 | 顯示：無人值守面板（列出並設定）、team 面板（lead／member 列）、`pdx team`（唯讀）。 |
| — | Accepted consequence: after this ships, **a session with no quota is no longer relayed automatically while unattended**. |

## 2. Facts this spec stands on (main @ 2026-10-09, alpha.632)

- Lineage: team.db `session_lineage(session_id PK, predecessor_session_id, predecessor_ref, op_id, at)` (`internal/module/team/relay_store.go:54-60`), written with the op's `cleared` in one transaction (`relay_store_report.go:218`, `checkLineage` `:35-46`). `Store.ChainRoots()` (`relay_store_lineage.go:74`) maps every session to its chain root by a full scan; a session never relayed is its own root.
- U23 auto-approve paths, all under `createMu` (`unattended.go`): create-time lead (`createApprovedLead` `:131`), adopt (`adopt_handler.go:273`), **self relay at begin** (`beginApproved` `:161` → `store.CreateSelfRelayApproved` `store_unattended.go:30-66`, one tx), and the sweeps — switch-on (`unattended_handler.go:106`), tick (`reconcileUnattended` `:318`), boot (`module.go:435`) — via `autoApprove` (`:189`) → `m.approve` → `store.CloseSelfRelayApproved` (`team_store.go:221`) → `closeSelfRelayApprovedIn(tx, …)` (`:245`). Both self-relay approve paths meet in `closeSelfRelayApprovedIn`.
- **Member relay is not built**: `POST /api/team/relays` and `pdx relay <ref>` are plan v3 P6-2b (`docs/specs/2026-10-06-lead-team-relay-plan-v3.md`), not on main (`relay_handler.go:185` only names the route in an error).
- Unattended routes are "App only" by the caller's own claim `client.kind == "app"` (`unattended_handler.go:92-96`, comment "this tells, not enforces") plus the skill rule (`SKILL.md:18`). There is no token-level restriction; the same model is used here.
- Display carriers: `GET /api/team` → `team.Member` (`wire_team.go:168`); roster → `team.RosterSession` (`wire_roster.go:17`, lead and members); unattended → `team.UnattendedView` (`wire_unattended.go`).
- `session_prefs(session_id, self_relay_paused, updated_at)` (`relay_store.go:61-65`) is per session, not per chain — the quota does not go there.

## 3. Behaviour

### 3.1 Storage and identity (R1, R3)
- New table `relay_quotas(root_session_id TEXT PRIMARY KEY, self_left INTEGER NOT NULL DEFAULT 0, member_pool_left INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL DEFAULT '')` (`CREATE TABLE IF NOT EXISTS`; team.db is deployed, a new table needs no column migration). No row = both 0.
- The key is the **chain root** of the session: walk `session_lineage` parents by primary key inside the caller's transaction (`chainRootIn(tx, sid)`: an iteration with a visited set and **no fixed depth** — the lineage contract is uncapped, `relay_store_lineage.go:17-20`; only a cycle is an error). A relay never copies or moves a quota: every session of the chain reads the same row (R3 for free). **Reads always go through the chain root.** A session the registry lists but whose `cleared` has not committed yet has no lineage row, so it is its own root; the route answers `pending_lineage: true` for it, and a row written under that provisional root is **not migrated** when the lineage is later written — it is orphaned (harmless: nothing reads it, because every read walks to the real root).
- Values are integers 0–99. `updated_by` holds the client label (audit), never a token.

### 3.2 Setting (R1, R5)
- `PUT /api/team/relay-quota` `{session_id, self_left?, member_pool_left?, client:{kind:"app", label}}`: sets absolute values for the session's chain root (fields left out unchanged); `client.kind` must be `app` (400 otherwise) — the same "tells, not enforces" rule as the unattended switch; the remote address is logged. Answers the stored row. Emits `team.relay_quota` `{root_session_id, self_left, member_pool_left}` and `rosterChanged()`.
- `member_pool_left` is accepted for any session (a pool on a session that never leads is simply unused; the App only offers it on a lead).
- **No `pdx` command writes a quota.** The skill gains one line beside the unattended rule: agents never call this route, never ask for more quota; quota is the user's.

### 3.3 The rule (R2)
- Only an **automatic** approval of a `self_relay` consumes quota. "Automatic" is an **internal flag**, `Close.Auto`, set only by `autoApprove` and `beginApproved` (never by anything an HTTP body can produce), **not** the wire `client.kind`: `handleDecide` only requires `kind`/`label` to be non-empty today, so a person's or an agent's decide request could otherwise pose as `unattended` (codex plan review #2). `handleDecide` also refuses the reserved kind `unattended` (400). In `closeSelfRelayApprovedIn`, when `c.Auto`, inside the same transaction: resolve the root, `UPDATE relay_quotas SET self_left = self_left - 1 WHERE root_session_id = ? AND self_left >= 1`; zero rows → return `ErrQuotaExhausted` and roll back (the row stays `open`).
- `beginApproved` (create-time, unattended on): the same check in `CreateSelfRelayApproved`'s transaction; on `ErrQuotaExhausted` the request is created **open** exactly as with unattended off (the session's mod waits for a person as it does today).
- The sweeps (switch-on, tick, boot) meet `ErrQuotaExhausted` per row: no log line per tick; the module keeps an in-memory "held for quota" set (logged once per row) and retries each tick — so a quota the user raises from the App (or iOS) while away approves the held row within one tick.
- A **person's** click (`client.kind` app/terminal) approves as today and consumes nothing (R2: present → manual, no deduction).
- `lead`, `adopt` auto-approval are unchanged (quota is about relays only).
- A member never self-relays (unchanged); its own `self_left` is not read while it is a member and applies again after release.

### 3.4 Member pool (R4) — depends on P6-2b
- When the lead starts a member relay (`POST /api/team/relays`, P6-2b) **while unattended is on**: in the op-creating transaction, decrement the **lead's** chain-root `member_pool_left` (≥1) and start the op; at 0, create a `member_relay` approval row (new kind, auto-approvable only through the pool) and leave the op `awaiting_approval`; the lead's `pdx relay <ref>` waits in the foreground like `pdx lead request` (Bash `timeout: 600000`), exit 0 on approval, 12 on deny/cancel.
- Unattended off: member relays need no approval and consume nothing (unchanged from P6's own design).
- Sweeps retry held `member_relay` rows like held self relays (a raised pool approves them within one tick).
- This section is the contract P6-2b must leave room for; it is implemented in RQ-2 right after P6-2b.
- **Cross-host member relay (P6-7a) is out of scope here:** the lead's pool and a member on another host live in different databases, so the pool deduction of a forwarded member relay (reserve on the lead's host with an idempotency key, finalise after the remote create-or-join succeeds, compensate on permanent failure) is settled in P6-7a's own plan. RQ-2 is the local path only.
- **RQ-2 needs its own small spec first** — the `member_relay` approval's state machine: approve / deny / cancel / expiry and what each does to the op, when the control message is sent, how the op long-poll wakes, restart reconciliation, the membership re-check at approve, `KindMemberRelay` in `AutoApprovable`, the SPA parser and card, the events. It goes through one more codex round before RQ-2 starts.

### 3.5 Display (R5)
- `team.RosterSession` and `team.Member` gain `relay_quota: {self_left, member_pool_left}` (omitempty when both 0? **no** — always present when the capability is on, so the App can show 0).
- `team.UnattendedView` gains `quotas: [{session_id, root_session_id, title, address, self_left, member_pool_left, is_lead}]` for the host's live sessions and `held: [Approval]` — the open self_relay / member_relay rows held for quota (so the panel can say 「額度用完，等你核准」).
- `pdx team` prints the lead's own quota and member pool in its header and nothing per member (members have none); `--json` carries the fields.
- Capability `team.relay_quota.v1`, appended last.

### 3.6 Release order (the consequence)
- The daemon rule (§3.3) changes what unattended does. It must not reach the user before the App can set quotas. Daemon deploys are frequent (every merge of either line), so a "deploy them together" promise is not enough: **the rule sits behind a host switch**, its **own host-config key `relay_quota`** (`{"rule": bool}`, default **false**, its own `PUT /api/hostconfig/relay_quota`; read per decision, so flipping it needs no restart). It is **not** a field of the `relay` key: that payload is a full replace whose missing booleans reset to their defaults (`hostconfig/relay.go` `relaySwitchesOf`), so an older App saving the relay switches would silently write the rule back to false (codex plan review #5). Off: unattended auto-approves self relays exactly as U23 does today and nothing is consumed (quotas are stored and shown only). On: §3.3 applies.
- RQ-1a and RQ-1b merge and deploy whenever ready, with the switch off. The coordinator turns the switch on (PUT `/api/hostconfig/relay_quota`) **only after 88's RQ-A is merged and the main checkout is fast-forwarded**, and says so to the user in the same message. Turning it back off is the rollback.
- The switch is the user's, like the unattended switch: no `pdx` command, and the skill forbids agents to change it.

## 4. Plan

| PR | Scope | ≈ lines / files | Owner | Deploy |
|---|---|---|---|---|
| RQ-0 | the provenance fix, deployable alone: `Close.Auto` (set only by `autoApprove` / `beginApproved`), `handleDecide` refuses the reserved kind `unattended` (400), a forged-decider mutation test — it also repairs the audit trustworthiness U23 already has | 150 / 4 | η | daemon, alone OK |
| RQ-1a | table + `chainRootIn` + `PUT /api/team/relay-quota` + event + view fields + capability (no rule yet) | 450 / 9 | η | daemon, alone OK |
| RQ-1b | the rule behind host-config key `relay_quota` (own key and PUT route, default false; a test: an older App's full-replace PUT of `relay` leaves it true): `quota.go` holds the quota logic and the held set (not `unattended.go`); `closeSelfRelayApprovedIn` / `CreateSelfRelayApproved` consume; `ErrQuotaExhausted`; sweeps hold quietly and retry; `UnattendedView.held` | 450 / 8 | η | any time, switch off; coordinator turns it on after RQ-A |
| RQ-1c | `pdx team` quota display (+ `--json`); skill line; embed pin | 150 / 4 | η | CLI + `pdx setup` |
| RQ-A | App: unattended panel lists sessions with steppers (self, and pool on leads), held rows; team panel shows quota (in TI-4 if the team panel is not out yet) | — | 88 | SPA; then the coordinator turns `relay_quota` on |
| P6-2b | member relay (`POST /api/team/relays`, `pdx relay <ref>`, claim, op long-poll) — plan v3, re-aligned to main first | 650 / 6 (plan v3) | η | daemon + CLI |
| RQ-2 | member pool (§3.4) on top of P6-2b — **after its own state-machine small spec and one more codex round**; local path only | 350 / 6 | η | daemon + CLI |

Tests (each PR): store — root walk (no lineage, 3-deep chain, **a chain of more than 256 hops returns the same root**, cycle refused); `pending_lineage` for a session whose `cleared` has not committed (claimed → cleared before/after, a rolled-back and retried cleared, lead and member roles moving with the session id, adopt / release interleaved), the orphaned row is never read; a decide request posing as `unattended` neither consumes nor is accepted; consume only on unattended decider, never on a click; 0 → stays open, no deduction; concurrent two auto-approvals with self_left=1 → exactly one approved (one tx each); raising quota approves a held row in one tick; release restores reading `self_left`; route refuses `kind != app`, values outside 0–99, unknown session. Mutations: consume on click; consume outside the tx; no root walk (keyed by session); sweep logs every tick; rule applied while `relay_quota` is false; consume on the wire kind instead of `Close.Auto`.

Measurements before RQ-1b ships (610 rule): the per-row root walk inside the approve transaction — time one approve with a 10-deep chain on mlab's team.db; the tick runs once a second over open rows only.

## 5. Coordination
- 88: RQ-A design (stepper in the unattended panel, held list wording 「額度用完，等你核准」, team panel badge) and that RQ-1b deploys with it.
- Users of U23: the release note says plainly that sessions need a quota to be relayed while unattended.
