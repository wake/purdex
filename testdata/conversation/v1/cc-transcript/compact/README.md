A manual /compact in the middle of a session: the compact boundary, the compact summary row and the local-command rows around it, recorded from Claude Code 2.1.294.

- Recorded with `claude --model sonnet` (2.1.294, acceptEdits) in a throwaway tmux session and git repo; prompts written for the fixture: "Write a haiku about rain", "Now one about wind", `/compact`, "Thanks.", then `/exit`. Scrubbed with `scrubfixture`; paths are `/work/…`.
- `/compact` stays in the same transcript file. It writes, in this order: the plain user row `/compact`, a `system/compact_boundary` row (`compactMetadata.trigger: manual`), the `isCompactSummary` user row (the summary, skipped by the normalizer), an `isMeta` `<local-command-caveat>` row (skipped), a user row `<command-name>/compact</command-name>…` and a user row `<local-command-stdout>Compacted…</local-command-stdout>`. It produces no `turn_duration`.
- Turns (6): two haiku prompts (each with an assistant reply and `turn_duration`); the plain `/compact` user row (turn 3, source `user`, holds the `compact_boundary`, hence the `compacted` item); the `<command-name>/compact` user row (turn 4, source `slash`, with the stdout as `command_output`); "Thanks." (a normal turn); `/exit` (slash, with its stdout).
- This build has no MultiEdit / Grep / Glob tools; no tools are used here.
- Closed session (`live: false`).

Notes for the reader
- The recorded rows carry no `turnPosition`, `origin` or `promptSource` on the plain `/compact` row, so by the spec it is an older-style prompt row: a non-meta user text row, source `user` (only `<command-name>` rows are `slash`). That is a literal reading; it looks odd that one `/compact` yields two turns (3 and 4). Turn 3 has no model work and no `turn_duration` and is not the last turn; as in `slash-and-local-command`, `facts.json` says `done` rather than the literal `interrupted` (killed process).
- The `compact_boundary` row follows the opening of turn 3, so it is inside that turn, not "outside a turn".
