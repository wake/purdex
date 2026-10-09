# Member-relay acceptance (and the relay-lock timing measurement)

> Source: `docs/specs/2026-10-06-lead-team-relay-plan-v3.md`, PR P6-6 "Acceptance" (spec §15 real acceptance 2 and 3, §8.2 steps 3–8, M1, M28).
> Not a CI gate. The unit tests are `claude plugin test cmd/pdx/plugin/purdex` (`hooks/member.test.ts`).
> Same method as `self-relay-acceptance.md`: throwaway sessions, a **scratch copy** of the plugin loaded with `--plugin-dir`.

## Ground rules

- **Throwaway only.** The lead, the members, their tmux sessions and the team are made for this run and removed at the end. The target of `pdx relay` is only ever a throwaway member: never yourself, never a peer that is doing real work.
- **Do not touch the live teams.** A second member needs the throwaway team's limit raised: use `PUT /api/team/max-members` for **that team's id only** (the token goes in a private header file, as in `self-relay-acceptance.md`; never in argv or the terminal). The limits and relay quotas of the live teams (A line `c933b0ee…`, 88 line) are left as they are.
- **Approval is the user's.** If an op sits in `awaiting_approval` (unattended mode off or the pool is 0), stop and tell the lead; do not approve it, and never turn unattended mode on.
- **Members run `--model sonnet`** (not Haiku).
- **Do not restart the shared daemon** (step 7).
- Needs a daemon with P6-5 (the relay route) and `pdx setup` already run for this build, or the member's mod is protocol 1 and the daemon answers `relay_unsupported`. Loaded with `--plugin-dir` from a scratch copy, the mod is protocol 2 whatever the host has.

## Preconditions

- `which pdx` is the build under test; `pdx peers` lists this host.
- The mod is not already loaded in the throwaway member sessions twice (`jq -r '.env.CLAUDE_CODE_PLUGIN_DIRS // empty' ~/.claude/settings.json`): if `pdx setup` put it there, the members already have it, leave `--plugin-dir` out and skip the scratch copy (except for step 2, which needs the shim copy).
- Scratch folder and cleanup trap, as in `self-relay-acceptance.md` step 1:
  ```bash
  REPO=~/Workspace/wake/purdex
  SCRATCH=$(mktemp -d -t member-acc)
  PLUG_TMP=$(mktemp -d -t member-acc-plugin)
  cp -R "$REPO/cmd/pdx/plugin/purdex" "$PLUG_TMP/purdex"
  HDR=$(mktemp -t pdx-acc-hdr); ( umask 077; awk -F'"' '/^token = /{printf "Authorization: Bearer %s\n", $2; exit}' ~/.config/pdx/config.toml > "$HDR" )
  trap 'rm -rf "$PLUG_TMP" "$SCRATCH"; rm -f "$HDR"' EXIT
  ```

## Steps

- [ ] 1. **A throwaway lead and team.** Start `claude --model sonnet` in `$SCRATCH` in a tmux session (`tmux -L member-acc`, `unset TMUX`); have it run `pdx lead request --reason "member relay acceptance" --name "relay-acc" --label "acc" --max-members 2` in the foreground; the user approves in Purdex.app. Note the team id (`pdx team --json | jq -r .team.id`). If the limit is below 2, raise it for this team only (`PUT /api/team/max-members {"team_id": "<id>", "max_members": 2}` with `-H @"$HDR"`).
- [ ] 2. **Lock timing (the open point of P6-3c).** See the section below; run it first, on the first member, before the relays of steps 3–4, so a "before" answer changes the code before the rest is checked.
- [ ] 3. **Two members.** From the lead: `pdx spawn --cwd "$SCRATCH" --title acc-1 --model sonnet --brief "Reply ok and wait."` twice (`acc-1`, `acc-2`). Wait for `pdx team` to show both live and a model reading. Note each ref and `/status`-equivalent (`pdx team --json | jq '.members[] | {ref, model:.context.model_id, effort:.context.effort}'`).
- [ ] 4. **Relay once while idle, once while a long turn runs.** From the lead, in the foreground (`timeout: 600000`):
  - (a) idle: `pdx relay <acc-1 ref> --wait 9m`.
  - (b) running: give `acc-2` the prompt "Run `sleep 80 && echo done` in the foreground, then reply done" (Claude Code blocks a bare `sleep 45`; use the `ping -c 80 127.0.0.1` of M28 if it refuses), and 10 s later run `pdx relay <acc-2 ref> --wait 9m`.
  Expect, both times:
  - the member's transcript (`/export`, or the pane) **never shows** the control text `[pdx-relay:control]`;
  - in (b) the daemon sees `seen` within about a second (`pdx relay op <op>` → `state: seen`) while the member's tool is still running, and the claim comes only after that turn ended (`pdx relay op` → `claimed` after the `sleep` returned: compare with the pane);
  - the member writes the handoff with one `Write` (file `<data_dir>/relay/<op>.md`; **§8 lists its lead `mlab/<lead name> (ref …, team …)`**), the screen clears, the new conversation answers `↪ 接手自 _<old ref>`;
  - the lead gets the `[pdx team]` done notice (and `pdx relay … --wait` exits 0);
  - `pdx relay op <op>` is `done`, and `pdx team` shows the member under its **new** session.
  Run the same on a throwaway **lead** once (a relay of the lead's own, `/relay` in its pane, not a member relay): its handoff §8 must list the two members (`- 我管理的 members：` and one line each with address, ref, title, cwd).
- [ ] 5. **Lineage.** `pdx msg send <host>/_<old ref> "ping"` for each relayed member still reaches the new conversation; `pdx peers` shows `(was _<old ref>)`.
- [ ] 6. **Model and effort (U20 (f), M21).** `pdx team --json` before and after each relay: the member's `model_id` and `effort` are equal. (The statusline of the new conversation is the source; give it one turn first.)
- [ ] 7. **Daemon restart mid-relay: needs the lead (not this run).** Restarting the shared mlab daemon is not done by this recipe. The lead performs it with the deploy (or runs this step against a test daemon with its own data dir and port: `pdx start --config <other config>` and a `--plugin-dir` mod whose `pdx.json` names that config): start `pdx relay <ref> --wait 9m` on a member, `pdx stop && pdx start` once it is `claimed`; expect the relay to finish, or to be reconciled from frames (spec §14), with no flag left (step 8).
- [ ] 8. **Cleanup and checks.**
  - `ls ~/.config/pdx/hooklocks/` lists no flag of any throwaway session (the daemon's safety net also removes it at `cleared`).
  - `pdx relay op <id>` for every op of the run is `done`, `failed` or `cancelled`; any other: `pdx relay report <op> cancelled --error abandoned` (13 means already closed).
  - `pdx kill <ref>` for each member (the lead is the throwaway's own; ask it, or `tmux -L member-acc kill-server`), `pdx peers` lists none of them, `tmux -L member-acc ls` fails (no server), `rm -rf "$SCRATCH" "$PLUG_TMP"; rm -f "$HDR"; trap - EXIT`.
  - The live teams' member limits and relay quotas are as before (`pdx team` of their leads; read-only).

## Relay-lock timing: does the real engine wait for `turn.start`'s await?

P6-3c raised the relay lock in the write turn's `turn.start` hook: `await pdx relay lock …`, then `next(e)`. The question was whether the engine really holds the model's first tool call until that await is done. A real sample (a self relay, op `02325587`) had 49 s between the write prompt and its only tool call, which proves nothing; this test makes the await long enough to tell.

**Method (scratch plugin copy, a `pdx` shim):**

1. In the scratch copy (`$PLUG_TMP/purdex`), write the shim and point the mod at it. The mod reads `pdx.json` beside `hooks/` (`$.plugin.root/pdx.json`):
   ```bash
   REAL=$(command -v pdx)
   LOG=$SCRATCH/lock-timing.log
   cat > "$PLUG_TMP/pdx-shim" <<EOF
   #!/bin/bash
   if [ "\$1" = relay ] && [ "\$2" = lock ]; then
     echo "\$(date -u +%FT%T.%3NZ) lock-start" >> "$LOG"
     sleep 20
     "$REAL" "\$@"; rc=\$?
     echo "\$(date -u +%FT%T.%3NZ) lock-done rc=\$rc" >> "$LOG"
     exit \$rc
   fi
   exec "$REAL" "\$@"
   EOF
   chmod +x "$PLUG_TMP/pdx-shim"
   printf '{"pdx":"%s"}\n' "$PLUG_TMP/pdx-shim" > "$PLUG_TMP/purdex/pdx.json"
   ```
   (`date +%3N` is GNU; on macOS use `python3 -c 'import datetime;print(datetime.datetime.utcnow().isoformat()+"Z")'` in the shim instead.) The `lock` call's own timeout is `LOCK_TIMEOUT_MS` = 8 s: **raise it to 30 s in the scratch copy** (`sed -i '' 's/LOCK_TIMEOUT_MS = 8_000/LOCK_TIMEOUT_MS = 30_000/' "$PLUG_TMP/purdex/hooks/register.js"`), or the mod gives up on the lock (fail-open) at 8 s and the test measures nothing.
2. Launch the throwaway member (or a self relay: `PDX_RELAY_THRESHOLD=5`, as in `self-relay-acceptance.md`) with `--plugin-dir "$PLUG_TMP/purdex"`, run one relay (step 4 (a) is enough).
3. Compare, for the **write turn** of that relay:
   - `$LOG`: `lock-start` and `lock-done` (about 20 s apart);
   - the daemon log `~/.config/pdx/logs/pdx.log`: the first `hook` line of that session for a **PreToolUse** decision ("hook allow/deny … relay op …", the `Write` of the handoff), by timestamp. Both are UTC; convert if the daemon log is local.
4. Read:
   - the first PreToolUse is **after** `lock-done` → the engine waits for the `turn.start` await; the main path stands (lock before `next(e)`). Record the method and the three timestamps below.
   - the first PreToolUse is **before** `lock-done` → the engine does not wait. Switch to the fallback of plan P6-3c rule 1: `await lockRelay` right **before** the write prompt's `$.prompt.submit` (in `startWrite`), say so in the PR, and tell 88 that their ask paths are unaffected (the lock only matters for the relay's own turn).

**Result (run 2026-10-09, alpha.652, op `c64e5a82-35d3-4370-9671-c5dcfca451a9`, a scratch member adopted into a throwaway team; times local UTC+8, the shim log is UTC):**

| | |
|---|---|
| `lock-start` / `lock-done` (shim, 20 s delay) | 21:52:35.9 / 21:52:55.99 |
| write prompt submitted (`UserPromptSubmit`) | 21:52:35 |
| first `PreToolUse` of the write turn (daemon log) | 21:52:43 |
| handoff file written (mtime) / `Stop` | 21:52:44 / 21:52:46 |
| daemon log "hook allow … holds the lock" for the op | none (the flag was not up) |
| Conclusion | **The engine does not hold a turn for `turn.start`'s awaited call.** The model's Write ran 12 s before the lock finished. Plan P6-3c rule 1's fallback is taken: the lock is raised in `startWrite`, before the write prompt's `$.prompt.submit`. |

After the fix the same measurement needs no shim: a relay's write turn must log `hook allow: … tool "Write" while relay op <op> holds the lock` in the daemon log.

## Not verified on a real session

- A lead's own relay carrying its roster in §8 (needs a lead self relay, another approval card); covered by `member.test.ts`.
- A daemon restart mid-relay (step 7; the lead's, on the shared daemon).
