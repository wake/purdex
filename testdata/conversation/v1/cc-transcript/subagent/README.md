Recorded Claude Code session: an Agent (subagent) call that Claude Code backgrounded, the task-notification turn that reports it, and the subagent's own transcript file under children/.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (`--permission-mode acceptEdits`) in a throwaway tmux session and a git repo of three `.md` files; the prompt was written for the fixture ("Use the Agent tool (general-purpose) to count the lines of every .md file in this repo and report the total"). The session ended with `/exit`. Scrubbed with `scrubfixture`.
- Main file, turn 1: the model calls `Agent` (`subagent_type: general-purpose`, `model: haiku`). **The call was auto-backgrounded**: the tool result is an immediate "Async agent launched successfully…" text (six lines) with `toolUseResult {agentId, description, isAsync: true}`; step kind `task`, status `done`. The turn then ends with the model saying it is still running.
- Turn 2: when the agent finishes, a `user` row with `origin.kind: "task-notification"` (`promptSource: "system"`, `turnOrigin: "task_notification"`) carries the `<task-notification>` and the model reports the total; source `task`.
- Turn 3: `/exit` (an `isMeta` caveat row, a `<command-name>` user row, a `<local-command-stdout>` user row).
- Child file: `children/a7a639d97d57c6f43.input.jsonl` is the subagent's file (every row `isSidechain: true`): its prompt as a plain user row, one `Bash` call (`find … | xargs wc -l`, four lines of output), an empty `thinking` row and the final text. There is no `turn_duration`, no `turnPosition`, no `origin`.
- `children/a7a639d97d57c6f43.facts.json` holds the child's facts in the same shape as `facts.json`; `facts.json` itself cannot carry them because the facts decoder refuses unknown fields.
- Live: `false` for both.

Notes for the reader:
- The child's one pseudo-turn: I wrote id = the first row's uuid, source `user` (a prompt row with no `origin`), outcome `done` (no `turn_duration`, `live: false`).
- The `/exit` rows open a last turn with source `slash`, closed `done` only because the session is not live; see the note in `edit-write-multiedit`.
