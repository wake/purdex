# Lead / member / team and context relay — spec

Status: **passed review by `air26/_9iwyyv` on 2026-10-06** (c358cd65 plus the released-prompt note), then revised for U5b. **Paused 2026-10-07 by the user**: no plan, codex review or implementation until the user resumes it.
- Spec writer: `mlab/purdex-4d` (`mlab/_v3o1ps`).
- Source: the brief `docs/ideas/2026-10-06-lead-team/brief.md` (untracked on mlab's main checkout), with the prototype mod `relay-mod/` and its handoff `handoff-run1.md` beside it.
- Research page: `https://pages.mlab.host/wake/purdex/context-relay.html`.
- The U5 strength question went through two answers on 2026-10-06. First U5a (human presence in v1), which U5b then withdrew: an approval is one click on any App, and the U5a layer of no CLI path, broadcast and audit stays (§2, §6.5).
- U13 (self-relay switches and approval) was added the same day. Its derivations (a)–(d) are in §8.7, and air26's review points (e) and (f) are in §8.4 and §8.3.
- U13a and U14 followed the same day: self-relay approval is one click, and the browser SPA is retired, so every client is a Purdex.app.

Every place where this spec departs from the brief's design draft (brief §5, D1–D8) is marked **⟲ changed from D…**, with the reason. §13 lists all of them.

## 1. Goal

The user manages the context of long tasks by hand today:

- **Parallel work.** A session A discusses the requirement and then develops it. For parallel work, A opens subagents, or the user opens extra Purdex tmux sessions and hands their addresses to A.
- **Handoff.** When any session has less than 30% context left, the user opens a new tmux session and tells the old one "hand the task over to {peer}".

This spec makes two things automatic:

1. **Self relay:** a single session hands off to itself, almost unnoticed. U13 adds one visible step: each self relay is approved by the user first (§8.7). "Almost unnoticed" covers the relay itself.
2. **Lead with a team:** a lead can open members, and can relay a member on the member's behalf.

## 2. User decisions (2026-10-06, do not reopen)

Copied verbatim from the brief §2.

| # | 決策 |
|---|---|
| U1 | 接力門檻：**已用超過 70%（剩餘低於 30%）時觸發** |
| U2 | 交接檔由 **agent 自己寫**：用 mod `$.prompt.submit` 送進對話，讓它有完整工具（git、讀檔、pdx）。**不用** `$.model.fork`（不能用工具），**不用** Haiku 代寫 |
| U3 | 定址**一律用 ref**。ref 在 `/clear` 後會變（見 F2），由 Purdex 負責讓 ref 繼續有效（設計草案用轉址鏈） |
| U4 | 詞彙改為 **lead / member / team**。PRODUCT.md §3.5 的 Role（worker / operator）與 §6.1 Operator 要配合修改。member 不能叫 worker，worker 是 Nexen 無頭執行的用詞 |
| U5 | 進入 lead 模式：由 session 判斷工作夠大、可以平行時，**向使用者申請**；**核准走 UI 層**，不能由 CLI 自己核准（cld-yolo 是 bypass 模式，模型能跑任何指令） |
| U6 | 核准要**同時推到所有 client**（瀏覽器 SPA、Purdex.app、其他裝置），**任一個 client 回應後，其他 client 的提示一起結束** |
| U7 | 申請期間要**鎖住 session**：在收到回應或逾時之前不能繼續做事。第一版採軟鎖（見 D3），**逾時視同拒絕** |
| U8 | lead 模式下，lead 能**主動開 tmux 加 session，並拿到它的 ref**（spawn） |
| U9 | member 的接力**由 lead 決定、由 lead 發動**，member 不自己觸發。**daemon 只做機械式的偵測、通知與執行，不替 lead 做決定**（包括沒有「daemon 到某條硬線就自動 relay」） |
| U10 | spawn 時**建議**用 worktree，但最後**由 lead 自己安排** |
| U11 | member 會有新的視覺，**另外獨立處理**，不在這份範圍 |
| U12 | daemon 重啟（Purdex 開發自己時常發生，**5–10 秒內恢復**）不能讓等待中的申請或 pdx 指令壞掉，見 D6 |

**Supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it in answer to this spec's §6.5 question.

| # | 決策 |
|---|---|
| U5a | U5 第一版就要做**「人在場驗證」**，不能只做到「看得見」。「不提供核准路徑、所有決定廣播加稽核紀錄」照樣保留，當作附加的一層 **（已由 U5b 撤回）** |

**Second supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U13 | 自我接力的開關與核准<br>- 一般 session（不是 lead 也不是 member）：自我接力預設開，可以關閉。<br>- lead：自我接力預設開，可以關閉。<br>- member：自我接力預設關，接力必須由 lead 安排（和 U9 一致）。<br>- 自我接力（一般 session 與 lead）每次都要先經過人類核准，比照 lead 模式：走同一套 UI 多 client 推播與 U5a 的人在場驗證（已登記的 App 加 SE 金鑰）。**（此點已由 U13a 修正）**<br>- lead 要求 member 接力（pdx relay）不需要核准，由 lead 自己安排。 |

**Third supplementary decisions.** The user made them on 2026-10-06, and `air26/_9iwyyv` relayed them.

| # | 決策 |
|---|---|
| U13a | 修正 U13：自我接力的核准**只要按一下確認，不用 Touch ID，也不需要人在場驗證**；任一個 client 按下都算。人在場驗證（U5a）**只用在 lead 申請**。自我接力仍然走同一套多 client 推播與「任一個回應就全部結束」 |
| U14 | 瀏覽器版 SPA 退役（使用者原話：「SPA 直接退役」） |

**How this spec reads U14** (air26's reading):
- Purdex.app is the only client.
- The App still renders the same SPA code.
- The Vite dev server stays for the App's HMR.

In this spec, U14 is **a premise only**. Turning the browser version off is not in any phase here.

**Fourth supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U5b | lead 申請不需要 Touch ID 或人在場驗證，和自我接力一樣，任一個 App 按一下就核准。理由：cld-yolo 本來就能用 Bash 自己開 tmux 和 claude，lead 模式沒有給 agent 新的能力，核准的意義是「告知與同意」，不是安全邊界。U5a 原本那一層照樣保留：pdx 沒有核准指令、skill 明文禁止自我核准、每筆決定都廣播並寫稽核紀錄。Secure Enclave 方案移到 §11（之後硬化時再做）。 |

Also decided on the research page (§9 "已決定"): `session.compact` is a safety net, so that auto-compact cannot get in before the relay.

## 3. Facts

### 3.1 Prototype (air26, Claude Code 2.1.291, Opus 5.5 1M; brief §3)

- **F1** The whole loop works:
  1. at the threshold, `prompt.submit` asks the agent to write the handoff;
  2. `turn.complete` checks the file;
  3. `$.command.run({command:'clear'})`;
  4. `classic.SessionStart` with `source==='clear'`;
  5. `prompt.submit` sends the takeover prompt;
  6. the new conversation restates the handoff and checks it with git.

  From trigger to the end of the takeover turn: about 40 s.
- **F2** After `/clear`: same pid, same tmux pane, the pdx name is kept, **the ref changes** (it derives from the sessionId).
- **F3** `prompt.submit` and `command.run` are refused inside a `command.run` hook. Sending them from `$.clock.after(100, …)` works. The mod's own `session.start` does not fire again after `/clear`, so the takeover hooks `classic.SessionStart`.
- **F4** An empty session starts at about 60K tokens here, about 6% of a 1M window.
- **F5** The handoff carried verbal decisions, open questions, the venv path and dead ends correctly.

### 3.2 Measured for this spec (2026-10-06, Claude Code 2.1.291; labelled M to keep them apart from phases)

A probe mod was loaded into a throwaway `claude` in tmux through `CLAUDE_CODE_PLUGIN_DIRS`.

- **M1 A pdx message can be a private control channel to a mod.**
  - `pdx msg send` to the session raised `session.receive` with `origin = {"kind":"peer"}`. It is not `peer-send-message`, so a matcher must not key on that. The text was the `<cross-session-message from=… from-name=…>` envelope.
  - Returning `{consumed}` kept it out of the model: the transcript holds no trace of it.
  - From a `$.clock.after` timer, the mod then:
    1. called `$.prompt.submit` (the turn ran);
    2. called `$.command.run({command:'clear'})` from `turn.complete`;
    3. saw `classic.SessionStart{source:'clear'}` with a new session id.
  - The ref changed (`_vstjse` → `_y6vgm3`), and the name was kept.
- **M2 `CLAUDE_CODE_PLUGIN_DIRS` loads a plugin into a tmux-launched interactive `claude`**, the same as `--plugin-dir`. It can also be set in the `env` block of `~/.claude/settings.json` (Claude Code plugin docs).
- **M3 After a `/clear`, the old ref is gone.** `pdx msg send mlab/_vstjse` answers `peer_not_found`. Its hint still says a ref "never changes", which is false after `/clear`.
- **M4 The CC registry's `cwd` follows `EnterWorktree`.** `~/.claude/sessions/45325.json` reads the worktree path, while `pdx peers` shows `/Users/wake` for the same session.
- **M5 The same plugin folder given twice loads once.** `CLAUDE_CODE_PLUGIN_DIRS=X` together with `--plugin-dir X` raised `session.start` once.
- **M6 An ad-hoc signed binary can use the Secure Enclave without any entitlement.**
  - Probe: a `swiftc` build, signature `adhoc,linker-signed`, no Team ID, no entitlements.
  - It created a CryptoKit `SecureEnclave.P256.Signing.PrivateKey`, signed and verified, and reloaded the key from its 284-byte `dataRepresentation` blob.
  - The blob is a file the program keeps. Nothing goes into the keychain.
- **M7 A user-presence key could not be created on mlab.** Creating one with `[.privateKeyUsage, .userPresence]` failed with `-25308` (`errSecInteractionNotAllowed`, AKS `-536870174`), both from tmux and from a `gui/501` LaunchAgent.
  - mlab's console is locked (`CGSSessionScreenIsLocked=Yes`), which explains it.
  - **Creation on an unlocked workstation is not measured yet.** Measure it if the §11 hardening is taken up.
- **M8 Origins the UI runs on:**
  - **Purdex.app** loads `app://./index.html`: a custom secure scheme whose host is `.` (`electron/main.ts:24-27`, `electron/window-manager.ts:81`). When the dev server answers, it loads `http://100.64.0.2:5174` instead (`window-manager.ts:78`).
  - **The browser SPA** is the Vite dev server `http://100.64.0.2:5174`.
  - **`https://purdex.mlab.host`** proxies to mlab's daemon API only, and `GET /` answers 401. No SPA is served over https today; the web version is unmerged.
- **M9 a19 has Touch ID.** `MacBookAir8,1`, Apple T2 chip, `bioutil` reports biometrics on for unlock, macOS 14.8.9.
- **M10 Electron's Touch ID WebAuthn needs a real signing identity.** `app.configureWebAuthn({ touchID: { keychainAccessGroup } })` exists, but Chromium's Touch ID authenticator requires the `keychain-access-groups` entitlement and a matching provisioning profile (Electron docs).
  - Purdex.app is ad-hoc signed; the signing roadmap's Apple Developer stage is not done.
  - Keychain items created without that entitlement are reported to fail with `-34018` (Apple developer forums; not measured here).
- **M11 Every new turn passes `prompt.submit`, and a hook can hold it past 10 s.** Probe mod, idle session; prompts whose text held `HOLD15` were held:
  - **A peer message** (`pdx msg send`) raised `session.receive`, then `prompt.submit` with `origin {"kind":"peer"}`.
  - **A typed prompt** raised `prompt.submit` with `origin {"kind":"composer"}`.
  - In both cases the hook awaited `$.process.run(['/bin/sleep','15'])`, was released 15 s later, and only then did `turn.start` fire. The 10 s hook budget did not cut it.
  - While held, the typed prompt shows as sent, with the busy spinner.
- **M12 A plugin's re-submitted prompt is not the original** (2.1.291 types, `PromptSubmitArgs.asUser`).
  - `asUser: true` removes the "The <plugin> plugin sent a message" frame.
  - But "`@file` mentions and pasted images are not expanded for a plugin's prompt, `asUser` or not", and the transcript still names the plugin.

### 3.3 Code (re-verified on origin/main `de37a4e5`, alpha.505)

The brief's §4 is mostly right. **Corrections** are marked ✱.

**Context usage**
- ✱ The statusline snapshot is **raw JSON, never parsed**: `json.RawMessage` (`internal/module/agent/handler.go:1231-1234`).
  - Its map is keyed by **pdx session code**, resolved from the tmux session *name* (`handler.go:1273`, `:1280`). Two CC panes in one tmux session overwrite each other.
  - It lives in memory only, and is not cleared when a session dies (`handler.go:1228-1230`, `:978-983`).
  - The payload carries `session_id`, `cwd`, `workspace.current_dir`, and `context_window.{used_percentage, context_window_size, …}`. `used_percentage` can be null early in a session.
  - Nothing reads the percentage: the SPA keeps `ccStatus` only for the statusline self-test.
- It is posted on every CC statusline refresh: a new assistant message or a mode change, with a 300 ms debounce. So an idle session does not refresh.

**Peers**
- `pdx peers` has no context column. `--json` exists and prints the `/api/peers` envelope (`cmd/pdx/peers.go:432-438`).
- **CWD bug confirmed.** `internal/tmux/executor.go:198` (`session_path`) → `internal/peers/record.go:173` `rec.Cwd = s.Cwd`.
  - `entry` rows already use the registry cwd (`record.go:344`).
  - The session-row branch ignores both the registry cwd and `owner.Cwd`, although both are in hand (`record.go:231-252`).
- **Ref** = `"_" + base36(FNV-1a-64(sessionId) mod 36^6)` (`internal/peers/ref.go:111-124`). There is no alias or predecessor anywhere.
- **Title** is stored per session id (`internal/store/peer_label.go:74-75`), so a `/clear` loses it today.
- **Delivery** goes to the registry's `messagingSocketPath`, which is per pid (`internal/peers/registry.go:42`). A `/clear` keeps the inbox; only the session id and ref change.
- **Resolution runs on the sending daemon** over the target host's rows, fetched from its `/api/peers` (`internal/module/peers/send.go:350-378`). So a field on the rows reaches remote senders with no new endpoint.
- The daemon has **no internal send API**: `deliverLocal` is HTTP-bound (`send_local.go:47-58`). The parts exist:
  - `ccuds.BuildFrame` / `WriteFrame` (`internal/peers/ccuds/frame.go:36,163`);
  - `ccuds.StartVirtualPeer` for a daemon-owned reply address (`virtual_peer.go:102`).

**Hooks and frames**
- ✱ `pdx hook` is fire-and-forget (`cmd/pdx/hook.go:86`). The 2 s is only the HTTP timeout: the tmux and `ps` steps before it have none.
  - There is no spool, queue or replay anywhere. Events during a daemon outage are lost.
  - The daemon is unreachable from the old image's listener close until the new image listens (`cmd/pdx/main.go:271-306`).
- ✱ `PreToolUse` **is** installed, with no matcher and no timeout, so it runs synchronously on every tool call (`internal/agent/cc/hooks.go:213-222`). It is observe-only: it never writes a decision.
- `/clear` gives `SessionEnd{reason:clear}` (old id), then `SessionStart{source:clear}` (new id). The pane's frame is replaced with a new `frame_id` and `session_id` (`internal/module/agent/frame_ops.go:136-173`).
- `SubscribeSessionStart` exists. Its only subscriber is nex's manual-resume (`internal/module/nex/manual_resume.go:66`).

**Sessions and launch**
- `POST /api/sessions` takes only `{name, cwd, mode}`. A duplicate name is **409**, never reused (`internal/module/session/handler.go:158-159`).
- `send-keys` with `expected_tmux_instance` sends bytes literally, behind a generation check (`internal/tmux/send_keys_conditional.go:68-100`).
- The only daemon-side "create tmux + start CC + wait" is nex take-to-terminal:
  - it waits for CC with `waitForCC`, 15 s;
  - then for a verified frame with `afterResume`, 3 s;
  - on timeout it kills the session (`internal/module/nex/handoff_steps.go:102-113`, `recent_resume.go:16-96`, `take_to_terminal.go:285-295`).
- `cld-yolo` is the user's shell alias for `claude --dangerously-skip-permissions`. The daemon never names it.
- **No worktree API** anywhere. A session's `cwd` must already exist (`internal/module/session/cwd.go:44-50`).

**Events and UI**
- `EventsBroadcaster.BroadcastEvent` sends `HostEvent{type, session, value string}` to every subscriber (`internal/core/events.go:14-22, 172-185`):
  - non-blocking, 64-deep buffers, **drop when full**, no replay;
  - `OnSubscribe` gives a module a snapshot hook per new subscriber (`events.go:216-250`).
- The SPA connects to every host (`spa/src/hooks/useMultiHostEventWs.ts:130`) and ignores unknown types (`:172-234`).
- **No precedent** for "daemon holds a request, any client answers, the rest close". The closest:
  - the store-driven singleton dialog `HandoffDialogHost`;
  - the per-nonce wait of the statusline self-test.
- Electron notifications are title + body only, with no action buttons (`electron/main.ts:157-186`).

**Restart and auth**
- `boot_id` is on `/api/health`, which needs no auth (`internal/core/info_handler.go:27`). Restart re-execs in place and keeps the pid.
- ✱ Only `POST /api/daemon/restart` answers `503 shutting_down`. Other endpoints serve during the drain and then refuse connections. `peers send` answers `503 not_ready` while stopping (`internal/module/peers/send.go:203`).
- ✱ During a restart the session module **removes the global tmux hooks** and reinstalls them on start (`internal/module/session/module.go:315-330`, `:195`).
- **One credential.** `TokenAuth` accepts the single host token, or a one-time WS ticket (`internal/middleware/middleware.go:71-86`). The SPA and every `pdx` command use the same token, from `~/.config/pdx/config.toml`.

**CLI and storage**
- The CLI has no shared client, **no retry** on refused connections, and no long-poll command.
- Exit codes are literals: 0 ok, 1 runtime/API error, 2 usage error.
- `lead`, `spawn`, `kill`, `relay` and `team` are free as top-level commands. `internal/terminal/relay.go` already uses "relay" for terminal WS, so Go identifiers must be qualified.
- SQLite files in `<data_dir>`: `meta.db`, `agent_events.db`, `host_config.db`, `profiles.db`, `backup.db`, `nex/nex.db`.
  - Migrations are per store: `CREATE IF NOT EXISTS` plus column checks.
  - **No table for pending requests or operations exists.**

**Vocabulary**
- `PRODUCT.md`: Role `worker` / `operator` is a row of the §3.5 table (`:75-76`). Operator also appears in §1 (`:15`), Law 2 (`:148`) and §6.1/§6.2 (`:203-209`).
- No `team` / `lead` / `member` concept exists in code. The CC hook `TeammateIdle` is ignored.
- Claude Code's own Agent Teams are experimental: a fixed lead, no nesting, and split panes unsupported in Ghostty (research page, "Session 與多 agent"). This spec does not build on them.

## 4. Vocabulary (U4)

| Term | Meaning |
|---|---|
| **team** | One lead and the members it opened. Lives on the lead's host. |
| **lead** | A Claude Code session the user approved for lead mode (§6). It can spawn, kill and relay its members. |
| **member** | A session a lead spawned (§7). Its relay is the lead's to decide (U9). |
| (none) | Every other session. The default; no word for it in the UI. |

- **Role in PRODUCT.md §3.5** becomes `(none)` / `lead` / `member`. `worker` stays Nexen's word for headless execution (U4).
- **`operator`** becomes **lead** in §1, Law 2's example and §6.1. §6.1 becomes "Lead / team":
  - a lead coordinates members over daemon-level message inject and the `pdx` team commands;
  - entry is by the user's approval.

  §6.2 follows the rename.
- This is one small separate PR (P0), as the brief asked. The §3.5 Mode row (`terminal / stream / agent`) is stale too, but it is not in scope here.

## 5. Architecture

**⟲ changed from D1.** The daemon is still the brain, but **the steps inside a session are executed by a Purdex Claude Code plugin** (the "Purdex mod"), not by send-keys.

**The daemon** (new module `internal/module/team`, own `team.db`) owns:
- lead requests and their decisions;
- teams, grants and members;
- spawn and relay operations, every step persisted;
- session lineage (the ref redirect chain);
- detection of member context usage, and the notices to the lead.

**The Purdex mod** runs inside each interactive Claude Code session. It is the executor for every relay of that session, its own or one its lead started:
- it asks the agent to write the handoff (`$.prompt.submit`, U2 literally);
- it checks the file;
- it clears (`$.command.run('clear')`);
- it seeds the new conversation;
- it reports each step to the daemon through `pdx`.

**Reasons for the change:**
- **M1 proves the mod can be driven by a pdx message the model never sees.**
- **send-keys `/clear` is fragile.** It types into the TUI: whatever is in the input box gets merged, and a stray key in CC's TUI has meanings (Ctrl-C twice exits, Esc-Esc opens rewind). `command.run` was proven in F1 and P1.
- **It matches U2's literal mechanism** for members too. The brief's D5 asked members through `pdx msg`.
- **A relay in flight survives a daemon restart** because the CC process drives it (U12); the daemon only records.
- **Self relay (goal 1) needs the mod anyway.** One executor serves both.

**Shipping.** The plugin (mod + skill) is embedded in the `pdx` binary and extracted to `<data_dir>/cc-plugin/purdex/` (versioned).
- It is loaded through `CLAUDE_CODE_PLUGIN_DIRS` in the `env` block of `~/.claude/settings.json` (M2).
- That entry is merged and removed by the installer that already merges the CC hooks and statusline (`internal/agent/cc/hooks.go`), and appended to any existing list.
- The mod does nothing in a headless session, i.e. a Nexen worker's `claude -p`. Worker relay goes through Nexen rebuild, not `/clear`.

## 6. Lead request and approval (U5, U6, U7, U12)

### 6.1 CLI

```
pdx lead request --reason <text> [--max-members N] [--root <dir>]... [--wait 9m]
```

1. pdx generates the request id (UUID v4, the idempotency key). It prints one stderr line: `申請 lead 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）`.
2. `POST /api/team/approvals {id, kind:"lead", origin_inbox, reason, max_members, roots, wait_s}`.
3. Long-poll `GET /api/team/approvals/{id}?wait=25` until the request is closed. **Each poll renews the request's lease.**
4. On approval it prints the grant on stdout (team id, max members, roots) and exits.
5. On SIGINT or SIGTERM it sends `DELETE /api/team/approvals/{id}` (best effort), then exits 12.

Defaults:
- `--max-members` 3, cap 8;
- `--root` is the caller's cwd;
- `--wait` 9 min, so it stays under the Bash tool's 10 min maximum (D3).

### 6.2 Daemon

**⟲ derived (U13 (b)): one model for every approval.** Two kinds of request share one table, one state machine, one event (`approval.request`) and one dialog host:
- lead requests (this section);
- self-relay requests (§8.7).

The table is `approval_requests{id, kind: lead | self_relay, origin_session_id, payload, state, deadline_at, lease_until, decided_by, decided_at}`. What follows is the `lead` kind; `self_relay` differs only in payload and grant.

**Create** (idempotent on `id`):
- The origin must be a live, deliverable CC session on this host. The existing `findOrigin` attributes it by inbox.
- Refused with **409**:
  - `already_lead`;
  - `member_cannot_lead` — no nested teams in v1;
  - `request_open`, carrying the open request's id.
- The row is stored with:
  - `state=open`;
  - an absolute `deadline_at` = now + `wait_s`, capped at 10 min;
  - `lease_until` = now + 30 s.
- Broadcasts host event `approval.request` `{op:"opened", request}`.

**Close.** Exactly one of these wins, by compare-and-set on `state=open`:

| Close | When | State |
|---|---|---|
| A client decides | `POST …/{id}/decide {decision, grant, client}`, one click (U5b) | `approved` / `denied` |
| Deadline passes | sweeper | `timeout` (U7: counts as a denial) |
| The requester gives up | `DELETE`, or the lease expires, or the origin session is gone | `cancelled` / `abandoned` |

- Every close broadcasts `{op:"closed", request}`, carrying `decided_by` and `decided_at`.
- A late `decide` gets **409 `already_decided`** with the closed request, so its client can say who handled it.
- Approval creates the team (§7.1) in the same transaction.

**Snapshot.** The module registers `OnSubscribe` and sends every open request to each new subscriber. Late or reconnecting clients see the same set (D2).

### 6.3 SPA and App (U6)

- A global `ApprovalDialogHost`, next to `HandoffDialogHost`, driven by a store keyed `hostId + requestId`. It is fed by a new `approval.request` branch in `useMultiHostEventWs` and by the snapshot. The dialog body depends on the kind; this section describes `lead`, and §8.7 describes `self_relay`.
- The dialog shows:
  - host;
  - the session's title or name, address and ref, cwd and tmux session;
  - the reason;
  - a countdown to the deadline.

  The user can edit the grant: max members, and the allowed roots (default the requested ones). Buttons: **核准** / **拒絕**.
- **核准** and **拒絕** are one click each, on any Purdex.app (U5b).
- Several requests queue, one dialog at a time, oldest first.
- **Closed elsewhere:** the dialog closes everywhere, and other clients get a toast `<主機>：<session> 的 <lead 申請／接力申請> 已由 <client> 核准／拒絕` (U6).
- **Notification:**
  - Electron raises a system notification through the existing `showNotification` path (`<主機>：<session> 申請成為 lead`); clicking it focuses the window, where the dialog already is.
- **During a daemon restart (D6):** the dialog stays, with its buttons disabled and `daemon 重啟中…`.
  - A click while disconnected is kept locally and re-sent on reconnect. CAS makes the resend safe.
  - A 409 then closes it with the "handled by" toast.

### 6.4 The soft lock (U7, D3)

This section covers lead requests, which the agent makes through Bash. A self-relay request is made by the mod, which holds the session itself (§8.7).

`pdx lead request` blocks the session's turn on one Bash call. The skill (§10) requires the call to run:
- in the foreground;
- with `timeout: 600000`;
- never in the background.

What happens when that is not followed:

| The model … | Result |
|---|---|
| uses the default 2 min Bash timeout | The Bash tool kills pdx. pdx cancels on the signal; on a SIGKILL the lease expires within 30 s. Either way the dialog closes on every client, and the session got no approval. |
| backgrounds the call | Nothing stops it in v1. This is the soft lock's known hole. The hard lock (§11) closes it. |

### 6.5 Who can approve (U5, U5b)

**One click on any Purdex.app, for both kinds (U5b, U13a).**
- **核准** and **拒絕** are one click each. There is no signature and no Touch ID.
- Any App's answer closes the prompt on every App (U6).

**Why that is enough** (U5b's reasoning, recorded): a cld-yolo session can already open tmux and run `claude` through Bash. Lead mode gives an agent no new capability; the approval means "told and agreed", not a security boundary.

**The layer that stays** (from U5a, kept by U5b):
- `pdx` has no approve command, and the skill forbids self-approval (§10).
- `decide` carries a `client` descriptor `{kind:"app", label}`, e.g. `Purdex.app @ air26`, and the daemon adds the remote address. These are audit labels, not a boundary.
- Every decision is broadcast and shown on every App (`已由 Purdex.app @ air26 核准`).
- Every decision is audited: in the request's `decided_by` / `decided_at` columns, and in one daemon log line per decision.

**What this protects, and what it does not:**
- **Protected:** an agent cannot approve through any path Purdex offers. An approval it fakes shows on every App, with the client label and address.
- **Not protected:**
  - One host token is shared by the SPA and `pdx` (§3.3), so a same-uid agent can call `decide` itself.
  - It can also rewrite `team.db`, or run tmux and `claude` without Purdex at all.

  The broadcast and the audit are what remain. Hardening with human presence is in §11.

**History.** A human-presence design for U5a was written on 2026-10-06: a Secure Enclave approver key per Mac in Purdex.app (this spec at commit `b9a6f239`, §6.5). U5b withdrew it the same day. The facts it rested on stay in §3.2 (M6–M10) for that hardening.

## 7. Team, spawn, kill (U8, U10)

### 7.1 Team

Created on approval.

- **Row:** `{id, host_id, lead_session_id, grant{max_members, roots[]}, request_id, created_at, ended_at}`.
- **The team follows the lead through its relays:** `lead_session_id` moves with the lineage (§8.4).
- **It ends when the lead's conversation ends:**
  - the lead's `SessionEnd` with any reason other than a relay's `/clear`;
  - this includes a manual `/clear`, which starts a new entity (conversation-entity spec E1) that does not know its members.
- **Members stay running when the team ends** (D4). They become ordinary sessions; team rows are kept for history.

### 7.2 Spawn

```
pdx spawn [--cwd <dir>] [--title <t>] [--brief-file <f> | --brief <text>]
```

1. pdx generates the operation id, then calls `POST /api/team/spawns {id, origin_inbox, cwd, title}`.
2. The daemon checks:
   - the origin is the lead of a live team;
   - active members < `max_members`;
   - `cwd` resolves (symlinks evaluated) under a granted root and exists.

   A refusal is **409** with a code: `not_lead`, `team_full`, `cwd_outside_grant`.
3. **tmux name `tm-<first 10 hex of the op id>`, derived from the id** (D4). A retry after a daemon restart finds the op row and its recorded step:
   - an existing session of that name is this op's, so the daemon continues;
   - nothing opens twice.
4. **Create the tmux session and launch the member.** This goes through the session module's create path, then a generation-checked literal send to window 0. Each step is persisted.
   - The launch command is the host config `team.member_command`, default `claude --dangerously-skip-permissions` (the expansion of `cld-yolo`, because the daemon cannot rely on a shell alias).
   - **A member always carries the Purdex mod:** the command always gets `--plugin-dir <data_dir>/cc-plugin/purdex`. Even with the global install the plugin loads once (M5), so spawn never depends on the user's settings.
5. **Wait up to 20 s for the member to register.** Same shape as take-to-terminal: a verified frame for the pane with a session id, plus a registry entry, so the ref is known.
   - On timeout: kill the tmux session, fail `member_start_timeout`. The member does not count against the limit.
6. **Store the member** (`team_members`: team, session id, pane, tmux name, title, spawn op, `state=active`) and set its title. Answer `{ref, address, tmux_session, session_id}`.

**⟲ changed from D4 — the brief is sent by the CLI, not the daemon.** After step 6, `pdx spawn` sends the brief through the existing `POST /api/peers/send`, with `origin_inbox` = the lead's inbox.
- So the member sees the message from the lead, and its replies go to the lead.
- No daemon-internal send is needed for spawn.

The text is prefixed with one line: `[pdx team] 你是 <lead address> 的 member（team <id>）。接力由 lead 決定，不要自己交接。`

**⟲ changed from D4 — no `--worktree` flag.**
- The daemon has no worktree API (§3.3), and U10 says the lead arranges it.
- The user's global rule forbids `git worktree add` outside `EnterWorktree`.

The skill tells the lead to recommend in the brief that the member run `EnterWorktree` itself, or to pass a `--cwd` it prepared.

**⟲ changed from D4 — no `--host` in v1.** Spawning on another host needs that host's daemon to trust a grant approved on the lead's host. That is a new trust path; see §11.

### 7.3 Kill and list

- **`pdx kill <ref>`:** only the member's own lead may run it; otherwise 409 `not_your_member`.
  - It kills the member's tmux session, so the member's CC exits.
  - Sets `state=killed`. Worktrees are the lead's business.
- **`pdx team [--json]`:** the caller's team — each member's address and ref, title, status, context %, cwd and tmux session.

## 8. Relay (U1, U2, U3, U9, U13)

### 8.1 One operation, two kinds

Both kinds are rows in `relay_ops` and run the same steps in the session's mod.

| Kind | Started by | When |
|---|---|---|
| `self` | the session's own mod | at a turn's end with used ≥ 70% (U1), **after the user approves** (U13, §8.7) |
| `member` | the lead, with `pdx relay <ref>` | when the lead decides (U9) |

**States:** `requested → claimed → writing → written → cleared → done`, or `failed{reason}` / `cancelled`. A self op starts in `awaiting_approval` and moves to `claimed` on approval. Each transition is a report from the mod (§8.3), stored with its time.

**Self relay needs the switches and an approval (U13, §8.7).** `pdx relay begin --self` refuses with **409**, and the mod does nothing:
- `member_relay_is_leads` for a member (U9);
- `self_relay_off` when the host switch is off;
- `self_relay_paused` when the session is paused.

**No daemon, no relay.** The approval and the record both live on the daemon. With the daemon unreachable, the mod cannot ask, so nothing relays; auto-compact runs as usual (§8.7). Every relay is therefore recorded, and the lineage (§8.4) is always written.

**Loop guard:** a seeded conversation must grow by 20K tokens before it may self-relay again (prototype `MIN_GROWTH`).

### 8.2 Member relay, end to end

1. **Lead:** `pdx relay <ref>` sends `POST /api/team/relays {id, origin_inbox, target}`.
   - The daemon checks the target is an active member of the caller's team, and that the member's mod has said hello (§8.3) with a compatible version.
   - **A member without the mod is refused:** **409 `relay_unsupported`** (exit 13). The lead is told why: `<ref> 沒有載入 Purdex mod（或版本不符），無法接力；請手動交接或重開這個 member`.
   - It stores the op as `requested`.
2. **Daemon → member:** a control message `[pdx-relay:control] op=<id>` goes to the member's inbox from the daemon's own virtual peer (§8.5).
3. **Member mod:** `session.receive`, matching that text, returns `{consumed}`. The model never sees it (M1).
   - The text is only a wake-up. The mod calls `pdx relay claim <op>` with its session id. The daemon accepts only when the op targets that session, and answers with the op and the facts for the handoff.
   - A spoofed or stale control message therefore does nothing.
   - If a turn is running, the mod waits for its `turn.complete`.
4. **Writing:** `$.prompt.submit` with the prototype's write prompt (8 sections), to the op's handoff path.
   - §8 "協作關係" is filled from the claim. A member gets its lead and team id; a lead gets its roster.
   - At `turn.complete` the mod checks the file: all 8 headings, more than 200 chars. It allows two fix rounds, else `failed{handoff_incomplete}`, with no `/clear`.
5. **Clear:** `$.command.run('clear')` from a timer (F3).
6. **Cleared:** at `classic.SessionStart{source:clear}`, the mod reports `cleared` with the new session id. The daemon then records the lineage, in one transaction (§8.4).
7. **Seed:** `$.prompt.submit` with the takeover prompt. Its first line is `↪ 接手自 <old ref>`.
8. **Done:** at that turn's end, the mod reports `done`. The daemon tells the lead `[pdx team] <old ref> 已由 <new ref> 接手（交接檔 <path>）` (D5.6).

**⟲ Why refuse, rather than fall back to send-keys** (air26 asked for one of the two, with the reason):
- A send-keys executor would be a second, untested path for the same steps. It types into the TUI (§5), and it reaches the agent by `pdx msg` instead of `$.prompt.submit`, which is U2's mechanism.
- Spawn always loads the mod, so a member without it means something is broken: a failed load, version skew, or a mod error. Surfacing that beats silently running a weaker relay.
- The cost is small. The lead still has the manual path it has today.

`pdx relay` returns once the op is accepted and prints the op id. `--wait` blocks until done or failed, with the same restart-aware polling as §6.1.

**Timeouts:**
- claim: 60 s, else `failed{member_unresponsive}`;
- whole op: 15 min.

The lead is told about every failure.

**⟲ changed from D5 steps 3–5.** The brief had the daemon ask by `pdx msg`, wait for the Stop hook, and send-keys `/clear`. Reasons are in §5. The detection (D5.1) and the report to the lead (D5.6) are unchanged.

### 8.3 The mod's daemon calls

| Call | When |
|---|---|
| `pdx relay hello --session <sid>` | at `session.start` and after each clear: says this session can relay, gives its version |
| `pdx relay begin --self` | self relay; answers the op and handoff facts, or a refusal |
| `pdx relay wait <request>` | self relay: long-polls the approval; renews its lease (§8.7) |
| `pdx relay self off\|on\|status` | the per-session pause, also behind the mod's `/relay` command (§8.7) |
| `pdx relay claim <op>` | member relay |
| `pdx relay report <op> <state> [--new-session <sid>] [--error <e>]` | each transition; idempotent per (op, state) |

The mod reaches the daemon through `$.process.run` on `pdx`, as the prototype did. The calls use the restart-aware client (§9.1).
- If a `report` still fails after its 30 s grace, the mod **keeps relaying**: the session matters more.
- It re-sends the report at the next `turn.complete`. The daemon's reconciliation (§9.3) covers the gap.

**Handoff file location:** `<data_dir>/relay/<op id>.md` on the session's host. The brief and the research page left this open.
- It does not dirty the repo, survives worktree removal, and the daemon can verify it.
- For a session without bypass permissions, the mod answers `tool.check` with allow for a write to exactly that path; the plan verifies the hook's shape.
- Moving the content into a pdx memory store keyed by uuid is later (§11).

**Retention (air26 review (f): the user does not want files piling up).**
- **The daemon cleans, nobody else.** The team module's sweeper runs at boot and hourly. Neither the mod nor the agent deletes files in `<data_dir>/relay/`.
- **What it keeps:**
  - per lineage chain, the newest **3** handoff files;
  - nothing older than **14 days**;
  - the files of `failed` or `cancelled` ops for **3 days**, for debugging.
- The `relay_ops` row keeps the path, marked `pruned` once deleted.

### 8.4 Lineage: the ref keeps working (U3)

`session_lineage{session_id, predecessor_session_id, predecessor_ref, op_id, at}` is written when an op reaches `cleared`. In the same transaction:
- if the old session was a team's lead, `teams.lead_session_id` moves to the new session;
- if it was a member, the member row's session id moves;
- the title moves to the new session id (titles are stored per session id, §3.3).

**Peer rows** gain `previous_refs` (newest first) for a live head: the **whole chain, uncapped.**
- **⟲ changed after air26's review (e).** A cap of 10 would break a member's oldest lead ref after the lead's eleventh relay. A ref is 7 bytes, so even a hundred relays add under 1 KB to one row.
- **When a lead relays,** the daemon also tells each active member: `[pdx team] 你的 lead 已換手：<new address> [<new ref>]（舊 ref 仍可用）`. So a member's own handoff records the current ref.

**`ipeers.Resolve` gains one tier, after a live ref:** a `_ref` that matches no live row but appears in exactly one row's `previous_refs` delivers to that row.
- Resolution runs on the sender's daemon over the target host's rows (§3.3), so this works across hosts.
- An older sending daemon ignores the field and answers `peer_not_found`, as today.

**Display:** `pdx peers` shows the address as `mlab/purdex-b0 [b3xxxx] (was _b1xxxx)`.

The `peer_not_found` hint stops saying a ref "never changes" (M3). It says a ref survives renames and relays, but not a manual `/clear`.

**Only relays write lineage.** A manual `/clear` is a new conversation (conversation-entity E1): messages to its old ref go nowhere, as today.

### 8.5 Detection and notice to the lead (U9, D5.1)

- **Parse context usage.** The agent module parses the statusline payload at ingest: `session_id`, `context_window.used_percentage`, `context_window_size`. It keeps the last value **per CC session id**, which also fixes the overwrite in a shared tmux session. Peers and the team module read it through an accessor.
- **Persist it for teams only.** The team module stores the last value on member and lead rows, so it survives a restart.
  - Other sessions show `—` after a restart until their next refresh.
- **Notice to the lead:**
  - when a member's usage reaches 70% and the member is idle (its `Stop`), the daemon sends the lead **one** notice: `[pdx team] member <address> [<ref>]「<title>」已用 72%，目前閒置。要接力請執行：pdx relay _<ref>`;
  - if the member is running when it crosses, the notice waits for its next `Stop`;
  - the notice re-arms only after a relay, or after usage drops below 70%.
- **The daemon decides nothing (U9).** Past 70% a member keeps working until its lead acts.
- **Daemon notices come from the daemon's own virtual peer** (`ccuds.StartVirtualPeer`). A reply to it gets one line back: `這是 pdx daemon 的自動通知，不會讀取回覆`.

**⟲ derived: auto-compact.**
- **Solo session or lead:** see §8.7. Only an already-approved relay skips the compaction.
- **Member:** never intercepted. It cannot self-relay (U9, U13), so its mod lets compaction run and reports `compacted`. The lead hears: `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）`.

### 8.6 Peers and the CWD fix

- **Context column.** `pdx peers` gains `CTX` (`72%`, or `—`). `--json` rows gain `agent.context {used_percentage, window, at}`.
- **CWD fix.** A session row's cwd prefers:
  1. the CC registry `cwd`, which follows `EnterWorktree` (M4);
  2. then the verified frame's cwd;
  3. then tmux `session_path`.

  This fixes the side bug from the brief §4; it lands in P1.

### 8.7 Self relay: switches, approval and the lock (U13)

**Who may self-relay:**

| Role | Default | Switch |
|---|---|---|
| (none) | on | host config `relay.self_solo` |
| lead | on | host config `relay.self_lead` |
| member | off | none: a member's relay is the lead's (U9, U13) |

**⟲ derived (a): where the switches live.**
- **Per host, in host config.** They are stored in `host_config.db` by the existing hostconfig module. The UI is Hosts → that host → a "接力" section with the two toggles and the line `member 的接力一律由 lead 安排`.
  - Reason: the daemon answers `begin`, and the mod on a host asks that host's daemon. A setting kept only in the SPA would not reach it. Hosts may differ.
- **Per session, a pause.**
  - The mod registers `/relay off`, `/relay on` and `/relay status`. The same is available as `pdx relay self off|on|status`, for scripts.
  - The daemon stores it per session id (`session_prefs`).
  - A session switch only narrows: `on` lifts the session's own pause, never a host switch that is off.
  - The self-relay dialog also offers **這個 session 不再詢問**, which sets the pause.
- **A member has no switch.** U13 says "預設關" and "接力必須由 lead 安排"; read with U9, that is not switchable. `/relay on` in a member answers `member 的接力由 lead 安排`.

**⟲ derived (b): approval.** U13 brings in U5 and U6; U13a and U5b make it one click.
- **Opening the request.** At a turn's end with used ≥ 70%, the mod calls `pdx relay begin --self`.
  - The daemon checks role, switch and pause (§8.1).
  - It then opens an `approval_requests` row of kind `self_relay` (§6.2) and a `relay_ops` row in `awaiting_approval`, and answers the request id.
- **The dialog shows:**
  - host;
  - the session: title, address, ref and cwd;
  - usage: `已用 72%`;
  - `核准後這個 session 會寫交接檔、清空並在原處接手（約 1 分鐘）`.
- **核准 is one click on any App (U13a), the same as a lead request (U5b).** So is **拒絕**.
  - The layer of §6.5 applies: no `pdx` approve command, the skill forbids self-approval, and every decision is broadcast and audited.
- **Deadline:** 10 minutes, absolute. The lease is renewed by the mod's wait (below). If the origin session is gone, the request is `abandoned`.
- **Approved:** the op moves to `claimed`, and the mod runs §8.2 steps 4–8: write, clear, seed.
- **Denied or timed out:** the op becomes `cancelled{denied|timeout}`.

**⟲ derived (b): the lock (U7) for a request the mod makes.** The request opens at a turn's end, so nothing is running. The lock means **no new turn starts until the request closes**.
- **The hold.** The mod's `prompt.submit` hook awaits `pdx relay wait <request>` through `$.process.run`.
  - This applies to the main conversation, whatever the origin. Typed prompts and peer messages both pass this hook (M11).
  - A hook's 10 s budget counts only its own code, never a `$` call in flight (`HookBudget` in the 2.1.291 types; the mods reference: "a `next` or `$` call in flight does not count"). M11 measured a 15 s hold. So the hold lasts until the request closes.
  - **Fallback, if a later Claude Code stops routing peer deliveries through `prompt.submit`** (air26 review): consume them in `session.receive` (M1), and replay them once the request closes. The mod's tests pin the current routing, so such a change turns a test red.
- **While holding:** status line `接力等待核准中`, plus one toast naming where to approve.
- **Approved:** the held prompts go through **into the current conversation** (`next(e)`), and the relay starts right after.
  - The mod submits the write prompt once idle. It recognises its own write turn by the turn that `$.prompt.submit` started, not by "the next `turn.complete`". So queued prompts that run first do not trigger the handoff check early.
  - **⟲ changed after air26's review (3), which asked to drop and then re-submit with `asUser: true`.** A re-submitted prompt loses its `@file` mentions and pasted images, and stays attributed to the plugin (M12). Letting the person's own prompt run in the old conversation keeps it whole. That costs one turn of context, which is affordable at 70–80% used.
  - The handoff then records that turn too.
  - **A note for the model on released prompts (air26 review).** The release is `next({ ...e, context: [...(e.context ?? []), NOTE] })`. `context` reaches the model beside the prompt and is never shown to the user (2.1.291 types, `PromptSubmitResult.context`). NOTE says:
    > 接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進交接檔「下一步」的第一項，由接手後的新對話處理。

    Why: without it, the old conversation could take on a large new task at its fullest (70–80%). That defeats relaying early, and could run into auto-compact.
    - `@file` mentions and images still expand in the old conversation, so the handoff can record their paths and gist.
    - This is a soft constraint: the model may not follow it. The backstop is §8.7(c): an approved relay not yet written skips auto-compact.
  - If the plan finds the write turn cannot be told apart reliably, it falls back to air26's way: drop, then re-submit after the seed, `asUser: true` for a typed prompt and the envelope kept for a peer message. In that case, a prompt carrying `@file` mentions or images is released instead of dropped.
- **Denied or timed out:** the held prompt goes through unchanged (`next(e)`).
- **Esc:** it abandons that dispatch, so that prompt is not sent. The request stays open.
- **No prompt arrives:** the mod also waits from a timer (`$.clock.after` → `pdx relay wait`), so an approval starts the relay at once.

**⟲ derived (c): asking again, and auto-compact.**
- **Asking again.** After a denial or a timeout, the mod asks again only when usage has grown **10 more points** since the last ask: 72 → 82 → 92. At most one open self-relay request per session.
- **Auto-compact never waits for an approval.** At `session.compact{trigger:auto}`:
  - **an approved relay not yet written:** skip the compaction (`{skip}`) and start writing;
  - **anything else:** compaction runs. An open request becomes `cancelled{compacted}`, and its dialog closes on every client.
  - Reason: an approval takes a person and minutes. Holding the compaction would hang the session when it is fullest.
  - Unmeasured (brief §3): whether `{skip}` mid-turn lets the turn go on. The plan measures it. If it does not, the approved case also lets compaction run, and starts the relay at the turn's end.
- **After a compaction**, the next ask needs usage ≥ 70% again.

**(d) No daemon.** With the daemon unreachable, the mod cannot ask, so nothing relays, and auto-compact runs (§8.1).
- `pdx relay wait` uses the restart-aware client (§9.1). A restart during the wait keeps the request.
- After `daemon_unavailable` (exit 20), the mod releases the hold and treats the request as not approved. The daemon's lease then closes it.

## 9. Daemon restart (U12, D6)

Waiting is tied to a **request or operation id**, never to a connection. All state is in `team.db`.

### 9.1 The pdx client

One shared client, `cmd/pdx/daemonclient`, is used by every new command and by the mod's calls.

**It treats these as "restarting":**
- connection refused, reset or EOF;
- `503` with `shutting_down` or `not_ready`;
- a `/api/health` `boot_id` that differs from the one first seen.

**Then:**
- It prints `daemon 重啟中，繼續等待…` once on stderr.
- It retries with backoff 0.25 s → 1 s, for a **30 s grace**: three times the usual 5–10 s.
- After the grace it exits 20, `daemon_unavailable`.
- Deadlines are absolute, so a restart's time counts against them.

**By kind of call:**
- reads and long-polls: retry transparently;
- writes carry their client id and retry with it;
- long flows are persisted and reconciled (§9.3).

**A 404 on a team route** means an older daemon: exit 21, `unsupported` (D6).

### 9.2 Leases

- An open lead request's lease is renewed by each poll.
- **On boot**, every open request gets `lease_until = max(lease_until, boot + 30 s)`, so its pdx can reconnect.
- **It is abandoned** when the lease runs out, or when the origin session's process is gone, whichever is first. It then closes, with a broadcast.

### 9.3 Reconciliation on boot

The daemon reads actual state; it never waits for lost hooks.

**Spawn ops not finished:**
- if the tmux session exists, continue from the recorded step: launch, or wait for registration;
- past the 20 s budget, kill it and fail.

**Open approval requests of every kind** get the lease grace above. A self-relay request's lease is renewed by the mod's `pdx relay wait`.

**Relay ops not finished:**
- `requested`, not claimed: send the control message again. The claim is CAS, so a duplicate does nothing.
- Past `claimed`: compare the op's old session id with the pane's current verified frame.
  - **A different session id** means the clear happened. Write the lineage from the frame's session id if the mod's report has not, and wait for `done`.
  - **No frame** means CC exited: `failed{member_gone}`.
- **The tmux hook gap** (§3.3) is covered the same way: every decision reads frames and the registry, never a hook that may have been lost.

### 9.4 Clients during the restart

See §6.3: the prompt stays, disabled; a click is queued and re-sent.

### 9.5 Restarting with work in flight

**⟲ changed from D6** ("respond with the list; the caller waits or adds `--force`"). `POST /api/daemon/restart` is not refused.
- Everything above survives a restart, so a 409 would only add friction.
- Instead, the restart confirm dialog (daemon-restart spec §3.2) gains lines, each shown only when non-zero: `N 個申請等待核准、N 個接力進行中（重啟後會接續）`.
- The counts come from `GET /api/team/inflight`, within the dialog's existing 3 s budget.

## 10. The skill (D2's last point)

The skill ships in the plugin (`skills/pdx-team/SKILL.md`). It says:

**When to ask, and how to wait:**
- when to ask for lead mode: the work is large and parallel (U5);
- run `pdx lead request` in the foreground with Bash `timeout: 600000`; never in the background, and never approve yourself;
- treat timeout as no.

**As a lead:**
- spawn and kill;
- recommend a worktree to members, by having them `EnterWorktree`, or prepare one (U10);
- when a `[pdx team]` notice arrives, decide whether and when to `pdx relay` (U9);
- write the team roster into your own handoff's §8.

**As a member:**
- never relay yourself;
- report to the lead's address.

**Self relay:**
- the Purdex mod asks the user on its own; the agent never asks for one and never approves one;
- `/relay off` is the user's switch, not the agent's.

## 11. Later (not in this spec's phases)

- **Hard lock (D3).** `pdx hook` for `PreToolUse` gains a synchronous path:
  - only when a local flag file for this session exists (written by `pdx lead request`, removed when it ends) does it ask the daemon, and on a pending request it writes a deny decision;
  - with no flag it never contacts the daemon;
  - an unreachable daemon allows.

  A relay could later lock a member with the same flag.
- **Human-presence approval (hardening; U5a, withdrawn by U5b).** The design written on 2026-10-06 (commit `b9a6f239`, §6.5):
  - a CryptoKit Secure Enclave key per Mac in Purdex.app, with `.userPresence` (Touch ID, or the login password);
  - enrolled per daemon host;
  - signing a challenge bound to the request and its grant.

  Alternatives, once available: Electron WebAuthn, after a Developer ID and provisioning profile (signing roadmap Stage 3). Facts: M6–M10.
- **Cross-host teams:** spawn, kill and relay on another host, and a remote host's trust in a grant approved elsewhere.
- **Adopting** an existing session as a member. Today's manual flow, where the user opens a session and hands its address to A, keeps working as plain messaging.
- **Handoff content in a pdx memory store** keyed by uuid.
- **Member visuals** (U11): a separate design.

## 12. Phases

One phase is one PR, ≤ 800 lines or ≤ 20 files; split further when larger.

| Phase | Content | Brief D8 |
|---|---|---|
| P0 | PRODUCT.md vocabulary (§4) | "separate small PR" |
| P1 | Statusline usage parsed per session id + accessor; peers `CTX` column and `agent.context`; CWD fix; `peer_not_found` hint text (§8.5, §8.6) | 1 (part) |
| P2 | `team` module skeleton and `team.db`; `daemonclient` with the restart rules (§9.1); lead requests: create, poll and lease, cancel, decide, sweeper, boot grace, `OnSubscribe` snapshot; `pdx lead request`; exit codes (§14) | 1 |
| P3 | Approval dialog host, store, event branch, one-click approve and deny (U5b), reconnect queue, notifications; restart-confirm line for open requests (§6.3, §9.5) | 1 |
| P4 | Teams and grants; `pdx spawn` / `kill` / `team`; spawn reconciliation; team end on the lead's exit | 1 |
| P5a | Daemon relay core: `relay_ops`; `session_lineage` with uncapped `previous_refs` and the Resolve tier; title, team and lead-ref moves; the `self_relay` approval kind; host switches and Hosts UI toggles; session pause; handoff retention sweeper | 2 (part), U13 |
| P5b | Plugin packaging (embed, extract, `CLAUDE_CODE_PLUGIN_DIRS` merge and uninstall) with the skill; mod self relay: `hello` / `begin` / `wait` / `report`, the prompt hold, asking again, the auto-compact rule, `/relay` | 2 (part), 4 (part), U13 |
| P6 | Member relay: `pdx relay`, the daemon's virtual peer and control message, `claim`, timeouts, boot reconciliation of relay ops; completion and failure notices; restart-confirm line for relays | 2 |
| P7 | Detection and the 70% notice to the lead; persisted usage on team rows; member auto-compact report | 3 |

**Notes on the split:**
- **P5a/P5b depend on P2 and P3.**
- **Order:** P0, P1, P2, P3, P5a, P5b, P4, P6, P7.
  - Self relay, goal 1, ships first.
  - P4 (team, spawn) needs P3, because a lead approval comes from the dialog.
- P6 may split in two: daemon first, then mod.
- The mod has its own tests, run by `claude plugin test`.
- Daemon deploys are batched with the other daemon lines (conversation-entity D14 practice).

## 13. Changes to the brief's draft

| Draft | Change | Reason |
|---|---|---|
| D1 | The daemon stays the brain; in-session steps move to the Purdex mod | M1 proves the control channel; `command.run` beats send-keys; U2 literally; relays survive restarts; goal 1 needs the mod anyway (§5) |
| D2 | Approve and deny are one click on any App; the `client` descriptor is an audit label | U5b (U5a withdrawn); one shared host token (§3.3) |
| D2 | Grant has no host list in v1 | Cross-host is §11 |
| D4 | No `--worktree`; no `--host`; the brief is sent by the CLI from the lead's inbox; tmux name `tm-<op>`; start timeout kills and frees the slot | No worktree API and U10; trust path; replies reach the lead; D4's own idempotency idea; the limit counts only live members |
| D4 | Launch command is `team.member_command`, default `claude --dangerously-skip-permissions` | The daemon cannot rely on the `cld-yolo` alias |
| D4 (air26 review) | A member is always launched with `--plugin-dir`; relay to a member without the mod is refused, not done by send-keys | Loads once even with the global install (M5); reasons in §8.2 |
| D5 3–5 | The mod writes, clears and seeds; the daemon only sends a control wake-up | §5 |
| D5 | Only relays write lineage; titles and team roles move with it | E1; titles are per session id |
| D5 | Self relay needs the daemon (`begin`) | Every relay is recorded |
| D5 (new) | A member's auto-compact is not intercepted; the lead is told | U9 forbids a member self-relay |
| D6 | No 409 / `--force` on restart; the confirm dialog lists in-flight work | Everything survives a restart (§9.5) |
| D7 | Adds 13 and 14 (§14); keeps 1 and 2 as today | Spawn, kill and relay need refusal and start-failure codes |
| D8 | More PRs than the brief's four phases | The 800-line / 20-file limit; U13 added work |
| U13 (a) | Switches per host in host config; a per-session pause by `/relay` and `pdx relay self`; members not switchable | The daemon is what answers `begin`; U9 |
| U13 (b) | One `approval_requests` table (kinds `lead`, `self_relay`); the self-relay lock holds `prompt.submit` inside a `$` wait; approve is one click (U13a, U5b) | air26 asked for a shared model; a hook's budget excludes `$` waits |
| U13 (c) | Ask again after 10 more points; auto-compact never waits, and only an already-approved relay skips it | The session must never hang |
| U13 (d) | No daemon: no ask, no relay, compaction runs | Approval lives on the daemon |
| §8.4 (review e) | `previous_refs` uncapped; members told the lead's new ref | A cap breaks old lead refs |
| §8.7 (review 3) | On approval, held prompts run in the current conversation, and the relay follows; no drop and re-submit | A re-submitted prompt loses `@file` and images, and stays attributed to the plugin (M12); air26's way is the fallback |
| §12 (review 1) | P5 depends on P2 and P3 | U13a; U5b removed P3a/P3b |
| §8.3 (review f) | Retention: 3 per chain, 14 days, 3 days for failed; the daemon cleans | The user does not want files piling up |

## 14. Exit codes (D7, extended)

| Code | Meaning |
|---|---|
| 0 | Approved / done / accepted |
| 1 | Other runtime or API error (existing convention) |
| 2 | Usage error (existing convention) |
| 10 | Denied |
| 11 | Timed out (counts as denied, U7) |
| 12 | Cancelled or abandoned |
| 13 | Refused by team rules: `not_lead`, `team_full`, `cwd_outside_grant`, `not_your_member`, `member_relay_is_leads`, `self_relay_off`, `self_relay_paused`, `relay_unsupported`, `already_lead`, `member_cannot_lead`, `request_open` |
| 14 | The member did not start or did not respond: `member_start_timeout`, `member_unresponsive` |
| 20 | Daemon unreachable through the 30 s grace |
| 21 | Daemon does not support this (404) |

## 15. Tests

**Daemon:**
- **Approvals of every kind:** they share one compare-and-set.
- **Retention:** 3 per chain, 14 days, and 3 days for failed ops; nothing outside `<data_dir>/relay/` is touched.
- **Lineage:** an uncapped chain still resolves a lead's oldest ref after 11 or more relays.
- **Lead request:** create is idempotent; exactly one close wins under concurrent decide, timeout and cancel; the 409 carries `decided_by`; the snapshot reaches a late subscriber; the lease is extended on boot; abandonment fires when the origin dies.
- **Spawn:** the limit, roots and symlink escape; retry after a mid-op restart opens nothing twice; the start timeout kills and frees the slot.
- **Relay:**
  - claim is accepted only for the target session;
  - reports are idempotent;
  - lineage moves title, lead and member in one transaction;
  - Resolve finds an old ref in exactly one row, and a live ref wins over `previous_refs`;
  - boot reconciliation from frames, with no hooks.
- **Usage:** parsed per session id; two panes in one tmux session no longer overwrite; a null `used_percentage`.
- **CWD:** the precedence order.

**CLI:**
- `daemonclient`: refused, then a new `boot_id`, then a retry succeeds; the grace expires into exit 20; a 404 gives 21.
- `pdx lead request` cancels on SIGTERM.
- Exit codes for each terminal state.

**Mod** (`claude plugin test`):
- the control message is consumed only with the marker;
- `claim` failure does nothing;
- write → check → fix rounds → clear → seed;
- the 20K loop guard;
- a member does not self-relay;
- auto-compact is intercepted for solo and lead, and passed through for a member;
- headless does nothing;
- **self relay under U13:**
  - a prompt that arrives while a request is open waits;
  - on approval it runs in the current conversation, and the write turn is recognised by its own turn even with queued prompts ahead of it;
  - a released prompt carries NOTE in its `context`, appended after any context already attached; a prompt released after a denial or a timeout carries no NOTE;
  - on denial it passes unchanged;
  - asking again only at +10 points;
  - auto-compact runs, and cancels the request, unless the relay is already approved;
  - `self_relay_off` and `self_relay_paused` are respected;
  - `/relay on` in a member is refused.

**SPA:**
- the dialog opens from the snapshot and the event;
- 核准 and 拒絕 are one click on every App, for both kinds;
- it closes on `closed` from another client, with the toast;
- disabled while disconnected; a queued click is re-sent; a 409 closes it.

**Mutation is a deliverable:**
- dropping the CAS lets two decisions win → red;
- dropping the claim's session check lets another session claim → red;
- dropping `previous_refs` from Resolve leaves the old ref at `peer_not_found` → red;
- dropping the prompt hold lets a prompt start a turn while a self-relay request is open → red;
- treating "the next `turn.complete`" as the write turn makes a queued prompt trigger the handoff check early → red;

**Real acceptance (mlab, then air26):**
1. A session requests lead; the user approves on air26's App with one click; the dialog closes on a19's App at the same moment, showing who approved.
2. The lead spawns two members, and relays one at 70% on a test threshold (`PDX_RELAY_THRESHOLD`, as in the prototype). The old ref still reaches it.
3. Restart the daemon during a pending request and during a relay; both finish.
4. Self relay on a solo session at a test threshold: the dialog appears on every client; deny, then see it ask again at +10 points; approve with one click, no Touch ID; a message typed during the wait is answered first, intact, then the relay runs.

## 16. Not in scope

- Member visuals (U11).
- Nexen worker relay: it goes through Nexen rebuild.
- Cross-host teams, adoption, the hard lock, Electron WebAuthn, and the handoff memory store: all in §11.
- Claude Code's built-in Agent Teams.
- **Turning the browser SPA off (U14).** Here U14 is a premise only.
  - The unmerged web version (branch `worktree-web-version`; `purdex.mlab.host` in front of the daemon) is affected.
  - The user decides separately whether to drop it, keep it as a view-only client, or fold it into the App.
