# Probes behind the lead-team relay spec's measured facts

These files reproduce the facts measured for `docs/specs/2026-10-06-lead-team-relay-spec.md` §3.2, numbered M1–M12. Everything was run on mlab on 2026-10-06 with Claude Code 2.1.291.

Run every probe in a **throwaway** tmux session, in a scratch directory (`<dir>`), with Haiku to keep it cheap:

```
tmux new-session -d -s <name> -c <dir>
tmux send-keys -t <name> "claude --plugin-dir <probe folder> --model claude-haiku-4-5-20251001 --dangerously-skip-permissions" Enter
```

Each mod logs to `<dir>/probe.log`. Kill the tmux session afterwards. A probe session registers as a peer, and it may reply to whoever messaged it.

| Probe | Facts | How |
|---|---|---|
| `probe-relay-mod/` | M1 | Find the probe's ref with `pdx peers`. Then `pdx msg send <host>/_<ref> '[pdx-relay:control] op=x'`. The log shows `receive origin={"kind":"peer"}`, `prompt.submit sent`, `turn.complete`, `command.run(clear) returned`, then `classic.SessionStart source=clear` with a new session id. The old transcript has no `pdx-relay:control` (`grep -c`), and `pdx msg send` to the old ref answers `peer_not_found` (M3). |
| the same, with `CLAUDE_CODE_PLUGIN_DIRS=<folder>` instead of `--plugin-dir` | M2 | The log shows `loaded`. |
| both `CLAUDE_CODE_PLUGIN_DIRS=<f>` and `--plugin-dir <f>` | M5 | `loaded` appears once. |
| `probe-hold-mod/` | M11 | Send a peer message whose text has `HOLD15`, and type a prompt with `HOLD15`. Each logs `submit#N origin=…` (`{"kind":"peer"}` or `{"kind":"composer"}`), `holding 15s`, `released` after 15 s, then `turn.start`. |
| `se-probe/main.swift` | M6, M7 | `swiftc -O -o se-probe main.swift`, then run it. It prints whether a CryptoKit Secure Enclave key can be created, used to sign, and reloaded without entitlements, and whether a `.userPresence` key can be created. On mlab the latter fails with `-25308`, because the console is locked (`ioreg -n Root -d1 \| grep CGSSessionScreenIsLocked`). To run it in the GUI session, load it as a `gui/501` LaunchAgent (`launchctl bootstrap gui/501 <plist>`, then `bootout`). Keep the plist in a scratch dir, not in `~/Library/LaunchAgents`. |
| a statusline that dumps its stdin | the P1 payload shape | `claude --settings <file>`, where `<file>` is `{"statusLine":{"type":"command","command":"cat > <dir>/sl.json; echo probe"}}`. `sl.json` then has top-level `session_id` and `context_window.{used_percentage (null at first), context_window_size, …}`. |

**M12 is not a probe.** It is read from the 2.1.291 mod types, `PromptSubmitArgs.asUser`: a plugin's prompt never expands `@file` mentions or pasted images.
