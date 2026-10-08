Queued prompts (`promptSource: queued`), a background-task notification turn and three Esc interrupts, from the iOS F2-B recording (Claude Code 2.1.292).

- Source: `docs/samples/2026-10-07-cc-2.1.292/f2b-queue-interrupt.jsonl` of the iOS repo (a26, auto mode), scrubbed with `scrubfixture` (`-home <home> -user <account>`). The message text was written for the recording and is kept.
- Covers: queued as a prompt row (3 turns), `task` source (the background `sleep 15` notification), interrupt marker `[Request interrupted by user]` ending three turns (one followed by a queued prompt that runs as the next turn), a `run_in_background` Bash step that is `done`.
- Not covered: queued as an absorbed `queued_command` attachment (this CC version wrote queued prompts as user rows; see `source-kinds`).
- Closed session (`live: false`); the last turn ends with a marker, so `live` does not change it.
