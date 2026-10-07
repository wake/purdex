# Self-relay acceptance at a test threshold

> Source: `docs/specs/2026-10-06-lead-team-relay-plan-v2.md`, PR P5b-3 "Acceptance recipe at a test threshold" (spec §15 "Real acceptance" 4, §8.7, U13, U16, U18 (a)).
> Not a CI gate. Run on mlab first, then on air26. The unit tests are `claude plugin test cmd/pdx/plugin/purdex`.

## Ground rule: throwaway only

The run must leave the host's own setup as it found it. **Do not run `pdx setup --agent cc` on a real host for this recipe unless the user has agreed to it first**: it rewrites `~/.claude/settings.json` (hooks and `env.CLAUDE_CODE_PLUGIN_DIRS`) and extracts the plugin under the daemon's data dir, so every Claude Code session on the host would load the mod from then on. The recipe loads the mod into one disposable session instead, with `--plugin-dir`, and its cleanup has nothing of the host's to undo.

## Preconditions

- P5a-1..3 and P5b-1..3 merged; the daemon running the new binary.
- `pdx` on `PATH` is that build (`which pdx`; `pdx path` explains how to fix it). Loaded with `--plugin-dir`, the mod finds no `pdx.json` beside it, so it runs the `pdx` on `PATH` with that binary's default config — which is the running daemon's.
- The mod is not already loaded on the host: `jq -r '.env.CLAUDE_CODE_PLUGIN_DIRS // empty' ~/.claude/settings.json` names no `…/cc-plugin/purdex` entry. If it does (someone ran `pdx setup --agent cc` since P5b-1), leave `--plugin-dir` out of step 1 — two copies of one plugin would both hook every prompt.
- Purdex.app open on at least two clients (mlab's and a26's), both viewing this host.
- Secrets: never print the daemon token; pass it as `$(pdx token)` inside the command.

## Steps

- [ ] 1. **Launch at a low threshold, the mod loaded only here.** In a scratch dir (not a repo you work in), with `REPO` the Purdex checkout:
  ```bash
  REPO=~/Workspace/wake/purdex
  SCRATCH=$(mktemp -d -t relay-acc)
  tmux new-session -d -s relay-acc -c "$SCRATCH"
  tmux send-keys -t relay-acc "PDX_RELAY_THRESHOLD=5 claude --plugin-dir '$REPO/cmd/pdx/plugin/purdex' --model claude-haiku-4-5-20251001 --dangerously-skip-permissions" Enter
  ```
  Expect: `/relay` is in the slash-command menu; `/relay status` answers `自我接力：開啟（主機開關 開；門檻 5%）`. Run `/status` and note the model and effort lines.
- [ ] 2. **`/relay off` / `on`.** `/relay off` answers `自我接力：本 session 暫停（主機開關 開；門檻 5%）`; a turn past 5 % asks nothing. `/relay on` answers `自我接力：開啟（…）` and the next turn end asks at once (the +10 guard is reset).
- [ ] 3. **Cross the threshold.** Paste a long prompt (or have the model read a big file) so `/context` passes 5 %. At that turn's end: the status line reads `接力等待核准中`, one toast names the App, and **the self-relay dialog opens on every client** (host; session title, address, ref, cwd; `已用 N%`; the one-minute line).
- [ ] 4. **Deny on one client.** The dialog closes on the other at once; the status line clears; a typed prompt runs **unchanged**. Grow usage to 15 % (not 10 %): the dialog returns (the +10 rule). `pdx relay op <id>` shows the first op `cancelled` with `denied`.
- [ ] 5. **Type during the wait, then approve.** With the dialog open, type a short question and press Enter: it shows as sent with the spinner, no turn starts. Press Esc on **another** prompt: it is abandoned and the dialog stays. Approve with one click. Expect, in order: the typed question is answered **first, intact** (its `@file` mentions expand) and briefly (the NOTE); then the write prompt runs; the file appears at `<data_dir>/relay/<op>.md`; the screen clears; the new conversation's answer starts with `↪ 接手自 _<old ref>`. `pdx peers` shows the same name with `(was _<old ref>)`; `pdx msg send <host>/_<old ref> "hi"` still reaches it.
  - Also try a prompt typed **right after** the threshold turn ends, before the status line appears (`pdx relay begin` still out): it is held the same way (at most 8 s while begin is out).
- [ ] 6. **Model and effort (U18 (a)).** Before approving in step 5: `curl -s -H "Authorization: Bearer $(pdx token)" http://100.64.0.2:7860/api/team/approvals/<request id>` — the `self_relay` row's payload has **both** `model_id` and `effort`, equal to step 1's `/status`. After the relay, `/status` again shows the same model and effort; `pdx relay op <id>` shows `state: done`. A payload missing either means the session had no statusline reading yet: retry after one more turn.
- [ ] 7. **Compaction while a request is open.** Cross the threshold again (+10), and with the dialog open type `/compact`: compaction runs, **the dialog closes on every client**, any held prompt goes on unchanged at once (no NOTE), and `pdx relay op <id>` shows `cancelled` with `compacted`. The next ask needs usage ≥ the threshold again (not +10).
- [ ] 8. **No daemon.** `pdx stop`; cross the threshold in a fresh session: nothing happens, no status line; `/relay status` answers `Purdex daemon 連不上，無法變更自我接力` within about 8 s; auto-compact (if reached) runs. `pdx start`.
- [ ] 9. **Measure once** (plan open question 6) and record:
  - (a) the `turn.start` text of the mod's write prompt contains `[pdx-relay op=… n=…]` (`claude --debug`, or the transcript's first user line of that turn);
  - (b) a held prompt stays held across several of its own 60 s `pdx relay wait` rounds and one of the timer loop's 9 min rounds (approve after > 9 min, before the 10 min deadline) and is still released with the NOTE;
  - (c) Esc on a held prompt leaves the request open;
  - (d) whether `{ skip }` on an auto-compaction that fires mid-turn lets the turn go on. In a throwaway session (`PDX_RELAY_THRESHOLD=5`), approve, then before the write turn give a long multi-tool task and force a compaction (by context size, or `/compact` with `--plugin-dir` pointing at a scratch copy of the plugin folder whose `register.js` treats `manual` as `auto`). Watch `claude --debug` for `session.compact` followed by the next `tool.call`. Record the answer as spec **M27**. If the turn does not go on, apply spec §8.7 (c)'s fallback: answer `next(e)`, remember `s.compactedWhileApproved`, and submit the write prompt at the next `turn.complete` (one branch in `settle`, one in `turn.complete`, one test).
- [ ] 10. **Clean up.** Nothing on the host was installed, so there is nothing to uninstall:
  - `tmux kill-session -t relay-acc` (it ends the throwaway Claude Code; the mod goes with it), then `rm -rf "$SCRATCH"` (and the scratch plugin copy of step 9 (d), if any).
  - For every op of the run that is not `done`, `failed` or `cancelled` (`pdx relay op <id>`): `pdx relay report <op> cancelled --error abandoned`, so no dialog stays open on any client. Exit 13 (`bad_transition`) means it was already closed. Retention is the daemon's sweeper.
