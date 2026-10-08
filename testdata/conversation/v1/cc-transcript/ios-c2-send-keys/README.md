Plain prompts, pasted and multi-line text, a refused Bash call, AskUserQuestion, a bash-mode turn and a background task, from the iOS C2 recording (Claude Code 2.1.292).

- Source: `docs/samples/2026-10-07-cc-2.1.292/c2-send-keys.jsonl` of the iOS repo (auto mode), scrubbed with `scrubfixture` (`-home <home> -user <account>`). The message text was written for the recording and is kept.
- Covers: 19 turns; a `<pasted_content …>` prompt that is a plain `user` source; a Bash call refused by the person (`toolDenialKind: user-rejected` + `[Request interrupted by user for tool use]`, the turn is `interrupted`); AskUserQuestion (`other`, `done`); a `run_in_background` Bash step, `[Request interrupted by user]` without any prompt fields, then the task-notification turn (`task` source); a bash-mode turn (`<bash-input>` → `bash` source, its `<bash-stdout>` row becomes a `command_output` item).
- Closed session (`live: false`).
