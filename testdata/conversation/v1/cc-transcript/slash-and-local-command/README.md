Slash commands: local commands (/model, /context, /usage, /exit) and a project command that reaches the model (/hello), recorded from Claude Code 2.1.294.

- Recorded with `claude --model sonnet` (2.1.294, default permission mode) in a throwaway tmux session and git repo; prompts written for the fixture. The repo had one project command, `.claude/commands/hello.md` ("Say hello to $ARGUMENTS in one short sentence."). Scrubbed with `scrubfixture`; paths are `/work/…`.
- Rows: `/model` opened the picker and was dismissed with Esc ("Kept model as Sonnet 5.5"), `/hello fixture` ran one real model turn, `/context` printed a large local-command result, `/usage` opened a dialog that was dismissed with Esc, then `/exit`.
- Turn 1, 3, 4: a `system/local_command` pair (`<command-name>` then `<local-command-stdout>`) each: `slash` source, one `command_output`, no model work, no `turn_duration`.
- Turn 2: a prompt row `<command-name>/hello</command-name>…<command-args>fixture</command-args>` (origin human, no `promptSource`) is `slash` with text `/hello fixture`; its `isMeta` expansion row is skipped; the assistant reply and `turn_duration` follow. The only turn with a `turn_duration` apart from the last.
- The `/context` markdown (an `isMeta` user row, 14 KB) and the `<local-command-caveat>` rows are `isMeta` and skipped.
- Turn 5: `/exit` as a user row with `<command-name>` followed by a user row with `<local-command-stdout>`; the stdout row is a continuation of the open command, not a new turn.
- This build has no MultiEdit / Grep / Glob tools; no tools are used here at all.
- Closed session (`live: false`).

Notes for the reader
- The spec says a turn that is not the last and has no `turn_duration` is `interrupted` (a killed process). Turns 1, 3 and 4 are local-command turns that never reached the model and have no `turn_duration` by design, and the same shape in `source-kinds` is `done`. `facts.json` follows that: `done`. If the normalizer reads the rule literally, it will say `interrupted` for these three and the rule needs a carve-out for turns with no model work.
