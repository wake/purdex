Recorded Claude Code session: a Bash command that exits 0 despite `ls` failing, a Bash failure (`Exit code 1`), a backgrounded Bash and the task-notification turn that follows it.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (`--permission-mode default`) in a throwaway tmux session and git repo; prompts written for the fixture, Bash permission dialogs approved. The session ended with `/exit`. Scrubbed with `scrubfixture`.
- Turn 1: `ls /nonexistent_dir_fixture; echo "exit code: $?"` — the model appended an `echo`, so the result has `is_error: false` and the text `…\nexit code: 1` (it does not start with `Exit code`). Step `done`.
- Turn 2: the bare `ls /nonexistent_dir_fixture` — `is_error: true`, text `Exit code 1\nls: …`, no `toolDenialKind`: step `failed`.
- Turn 3: `sleep 6; echo finished` with `run_in_background`. The tool result is an immediate "Command running in background with ID: …" text (`toolUseResult.backgroundTaskId` is set); step `done`, one line. The turn ends with its own `turn_duration`.
- Turn 4: about six seconds later the completion arrives as a `user` row with `origin.kind: "task-notification"`, `promptSource: "system"`, `turnOrigin: "task_notification"` (no `queue-operation` row survives scrubbing); it opens its own turn (source `task`) in which the model answers; it has a `turn_duration`.
- Turn 5: `/exit` (an `isMeta` caveat row, a `<command-name>` user row, a `<local-command-stdout>` user row).
- Live: `false`. All outputs are tiny, none is truncated.

Notes for the reader:
- The background `Bash` step stays `done` even though the process was still running when its result was written (a result exists).
- The `/exit` rows open a last turn with source `slash`, closed `done` only because the session is not live; see the note in `edit-write-multiedit`.
