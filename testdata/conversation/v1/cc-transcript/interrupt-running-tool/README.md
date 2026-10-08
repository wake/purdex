Bash edge cases: a blocked foreground sleep re-run in the background with a task notification turn, and two Esc-refused tool calls ending in `[Request interrupted by user for tool use]`, recorded from Claude Code 2.1.294.

- Recorded with `claude --model sonnet` (2.1.294, default permission mode) in a throwaway tmux session and git repo; prompts written for the fixture. Scrubbed with `scrubfixture`; paths are `/work/…`.
- Turn 1: "Run `sleep 120` with Bash". This build blocks a standalone foreground `sleep` (`<tool_use_error>Blocked: standalone sleep…`, `is_error`, no `toolDenialKind`) → that step is `failed`; the model re-runs it with `run_in_background` → `done` (result "Command running in background…", `toolUseResult.backgroundTaskId`). An Esc meant to interrupt came too late, so nothing was interrupted here.
- Turn 2: the background task finishing (`origin.kind: task-notification`, `turnOrigin: task_notification`) → source `task`, one reply, `turn_duration`.
- Turn 3: a foreground `python3 … time.sleep(120)`; approved in the dialog, Esc at about 6 s: the result is `is_error` with `toolDenialKind: user-rejected` → `denied`/`user-rejected`, then a user row `[Request interrupted by user for tool use]` (carries `interruptedMessageId`) and **no** `turn_duration` → `interrupted`.
- Turn 4: `echo refuse-me`, which is auto-allowed (no dialog) → an ordinary done turn.
- Turn 5: `python3 -c "print('refuse-me-2')"` refused with Esc at the dialog: `user-rejected` denial, the marker row and then a `turn_duration` → still `interrupted` (the marker decides).
- Turn 6: `/exit` (slash, with its stdout row).
- All steps are Bash (`execute`); outputs are tiny, none truncated. This build has no MultiEdit / Grep / Glob tools.
- Closed session (`live: false`).

Notes for the reader
- The marker row of turn 5 has no `interruptedMessageId` (turn 3's has); it is recognised by its `[Request interrupted by user` text prefix. It is not a user item and does not open a turn.
- Turn 5 has both a marker and a `turn_duration`; `facts.json` follows the spec order (marker wins, `interrupted`).
