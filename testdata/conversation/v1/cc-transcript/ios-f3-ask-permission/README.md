AskUserQuestion answers and permission prompts that were allowed or refused, from the iOS F3 recording (Claude Code 2.1.292, `--permission-mode default`).

- Source: `docs/samples/2026-10-07-cc-2.1.292/f3-ask-permission.jsonl` of the iOS repo (a26), scrubbed with `scrubfixture` (`-home <home> -user <account>`). The message text was written for the recording and is kept.
- Covers: single, multi and two-question AskUserQuestion steps (`other`, `done`); Bash steps that were allowed (`done`); three refusals (`toolDenialKind: user-rejected`, with `is_error: true`) of a Bash and of an AskUserQuestion call, each followed by `[Request interrupted by user for tool use]` and a `turn_duration`: the marker wins, the turn is `interrupted` (ruling D5).
- Closed session (`live: false`).
