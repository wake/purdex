Recorded Claude Code session: `seq 1 5000` through Bash, an output larger than the 16 KiB cap, kept from the tail.

- Recorded with Claude Code 2.1.294, `claude --model sonnet` (default permission mode, no dialog for this read-only command) in a throwaway tmux session and git repo; the prompt "Run `seq 1 5000` with Bash and tell me the last number printed." was written for the fixture. The session ended with `/exit`. Scrubbed with `scrubfixture`.
- Turn 1: one `Bash` step (kind `execute`, `done`). The result is stored inline (no `<persisted-output>` wrapper): 5000 lines, 23,892 bytes (`1`..`5000` joined by `\n`, no trailing newline), so over the 16 KiB cap: `truncated: true`, `keep: tail`, totals count the whole text.
- Turn 2: `/exit` (an `isMeta` caveat row, a `<command-name>` user row, a `<local-command-stdout>` user row).
- Live: `false`.

Notes for the reader:
- A spilled `<persisted-output>` result is not covered here (the output was below Claude Code's spill threshold); `output-caps` covers the cap synthetically.
- The `/exit` rows open a second turn with source `slash`, closed `done` only because the session is not live; see the note in `edit-write-multiedit`.
